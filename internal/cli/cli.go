// Package cli implements the partyline command line entry point.
package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/term"

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
  login                  approve this machine in the browser (or read the agent token from
                         stdin) and save the instance URL and token
  setup                  choose the harnesses Partyline registers itself in: a checklist on a
                         terminal, --harness a,b / --all-detected / --remove a,b without one;
                         --always also starts Partyline in sessions not started by
                         partyline <harness> (--always=false undoes it);
                         --url/--token/--token-stdin/--browser run the login step first;
                         --uninstall undoes it
  channel                run the Claude Code channel (MCP over stdio; PARTYLINE_URL, PARTYLINE_TOKEN or the config file);
                         without PARTYLINE_HARNESS (set by partyline <harness>) it answers with no tools;
                         --always runs it anyway, as partyline setup --always records;
                         --tools-only serves the tools without receiving messages;
                         --harness omp describes the omp extension's message framing;
                         with --tools-only, --harness codex|gemini|opencode|pi names the harness
  listen                 print incoming messages as JSON lines (used by the pi and OpenCode adapters)
  agents                 list the AIs you host, grouped by organization, with online markers
  orgs [slug]            list your organizations and where you stand in them; with an address, its positions
  hook codex-stop|gemini-after-agent
                         turn-end hook: continue the session with undelivered room messages
  doctor                 check this machine: binary, config file, server, token, harnesses, versions
  update                 replace this binary with the latest release after verifying its checksum;
                         --check only prints the current and latest versions (exit 1 when newer)
  healthcheck            exit 0 when the local server answers /healthz with 200
  version                print the build version

Start a session with Partyline (the arguments go to the harness unchanged):
  claude [args...]       Claude Code with the Partyline channel loaded (also: claudecode, claude-code)
  opencode [args...]     OpenCode
  pi [args...]           pi
  omp [args...]          oh-my-pi
  codex [args...]        Codex CLI
  gemini [args...]       Gemini CLI
A session is the AI of the folder it starts in. Put --take-over before the arguments to take the
AI's live session over from another one, and --as <handle> to adopt an AI that has no folder yet
(one from before folders).
A new AI joins your default organization. Put --org <slug> before the arguments to start it in
another one, --position <name> to give it a position there and --parent <@handle> when one of your
AIs starts it; PARTYLINE_ORG, PARTYLINE_POSITION and PARTYLINE_PARENT stand in for the flags. An AI
keeps the organization it was created in. partyline orgs lists the organizations and their positions.
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
	// pid and ppid are this process's id and its parent's (nil: os.Getpid, os.Getppid); the launcher
	// and the live id of a pi or OpenCode session use them (liveEnvVar).
	pid, ppid func() int
	// rawTerm offers raw terminal mode for the checklist menu; tests inject a fake.
	rawTerm rawTerminal
	// isTerminal reports whether fd is a terminal (nil: golang.org/x/term.IsTerminal);
	// termSize returns its width and height (nil: golang.org/x/term.GetSize).
	isTerminal func(fd int) bool
	termSize   func(fd int) (width, height int, err error)
	// now is the launcher's clock (nil: time.Now); its location formats a live session's start.
	now func() time.Time
	// sessionHTTP asks GET /agent/session before a launch (nil: a client with
	// launcherSessionTimeout).
	sessionHTTP *http.Client
	// updateHTTP fetches the release for `partyline update` (nil: a plain client; the redirect
	// policy applies either way); goos and goarch name the platform it fetches the archive for
	// (empty: runtime.GOOS and runtime.GOARCH).
	updateHTTP   *http.Client
	goos, goarch string
}

// Run executes the command named in args[1] and returns the process exit code. SIGINT and
// SIGTERM stop a running server gracefully.
func Run(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return execute(ctx, args, stdio{
		stdin: os.Stdin, stdout: stdout, stderr: stderr,
		getenv: os.Getenv, getwd: os.Getwd, launcherEnv: procLauncherEnv, rawTerm: osRawTerm{},
		isTerminal: term.IsTerminal,
		termSize:   term.GetSize,
	})
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
	case "login":
		return loginCmd(ctx, args[2:], io)
	case "channel":
		return channelCmd(ctx, args[2:], io)
	case "listen":
		return listenCmd(ctx, args[2:], io)
	case "hook":
		return hookCmd(ctx, args[2:], io)
	case "agents":
		return agentsCmd(ctx, args[2:], io)
	case "orgs":
		return orgsCmd(ctx, args[2:], io)
	case "update":
		return updateCmd(ctx, args[2:], io)
	case "doctor":
		return doctor(ctx, io)
	case "healthcheck":
		return healthcheck(ctx, io)
	default:
		fmt.Fprintf(io.stderr, "unknown command %q\n\n%s", args[1], usage)
		return 2
	}
}
