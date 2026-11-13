//go:build unix

package channel

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// mode is the permission bits of path, not following a symlink.
func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Lstat(path)
	require.NoError(t, err)
	return info.Mode()
}

// SR-08: the secret is written to <dir>/<ai id>/<NAME> with mode 0600 in folders of mode 0700, whatever the
// umask lets a new file have, and the path returned is that file.
func TestWriteSecretMakesAPrivateFile(t *testing.T) {
	old := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(old) })
	dir := filepath.Join(t.TempDir(), "partyline", "secrets")

	path, err := writeSecret(dir, "ai-1", "EXAMPLE_API_KEY", "value one")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "ai-1", "EXAMPLE_API_KEY"), path)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "value one", string(data))
	require.Equal(t, os.FileMode(0o600), mode(t, path).Perm())
	require.Equal(t, os.FileMode(0o700), mode(t, dir).Perm())
	require.Equal(t, os.FileMode(0o700), mode(t, filepath.Join(dir, "ai-1")).Perm())
}

// SR-08: a second value replaces the file as a whole, by a rename, and leaves no temporary file; a symlink
// planted at the file's path is replaced, and the file it pointed to is never written.
func TestWriteSecretReplacesTheFileAtomically(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secrets")
	path, err := writeSecret(dir, "ai-1", "EXAMPLE_API_KEY", "a long first value")
	require.NoError(t, err)
	before, err := os.Stat(path)
	require.NoError(t, err)

	_, err = writeSecret(dir, "ai-1", "EXAMPLE_API_KEY", "short")
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "short", string(data), "the new value, with nothing of the old one")
	after, err := os.Stat(path)
	require.NoError(t, err)
	require.False(t, os.SameFile(before, after), "a new file was renamed into place, the old one was not rewritten")
	entries, err := os.ReadDir(filepath.Join(dir, "ai-1"))
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temporary file is left behind")

	elsewhere := filepath.Join(t.TempDir(), "other")
	require.NoError(t, os.WriteFile(elsewhere, []byte("untouched"), 0o600))
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Symlink(elsewhere, path))
	_, err = writeSecret(dir, "ai-1", "EXAMPLE_API_KEY", "third")
	require.NoError(t, err)
	require.Zero(t, mode(t, path)&os.ModeSymlink, "the link was replaced by the file")
	data, err = os.ReadFile(elsewhere)
	require.NoError(t, err)
	require.Equal(t, "untouched", string(data))
}

// SR-08: a folder of the AI that others may open is made private; one that is a symlink is refused and
// nothing is written through it. The error names the folder and never the value.
func TestWriteSecretKeepsItsFoldersPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secrets")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "ai-1"), 0o755))
	require.NoError(t, os.Chmod(filepath.Join(dir, "ai-1"), 0o755))
	_, err := writeSecret(dir, "ai-1", "EXAMPLE_API_KEY", "v")
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), mode(t, filepath.Join(dir, "ai-1")).Perm())

	target := t.TempDir()
	require.NoError(t, os.Symlink(target, filepath.Join(dir, "ai-2")))
	_, err = writeSecret(dir, "ai-2", "EXAMPLE_API_KEY", "do-not-leak-value")
	require.Error(t, err)
	require.Contains(t, err.Error(), filepath.Join(dir, "ai-2")+" is not a plain directory")
	require.NotContains(t, err.Error(), "do-not-leak-value")
	entries, err := os.ReadDir(target)
	require.NoError(t, err)
	require.Empty(t, entries)
}

// SR-08: a name outside the pattern, an AI id that is a path and a session without a config folder are
// refused before anything is written.
func TestWriteSecretRefusesWhatCannotBeAFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secrets")
	for name, tc := range map[string][3]string{
		"a name with a path": {dir, "ai-1", "../KEY"},
		"a lower-case name":  {dir, "ai-1", "key"},
		"an id with a path":  {dir, "../ai", "KEY"},
		"no id yet":          {dir, "", "KEY"},
		"no config folder":   {"", "ai-1", "KEY"},
	} {
		_, err := writeSecret(tc[0], tc[1], tc[2], "v")
		require.Error(t, err, name)
	}
	_, err := os.Stat(dir)
	require.ErrorIs(t, err, os.ErrNotExist, "nothing was created")
}
