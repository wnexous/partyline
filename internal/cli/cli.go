// Package cli implements the partyline command line entry point.
package cli

import (
	"fmt"
	"io"
)

// Version is the build version, set at link time with
// -ldflags "-X github.com/wnexous/partyline/internal/cli.Version=v1.2.3".
var Version = "dev"

const usage = `Usage: partyline <command>

Commands:
  version   print the build version
`

// Run executes the command named in args[1] and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[1] {
	case "version":
		fmt.Fprintln(stdout, Version)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[1], usage)
		return 2
	}
}
