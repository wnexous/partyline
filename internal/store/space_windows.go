//go:build windows

package store

import (
	"math"

	"golang.org/x/sys/windows"
)

// freeSpace returns the bytes available to the process on the volume of dir, and whether they
// are known.
func freeSpace(dir string) (int64, bool) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, false
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return 0, false
	}
	if free > math.MaxInt64 {
		return math.MaxInt64, true
	}
	// #nosec G115 -- the comparison above bounds the value to int64.
	return int64(free), true
}
