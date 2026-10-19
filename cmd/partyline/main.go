// Command partyline runs the Partyline server and its helper commands.
package main

import (
	"os"

	"github.com/wnexous/partyline/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args, os.Stdout, os.Stderr))
}
