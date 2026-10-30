//go:build !linux && !darwin && !freebsd && !windows

package store

// freeSpace reports nothing on the platforms this file covers; the disk check is skipped there
// and a copy that does not fit fails with the filesystem's own error.
func freeSpace(string) (int64, bool) { return 0, false }
