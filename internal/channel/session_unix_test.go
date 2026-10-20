//go:build unix

package channel

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// PUSH-02: listen's session file carries the agent name to the tools-only channel; it lives in
// a private directory and is ignored when anyone else could have written it.
func TestSessionNameRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	path := sessionFile(tmp, "4242")
	require.Equal(t, filepath.Join(tmp, "partyline", ".sessions", "4242"), path)
	require.Empty(t, readSessionName(path), "no file yet")

	require.NoError(t, writeSessionName(path, "proj-2"))
	require.Equal(t, "proj-2", readSessionName(path))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	dir, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), dir.Mode().Perm())

	require.NoError(t, writeSessionName(path, "proj-3"), "a reconnect under a new name replaces it")
	require.Equal(t, "proj-3", readSessionName(path))

	removeSessionName(path, "proj-2")
	require.Equal(t, "proj-3", readSessionName(path), "a stale owner does not remove a newer name")
	removeSessionName(path, "proj-3")
	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestSessionFileKeys(t *testing.T) {
	for _, key := range []string{"", ".", "..", "a/b", "../x", "a b"} {
		require.Empty(t, sessionFile("/tmp", key), "key %q", key)
	}
	require.NotEmpty(t, sessionFile("/tmp", "ses-1.A_b"))
}

func TestSessionNameRefusesUntrustedFiles(t *testing.T) {
	cases := map[string]func(t *testing.T, path string){
		"group-readable file": func(t *testing.T, path string) { require.NoError(t, os.Chmod(path, 0o640)) },
		"other-readable file": func(t *testing.T, path string) { require.NoError(t, os.Chmod(path, 0o604)) },
		"group-open directory": func(t *testing.T, path string) {
			require.NoError(t, os.Chmod(filepath.Dir(path), 0o750))
		},
		"other-open directory": func(t *testing.T, path string) {
			require.NoError(t, os.Chmod(filepath.Dir(path), 0o705))
		},
		"symlink": func(t *testing.T, path string) {
			target := filepath.Join(filepath.Dir(path), "target")
			require.NoError(t, os.Rename(path, target))
			require.NoError(t, os.Symlink(target, path))
		},
		"not an agent name": func(t *testing.T, path string) {
			require.NoError(t, os.WriteFile(path, []byte("../../etc\n"), 0o600))
		},
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			path := sessionFile(t.TempDir(), "4242")
			require.NoError(t, writeSessionName(path, "proj-2"))
			spoil(t, path)
			require.Empty(t, readSessionName(path))
		})
	}
}
