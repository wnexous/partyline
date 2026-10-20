package channel

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/wnexous/partyline/internal/api/dto"
)

// A harness that cannot take MCP notifications (pi, OpenCode) runs two partyline processes:
// `partyline listen` for the pushed messages and `partyline channel --tools-only` for the
// tools. Both are children of the harness process. listen records the agent name the server
// gave it in a session file named after that process (the session key), and the tools-only
// channel reads it on every tool call, so both act as the same agent (PUSH-02).

// sessionKeyPattern keeps session keys to one safe path component.
var sessionKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

var agentNamePattern = regexp.MustCompile(dto.AgentNamePattern)

// sessionFile is <tempDir>/partyline/.sessions/<key>; "" when key is not a safe name. The
// directory cannot clash with a download directory: agent names never start with a dot.
func sessionFile(tempDir, key string) string {
	if !sessionKeyPattern.MatchString(key) || key == "." || key == ".." {
		return ""
	}
	return filepath.Join(tempDir, "partyline", ".sessions", key)
}

// writeSessionName records name in path (0600, in a private 0700 directory), replacing the
// file atomically.
func writeSessionName(path, name string) error {
	dir := filepath.Dir(path)
	if err := privateDir(dir); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".name-*")
	if err != nil {
		return fmt.Errorf("session file: %w", err)
	}
	tmp := f.Name()
	_, err = f.WriteString(name + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("session file: %w", err)
	}
	return nil
}

// readSessionName returns the agent name recorded in path, or "" when there is none or the
// file cannot be trusted (a symlink, open to other users, owned by someone else, in such a
// directory, or not an agent name).
func readSessionName(path string) string {
	if path == "" {
		return ""
	}
	for _, p := range []string{filepath.Dir(path), path} {
		info, err := os.Lstat(p)
		if err != nil || info.Mode()&fs.ModeSymlink != 0 || checkPrivate(p, info) != nil {
			return ""
		}
	}
	data, err := os.ReadFile(path) // #nosec G304 -- private per-user session file
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(data))
	if !agentNamePattern.MatchString(name) {
		return ""
	}
	return name
}

// removeSessionName deletes path when it still records name (a newer listen may own it).
func removeSessionName(path, name string) {
	data, err := os.ReadFile(path) // #nosec G304 -- private per-user session file
	if err != nil {
		return
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte(name)) {
		_ = os.Remove(path)
	}
}
