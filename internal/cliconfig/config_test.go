package cliconfig_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/cliconfig"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
}

// writeFile writes a config file with the given file and directory modes.
func writeFile(t *testing.T, path, body string, mode, dirMode fs.FileMode) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(body), mode))
	require.NoError(t, os.Chmod(path, mode))
	require.NoError(t, os.Chmod(filepath.Dir(path), dirMode))
}

// ONB-04 / design: ~/.config/partyline/config.json, $XDG_CONFIG_HOME respected.
func TestPath(t *testing.T) {
	p, err := cliconfig.Path(env(map[string]string{"HOME": "/home/u"}))
	require.NoError(t, err)
	require.Equal(t, filepath.Join("/home/u", ".config", "partyline", "config.json"), p)

	p, err = cliconfig.Path(env(map[string]string{"HOME": "/home/u", "XDG_CONFIG_HOME": "/xdg"}))
	require.NoError(t, err)
	require.Equal(t, filepath.Join("/xdg", "partyline", "config.json"), p)

	_, err = cliconfig.Path(env(nil))
	require.ErrorIs(t, err, cliconfig.ErrNoPath)
}

// ONB-04: setup writes the file with mode 0600 (directory 0700) and Load reads it back.
func TestSaveWritesPrivateFile(t *testing.T) {
	skipOnWindows(t)
	path := filepath.Join(t.TempDir(), "cfg", "partyline", "config.json")
	want := cliconfig.Config{URL: "https://partyline.example.com", Token: "pl_agent_synthetic000"}
	require.NoError(t, cliconfig.Save(path, want))

	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, fs.FileMode(0o600), fi.Mode().Perm())
	di, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	require.Equal(t, fs.FileMode(0o700), di.Mode().Perm())

	got, err := cliconfig.Load(path)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// ONB-04: the write is atomic and leaves 0600 even over an existing file others could read;
// no temporary file stays behind.
func TestSaveReplacesExistingFileAs0600(t *testing.T) {
	skipOnWindows(t)
	path := filepath.Join(t.TempDir(), "partyline", "config.json")
	writeFile(t, path, `{"url":"http://old.example.com","token":"pl_agent_old"}`, 0o644, 0o755)

	want := cliconfig.Config{URL: "https://new.example.com", Token: "pl_agent_new"}
	require.NoError(t, cliconfig.Save(path, want))

	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, fs.FileMode(0o600), fi.Mode().Perm())
	di, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	require.Equal(t, fs.FileMode(0o700), di.Mode().Perm())
	got, err := cliconfig.Load(path)
	require.NoError(t, err)
	require.Equal(t, want, got)
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 1, "only config.json remains")
}

// Edge case (ONB-05): a config file with a mode other than 0600 is refused with the chmod fix;
// group-only and other-only bits count (lesson L-020).
func TestLoadRefusesFileNot0600(t *testing.T) {
	skipOnWindows(t)
	for _, mode := range []fs.FileMode{0o644, 0o640, 0o604, 0o660} {
		t.Run(mode.String(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "partyline", "config.json")
			writeFile(t, path, `{"url":"http://x.example.com","token":"pl_agent_x"}`, mode, 0o700)

			_, err := cliconfig.Load(path)
			var np *cliconfig.NotPrivateError
			require.ErrorAs(t, err, &np)
			require.Contains(t, err.Error(), "chmod 600 "+path)
		})
	}
}

// Edge case (ONB-05): a config directory others can enter is refused with the chmod fix.
func TestLoadRefusesOpenDirectory(t *testing.T) {
	skipOnWindows(t)
	for _, mode := range []fs.FileMode{0o750, 0o705} {
		t.Run(mode.String(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "partyline", "config.json")
			writeFile(t, path, `{"url":"http://x.example.com","token":"pl_agent_x"}`, 0o600, mode)

			_, err := cliconfig.Load(path)
			require.Error(t, err)
			require.Contains(t, err.Error(), "chmod 700 "+filepath.Dir(path))
		})
	}
}

// A missing file is reported as fs.ErrNotExist.
func TestLoadMissingFile(t *testing.T) {
	_, err := cliconfig.Load(filepath.Join(t.TempDir(), "partyline", "config.json"))
	require.True(t, errors.Is(err, fs.ErrNotExist), "err: %v", err)
}

