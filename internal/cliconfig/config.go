// Package cliconfig stores the machine's Partyline credentials for the command line tools
// (`setup` writes them; `channel`, `listen` and `doctor` read them) in
// $XDG_CONFIG_HOME/partyline/config.json, else ~/.config/partyline/config.json. The file must
// be private (mode 0600, directory without group or other access): it holds an agent token.
package cliconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// FileMode and DirMode are the permissions Save writes and Load requires (ONB-04, ONB-05).
const (
	FileMode fs.FileMode = 0o600
	DirMode  fs.FileMode = 0o700
)

// Config is what the config file holds.
type Config struct {
	// URL is the Partyline instance base URL.
	URL string `json:"url"`
	// Token is an agent token (pl_agent_...).
	Token string `json:"token"`
	// RunnerToken is the runner token `partyline runner` saves (pl_runner_..., CA-01). It is a
	// separate key from Token, so joining this machine as a runner never replaces its login.
	RunnerToken string `json:"runner_token,omitempty"`
}

// ErrNoPath means neither XDG_CONFIG_HOME nor HOME is set, so there is no config file.
var ErrNoPath = errors.New("no config path: set HOME or XDG_CONFIG_HOME")

// NotPrivateError refuses a config file or directory that other users may read. Its text
// names the chmod command that fixes it.
type NotPrivateError struct {
	Path string
	Mode fs.FileMode
	Dir  bool
}

func (e *NotPrivateError) Error() string {
	if e.Dir {
		return fmt.Sprintf("config directory %s has mode %04o; other users could read the token: run chmod 700 %s", e.Path, e.Mode, e.Path)
	}
	return fmt.Sprintf("config file %s has mode %04o, want 0600; other users could read the token: run chmod 600 %s", e.Path, e.Mode, e.Path)
}

// Path returns the config file path from the environment: $XDG_CONFIG_HOME/partyline/config.json
// when XDG_CONFIG_HOME is set, else $HOME/.config/partyline/config.json.
func Path(getenv func(string) string) (string, error) {
	if dir := strings.TrimSpace(getenv("XDG_CONFIG_HOME")); dir != "" {
		return filepath.Join(dir, "partyline", "config.json"), nil
	}
	if home := strings.TrimSpace(getenv("HOME")); home != "" {
		return filepath.Join(home, ".config", "partyline", "config.json"), nil
	}
	return "", ErrNoPath
}

// Load reads the config file at path. A missing file returns an error matching
// fs.ErrNotExist; a file or directory others can access returns *NotPrivateError without
// reading the file.
func Load(path string) (Config, error) {
	if err := checkPrivate(path); err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("config file %s: %w", path, err)
	}
	c.URL, c.Token = strings.TrimSpace(c.URL), strings.TrimSpace(c.Token)
	c.RunnerToken = strings.TrimSpace(c.RunnerToken)
	return c, nil
}

func checkPrivate(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil // no unix permission bits
	}
	if fi.Mode().Perm() != FileMode {
		return &NotPrivateError{Path: path, Mode: fi.Mode().Perm()}
	}
	dir := filepath.Dir(path)
	di, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if di.Mode().Perm()&0o077 != 0 {
		return &NotPrivateError{Path: dir, Mode: di.Mode().Perm(), Dir: true}
	}
	return nil
}

// Save writes c to path atomically (temporary file in the same directory, then rename) with
// mode 0600, creating the directory with mode 0700.
func Save(path string, c Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, DirMode); err != nil {
		return err
	}
	if err := os.Chmod(dir, DirMode); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // no-op after the rename
	if err := f.Chmod(FileMode); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Resolve returns the credentials a command uses (ONB-05): each value from flags when set,
// else from PARTYLINE_URL / PARTYLINE_TOKEN, else from the config file. The file is read only
// when a value is still missing; a missing file is not an error (the values stay empty), a
// file that is not private or not valid JSON is. The saved token is only ever sent to the
// saved URL (ONB-08): when flags or env name another instance and no token, Resolve fails
// instead of pairing the saved token with that URL.
func Resolve(flags Config, getenv func(string) string) (Config, error) {
	pick := func(flag, env string) string {
		if v := strings.TrimSpace(flag); v != "" {
			return v
		}
		return strings.TrimSpace(getenv(env))
	}
	c := Config{URL: pick(flags.URL, "PARTYLINE_URL"), Token: pick(flags.Token, "PARTYLINE_TOKEN")}
	if c.URL != "" && c.Token != "" {
		return c, nil
	}
	path, err := Path(getenv)
	if err != nil {
		return c, nil
	}
	file, err := Load(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if c.Token == "" && file.Token != "" && c.URL != "" && !SameURL(c.URL, file.URL) {
		return c, &TokenURLMismatchError{URL: c.URL, SavedURL: file.URL}
	}
	if c.URL == "" {
		c.URL = file.URL
	}
	if c.Token == "" {
		c.Token = file.Token
	}
	return c, nil
}

// TokenURLMismatchError refuses to send the saved token to an instance other than the one it
// was saved for (ONB-08).
type TokenURLMismatchError struct {
	URL, SavedURL string
}

func (e *TokenURLMismatchError) Error() string {
	return fmt.Sprintf("the saved agent token is for %s, not %s: set PARTYLINE_TOKEN (or --token) for %s, or run partyline setup --url %s",
		e.SavedURL, e.URL, e.URL, e.URL)
}

// SameURL reports whether a and b name the same instance: scheme and host compared without
// case, the path without trailing slashes.
func SameURL(a, b string) bool {
	ua, errA := url.Parse(strings.TrimSpace(a))
	ub, errB := url.Parse(strings.TrimSpace(b))
	if errA != nil || errB != nil || ua.Host == "" || ub.Host == "" {
		return false
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) && strings.EqualFold(ua.Host, ub.Host) &&
		strings.TrimRight(ua.Path, "/") == strings.TrimRight(ub.Path, "/")
}
