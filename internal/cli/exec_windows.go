//go:build windows

package cli

import (
	"errors"
	"os"
	"os/exec"
)

// execProgram runs path as a child with this process's terminal (Windows has no exec) and
// returns its exit code.
func execProgram(path string, argv, env []string) (int, error) {
	cmd := exec.Command(path, argv[1:]...) // #nosec G204 -- the harness binary the user asked for
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}

// execKeepsPID: the harness runs as a child with its own process id.
const execKeepsPID = false
