//go:build !windows

package cli

import "syscall"

// execProgram replaces the process with path; it returns only when that fails.
func execProgram(path string, argv, env []string) (int, error) {
	return 1, syscall.Exec(path, argv, env) // #nosec G204 -- the harness binary the user asked for
}
