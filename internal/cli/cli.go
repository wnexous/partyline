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
  channel                run the Claude Code channel (MCP over stdio; PARTYLINE_URL, PARTYLINE_TOKEN, PARTYLINE_AGENT or the config file);
                         --tools-only serves the tools without receiving messages;
                         --harness omp describes the omp extension's message framing
  listen                 print incoming messages as JSON lines (used by the pi and OpenCode adapters)
  agents                 list your agents by project with their online state; --all adds archived ones
  hook codex-stop|gemini-after-agent
                         turn-end hook: continue the session with undelivered room messages
  doctor                 check this machine: binary, config file, server, token, harnesses, versions
  healthcheck            exit 0 when the local server answers /healthz with 200
  version                print the build version

Start a session with Partyline (the arguments go to the harness unchanged):
  claude [args...]       Claude Code with the Partyline channel loaded (also: claudecode, claude-code)
  opencode [args...]     OpenCode
  pi [args...]           pi
  omp [args...]          oh-my-pi
  codex [args...]        Codex CLI
  gemini [args...]       Gemini CLI
Put --name <agent> (a fixed agent) and --project <name> before the arguments to choose the
session's agent and project; otherwise it is a temporary agent of the folder's project.
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
	// exec replaces the process with a program (nil: execProgram) and returns its exit code
	// where it cannot; environ is the environment a launcher passes on (nil: os.Environ).
	exec    func(path string, argv, env []string) (int, error)
	environ func() []string
	// launcherEnv returns the launcher variables of the nearest launched ancestor process
	// (withLauncherEnv); nil means none (Run sets procLauncherEnv).
	launcherEnv func() map[string]string
}

// Run executes the command named in args[1] and returns the process exit code. SIGINT and
// SIGTERM stop a running server gracefully.
func Run(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return execute(ctx, args, stdio{stdin: os.Stdin, stdout: stdout, stderr: stderr, getenv: os.Getenv, getwd: os.Getwd, launcherEnv: procLauncherEnv})
}

func execute(ctx context.Context, args []string, io stdio) int {
	if len(args) < 2 {
		fmt.Fprint(io.stderr, usage)
		return 2
	}
	if l, ok := findLauncher(args[1]); ok {
		return launch(ctx, l, args[2:], io)
	}
	switch args[1] {
	case "-h", "--help", "help":
		fmt.Fprint(io.stdout, usage)
		return 0
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
	case "listen":
		return listenCmd(ctx, args[2:], io)
	case "hook":
		return hookCmd(ctx, args[2:], io)
	case "agents":
		return agentsCmd(ctx, args[2:], io)
	case "doctor":
		return doctor(ctx, io)
	case "healthcheck":
		return healthcheck(ctx, io)
	default:
		fmt.Fprintf(io.stderr, "unknown command %q\n\n%s", args[1], usage)
		return 2
	}
}
