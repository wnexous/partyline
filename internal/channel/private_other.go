//go:build !unix

package channel

import "io/fs"

// checkPrivate has nothing to check where the temp directory is per user and Go reports no
// unix permission bits (Windows).
func checkPrivate(string, fs.FileInfo) error { return nil }
