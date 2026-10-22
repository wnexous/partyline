package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/cliconfig"
)

// agentsTimeout bounds the request of `partyline agents`.
const agentsTimeout = 15 * time.Second

// agentsCmd runs `partyline agents`: it prints the AIs of the token's owner as one machine ›
// folder tree with online markers (TREE-07).
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
	tree, err := fetchAgentTree(ctx, creds)
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline agents:", err)
		return 1
	}
	if len(tree.Machines) == 0 && len(tree.Unlinked) == 0 {
		fmt.Fprintln(io.stdout, "No AIs yet: start a session with partyline claude")
		return 0
	}
	printAgentTree(io.stdout, tree, time.Now())
	return 0
}

func fetchAgentTree(ctx context.Context, creds cliconfig.Config) (dto.AgentContactsTree, error) {
	ctx, cancel := context.WithTimeout(ctx, agentsTimeout)
	defer cancel()
	u := strings.TrimRight(creds.URL, "/") + "/api/v1/agent/contacts/tree"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return dto.AgentContactsTree{}, err
	}
	req.Header.Set("Authorization", "Bearer "+creds.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return dto.AgentContactsTree{}, fmt.Errorf("cannot reach %s", creds.URL)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		var body dto.ErrorBody
		if json.NewDecoder(resp.Body).Decode(&body) == nil && body.Error.Message != "" {
			return dto.AgentContactsTree{}, fmt.Errorf("the server answered %d: %s", resp.StatusCode, body.Error.Message)
		}
		return dto.AgentContactsTree{}, fmt.Errorf("the server answered %d", resp.StatusCode)
	}
	var tree dto.AgentContactsTree
	if err := json.NewDecoder(resp.Body).Decode(&tree); err != nil {
		return dto.AgentContactsTree{}, fmt.Errorf("unexpected answer: %w", err)
	}
	return tree, nil
}

// printAgentTree writes each machine with its AIs as a folder tree and the unlinked identities
// last, two spaces per level and tab-aligned columns (TREE-07).
func printAgentTree(out io.Writer, tree dto.AgentContactsTree, now time.Time) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, m := range tree.Machines {
		fmt.Fprintln(w, machineTitle(m))
		printTreeNodes(w, m.Nodes, 0, now)
	}
	if len(tree.Unlinked) > 0 {
		fmt.Fprintln(w, "Unlinked")
		for _, a := range tree.Unlinked {
			fmt.Fprintf(w, "  %s %s\t%s\n", onlineMark(a.Online), a.Intelligence.DisplayName, handle(a))
		}
	}
	_ = w.Flush()
}

// printTreeNodes writes the nodes of one machine level by level, indenting each level by two
// more spaces; online AIs and their last seen fill the state column.
func printTreeNodes(w io.Writer, nodes []dto.TreeNode, depth int, now time.Time) {
	for _, n := range nodes {
		name := n.AI.Intelligence.DisplayName
		if n.Path != "" {
			name = n.Path + " › " + name
		}
		fmt.Fprintf(w, "%s%s %s\t%s\t%s\t%s\t%s\n",
			strings.Repeat("  ", depth+1), onlineMark(n.AI.Online), name, handle(n.AI),
			appLabel(n.AI.App), seenState(n.AI, now), n.AI.FolderLabel)
		printTreeNodes(w, n.Children, depth+1, now)
	}
}

// machineTitle is the machine's label, or its hostname when it has none, with the hostname in
// parentheses when a label is set.
func machineTitle(m dto.MachineTree) string {
	if m.Label == "" || m.Label == m.Hostname {
		return m.Hostname
	}
	return m.Label + " (" + m.Hostname + ")"
}

// onlineMark is the tree's online marker.
func onlineMark(online bool) string {
	if online {
		return "●"
	}
	return "○"
}

// handle is the @handle column; an AI without a handle leaves it empty.
func handle(a dto.AIContact) string {
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
func seenState(a dto.AIContact, now time.Time) string {
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
