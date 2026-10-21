package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/cliconfig"
)

// agentsTimeout bounds the request of `partyline agents`.
const agentsTimeout = 15 * time.Second

// noProject heads the agents of no project in `partyline agents`.
const noProject = "No project"

// agentsCmd runs `partyline agents [--all]`: it lists the agents of the token's owner grouped
// by project, projects with an online session first, each agent with its online state,
// readable and internal name, harness, machine and kind (CLI-02). --all adds archived agents.
func agentsCmd(ctx context.Context, args []string, io stdio) int {
	fs := flag.NewFlagSet("agents", flag.ContinueOnError)
	fs.SetOutput(io.stderr)
	url := fs.String("url", "", "Partyline base URL (default $PARTYLINE_URL)")
	token := fs.String("token", "", "agent token (default $PARTYLINE_TOKEN)")
	all := fs.Bool("all", false, "include archived agents")
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
	list, err := fetchAgents(ctx, creds, *all)
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline agents:", err)
		return 1
	}
	if len(list) == 0 {
		fmt.Fprintln(io.stdout, "No agents yet: start a session with partyline claude (see partyline --help).")
		return 0
	}
	printAgents(io, list)
	return 0
}

func fetchAgents(ctx context.Context, creds cliconfig.Config, all bool) ([]dto.Agent, error) {
	ctx, cancel := context.WithTimeout(ctx, agentsTimeout)
	defer cancel()
	u := strings.TrimRight(creds.URL, "/") + "/api/v1/agent/agents"
	if all {
		u += "?include_archived=true"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+creds.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s", creds.URL)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		var body dto.ErrorBody
		if json.NewDecoder(resp.Body).Decode(&body) == nil && body.Error.Message != "" {
			return nil, fmt.Errorf("the server answered %d: %s", resp.StatusCode, body.Error.Message)
		}
		return nil, fmt.Errorf("the server answered %d", resp.StatusCode)
	}
	var list dto.List[dto.Agent]
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("unexpected answer: %w", err)
	}
	return list.Items, nil
}

type agentGroup struct {
	name   string
	agents []dto.Agent
	online bool
}

// printAgents writes one block per project: the project name, then one line per agent
// (online ones first, then by readable name). Projects with an online agent come first, then
// by name; agents without a project come last.
func printAgents(io stdio, list []dto.Agent) {
	byProject := map[string]*agentGroup{}
	var groups []*agentGroup
	for _, a := range list {
		key, name := "", noProject
		if a.Project != nil {
			key, name = a.Project.ID, a.Project.Name
		}
		g := byProject[key]
		if g == nil {
			g = &agentGroup{name: name}
			byProject[key] = g
			groups = append(groups, g)
		}
		g.agents = append(g.agents, a)
		g.online = g.online || a.Online
	}
	slices.SortStableFunc(groups, func(x, y *agentGroup) int {
		return cmp.Or(cmp.Compare(rank(x), rank(y)), cmp.Compare(strings.ToLower(x.name), strings.ToLower(y.name)))
	})
	w := tabwriter.NewWriter(io.stdout, 0, 0, 2, ' ', 0)
	for i, g := range groups {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, g.name)
		slices.SortStableFunc(g.agents, func(x, y dto.Agent) int {
			return cmp.Or(cmp.Compare(onlineRank(x), onlineRank(y)), cmp.Compare(strings.ToLower(x.DisplayName), strings.ToLower(y.DisplayName)))
		})
		for _, a := range g.agents {
			state := "offline"
			if a.Online {
				state = "online"
			}
			notes := []string{string(a.Kind)}
			if a.Archived {
				notes = append(notes, "archived")
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\n", state, a.DisplayName, a.Name, a.Harness, a.Machine, strings.Join(notes, ", "))
		}
		_ = w.Flush()
	}
	_ = w.Flush()
}

// rank orders groups: projects with an online agent, other projects, then no project.
func rank(g *agentGroup) int {
	switch {
	case g.name == noProject && len(g.agents) > 0 && g.agents[0].Project == nil:
		return 2
	case g.online:
		return 0
	default:
		return 1
	}
}

func onlineRank(a dto.Agent) int {
	if a.Online {
		return 0
	}
	return 1
}
