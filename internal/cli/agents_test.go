package cli

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

func runAgents(env map[string]string, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = execute(context.Background(), append([]string{"partyline", "agents"}, args...), stdio{
		stdout: &out, stderr: &errOut, getenv: func(k string) string { return env[k] },
	})
	return code, out.String(), errOut.String()
}

// aiOn creates alice's AI of the folder on a machine with its home and harness, and names it
// (display name and handle).
func (e *channelEnv) aiOn(t *testing.T, machine, folder, home, harness, name, handle string) db.Agent {
	t.Helper()
	a, err := e.env.App.Agents.ResolveFolder(context.Background(), e.alice.User.ID,
		agents.FolderID{Machine: machine, Folder: folder, Home: home, Harness: harness}, false)
	require.NoError(t, err)
	p := authn.Principal{Kind: authn.KindUser, UserID: e.alice.User.ID, Role: string(e.alice.User.Role)}
	_, err = e.env.App.Intelligences.Update(context.Background(), p, a.ID, intelligences.ProfileUpdate{DisplayName: &name, Handle: &handle})
	require.NoError(t, err)
	return a
}

// seenAgo records that the AI went offline ago.
func (e *channelEnv) seenAgo(t *testing.T, id string, ago time.Duration) {
	t.Helper()
	at := time.Now().Add(-ago).UnixMilli()
	require.NoError(t, e.env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
		return q.TouchAgent(context.Background(), db.TouchAgentParams{LastSeenAt: sql.NullInt64{Int64: at, Valid: true}, ID: id})
	}))
}

// newOrg makes an organization alice administers.
func (e *channelEnv) newOrg(t *testing.T, name string) db.Organization {
	t.Helper()
	created, err := e.env.App.Orgs.Create(context.Background(), e.alice.User.ID, name)
	require.NoError(t, err)
	return created.Organization
}

// aiIn is aiOn for an AI whose home is the organization: alice's default is that organization while it is made,
// and her personal one again afterwards.
func (e *channelEnv) aiIn(t *testing.T, orgID, machine, folder, home, harness, name, handle string) db.Agent {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, e.env.App.Orgs.SetDefault(ctx, e.alice.User.ID, orgID))
	a := e.aiOn(t, machine, folder, home, harness, name, handle)
	require.NoError(t, e.env.App.Orgs.SetDefault(ctx, e.alice.User.ID, e.alice.PersonalOrg().ID))
	return a
}

// ORG-02, AD-022 (T57): `partyline agents` prints the AIs the token's user hosts grouped by organization, the
// organizations by name, each AI on a line in tab-aligned columns (online marker, name, handle, app, state and
// the folder as `~` reads it) by name, and an AI that has no organization last. An organization whose address
// differs from its name shows it. No machine is named anywhere: not the hostname of the computer an AI lives on,
// and not a folder tree.
func TestAgentsPrintsTheHostedAIsByOrganization(t *testing.T) {
	e := newChannelEnv(t)
	acme := e.newOrg(t, "Acme Corp")
	atlas := e.aiOn(t, "devbox", "/home/alice/atlas", "/home/alice", "claude", "Atlas", "atlas")
	notes := e.aiOn(t, "devbox", "/home/alice/atlas/notes", "/home/alice", "opencode", "Notes", "notes")
	dev := e.aiOn(t, "notebook", "/home/alice", "/home/alice", "claude", "Dev", "dev")
	lab := e.aiIn(t, acme.ID, "devbox", "/srv/lab", "/home/alice", "codex", "Lab", "lab")
	e.legacy(t, "Old helper")
	e.seenAgo(t, notes.ID, 2*time.Hour)
	e.seenAgo(t, lab.ID, 3*24*time.Hour)
	e.seenAgo(t, dev.ID, 5*24*time.Hour)

	conn, resp := e.alice.WithBearer(e.secret).DialFolder(testkit.Folder{Machine: "devbox", Path: "/home/alice/atlas", Home: "/home/alice", Harness: "claude"})
	require.NotNil(t, conn, "upgrade refused: %d", resp.Status)
	defer conn.Close()
	conn.Welcome(chanWait)
	require.Eventually(t, func() bool { return e.env.App.Presence.Online(atlas.ID) }, chanWait, 20*time.Millisecond)

	require.Equal(t, "alice", e.alice.PersonalOrg().Name, "the personal organization is named for its address")
	code, out, errOut := runAgents(e.vars())
	require.Equal(t, 0, code, errOut)
	require.Equal(t, `Acme Corp (acme-corp)
  ○ Lab  @lab  Codex  3d ago  /srv/lab
alice
  ● Atlas  @atlas  Claude Code  online  ~/atlas
  ○ Dev    @dev    Claude Code  5d ago  ~
  ○ Notes  @notes  OpenCode     2h ago  ~/atlas/notes
No organization
  ○ Old helper  @old-helper  Agent
`, out)
	for _, machine := range []string{"devbox", "notebook"} {
		require.NotContains(t, out, machine, "no machine is named")
	}
}

