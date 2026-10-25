//go:build !windows

package datalock

import (
	"errors"
	"os"
	"syscall"
)

// lock takes the exclusive lock of f without waiting. flock locks belong to the open file, not to the
// process, so a second Acquire of the same directory fails even inside one process.
func lock(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) // #nosec G115 -- a file descriptor fits an int
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

func unlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) // #nosec G115 -- a file descriptor fits an int
}

// isLocked tells that lock failed because another open file holds the lock, not for another reason.
func isLocked(err error) bool { return errors.Is(err, syscall.EWOULDBLOCK) }
