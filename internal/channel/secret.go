package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/coder/websocket"

	"github.com/wnexous/partyline/internal/api/dto"
)

// Secret requests on the session's side (SR-08, SR-09): the server hands the value of a secret a person
// gave in one secret frame; the session writes it to a private file and answers with the path. The value
// never becomes a notification, a line of `partyline listen` or a log line: the harness and the model only
// ever learn the path.

var secretNamePattern = regexp.MustCompile(dto.SecretNamePattern)

// secretPath is where the session writes the secret name of the AI aiID (SR-08):
// <user config dir>/partyline/secrets/<ai id>/<NAME>, dir being <user config dir>/partyline/secrets.
func secretPath(dir, aiID, name string) string { return filepath.Join(dir, aiID, name) }

// safeComponent reports whether s can be one component of a path: not empty, no separator, not . or ..
func safeComponent(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\`) && !strings.ContainsRune(s, 0)
}

// writeSecret writes value to secretPath(dir, aiID, name) (SR-08): the file has mode 0600 and replaces an
// existing one atomically (a fresh temporary file renamed over it, so a reader never sees half of it and a
// planted symlink is replaced, never written through); the secrets directory and the AI's directory have mode
// 0700 and must be plain directories. It returns the absolute path. Its errors name paths, never the value.
func writeSecret(dir, aiID, name, value string) (string, error) {
	switch {
	case dir == "":
		return "", errors.New("this session has no config directory to keep secrets in (set HOME or XDG_CONFIG_HOME)")
	case !secretNamePattern.MatchString(name):
		return "", errors.New("the secret's name is not a valid name")
	case !safeComponent(aiID):
		return "", errors.New("this session does not know which AI it is yet")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("secrets directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return "", fmt.Errorf("create the secrets directory: %w", err)
	}
	aiDir := filepath.Join(dir, aiID)
	for _, d := range []string{dir, aiDir} {
		if err := privateSecretDir(d); err != nil {
			return "", err
		}
	}
	f, err := os.CreateTemp(aiDir, "."+name+"-*") // mode 0600
	if err != nil {
		return "", fmt.Errorf("create the secret file in %s: %w", aiDir, err)
	}
	tmp := f.Name()
	_, err = f.WriteString(value)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o600) // CreateTemp makes 0600 already; a umask never widens it, but be explicit
	}
	path := secretPath(dir, aiID, name)
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("write the secret file %s: %w", path, pathOnly(err))
	}
	return path, nil
}

// privateSecretDir makes d with mode 0700 when it is missing, refuses it when it is a symlink or not a
// directory, and takes group and other access away from one that has it: the directory is the session's own.
func privateSecretDir(d string) error {
	if err := os.Mkdir(d, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("create the secrets directory %s: %w", d, pathOnly(err))
	}
	info, err := os.Lstat(d)
	if err != nil {
		return fmt.Errorf("check the secrets directory %s: %w", d, pathOnly(err))
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("the secrets directory %s is not a plain directory; remove it", d)
	}
	if info.Mode().Perm() != 0o700 {
		if err := os.Chmod(d, 0o700); err != nil { // #nosec G302 -- a directory: 0700 is the private mode
			return fmt.Errorf("make the secrets directory %s private: %w", d, pathOnly(err))
		}
	}
	return nil
}

// pathOnly keeps the reason of a file error without repeating the operation and path a *fs.PathError
// carries, which the message around it names already.
func pathOnly(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// isSecretFrame reports whether a server frame is a secret frame, which the read loop handles at once,
// before any notification and whether or not the harness is ready (SR-08).
func isSecretFrame(data []byte) bool {
	var head struct {
		Type dto.AgentFrameType `json:"type"`
	}
	return json.Unmarshal(data, &head) == nil && head.Type == dto.AgentFrameTypeSecret
}

// saveSecret writes the value of a secret frame and answers it (SR-08): secret_ack with the path, or with
// the reason it failed. Nothing of the frame reaches the harness or stderr, and the frame's bytes are
// cleared once it is handled. A failed answer ends the connection, like a failed ack.
func (l *link) saveSecret(ctx context.Context, ws *websocket.Conn, data []byte) error {
	defer clear(data)
	var f dto.AgentSecretFrame
	if json.Unmarshal(data, &f) != nil || f.RequestID == "" {
		return nil
	}
	ack := dto.AgentSecretAckFrame{Type: dto.AgentFrameTypeSecretAck, RequestID: f.RequestID}
	path, err := writeSecret(l.secretsDir, l.selfRef().ID, f.Name, f.Value)
	f.Value = ""
	if err != nil {
		ack.Error = err.Error()
	} else {
		ack.OK, ack.Path = true, path
	}
	out, _ := json.Marshal(ack)
	actx, cancel := context.WithTimeout(ctx, ackTimeout)
	defer cancel()
	if err := ws.Write(actx, websocket.MessageText, out); err != nil {
		return errAck
	}
	return nil
}

// secretSavedNote is what a session reads when the answer to its secret request reaches it (SR-09): the
// name, where the file is and who gave it, and what to do with it. Empty for every other message.
func secretSavedNote(msg dto.Message, dir, selfID string) string {
	a := msg.Answers
	if a == nil || a.Secret == nil || !a.Secret.Saved || !secretNamePattern.MatchString(a.Secret.Name) {
		return ""
	}
	where := "a file in your Partyline config directory (partyline/secrets/<your id>/" + a.Secret.Name + ")"
	if dir != "" && safeComponent(selfID) {
		where = secretPath(dir, selfID, a.Secret.Name)
	}
	by := msg.AuthorName
	if h := msg.Author.Handle; h != "" {
		by = strings.TrimSpace(by + " (@" + h + ")")
	}
	if by == "" {
		by = "someone who manages you"
	}
	return "The secret " + a.Secret.Name + " was saved to " + where + " by " + by + ". Read that file when you need the value; never print it, and never paste it in a room or a message."
}

// withSecretNote adds secretSavedNote to the notification of a message that answers a secret request, and
// the path as secret_path (SR-09); any other notification is returned as it is.
func withSecretNote(n Notification, msg dto.Message, dir, selfID string) Notification {
	note := secretSavedNote(msg, dir, selfID)
	if note == "" || msg.DeletedAt != nil {
		return n
	}
	n.Content += "\n" + note
	if dir != "" && safeComponent(selfID) {
		n.Meta["secret_path"] = secretPath(dir, selfID, msg.Answers.Secret.Name)
	}
	return n
}