// ORG-02 (T57): the organizations come by name whatever the order they were made or the AIs came in, ignoring
// case; the AIs of one organization by name.
func TestAgentsOrdersTheOrganizationsByName(t *testing.T) {
	e := newChannelEnv(t)
	zeta, alpha := e.newOrg(t, "Zeta Labs"), e.newOrg(t, "alpha")
	e.aiIn(t, zeta.ID, "devbox", "/srv/z2", "/home/alice", "claude", "Zoe", "zoe")
	e.aiIn(t, zeta.ID, "devbox", "/srv/z1", "/home/alice", "claude", "adam", "adam")
	e.aiIn(t, alpha.ID, "devbox", "/srv/a", "/home/alice", "claude", "Ann", "ann")

	code, out, errOut := runAgents(e.vars())
	require.Equal(t, 0, code, errOut)
	require.Equal(t, `alpha
  ○ Ann  @ann  Claude Code    /srv/a
Zeta Labs (zeta-labs)
  ○ adam  @adam  Claude Code    /srv/z1
  ○ Zoe   @zoe   Claude Code    /srv/z2
`, out)
}

// TREE-07: an owner without AIs reads how to start one.
func TestAgentsEmptyList(t *testing.T) {
	e := newChannelEnv(t)
	code, out, errOut := runAgents(e.vars())
	require.Equal(t, 0, code, errOut)
	require.Equal(t, "No AIs yet: start a session with partyline claude\n", out)
}

// TREE-03: the state column reads online, now, Nm, Nh or Nd; never seen is empty.
func TestRelativeLastSeen(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		ago  time.Duration
		want string
	}{
		{30 * time.Second, "now"},
		{90 * time.Second, "1m ago"},
		{59 * time.Minute, "59m ago"},
		{61 * time.Minute, "1h ago"},
		{23 * time.Hour, "23h ago"},
		{25 * time.Hour, "1d ago"},
		{6 * 24 * time.Hour, "6d ago"},
	} {
		seen := now.Add(-c.ago)
		require.Equal(t, c.want, relativeLastSeen(&seen, now), "seen %s ago", c.ago)
	}
	require.Empty(t, relativeLastSeen(nil, now), "never seen")
}

// TREE-07: without credentials the command explains how to set them; a rejected token is an
// error.
func TestAgentsNeedsCredentials(t *testing.T) {
	code, _, errOut := runAgents(map[string]string{})
	require.Equal(t, 2, code)
	require.Contains(t, errOut, "partyline agents: set PARTYLINE_URL and PARTYLINE_TOKEN")

	e := newChannelEnv(t)
	code, _, errOut = runAgents(map[string]string{"PARTYLINE_URL": e.env.URL, "PARTYLINE_TOKEN": "pl_agent_wrong"})
	require.Equal(t, 1, code)
	require.Contains(t, errOut, "partyline agents: the server answered 401")
}

// ORG-02 (T57, L-045): a server that fails, or that cannot be reached, is a non-zero exit with the reason on stderr
// and nothing on stdout; a server of an older release that has no such route says so the same way.
func TestAgentsFailsWhenTheServerFails(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		want   string
	}{
		{"an internal error", http.StatusInternalServerError, "partyline agents: the server answered 500: boom"},
		{"an unknown route", http.StatusNotFound, "partyline agents: the server answered 404: not found"},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/v1/agent/agents", r.URL.Path, "the command reads the agents list")
				msg := "boom"
				if c.status == http.StatusNotFound {
					msg = "not found"
				}
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(`{"error":{"code":"x","message":"` + msg + `"}}`))
			}))
			defer srv.Close()
			code, out, errOut := runAgents(map[string]string{"PARTYLINE_URL": srv.URL, "PARTYLINE_TOKEN": "pl_agent_x"})
			require.Equal(t, 1, code)
			require.Empty(t, out)
			require.Equal(t, c.want+"\n", errOut)
		})
	}

	gone := httptest.NewServer(http.NotFoundHandler())
	url := gone.URL
	gone.Close()
	code, out, errOut := runAgents(map[string]string{"PARTYLINE_URL": url, "PARTYLINE_TOKEN": "pl_agent_x"})
	require.Equal(t, 1, code)
	require.Empty(t, out)
	require.Equal(t, "partyline agents: cannot reach "+url+"\n", errOut)
}

