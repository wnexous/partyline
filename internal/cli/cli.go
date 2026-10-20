// Package cli implements the partyline command line entry point.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/wnexous/partyline/internal/harness"
)

// Version is the build version, set at link time with
// -ldflags "-X github.com/wnexous/partyline/internal/cli.Version=v1.2.3".
var Version = "dev"

const usage = `Usage: partyline <command>

Commands:
  serve                  run the server (configured with PARTYLINE_* environment variables)
  admin create-user      create an account; the password is read from stdin
  admin reset-password   set a new password and end the user's sessions; reads stdin
  setup                  save the instance URL and an agent token (or approve this machine in the
                         browser) and register Partyline in the installed harnesses; --uninstall undoes it
  channel                run the Claude Code channel (MCP over stdio; PARTYLINE_URL, PARTYLINE_TOKEN, PARTYLINE_AGENT or the config file)
  healthcheck            exit 0 when the local server answers /healthz with 200
  version                print the build version
`

// stdio holds what a command reads and writes; tests replace each part.
type stdio struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	getenv         func(string) string
	getwd          func() (string, error) // nil means os.Getwd
	hostname       func() (string, error) // nil means os.Hostname
	// sleep waits between device approval polls; nil means a real timer.
	sleep func(ctx context.Context, d time.Duration) bool
	// executable is the path of this binary (nil: os.Executable); runHarness and lookPath run
	// and find harness CLIs for setup (nil: for real).
	executable func() (string, error)
	runHarness harness.Runner
	lookPath   func(string) (string, error)
}

// Run executes the command named in args[1] and returns the process exit code. SIGINT and
// SIGTERM stop a running server gracefully.
func Run(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return execute(ctx, args, stdio{stdin: os.Stdin, stdout: stdout, stderr: stderr, getenv: os.Getenv, getwd: os.Getwd})
}

func execute(ctx context.Context, args []string, io stdio) int {
	if len(args) < 2 {
		fmt.Fprint(io.stderr, usage)
		return 2
	}
	switch args[1] {
	case "version":
		fmt.Fprintln(io.stdout, buildVersion())
		return 0
	case "serve":
		return serve(ctx, io)
	case "admin":
		return admin(ctx, args[2:], io)
	case "setup":
		return setupCmd(ctx, args[2:], io)
	case "channel":
		return channelCmd(ctx, args[2:], io)
	case "healthcheck":
		return healthcheck(ctx, io)
	default:
		fmt.Fprintf(io.stderr, "unknown command %q\n\n%s", args[1], usage)
		return 2
	}
}