// ONB-05: flags > environment > file; the file fills only what is missing.
func TestResolvePrecedence(t *testing.T) {
	skipOnWindows(t)
	home := t.TempDir()
	path := filepath.Join(home, ".config", "partyline", "config.json")
	writeFile(t, path, `{"url":"http://file.example.com","token":"pl_agent_file"}`, 0o600, 0o700)

	got, err := cliconfig.Resolve(cliconfig.Config{}, env(map[string]string{"HOME": home}))
	require.NoError(t, err)
	require.Equal(t, cliconfig.Config{URL: "http://file.example.com", Token: "pl_agent_file"}, got)

	// The same instance written differently still gets the saved token.
	got, err = cliconfig.Resolve(cliconfig.Config{}, env(map[string]string{"HOME": home, "PARTYLINE_URL": "HTTP://File.Example.com/"}))
	require.NoError(t, err)
	require.Equal(t, cliconfig.Config{URL: "HTTP://File.Example.com/", Token: "pl_agent_file"}, got)

	got, err = cliconfig.Resolve(cliconfig.Config{}, env(map[string]string{"HOME": home, "PARTYLINE_TOKEN": "pl_agent_env"}))
	require.NoError(t, err)
	require.Equal(t, cliconfig.Config{URL: "http://file.example.com", Token: "pl_agent_env"}, got)

	got, err = cliconfig.Resolve(cliconfig.Config{URL: "http://flag.example.com", Token: "pl_agent_flag"},
		env(map[string]string{"HOME": home, "PARTYLINE_URL": "http://env.example.com", "PARTYLINE_TOKEN": "pl_agent_env"}))
	require.NoError(t, err)
	require.Equal(t, cliconfig.Config{URL: "http://flag.example.com", Token: "pl_agent_flag"}, got)
}

// ONB-05: without a file the values stay empty and no error is reported; a file that is not
// private is an error when it is needed, and not read when flags and env provide everything.
func TestResolveMissingAndOpenFile(t *testing.T) {
	skipOnWindows(t)
	got, err := cliconfig.Resolve(cliconfig.Config{}, env(map[string]string{"HOME": t.TempDir()}))
	require.NoError(t, err)
	require.Equal(t, cliconfig.Config{}, got)

	home := t.TempDir()
	path := filepath.Join(home, ".config", "partyline", "config.json")
	writeFile(t, path, `{"url":"http://file.example.com","token":"pl_agent_file"}`, 0o644, 0o700)
	_, err = cliconfig.Resolve(cliconfig.Config{}, env(map[string]string{"HOME": home}))
	require.ErrorContains(t, err, "chmod 600 "+path)

	got, err = cliconfig.Resolve(cliconfig.Config{}, env(map[string]string{"HOME": home, "PARTYLINE_URL": "http://env.example.com", "PARTYLINE_TOKEN": "pl_agent_env"}))
	require.NoError(t, err)
	require.Equal(t, cliconfig.Config{URL: "http://env.example.com", Token: "pl_agent_env"}, got)
}

// ONB-08: the saved token is never paired with another instance: a URL from the environment
// or a flag that differs from the saved URL, without a token, is an error naming both URLs;
// with an explicit token it is used as given.
func TestResolveKeepsSavedTokenWithItsURL(t *testing.T) {
	skipOnWindows(t)
	home := t.TempDir()
	path := filepath.Join(home, ".config", "partyline", "config.json")
	writeFile(t, path, `{"url":"http://file.example.com","token":"pl_agent_file"}`, 0o600, 0o700)

	for _, tc := range []struct {
		name  string
		flags cliconfig.Config
		env   map[string]string
	}{
		{name: "env URL", env: map[string]string{"PARTYLINE_URL": "http://evil.example.com"}},
		{name: "flag URL", flags: cliconfig.Config{URL: "http://evil.example.com"}},
		{name: "other port", env: map[string]string{"PARTYLINE_URL": "http://file.example.com:8080"}},
		{name: "other scheme", env: map[string]string{"PARTYLINE_URL": "https://file.example.com"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := map[string]string{"HOME": home}
			for k, v := range tc.env {
				e[k] = v
			}
			got, err := cliconfig.Resolve(tc.flags, env(e))
			var mismatch *cliconfig.TokenURLMismatchError
			require.ErrorAs(t, err, &mismatch)
			require.ErrorContains(t, err, "the saved agent token is for http://file.example.com")
			require.Empty(t, got.Token, "the saved token is not returned")
		})
	}

	got, err := cliconfig.Resolve(cliconfig.Config{Token: "pl_agent_flag"}, env(map[string]string{"HOME": home, "PARTYLINE_URL": "http://other.example.com"}))
	require.NoError(t, err)
	require.Equal(t, cliconfig.Config{URL: "http://other.example.com", Token: "pl_agent_flag"}, got)
}
