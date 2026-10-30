//go:build linux || darwin || freebsd

package store

import (
	"math"

	"golang.org/x/sys/unix"
)

// freeSpace returns the bytes available to the process on the filesystem of dir, and whether
// they are known.
func freeSpace(dir string) (int64, bool) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, false
	}
	// #nosec G115 -- statfs reports block counts and sizes, never negative values.
	avail, block := uint64(st.Bavail), uint64(st.Bsize)
	if block == 0 || avail > math.MaxInt64/block {
		return math.MaxInt64, true
	}
	// #nosec G115 -- the division above bounds the product to int64.
	return int64(avail * block), true
}
