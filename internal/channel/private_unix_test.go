//go:build unix

package channel

import (
	"io/fs"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// dirInfo is the file info of a directory with the given mode and owner.
type dirInfo struct {
	mode fs.FileMode
	uid  uint32
}

func (d dirInfo) Name() string       { return "builder" }
func (d dirInfo) Size() int64        { return 0 }
func (d dirInfo) Mode() fs.FileMode  { return fs.ModeDir | d.mode }
func (d dirInfo) ModTime() time.Time { return time.Time{} }
func (d dirInfo) IsDir() bool        { return true }
func (d dirInfo) Sys() any           { return &syscall.Stat_t{Uid: d.uid} }

// TOOL-06 hardening (S1): a download directory owned by another user is refused even when
// its mode is private; the user's own private directory is accepted.
func TestCheckPrivateRefusesAnotherUsersDirectory(t *testing.T) {
	me := uint32(os.Geteuid()) // #nosec G115 -- a uid fits in uint32
	err := checkPrivate("/tmp/partyline/builder", dirInfo{mode: 0o700, uid: me + 1})
	require.EqualError(t, err, "download directory /tmp/partyline/builder belongs to another user; set TMPDIR to a private directory")
	require.NoError(t, checkPrivate("/tmp/partyline/builder", dirInfo{mode: 0o700, uid: me}))
}
