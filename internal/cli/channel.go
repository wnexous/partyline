package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/wnexous/partyline/internal/channel"
	"github.com/wnexous/partyline/internal/cliconfig"
)

// agentFlags are the connection flags of channel and listen.
type agentFlags struct {
	url, token, as, name, agent, project *string
}

func addAgentFlags(fs *flag.FlagSet) agentFlags {
	return agentFlags{
		url:     fs.String("url", "", "Partyline base URL (default $PARTYLINE_URL)"),
		token:   fs.String("token", "", "agent token (default $PARTYLINE_TOKEN; prefer the variable, flags show up in ps)"),
		as:      fs.String("as", "", "handle or v0.3 name of an AI without a folder to adopt for this folder (default $PARTYLINE_AGENT)"),
		name:    fs.String("name", "", "same as --as"),
		agent:   fs.String("agent", "", "same as --as"),
		project: fs.String("project", "", "ignored: an AI is identified by its folder"),
	}
}

// agentConn is what channel and listen connect with: the instance, the token and who the
// process is (channel.Identity).
type agentConn struct {
	url, token string
	id         channel.Identity
	// takeover is PARTYLINE_TAKEOVER=1: the person agreed to take the AI's live session over.
	takeover bool
}

// projectIgnored is the line printed when --project is given: AIs no longer belong to projects.
func projectIgnored(cmd string) string {
	return fmt.Sprintf("partyline %s: --project is ignored: an AI is identified by its folder", cmd)
}

// sessionFolder is the canonical folder a process works in (AID-07): PARTYLINE_FOLDER (set by
// the launcher) else the working directory, made absolute with symlinks resolved. It is empty
// when neither is known, which the server refuses with folder_required.
func sessionFolder(io stdio) string {
	dir := strings.TrimSpace(io.getenv("PARTYLINE_FOLDER"))
	if dir == "" {
		getwd := io.getwd
		if getwd == nil {
			getwd = os.Getwd
		}
		wd, err := getwd()
		if err != nil {
			return ""
		}
		dir = wd
	}
	folder, err := channel.CanonicalFolder(dir)
	if err != nil {
		return ""
	}
	return folder
}

// homeFolder is the canonical home folder of the user, empty when unknown.
func homeFolder(io stdio) string {
	home := strings.TrimSpace(io.getenv("HOME"))
	if home == "" {
		home = strings.TrimSpace(io.getenv("USERPROFILE"))
	}
	if home == "" {
		return ""
	}
	folder, err := channel.CanonicalFolder(home)
	if err != nil {
		return ""
	}
	return folder
}

// sessionIdentity gathers what a process tells the server about itself (AID-07): the
// hostname, the canonical folder (sessionFolder), the canonical home folder, the harness
// (PARTYLINE_HARNESS, set by the launchers) and the identity to adopt (adopt, else
// PARTYLINE_AGENT) (MIG-02).
func sessionIdentity(adopt string, io stdio) channel.Identity {
	id := channel.Identity{
		Folder: sessionFolder(io), Home: homeFolder(io),
		Harness: strings.TrimSpace(io.getenv("PARTYLINE_HARNESS")), Adopt: strings.TrimSpace(adopt),
	}
	if id.Adopt == "" {
		id.Adopt = strings.TrimSpace(io.getenv("PARTYLINE_AGENT"))
	}
	hostname := io.hostname
	if hostname == nil {
		hostname = os.Hostname
	}
	if h, err := hostname(); err == nil {
		id.Machine = strings.TrimSpace(h)
	}
	return id
}

