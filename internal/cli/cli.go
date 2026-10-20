// Package cli implements the partyline command line entry point.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// Version is the build version, set at link time with
// -ldflags "-X github.com/wnexous/partyline/internal/cli.Version=v1.2.3".
var Version = "dev"

const usage = `Usage: partyline <command>

Commands:
  serve                  run the server (configured with PARTYLINE_* environment variables)
  admin create-user      create an account; the password is read from stdin
  admin reset-password   set a new password and end the user's sessions; reads stdin
  healthcheck            exit 0 when the local server answers /healthz with 200
  version                print the build version
`

// stdio holds what a command reads and writes; tests replace each part.
type stdio struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	getenv         func(string) string
}

// Run executes the command named in args[1] and returns the process exit code. SIGINT and
// SIGTERM stop a running server gracefully.
func Run(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return execute(ctx, args, stdio{stdin: os.Stdin, stdout: stdout, stderr: stderr, getenv: os.Getenv})
}

func execute(ctx context.Context, args []string, io stdio) int {
	if len(args) < 2 {
		fmt.Fprint(io.stderr, usage)
		return 2
	}
	switch args[1] {
	case "version":
		fmt.Fprintln(io.stdout, Version)
		return 0
	case "serve":
		return serve(ctx, io)
	case "admin":
		return admin(ctx, args[2:], io)
	case "healthcheck":
		return healthcheck(ctx, io)
	default:
		fmt.Fprintf(io.stderr, "unknown command %q\n\n%s", args[1], usage)
		return 2
	}
}
