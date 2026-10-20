//go:build unix

package channel

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TOOL-06 hardening (S1): the download directory must be private to the user. A symlinked
// <TempDir>/partyline, or an agent directory other users can open, is refused and nothing is
// written through it.
func TestToolDownloadRefusesSharedDirectory(t *testing.T) {
	t.Run("symlinked partyline dir", func(t *testing.T) {
		e := newChanEnv(t)
		o := e.options()
		elsewhere := t.TempDir()
		require.NoError(t, os.Symlink(elsewhere, filepath.Join(o.TempDir, "partyline")))
		s := e.connected(o)
		m := e.postFile("report.txt", "secret report")
		text, isErr := s.call("download_attachment", toolArgs("attachment_id", m.Attachments[0].ID))
		require.True(t, isErr, text)
		require.Contains(t, text, "download directory")
		entries, err := os.ReadDir(elsewhere)
		require.NoError(t, err)
		require.Empty(t, entries, "nothing is written through the symlink")
	})

	t.Run("agent dir open to others", func(t *testing.T) {
		e := newChanEnv(t)
		o := e.options()
		dir := filepath.Join(o.TempDir, "partyline", "builder")
		require.NoError(t, os.MkdirAll(dir, 0o700))
		require.NoError(t, os.Chmod(dir, 0o777)) // #nosec G302 -- the test plants a shared directory
		s := e.connected(o)
		m := e.postFile("report.txt", "secret report")
		text, isErr := s.call("download_attachment", toolArgs("attachment_id", m.Attachments[0].ID))
		require.True(t, isErr, text)
		require.Contains(t, text, "download directory")
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Empty(t, entries, "nothing is written into a shared directory")
	})
}

// TOOL-06 hardening (S1): a symlink planted at the download's file name is replaced by the
// downloaded file; its target is never written.
func TestToolDownloadReplacesPlantedSymlink(t *testing.T) {
	e := newChanEnv(t)
	o := e.options()
	s := e.connected(o)
	m := e.postFile("report.txt", "the real report")

	victim := filepath.Join(t.TempDir(), "victim")
	require.NoError(t, os.WriteFile(victim, []byte("untouched"), 0o600))
	dir := filepath.Join(o.TempDir, "partyline", "builder")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	target := filepath.Join(dir, m.Attachments[0].ID+"-report.txt")
	require.NoError(t, os.Symlink(victim, target))

	path, isErr := s.call("download_attachment", toolArgs("attachment_id", m.Attachments[0].ID))
	require.False(t, isErr, path)
	require.Equal(t, target, path)
	data, err := os.ReadFile(victim)
	require.NoError(t, err)
	require.Equal(t, "untouched", string(data), "the symlink target is not written")
	info, err := os.Lstat(path)
	require.NoError(t, err)
	require.True(t, info.Mode().IsRegular(), "the symlink is replaced by a regular file")
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "the real report", string(data))
}