// resolveAgent applies flags > PARTYLINE_URL, PARTYLINE_TOKEN > the config file written by
// `partyline setup`, and the session identity (sessionIdentity). It prints the problem and
// returns an exit code when URL or token are missing (2) or the config file cannot be used (1).
func resolveAgent(cmd string, f agentFlags, io stdio) (agentConn, int) {
	io = withLauncherEnv(io)
	creds, err := cliconfig.Resolve(cliconfig.Config{URL: *f.url, Token: *f.token}, io.getenv)
	if err != nil {
		fmt.Fprintf(io.stderr, "partyline %s: %v\n", cmd, err)
		return agentConn{}, 1
	}
	if creds.URL == "" || creds.Token == "" {
		fmt.Fprintf(io.stderr, "partyline %s: set PARTYLINE_URL and PARTYLINE_TOKEN (or --url and --token), or run partyline setup\n", cmd)
		return agentConn{}, 2
	}
	if strings.TrimSpace(*f.project) != "" {
		fmt.Fprintln(io.stderr, projectIgnored(cmd))
	}
	adopt := slices.DeleteFunc([]string{*f.as, *f.name, *f.agent}, func(v string) bool { return strings.TrimSpace(v) == "" })
	if len(adopt) == 0 {
		adopt = []string{""}
	}
	return agentConn{
		url: creds.URL, token: creds.Token, id: sessionIdentity(adopt[0], io),
		takeover: strings.TrimSpace(io.getenv("PARTYLINE_TAKEOVER")) == "1",
	}, 0
}

// toolsOnlyHarnesses are the --harness values of `channel --tools-only`: the harnesses that
// run the tools without the push socket. setup names the harness for Codex and Gemini, which
// start MCP servers without the launcher's PARTYLINE_HARNESS (ID-01).
var toolsOnlyHarnesses = []string{"codex", "gemini", "opencode", "pi"}

// channelCmd runs `partyline channel`: the MCP stdio server Claude Code loads as a channel.
// The session is the AI of the machine and folder it runs in (AID-01); --as adopts an AI
// without a folder (MIG-02). With --tools-only it serves the tools without the push socket
// (PUSH-02), acting as the same AI as `partyline listen` of the folder. With --harness omp
// the MCP instructions describe the omp extension's <partyline …> framing; with --tools-only,
// --harness names the session's harness and wins over PARTYLINE_HARNESS.
func channelCmd(ctx context.Context, args []string, io stdio) int {
	fs := flag.NewFlagSet("channel", flag.ContinueOnError)
	fs.SetOutput(io.stderr)
	f := addAgentFlags(fs)
	toolsOnly := fs.Bool("tools-only", false, "serve the tools without receiving messages (pair with partyline listen, or for harnesses without push)")
	harness := fs.String("harness", "", "the harness running the full channel when it is not Claude Code (omp); with --tools-only the session's harness (codex, gemini, opencode, pi)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	switch {
	case *harness == "":
	case *harness == channel.HarnessOmp && *toolsOnly:
		fmt.Fprintf(io.stderr, "partyline channel: --harness %s runs the full channel; drop --tools-only\n", *harness)
		return 2
	case *toolsOnly && !slices.Contains(toolsOnlyHarnesses, *harness):
		fmt.Fprintf(io.stderr, "partyline channel: unknown --harness %q with --tools-only (known: %s)\n", *harness, strings.Join(toolsOnlyHarnesses, ", "))
		return 2
	case !*toolsOnly && *harness != channel.HarnessOmp:
		fmt.Fprintf(io.stderr, "partyline channel: unknown --harness %q (known: %s)\n", *harness, channel.HarnessOmp)
		return 2
	}
	c, code := resolveAgent("channel", f, io)
	if code != 0 {
		return code
	}
	if *harness != "" {
		c.id.Harness = *harness
	}
	err := channel.Run(ctx, channel.Options{
		URL: c.url, Token: c.token, Machine: c.id.Machine, Folder: c.id.Folder, Home: c.id.Home, Adopt: c.id.Adopt,
		Harness: c.id.Harness, Version: buildVersion(), ToolsOnly: *toolsOnly, Takeover: c.takeover,
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
		URL: c.url, Token: c.token, Machine: c.id.Machine, Folder: c.id.Folder, Home: c.id.Home, Harness: c.id.Harness, Adopt: c.id.Adopt,
		Takeover: c.takeover, Stdin: io.stdin, Stdout: io.stdout, Stderr: io.stderr,
	})
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline listen:", err)
		return 1
	}
	return 0
}
