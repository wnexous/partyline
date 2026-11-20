package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/orgs"
)

// placementE2E is "Acme" with alice its admin (her own organization stays her default), and a launcher that
// starts Claude Code for her. What the launcher hands the
// harness is given to the session's own `partyline channel` (or `listen`), which talks to the real server.
type placementE2E struct {
	e                 *channelEnv
	m                 *launchMachine
	acme              orgs.Membership
	alicePersonalSlug string
	launched          map[string]string // the environment the harness got from the last launch
	started           *mcpClient
}

func newPlacementE2E(t *testing.T) *placementE2E {
	t.Helper()
	e := newChannelEnv(t)
	acme, err := e.env.App.Orgs.Create(context.Background(), e.alice.User.ID, "Acme")
	require.NoError(t, err)
	p := &placementE2E{e: e, acme: acme, alicePersonalSlug: e.alice.PersonalOrg().Slug}
	p.m = claudeLaunch(t)
	p.m.saveConfig(t, e.env.URL, e.secret)
	return p
}

// envMap is the environment of a process as a map.
func envMap(environ []string) map[string]string {
	out := map[string]string{}
	for _, kv := range environ {
		name, value, _ := strings.Cut(kv, "=")
		out[name] = value
	}
	return out
}

// launch runs `partyline claude args…` in folder and, as Claude Code would, starts the session's channel in the
// environment the launcher handed on. It returns the launch and, when the harness started, the AI once online.
func (p *placementE2E) launch(t *testing.T, folder string, args ...string) (launchRun, dto.IntelligenceProfile) {
	t.Helper()
	p.m.cwd = folder
	p.started = nil
	p.m.afterExec = func(environ []string) {
		p.launched = envMap(environ)
		vars := p.e.vars()
		for _, name := range []string{"PARTYLINE_FOLDER", "PARTYLINE_HARNESS", "PARTYLINE_LAUNCH_ORG", "PARTYLINE_LAUNCH_PARENT"} {
			if v, ok := p.launched[name]; ok {
				vars[name] = v
			}
		}
		p.started = startChannel(t, nil, vars, folder)
		initialize(p.started)
	}
	r := p.m.run(t, append([]string{"claude"}, args...)...)
	if !r.execed {
		return r, dto.IntelligenceProfile{}
	}
	return r, p.e.ai(t, folder)
}

// inside makes the next launch one run from a shell inside the session of the last one: its environment is the
// harness's, and the launcher process has it too.
func (p *placementE2E) inside(t *testing.T) {
	t.Helper()
	require.NotNil(t, p.launched, "a session was launched")
	p.m.environ, p.m.getenv = nil, map[string]string{}
	for name, value := range p.launched {
		p.m.environ = append(p.m.environ, name+"="+value)
		p.m.getenv[name] = value
	}
	p.m.getenv["HOME"] = p.m.home
}

// member returns the membership of the AI in its (only) organization.
func requireMembership(t *testing.T, prof dto.IntelligenceProfile, org, permission string) {
	t.Helper()
	require.Len(t, prof.Memberships, 1, "the AI belongs to one organization")
	m := prof.Memberships[0]
	require.Equal(t, []any{org, permission}, []any{m.OrgSlug, m.Permission})
}

// ORG-04 end to end: `partyline claude --org acme` puts the AI of the folder in Acme, whatever alice's default
// is; the flags in both forms and the variable do the same.
func TestTheLauncherPutsTheAIInTheOrganizationItIsAskedFor(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		env  map[string]string
	}{
		"flags":              {args: []string{"--org", "acme"}},
		"inline flags":       {args: []string{"--org=ACME"}},
		"variables":          {env: map[string]string{"PARTYLINE_ORG": "acme"}},
		"flag over variable": {args: []string{"--org", "acme"}, env: map[string]string{"PARTYLINE_ORG": "no-such-organization"}},
		// SR2-05: a PARTYLINE_POSITION left in the shell is ignored, never an error.
		"a stale position variable": {args: []string{"--org", "acme"}, env: map[string]string{"PARTYLINE_POSITION": "Worktrees"}},
	} {
		t.Run(name, func(t *testing.T) {
			p := newPlacementE2E(t)
			p.m.getenv = tc.env
			_, prof := p.launch(t, "/work/placed", tc.args...)
			requireMembership(t, prof, "acme", "member")
			require.Equal(t, "claude", prof.AI.App)
			require.NotContains(t, p.launched, "PARTYLINE_POSITION", "never handed on")
			require.NotContains(t, p.launched, "PARTYLINE_LAUNCH_POSITION")
		})
	}

	t.Run("a launch with no placement uses the defaults", func(t *testing.T) {
		p := newPlacementE2E(t)
		_, prof := p.launch(t, "/work/plain")
		requireMembership(t, prof, p.alicePersonalSlug, "member")
	})
}