// ORG-02 (T58): `partyline agents` lists the AIs the token's user hosts, and only those: the AIs of the organizations
// they belong to that other users host are not theirs to list here (the contacts of the web app show those), and
// the same command run by that other user lists theirs. The heading is the organization's, whoever hosts.
func TestAgentsListsOnlyTheAIsTheTokenUserHosts(t *testing.T) {
	e := newChannelEnv(t)
	acme := e.newOrg(t, "Acme")
	bob := e.env.NewUser("bob")
	e.env.AddMember(acme.ID, bob.User.ID, orgs.PermissionMember, "")
	e.aiIn(t, acme.ID, "devbox", "/srv/lab", "/home/alice", "claude", "Lab", "lab")
	require.NotEmpty(t, e.env.NewAIIn(bob, acme.ID, "sage"), "an AI bob hosts in Acme, which alice is in too")

	code, out, errOut := runAgents(e.vars())
	require.Equal(t, 0, code, errOut)
	require.Equal(t, "Acme\n  ○ Lab  @lab  Claude Code    /srv/lab\n", out, "alice's own AI, and not bob's sage")

	code, out, errOut = runAgents(map[string]string{"PARTYLINE_URL": e.env.URL, "PARTYLINE_TOKEN": e.tokenOf(t, bob)})
	require.Equal(t, 0, code, errOut)
	require.Contains(t, out, "@sage")
	require.NotContains(t, out, "@lab", "and not alice's lab")
}

// AD-022, ORG-02 (T58): the same folder on two computers is two AIs that differ only by their handle and history -
// machines are not shown to tell them apart: both rows read the same folder, in the same organization, and neither
// computer is named.
func TestAgentsListsTheSameFolderOfTwoComputersAsTwoAIs(t *testing.T) {
	e := newChannelEnv(t)
	e.aiOn(t, "devbox", "/home/alice/atlas", "/home/alice", "claude", "Atlas", "atlas")
	e.aiOn(t, "notebook", "/home/alice/atlas", "/home/alice", "claude", "Atlas", "atlas-2")

	code, out, errOut := runAgents(e.vars())
	require.Equal(t, 0, code, errOut)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 3, out)
	require.Equal(t, "alice", lines[0], "one group: both are alice's, in her own organization")
	require.ElementsMatch(t, []string{
		"  ○ Atlas  @atlas    Claude Code    ~/atlas",
		"  ○ Atlas  @atlas-2  Claude Code    ~/atlas",
	}, lines[1:])
	for _, machine := range []string{"devbox", "notebook"} {
		require.NotContains(t, out, machine)
	}
}

// The credentials come from --url and --token as well, and win over the environment.
func TestAgentsTakesItsCredentialsFromFlags(t *testing.T) {
	e := newChannelEnv(t)
	e.aiOn(t, "devbox", "/srv/lab", "/home/alice", "claude", "Lab", "lab")
	code, out, errOut := runAgents(map[string]string{"PARTYLINE_URL": "http://127.0.0.1:1", "PARTYLINE_TOKEN": "pl_agent_wrong"}, "--url", e.env.URL, "--token", e.secret)
	require.Equal(t, 0, code, errOut)
	require.Contains(t, out, "@lab")
	code, out, errOut = runAgents(map[string]string{}, "--url", e.env.URL)
	require.Equal(t, 2, code, "a flag for the URL alone does not make credentials")
	require.Empty(t, out)
	require.Contains(t, errOut, "set PARTYLINE_URL and PARTYLINE_TOKEN")
}

// AD-022: --help describes `partyline agents` as it is - the AIs you host, by organization - not as the machine
// tree it used to print.
func TestHelpDescribesAgentsByOrganization(t *testing.T) {
	var out bytes.Buffer
	require.Equal(t, 0, execute(context.Background(), []string{"partyline", "--help"}, stdio{stdout: &out, stderr: &bytes.Buffer{}, getenv: func(string) string { return "" }}))
	require.Contains(t, out.String(), "  agents                 list the AIs you host, grouped by organization, with online markers\n")
	require.NotContains(t, out.String(), "machine › folder", "the machine tree is gone")
}

// SESS-43: an online AI with parallel sessions lists each labelled one under its row, with the address that
// reaches it, its app and since when; the principal is the AI's own row and an offline AI lists none.
func TestAgentsListsTheParallelSessionsOfAnOnlineAI(t *testing.T) {
	since := time.Date(2026, 10, 28, 14, 5, 0, 0, time.Local)
	list := []dto.Agent{
		{
			Intelligence: dto.IntelligenceRef{DisplayName: "Atlas", Handle: "atlas"}, Harness: "claude", Online: true,
			FolderLabel: "~/atlas",
			Sessions: []dto.SessionInfo{
				{Label: "", Harness: "claude", ConnectedAt: since},
				{Label: "pi-2", Harness: "pi", ConnectedAt: since},
			},
		},
		{
			Intelligence: dto.IntelligenceRef{DisplayName: "Dev", Handle: "dev"}, Harness: "claude",
			Sessions: []dto.SessionInfo{{Label: "wt", Harness: "claude", ConnectedAt: since}},
		},
	}
	var out bytes.Buffer
	printAgents(&out, list, since)
	require.Contains(t, out.String(), "@atlas:pi-2")
	require.Contains(t, out.String(), "since 14:05")
	require.NotContains(t, out.String(), "@dev:wt", "an offline AI lists no session")
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 4, out.String())
	require.Contains(t, lines[1], "@atlas ")
	require.Contains(t, lines[2], "↳ :pi-2")
}
