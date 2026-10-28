package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/cliconfig"
)

// agentsTimeout bounds the request of `partyline agents`.
const agentsTimeout = 15 * time.Second

// agentsCmd runs `partyline agents`: it prints the AIs the token's user hosts, grouped by the
// organization they belong to, with online markers (CLI-02, ORG-02). Machines are not shown (AD-022).
func agentsCmd(ctx context.Context, args []string, io stdio) int {
	fs := flag.NewFlagSet("agents", flag.ContinueOnError)
	fs.SetOutput(io.stderr)
	url := fs.String("url", "", "Partyline base URL (default $PARTYLINE_URL)")
	token := fs.String("token", "", "agent token (default $PARTYLINE_TOKEN)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	creds, err := cliconfig.Resolve(cliconfig.Config{URL: *url, Token: *token}, io.getenv)
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline agents:", err)
		return 1
	}
	if creds.URL == "" || creds.Token == "" {
		fmt.Fprintln(io.stderr, "partyline agents: set PARTYLINE_URL and PARTYLINE_TOKEN (or --url and --token), or run partyline setup")
		return 2
	}
	list, err := fetchHostedAgents(ctx, creds)
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline agents:", err)
		return 1
	}
	if len(list) == 0 {
		fmt.Fprintln(io.stdout, "No AIs yet: start a session with partyline claude")
		return 0
	}
	printAgents(io.stdout, list, time.Now())
	return 0
}

// fetchHostedAgents reads the AIs the token's user hosts from GET /api/v1/agent/agents.
func fetchHostedAgents(ctx context.Context, creds cliconfig.Config) ([]dto.Agent, error) {
	list, err := getFromAgentAPI[dto.List[dto.Agent]](ctx, creds, "agents")
	return list.Items, err
}

// agentGroup is the AIs of one organization, as `partyline agents` prints them.
type agentGroup struct {
	org    *dto.ContactOrg // nil for the AIs that have no organization
	agents []dto.Agent
}

// groupAgents groups the AIs by their home organization, the organizations by name and the AIs that
// have none last, each group's AIs by name.
func groupAgents(list []dto.Agent) []agentGroup {
	byOrg := map[string]*agentGroup{}
	var groups []*agentGroup
	for _, a := range list {
		id := ""
		if a.Organization != nil {
			id = a.Organization.ID
		}
		g, ok := byOrg[id]
		if !ok {
			g = &agentGroup{org: a.Organization}
			byOrg[id] = g
			groups = append(groups, g)
		}
		g.agents = append(g.agents, a)
	}
	slices.SortStableFunc(groups, func(a, b *agentGroup) int {
		switch {
		case a.org == nil && b.org == nil:
			return 0
		case a.org == nil:
			return 1
		case b.org == nil:
			return -1
		}
		if c := strings.Compare(strings.ToLower(a.org.Name), strings.ToLower(b.org.Name)); c != 0 {
			return c
		}
		return strings.Compare(a.org.ID, b.org.ID)
	})
	out := make([]agentGroup, len(groups))
	for i, g := range groups {
		slices.SortStableFunc(g.agents, func(a, b dto.Agent) int {
			if c := strings.Compare(strings.ToLower(a.Intelligence.DisplayName), strings.ToLower(b.Intelligence.DisplayName)); c != 0 {
				return c
			}
			return strings.Compare(a.ID, b.ID)
		})
		out[i] = *g
	}
	return out
}

// orgTitle is the heading of a group: the organization's name, with its address in parentheses when the
// name is not already the address.
func orgTitle(o *dto.ContactOrg) string {
	switch {
	case o == nil:
		return "No organization"
	case o.Slug == "" || strings.EqualFold(o.Name, o.Slug):
		return o.Name
	}
	return o.Name + " (" + o.Slug + ")"
}

// printAgents writes each organization with the AIs it holds, two spaces in, in tab-aligned columns:
// online marker and name, handle, app, state and the folder as `~` reads it (CLI-02). Trailing empty
// columns are left out, so an AI without a folder has no trailing spaces.
func printAgents(out io.Writer, list []dto.Agent, now time.Time) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, g := range groupAgents(list) {
		fmt.Fprintln(w, orgTitle(g.org))
		for _, a := range g.agents {
			cells := []string{
				"  " + onlineMark(a.Online) + " " + a.Intelligence.DisplayName,
				handle(a), appLabel(a.Harness), seenState(a, now), a.FolderLabel,
			}
			for len(cells) > 1 && cells[len(cells)-1] == "" {
				cells = cells[:len(cells)-1]
			}
			fmt.Fprintln(w, strings.Join(cells, "\t"))
			for _, sess := range parallelSessions(a) {
				fmt.Fprintln(w, "      ↳ :"+sess.Label+"\t"+handle(a)+":"+sess.Label+"\t"+appLabel(sess.Harness)+"\tsince "+sess.ConnectedAt.Local().Format("15:04"))
			}
		}
	}
	_ = w.Flush()
}

// parallelSessions are the labelled live sessions of an online AI, one row each under the AI with
// the address that reaches it; the principal is the AI's own row. An AI without a handle has no
// address to show, so its sessions are left out.
func parallelSessions(a dto.Agent) []dto.SessionInfo {
	if !a.Online || a.Intelligence.Handle == "" {
		return nil
	}
	var out []dto.SessionInfo
	for _, s := range a.Sessions {
		if s.Label != "" {
			out = append(out, s)
		}
	}
	return out
}

// onlineMark is the online marker of a line.
func onlineMark(online bool) string {
	if online {
		return "●"
	}
	return "○"
}

// handle is the @handle column; an AI without a handle leaves it empty.
func handle(a dto.Agent) string {
	if a.Intelligence.Handle == "" {
		return ""
	}
	return "@" + a.Intelligence.Handle
}

// appLabel is the readable harness name ("Claude Code"); an AI that never named one leaves the
// column empty.
func appLabel(app string) string {
	if app == "" {
		return ""
	}
	return agents.HarnessLabel(app)
}

// seenState is "online", the relative last seen, or empty for an AI never seen.
func seenState(a dto.Agent, now time.Time) string {
	if a.Online {
		return "online"
	}
	return relativeLastSeen(a.LastSeenAt, now)
}

// relativeLastSeen renders how long ago the AI was last seen: now, Nm ago, Nh ago or Nd ago.
// It is empty when the AI was never seen.
func relativeLastSeen(seen *time.Time, now time.Time) string {
	if seen == nil {
		return ""
	}
	d := now.Sub(*seen)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
}
