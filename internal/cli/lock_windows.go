//go:build windows

package cli

import "os"

// lockExclusive is never needed on Windows, where automatic updates are off (AU-08).
func lockExclusive(*os.File) error { return nil }
