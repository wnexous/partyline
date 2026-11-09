//go:build !linux && !darwin && !freebsd && !windows

package diskspace

// Free reports nothing on the platforms this file covers; the disk check is skipped there
// and a copy that does not fit fails with the filesystem's own error.
func Free(string) (int64, bool) { return 0, false }
