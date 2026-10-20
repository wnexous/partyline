package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wnexous/partyline/internal/channel"
	"github.com/wnexous/partyline/internal/cliconfig"
)

// channelCmd runs `partyline channel`: the MCP stdio server Claude Code loads as a channel.
// Flags win over PARTYLINE_URL, PARTYLINE_TOKEN and PARTYLINE_AGENT, which win over the config
// file written by `partyline setup`; the agent name defaults to the working directory's name
// (the channel slugifies it like the server).
func channelCmd(ctx context.Context, args []string, io stdio) int {
	fs := flag.NewFlagSet("channel", flag.ContinueOnError)
	fs.SetOutput(io.stderr)
	urlFlag := fs.String("url", "", "Partyline base URL (default $PARTYLINE_URL)")
	tokenFlag := fs.String("token", "", "agent token (default $PARTYLINE_TOKEN; prefer the variable, flags show up in ps)")
	agentFlag := fs.String("agent", "", "agent name (default $PARTYLINE_AGENT, else the working directory name)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	creds, err := cliconfig.Resolve(cliconfig.Config{URL: *urlFlag, Token: *tokenFlag}, io.getenv)
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline channel:", err)
		return 1
	}
	url, token := creds.URL, creds.Token
	agent := strings.TrimSpace(*agentFlag)
	if agent == "" {
		agent = strings.TrimSpace(io.getenv("PARTYLINE_AGENT"))
	}
	if url == "" || token == "" {
		fmt.Fprintln(io.stderr, "partyline channel: set PARTYLINE_URL and PARTYLINE_TOKEN (or --url and --token), or run partyline setup")
		return 2
	}
	if agent == "" {
		getwd := io.getwd
		if getwd == nil {
			getwd = os.Getwd
		}
		if wd, err := getwd(); err == nil {
			agent = filepath.Base(wd)
		}
	}
	err = channel.Run(ctx, channel.Options{
		URL: url, Token: token, Agent: agent, Version: buildVersion(),
		Stdin: io.stdin, Stdout: io.stdout, Stderr: io.stderr,
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(io.stderr, "partyline channel:", err)
		return 1
	}
	return 0
}
