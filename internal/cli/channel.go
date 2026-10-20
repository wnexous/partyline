package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/wnexous/partyline/internal/channel"
	"github.com/wnexous/partyline/internal/cliconfig"
)

// channelCmd runs `partyline channel`: the MCP stdio server Claude Code loads as a channel.
// Flags win over PARTYLINE_URL, PARTYLINE_TOKEN and PARTYLINE_AGENT, which win over the config
// file written by `partyline setup`. Without a name the channel derives one from the git
// repository root, else the working directory (NAME-01), and asks the server for a free
// "<name>-N" when another session holds it (NAME-02).
func channelCmd(ctx context.Context, args []string, io stdio) int {
	fs := flag.NewFlagSet("channel", flag.ContinueOnError)
	fs.SetOutput(io.stderr)
	urlFlag := fs.String("url", "", "Partyline base URL (default $PARTYLINE_URL)")
	tokenFlag := fs.String("token", "", "agent token (default $PARTYLINE_TOKEN; prefer the variable, flags show up in ps)")
	agentFlag := fs.String("agent", "", "agent name (default $PARTYLINE_AGENT, else the git repository or working directory name)")
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
	derived := agent == ""
	if derived {
		getwd := io.getwd
		if getwd == nil {
			getwd = os.Getwd
		}
		if wd, err := getwd(); err == nil {
			agent = channel.DeriveName(wd)
		}
	}
	err = channel.Run(ctx, channel.Options{
		URL: url, Token: token, Agent: agent, UniqueName: derived, Version: buildVersion(),
		Stdin: io.stdin, Stdout: io.stdout, Stderr: io.stderr,
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(io.stderr, "partyline channel:", err)
		return 1
	}
	return 0
}
