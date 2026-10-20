package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/wnexous/partyline/internal/channel"
	"github.com/wnexous/partyline/internal/cliconfig"
)

// agentFlags are the connection flags of channel and listen.
type agentFlags struct {
	url, token, agent *string
}

func addAgentFlags(fs *flag.FlagSet) agentFlags {
	return agentFlags{
		url:   fs.String("url", "", "Partyline base URL (default $PARTYLINE_URL)"),
		token: fs.String("token", "", "agent token (default $PARTYLINE_TOKEN; prefer the variable, flags show up in ps)"),
		agent: fs.String("agent", "", "agent name (default $PARTYLINE_AGENT, else the git repository or working directory name)"),
	}
}

// agentConn is what channel and listen connect with.
type agentConn struct {
	url, token, agent string
	derived           bool // the name came from the directory (NAME-01), not from the user
	sessionKey        string
}

// resolveAgent applies flags > PARTYLINE_URL, PARTYLINE_TOKEN, PARTYLINE_AGENT > the config
// file written by `partyline setup`; without a name it derives one from the git repository
// root, else the working directory (NAME-01). It prints the problem and returns an exit code
// when URL or token are missing (2) or the config file cannot be used (1).
func resolveAgent(cmd string, f agentFlags, io stdio) (agentConn, int) {
	creds, err := cliconfig.Resolve(cliconfig.Config{URL: *f.url, Token: *f.token}, io.getenv)
	if err != nil {
		fmt.Fprintf(io.stderr, "partyline %s: %v\n", cmd, err)
		return agentConn{}, 1
	}
	if creds.URL == "" || creds.Token == "" {
		fmt.Fprintf(io.stderr, "partyline %s: set PARTYLINE_URL and PARTYLINE_TOKEN (or --url and --token), or run partyline setup\n", cmd)
		return agentConn{}, 2
	}
	c := agentConn{url: creds.URL, token: creds.Token, agent: strings.TrimSpace(*f.agent)}
	if c.agent == "" {
		c.agent = strings.TrimSpace(io.getenv("PARTYLINE_AGENT"))
	}
	if c.agent == "" {
		c.derived = true
		getwd := io.getwd
		if getwd == nil {
			getwd = os.Getwd
		}
		if wd, err := getwd(); err == nil {
			c.agent = channel.DeriveName(wd)
		}
	}
	// listen and a tools-only channel spawned by the same harness process share the session
	// key: PARTYLINE_SESSION, else the parent process id.
	c.sessionKey = strings.TrimSpace(io.getenv("PARTYLINE_SESSION"))
	if c.sessionKey == "" {
		c.sessionKey = strconv.Itoa(os.Getppid())
	}
	return c, 0
}

// channelCmd runs `partyline channel`: the MCP stdio server Claude Code loads as a channel.
// Without a name it asks the server for a free "<name>-N" when another session holds the
// derived one (NAME-02). With --tools-only it serves the tools without the push socket
// (PUSH-02), acting as the agent `partyline listen` of the same harness recorded. With
// --harness omp the MCP instructions describe the omp extension's <partyline …> framing.
func channelCmd(ctx context.Context, args []string, io stdio) int {
	fs := flag.NewFlagSet("channel", flag.ContinueOnError)
	fs.SetOutput(io.stderr)
	f := addAgentFlags(fs)
	toolsOnly := fs.Bool("tools-only", false, "serve the tools without receiving messages (pair with partyline listen, or for harnesses without push)")
	harness := fs.String("harness", "", "the harness running the full channel when it is not Claude Code (omp)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	switch {
	case *harness != "" && *harness != channel.HarnessOmp:
		fmt.Fprintf(io.stderr, "partyline channel: unknown --harness %q (known: %s)\n", *harness, channel.HarnessOmp)
		return 2
	case *harness != "" && *toolsOnly:
		fmt.Fprintf(io.stderr, "partyline channel: --harness %s runs the full channel; drop --tools-only\n", *harness)
		return 2
	}
	c, code := resolveAgent("channel", f, io)
	if code != 0 {
		return code
	}
	err := channel.Run(ctx, channel.Options{
		URL: c.url, Token: c.token, Agent: c.agent, UniqueName: c.derived && !*toolsOnly, Version: buildVersion(),
		ToolsOnly: *toolsOnly, SessionKey: c.sessionKey, Harness: *harness,
		Stdin: io.stdin, Stdout: io.stdout, Stderr: io.stderr,
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(io.stderr, "partyline channel:", err)
		return 1
	}
	return 0
}

// listenCmd runs `partyline listen`: it holds the agent WebSocket and prints one JSON line per
// incoming message (PUSH-01) for the pi and OpenCode adapters. It ends when stdin closes.
func listenCmd(ctx context.Context, args []string, io stdio) int {
	fs := flag.NewFlagSet("listen", flag.ContinueOnError)
	fs.SetOutput(io.stderr)
	f := addAgentFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	c, code := resolveAgent("listen", f, io)
	if code != 0 {
		return code
	}
	err := channel.Listen(ctx, channel.ListenOptions{
		URL: c.url, Token: c.token, Agent: c.agent, UniqueName: c.derived, SessionKey: c.sessionKey,
		Stdin: io.stdin, Stdout: io.stdout, Stderr: io.stderr,
	})
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline listen:", err)
		return 1
	}
	return 0
}
