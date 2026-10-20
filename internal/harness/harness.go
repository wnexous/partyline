// Package harness registers Partyline in the coding agents ("harnesses") installed on a
// machine (HAR-01, HAR-02, HAR-04): Claude Code, Codex CLI and Gemini CLI through their own
// CLIs, OpenCode, pi and omp by editing their config files. Every registration runs the
// partyline binary by its absolute path and keeps the harness's other settings as they were.
package harness

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ServerName is the name Partyline is registered under in every harness.
const ServerName = "partyline"

// Runner runs a harness CLI and returns its combined output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Env is the machine the registrars work on. Tests point Home at a temp dir and Run and
// LookPath at fakes.
type Env struct {
	// Home is the user's home directory.
	Home string
	// Getenv reads environment variables such as XDG_CONFIG_HOME and CODEX_HOME.
	Getenv func(string) string
	// Binary is the absolute path of the partyline binary the harnesses run.
	Binary string
	// Version is written into the extension files Partyline installs.
	Version string
	// Run runs harness CLIs; nil runs them for real.
	Run Runner
	// LookPath finds harness CLIs; nil means exec.LookPath.
	LookPath func(string) (string, error)
}

func (e Env) getenv(k string) string {
	if e.Getenv == nil {
		return ""
	}
	return strings.TrimSpace(e.Getenv(k))
}

func (e Env) lookPath(name string) (string, error) {
	if e.LookPath != nil {
		return e.LookPath(name)
	}
	return exec.LookPath(name)
}

func (e Env) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if e.Run != nil {
		return e.Run(ctx, name, args...)
	}
	path, err := e.lookPath(name)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, args...) // #nosec G204 -- fixed harness CLI and arguments
	// The CLI writes the config of the home the registrars work on.
	cmd.Env = append(os.Environ(), "HOME="+e.Home)
	return cmd.CombinedOutput()
}

// runCLI runs a harness CLI and turns a failure into an error that carries its output.
func (e Env) runCLI(ctx context.Context, name string, args ...string) error {
	out, err := e.run(ctx, name, args...)
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Entry is one registered item that runs the partyline binary: an MCP server or a hook.
type Entry struct {
	// What names the item, such as "MCP server in ~/.claude.json".
	What string
	// Binary is the program the item runs.
	Binary string
}

// Registrar registers Partyline in one harness.
type Registrar interface {
	// ID is the short name used by --harness (claude, codex, gemini, opencode, pi, omp).
	ID() string
	// Title is the harness's display name.
	Title() string
	// Detected reports whether the harness is installed.
	Detected() bool
	// Plan describes the exact changes Apply makes, one line each (HAR-01).
	Plan() []string
	// Apply registers Partyline, updating an existing registration in place (HAR-02).
	Apply(ctx context.Context) error
	// RemovePlan describes what Remove deletes; empty when nothing is registered.
	RemovePlan(ctx context.Context) []string
	// Remove deletes exactly what Apply added (HAR-04).
	Remove(ctx context.Context) error
	// Entries lists the registered items and the binary each runs (for doctor).
	Entries(ctx context.Context) ([]Entry, error)
}

// All returns the registrars of every supported harness.
func All(env Env) []Registrar {
	return []Registrar{
		&claude{env: env},
		&codex{env: env},
		&gemini{env: env},
		newOpenCode(env),
		newPi(env),
		newOmp(env),
	}
}

// shellQuote quotes s for a POSIX shell when it holds anything but safe characters.
func shellQuote(s string) string {
	safe := s != "" && strings.IndexFunc(s, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("/._-+:@%=,", r)
	}) < 0
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// hookCommand is the shell command of a Partyline hook.
func hookCommand(binary, hook string) string { return shellQuote(binary) + " hook " + hook }

// hookBinary returns the binary of a Partyline hook command, or "" when cmd is not one.
func hookBinary(cmd, hook string) string {
	bin, ok := strings.CutSuffix(strings.TrimSpace(cmd), " hook "+hook)
	if !ok {
		return ""
	}
	bin = strings.TrimSpace(bin)
	if len(bin) >= 2 && bin[0] == '\'' && bin[len(bin)-1] == '\'' {
		bin = strings.ReplaceAll(bin[1:len(bin)-1], `'\''`, "'")
	}
	if !strings.HasPrefix(filepath.Base(bin), "partyline") {
		return ""
	}
	return bin
}

// tildePath shortens a path under home to ~/… for display.
func (e Env) tildePath(p string) string {
	if rel, err := filepath.Rel(e.Home, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join("~", rel)
	}
	return p
}

// readDoc reads a JSON config file for editing; a missing file is an empty document.
func readDoc(path string) (*docEdit, bool, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- harness config path under the user's home
	if errors.Is(err, fs.ErrNotExist) {
		return &docEdit{path: path}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	d := &docEdit{path: path, data: data}
	if _, err := d.root(); err != nil {
		return nil, true, err
	}
	return d, true, nil
}

// backupSuffix names the copy of a harness config file setup keeps before changing it.
const backupSuffix = ".partyline.bak"

// writeDoc saves an edited config file with the previous mode (0644 for a new file) through
// a temp file renamed over path. With backup set, the previous content is first copied to
// <path>.partyline.bak.
func writeDoc(d *docEdit, existed, backup bool) error {
	mode := fs.FileMode(0o644)
	if existed {
		fi, err := os.Stat(d.path)
		if err != nil {
			return err
		}
		mode = fi.Mode().Perm()
		if backup {
			old, err := os.ReadFile(d.path)
			if err != nil {
				return err
			}
			if err := writeFileAtomic(d.path+backupSuffix, old, mode); err != nil {
				return err
			}
		}
	}
	return writeFileAtomic(d.path, d.data, mode)
}

// writeFileAtomic writes data to path through a temp file in the same directory.
func writeFileAtomic(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- harness config dirs are not secret
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// saveRemoval writes d after a removal, or deletes the file when only an empty object is
// left, and deletes the backup setup kept.
func saveRemoval(d *docEdit) error {
	var err error
	if d.empty() {
		err = os.Remove(d.path)
	} else {
		err = writeDoc(d, true, false)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Remove(d.path + backupSuffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
