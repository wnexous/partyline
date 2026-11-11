//go:build !windows

package cli

import (
	"os"
	"syscall"
)

// lockExclusive waits for the exclusive lock of f; closing f releases it.
func lockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) // #nosec G115 -- a file descriptor fits an int
}