// SR2-02, SR2-09 end to end: --parent @lead makes the AI that started this one its creator and its instructor.
// The new AI joins the parent's organization as a member, and the parent's messages are trusted by it.
func TestTheLauncherStartsAWorkerUnderAParentAI(t *testing.T) {
	p := newPlacementE2E(t)
	_, lead := p.launch(t, "/work/lead", "--org", "acme")
	requireMembership(t, lead, "acme", "member")

	_, worker := p.launch(t, "/work/worker", "--parent", "@lead")
	requireMembership(t, worker, "acme", "member")
	trusted, err := p.e.env.App.Authz.Trusts(context.Background(), lead.ID, worker.ID)
	require.NoError(t, err)
	require.True(t, trusted, "the parent instructs the worker it started")
}

// SR2-05: --position was removed: the launcher refuses it with a short message (exit 2), starts nothing and asks
// the server nothing.
func TestTheLauncherRefusesPosition(t *testing.T) {
	for _, args := range [][]string{{"--position", "Worktrees"}, {"--position=Worktrees"}, {"--org", "acme", "--position", "Lead"}} {
		p := newPlacementE2E(t)
		before := p.e.folders(t)
		r, _ := p.launch(t, "/work/refused", args...)
		require.Equal(t, 2, r.code, r.stderr)
		require.False(t, r.execed, "the harness is not started")
		require.Contains(t, r.stderr, "--position was removed")
		require.Equal(t, before, p.e.folders(t), "nothing was created")
	}
}

// Design, Risks: a launch from inside a launched session is another AI. It does not inherit the organization or
// the parent AI of the session it runs in: with no flags it lands in alice's default organization - not in Acme.
func TestANestedLaunchStartsAnAIWithoutTheSessionsPlacement(t *testing.T) {
	p := newPlacementE2E(t)
	_, lead := p.launch(t, "/work/lead", "--org", "acme")
	requireMembership(t, lead, "acme", "member")
	require.Equal(t, "acme", p.launched["PARTYLINE_LAUNCH_ORG"], "fixture: the session carries its placement")

	p.inside(t)
	_, other := p.launch(t, "/work/other")
	requireMembership(t, other, p.alicePersonalSlug, "member")
	require.Len(t, p.e.folders(t), 2)

	p.inside(t)
	_, third := p.launch(t, "/work/third", "--org", "acme", "--parent", "@lead")
	requireMembership(t, third, "acme", "member")
}

// ORG-05, ORG-04 end to end (L-045): a start the server refuses is not a start. The launcher prints the
// server's message, exits 1, runs no harness and creates nothing.
func TestTheLauncherRefusesAStartTheServerRefuses(t *testing.T) {
	cases := []struct {
		name    string
		as      func(p *placementE2E)
		args    []string
		message string
	}{
		{"an organization the creator is not in", func(p *placementE2E) {
			g, err := p.e.env.App.Orgs.Create(context.Background(), p.e.env.NewUser("gus").User.ID, "Globex")
			require.NoError(t, err)
			require.NotEmpty(t, g.ID)
		}, []string{"--org", "globex"}, "you are not a member of that organization"},
		{"an organization that does not exist", nil, []string{"--org", "no-such-organization"},
			"no organization has that slug or id: partyline orgs lists yours"},
		{"a parent AI that is not in the organization", func(p *placementE2E) {
			p.launch(t, "/work/lead", "--org", "acme")
			g, err := p.e.env.App.Orgs.Create(context.Background(), p.e.alice.User.ID, "Globex")
			require.NoError(t, err)
			require.NotEmpty(t, g.ID)
		}, []string{"--org", "globex", "--parent", "@lead"}, "the parent AI is not a member of that organization"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newPlacementE2E(t)
			if c.as != nil {
				c.as(p)
			}
			before := p.e.folders(t)
			r, prof := p.launch(t, "/work/refused", c.args...)
			require.Equal(t, 1, r.code, r.stderr)
			require.False(t, r.execed, "the harness is not started")
			require.Equal(t, "partyline claude: "+c.message+"\n", r.stderr)
			require.Empty(t, prof.ID)
			require.Equal(t, before, p.e.folders(t), "nothing was created")
		})
	}

	t.Run("an AI that already has a home", func(t *testing.T) {
		p := newPlacementE2E(t)
		p.launch(t, "/work/homebody", "--org", "acme")
		p.m.getenv = nil
		r, _ := p.launch(t, "/work/homebody", "--org", p.alicePersonalSlug)
		require.Equal(t, 1, r.code, r.stderr)
		require.False(t, r.execed)
		require.Equal(t, "partyline claude: this AI belongs to Acme (acme): an AI does not move to another organization\n", r.stderr)
	})
}
