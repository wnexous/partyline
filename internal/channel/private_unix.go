//go:build unix

package channel

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// checkPrivate refuses a directory owned by another user or open to group or others.
func checkPrivate(dir string, info fs.FileInfo) error {
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("download directory %s belongs to another user; set TMPDIR to a private directory", dir)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("download directory %s is open to other users (mode %04o); remove it or set TMPDIR", dir, perm)
	}
	return nil
}
