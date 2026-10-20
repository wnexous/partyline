package attachments

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "blobs")
	s, err := Open(dir)
	require.NoError(t, err)
	return s, dir
}

// files lists every regular file under dir (relative paths).
func files(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, rel)
		}
		return nil
	}))
	return out
}

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// The blob is addressed by its SHA-256 under <aa>/<sha> and reads back unchanged.
func TestPutAndOpen(t *testing.T) {
	s, dir := newStore(t)
	sha, size, err := s.Put(strings.NewReader("hello blob"), 1024)
	require.NoError(t, err)
	require.Equal(t, hashOf("hello blob"), sha)
	require.Equal(t, int64(10), size)
	require.Equal(t, []string{filepath.Join(sha[:2], sha)}, files(t, dir))

	f, err := s.Open(sha)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	require.NoError(t, err)
	require.Equal(t, "hello blob", string(data))
}

// Same content is stored once.
func TestSameContentStoredOnce(t *testing.T) {
	s, dir := newStore(t)
	a, _, err := s.Put(strings.NewReader("same"), 100)
	require.NoError(t, err)
	b, _, err := s.Put(strings.NewReader("same"), 100)
	require.NoError(t, err)
	require.Equal(t, a, b)
	c, _, err := s.Put(strings.NewReader("other"), 100)
	require.NoError(t, err)
	require.NotEqual(t, a, c)
	require.Len(t, files(t, dir), 2)
}

// SEC-03: content of exactly the limit is accepted; limit+1 bytes is ErrTooLarge and leaves no file.
func TestPutLimit(t *testing.T) {
	s, dir := newStore(t)
	_, size, err := s.Put(strings.NewReader(strings.Repeat("x", 64)), 64)
	require.NoError(t, err)
	require.Equal(t, int64(64), size)
	require.Len(t, files(t, dir), 1)

	_, _, err = s.Put(strings.NewReader(strings.Repeat("y", 65)), 64)
	require.ErrorIs(t, err, ErrTooLarge)
	require.Len(t, files(t, dir), 1, "no partial or temp file is left behind")
}

// Files are 0600 and directories 0700.
func TestPermissions(t *testing.T) {
	s, dir := newStore(t)
	sha, _, err := s.Put(strings.NewReader("secret-ish"), 100)
	require.NoError(t, err)

	for _, d := range []string{dir, filepath.Join(dir, sha[:2])} {
		info, err := os.Stat(d)
		require.NoError(t, err)
		require.Equal(t, fs.FileMode(0o700), info.Mode().Perm(), d)
	}
	info, err := os.Stat(filepath.Join(dir, sha[:2], sha))
	require.NoError(t, err)
	require.Equal(t, fs.FileMode(0o600), info.Mode().Perm())
}

// Delete removes the blob; missing and malformed hashes are not found and never escape the dir.
func TestDeleteAndNotFound(t *testing.T) {
	s, dir := newStore(t)
	sha, _, err := s.Put(strings.NewReader("bye"), 100)
	require.NoError(t, err)

	require.NoError(t, s.Delete(sha))
	require.Empty(t, files(t, dir))
	_, err = s.Open(sha)
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, s.Delete(sha), "deleting a missing blob is fine")

	for _, bad := range []string{"", "../secret.key", strings.Repeat("G", 64), strings.ToUpper(sha)} {
		_, err := s.Open(bad)
		require.ErrorIs(t, err, ErrNotFound, bad)
	}
}
