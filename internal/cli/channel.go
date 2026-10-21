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
	url, token, name, agent, project *string
}

func addAgentFlags(fs *flag.FlagSet) agentFlags {
	return agentFlags{
		url:     fs.String("url", "", "Partyline base URL (default $PARTYLINE_URL)"),
		token:   fs.String("token", "", "agent token (default $PARTYLINE_TOKEN; prefer the variable, flags show up in ps)"),
		name:    fs.String("name", "", "fixed agent name (default $PARTYLINE_AGENT; without one the session is temporary, named after project and harness)"),
		agent:   fs.String("agent", "", "same as --name"),
		project: fs.String("project", "", "project of the session (default $PARTYLINE_PROJECT, else the git repository or working directory name)"),
	}
}

// agentConn is what channel and listen connect with.
type agentConn struct {
	url, token string
	// agent is the explicit agent name; empty makes a temporary session (ID-01, ID-02).
	agent                     string
	project, harness, machine string
	sessionKey                string
}

// homeWarning is the line printed when a session starts in the home folder or / without
// --project or --name (ID-03).
func homeWarning(cmd string) string {
	return fmt.Sprintf("partyline %s: started in the home folder; using project %q (pass --project <name> or --name <agent> to choose)", cmd, channel.HomeProject)
}

// sessionIdentity resolves a session's agent name, project, harness and machine:
// --name/--agent > PARTYLINE_AGENT; --project > PARTYLINE_PROJECT > the git repository root,
// else the working directory, or "home" (with one warning line on stderr when neither a
// project nor a name was given) in the home folder or / (ID-01..03, CLI-01);
// PARTYLINE_HARNESS (set by the launchers); the hostname.
func sessionIdentity(cmd string, name, agent, project string, io stdio) (channel.Identity, bool) {
	id := channel.Identity{Name: strings.TrimSpace(name), Project: strings.TrimSpace(project)}
	if id.Name == "" {
		id.Name = strings.TrimSpace(agent)
	}
	if id.Name == "" {
		id.Name = strings.TrimSpace(io.getenv("PARTYLINE_AGENT"))
	}
	if id.Project == "" {
		id.Project = strings.TrimSpace(io.getenv("PARTYLINE_PROJECT"))
	}
	warned := false
	if id.Project == "" {
		getwd := io.getwd
		if getwd == nil {
			getwd = os.Getwd
		}
		if wd, err := getwd(); err == nil {
			if channel.IsHomeDir(wd, io.getenv("HOME")) {
				id.Project = channel.HomeProject
				if id.Name == "" {
					fmt.Fprintln(io.stderr, homeWarning(cmd))
					warned = true
				}
			} else {
				id.Project = channel.DeriveProject(wd)
			}
		}
	}
	id.Harness = strings.TrimSpace(io.getenv("PARTYLINE_HARNESS"))
	hostname := io.hostname
	if hostname == nil {
		hostname = os.Hostname
	}
	if h, err := hostname(); err == nil {
		id.Machine = h
	}
	return id, warned
}

// resolveAgent applies flags > PARTYLINE_URL, PARTYLINE_TOKEN > the config file written by
// `partyline setup`, and the session identity (sessionIdentity). It prints the problem and
// returns an exit code when URL or token are missing (2) or the config file cannot be used (1).
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
	id, _ := sessionIdentity(cmd, *f.name, *f.agent, *f.project, io)
	c := agentConn{url: creds.URL, token: creds.Token, agent: id.Name, project: id.Project, harness: id.Harness, machine: id.Machine}
	// listen and a tools-only channel spawned by the same harness process share the session
	// key: PARTYLINE_SESSION, else the parent process id.
	c.sessionKey = strings.TrimSpace(io.getenv("PARTYLINE_SESSION"))
	if c.sessionKey == "" {
		c.sessionKey = strconv.Itoa(os.Getppid())
	}
	return c, 0
}

// channelCmd runs `partyline channel`: the MCP stdio server Claude Code loads as a channel.
// Without --name the session is temporary: the server names it after its project and harness
// (ID-01). With --tools-only it serves the tools without the push socket
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
	if *harness != "" {
		c.harness = *harness
	}
	err := channel.Run(ctx, channel.Options{
		URL: c.url, Token: c.token, Agent: c.agent, Project: c.project, Machine: c.machine, Version: buildVersion(),
		ToolsOnly: *toolsOnly, SessionKey: c.sessionKey, Harness: c.harness,
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
		URL: c.url, Token: c.token, Agent: c.agent, Project: c.project, Harness: c.harness, Machine: c.machine,
		SessionKey: c.sessionKey,
		Stdin:      io.stdin, Stdout: io.stdout, Stderr: io.stderr,
	})
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline listen:", err)
		return 1
	}
	return 0
}
