package api_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// listedAgent returns the agent with id from GET /agents of c.
func listedAgent(t *testing.T, c *testkit.Client, id string) dto.Agent {
	t.Helper()
	list := testkit.Decode[dto.List[dto.Agent]](c.Get("/api/v1/agents").RequireStatus(http.StatusOK))
	for _, a := range list.Items {
		if a.ID == id {
			return a
		}
	}
	require.Failf(t, "agent not listed", "%s", id)
	return dto.Agent{}
}

// PROJ-01: every agent carries its project {id, slug, name}, display name, harness, machine,
// kind and archived flag; an agent of no project has no project field.
func TestAgentListCarriesIdentity(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	id := registerAgent(t, env, alice, "builder")

	a := listedAgent(t, alice, id)
	require.Nil(t, a.Project)
	require.Equal(t, []any{"builder", "builder", "agent", "", dto.AgentKindFixed, false},
		[]any{a.Name, a.DisplayName, a.Harness, a.Machine, a.Kind, a.Archived})

	ctx := context.Background()
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateProjectIfMissing(ctx, db.CreateProjectIfMissingParams{ID: "p1", OwnerID: alice.User.ID, Slug: "proj", Name: "Proj", CreatedAt: 1}); err != nil {
			return err
		}
		return q.UpdateAgentIdentity(ctx, db.UpdateAgentIdentityParams{
			ProjectID: sql.NullString{String: "p1", Valid: true}, Harness: "claude", Machine: "laptop",
			Kind: string(dto.AgentKindTemporary), AutoName: "Proj · Claude Code", ID: id,
		})
	}))

	a = listedAgent(t, alice, id)
	require.Equal(t, &dto.ProjectRef{ID: "p1", Slug: "proj", Name: "Proj"}, a.Project)
	require.Equal(t, []any{"Proj · Claude Code", "claude", "laptop", dto.AgentKindTemporary, false},
		[]any{a.DisplayName, a.Harness, a.Machine, a.Kind, a.Archived})
}

// sessionHeaders are the identity headers of a session: project, harness and machine, plus
// an agent name and mode when not empty.
func sessionHeaders(project, harness, machine, name, mode string) http.Header {
	h := http.Header{}
	for k, v := range map[string]string{
		dto.HeaderProject: project, dto.HeaderHarness: harness, dto.HeaderMachine: machine,
		dto.HeaderAgent: name, dto.HeaderAgentMode: mode,
	} {
		if v != "" {
			h.Set(k, v)
		}
	}
	return h
}

// dialSession opens an agent WebSocket with h and returns it with its welcome agent.
func dialSession(t *testing.T, c *testkit.Client, h http.Header) (*testkit.AgentConn, dto.AgentInfo) {
	t.Helper()
	conn, resp := c.DialAgentHeaders(h)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	return conn, conn.Welcome(wsWait).Agent
}

// agentByName returns the agent named name from GET /agents of c.
func agentByName(t *testing.T, c *testkit.Client, name string) dto.Agent {
	t.Helper()
	list := testkit.Decode[dto.List[dto.Agent]](c.Get("/api/v1/agents").RequireStatus(http.StatusOK))
	for _, a := range list.Items {
		if a.Name == name {
			return a
		}
	}
	require.Failf(t, "agent not listed", "%s", name)
	return dto.Agent{}
}

// setAgentKind changes an agent's kind straight in the store.
func setAgentKind(t *testing.T, env *testkit.Env, agentID string, kind dto.AgentKind) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		a, err := q.GetAgent(ctx, agentID)
		if err != nil {
			return err
		}
		return q.UpdateAgentIdentity(ctx, db.UpdateAgentIdentityParams{
			ProjectID: a.ProjectID, Harness: a.Harness, Machine: a.Machine, Kind: string(kind),
			AutoName: a.AutoName, SessionNo: a.SessionNo, ID: a.ID,
		})
	}))
}

// waitOffline waits until GET /agents shows the agent named name offline.
func waitOffline(t *testing.T, c *testkit.Client, name string) {
	t.Helper()
	require.Eventually(t, func() bool { return !agentByName(t, c, name).Online }, wsWait, 20*time.Millisecond)
}

// ID-01: a session without --name gets a temporary agent named <project>-<harness>, read as
// "<Project> · <Harness label>"; another session of the same project and harness gets -2 and
// " #2"; another harness is its own name. All of them join the same project and record
// harness and machine.
func TestAgentWSTemporaryAgentNamedAfterProjectAndHarness(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")

	first, one := dialSession(t, tok, sessionHeaders("proj", "claude", "laptop", "", ""))
	require.Equal(t, []string{"proj-claude", "Proj · Claude Code"}, []string{one.Name, one.DisplayName})
	second, two := dialSession(t, tok, sessionHeaders("proj", "claude", "desktop", "", ""))
	require.Equal(t, []string{"proj-claude-2", "Proj · Claude Code #2"}, []string{two.Name, two.DisplayName})
	_, codex := dialSession(t, tok, sessionHeaders("proj", "codex", "laptop", "", ""))
	require.Equal(t, []string{"proj-codex", "Proj · Codex"}, []string{codex.Name, codex.DisplayName})
	require.NoError(t, first.Closed(300*time.Millisecond), "the first session is not replaced")
	require.NoError(t, second.Closed(300*time.Millisecond), "the second session is not replaced")

	a := agentByName(t, alice, "proj-claude-2")
	require.NotNil(t, a.Project)
	require.Equal(t, []any{"Proj · Claude Code #2", dto.AgentKindTemporary, "proj", "Proj", "claude", "desktop", true},
		[]any{a.DisplayName, a.Kind, a.Project.Slug, a.Project.Name, a.Harness, a.Machine, a.Online})
	b := agentByName(t, alice, "proj-codex")
	require.Equal(t, []any{dto.AgentKindTemporary, a.Project.ID, "codex", "laptop"}, []any{b.Kind, b.Project.ID, b.Harness, b.Machine})
	require.Equal(t, a.Project.ID, agentByName(t, alice, "proj-claude").Project.ID, "one project per (owner, slug)")
}

// ID-01: a new session takes the lowest N whose agent is offline and temporary: a closed
// session's agent is reused (same id), a fixed agent of that name never is.
func TestAgentWSTemporaryNameReusesOfflineTemporaryAgents(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	h := sessionHeaders("proj", "claude", "laptop", "", "")

	first, one := dialSession(t, tok, h)
	second, two := dialSession(t, tok, h)
	first.Close()
	waitOffline(t, alice, "proj-claude")
	_, again := dialSession(t, tok, h)
	require.Equal(t, one, again, "the offline temporary agent is reused")

	second.Close()
	waitOffline(t, alice, "proj-claude-2")
	setAgentKind(t, env, two.ID, dto.AgentKindFixed)
	_, three := dialSession(t, tok, h)
	require.Equal(t, []string{"proj-claude-3", "Proj · Claude Code #3"}, []string{three.Name, three.DisplayName},
		"an offline fixed agent is not taken over")
}

// ID-01 edge case: sessions of one project and harness connecting at the same instant all get
// distinct numbers; none replaces another.
func TestAgentWSConcurrentTemporarySessionsGetDistinctNumbers(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")

	const n = 6
	conns := make([]*testkit.AgentConn, n)
	names := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			conn, resp := tok.DialAgentHeaders(sessionHeaders("proj", "claude", "laptop", "", ""))
			if conn == nil {
				t.Errorf("upgrade refused: %d %s", resp.Status, resp.Body)
				return
			}
			conns[i] = conn
			names[i] = conn.Welcome(wsWait).Agent.DisplayName
		})
	}
	wg.Wait()
	want := map[string]bool{"Proj · Claude Code": true}
	for i := 2; i <= n; i++ {
		want[fmt.Sprintf("Proj · Claude Code #%d", i)] = true
	}
	got := map[string]bool{}
	for _, name := range names {
		got[name] = true
	}
	require.Equal(t, want, got)
	for _, c := range conns {
		require.NoError(t, c.Closed(300*time.Millisecond), "no session was replaced")
	}
}

// ID-02: --name <n> uses the owner's fixed agent <n>, creating it and recording its project,
// harness and machine; a second session with that name replaces the first (newest wins).
// Naming an existing temporary agent makes it fixed.
func TestAgentWSExplicitNameUsesFixedAgent(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")

	first, info := dialSession(t, tok, sessionHeaders("proj", "pi", "box", "Reviewer", ""))
	require.Equal(t, []string{"reviewer", "reviewer"}, []string{info.Name, info.DisplayName})
	a := agentByName(t, alice, "reviewer")
	require.NotNil(t, a.Project)
	require.Equal(t, []any{dto.AgentKindFixed, "proj", "pi", "box"}, []any{a.Kind, a.Project.Slug, a.Harness, a.Machine})

	_, again := dialSession(t, tok, sessionHeaders("proj", "pi", "box", "reviewer", ""))
	require.Equal(t, info.ID, again.ID)
	requireClosedWith(t, first, 4000, "replaced")

	_, temp := dialSession(t, tok, sessionHeaders("proj", "claude", "box", "", ""))
	_, named := dialSession(t, tok, sessionHeaders("proj", "claude", "box", temp.Name, ""))
	require.Equal(t, temp.ID, named.ID)
	require.Equal(t, temp.Name, named.DisplayName, "an explicit name is what people read")
	require.Equal(t, dto.AgentKindFixed, agentByName(t, alice, temp.Name).Kind)
}

// ID-01: a session reconnecting under its assigned temporary name (mode temporary) gets the
// same temporary agent and replaces its stale socket; when that agent is gone it is created
// again as a temporary agent with the same number.
func TestAgentWSTemporaryModeReclaimsAssignedName(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	h := sessionHeaders("proj", "claude", "laptop", "", "")
	_, _ = dialSession(t, tok, h)
	stale, two := dialSession(t, tok, h)

	_, back := dialSession(t, tok, sessionHeaders("proj", "claude", "laptop", two.Name, dto.AgentModeTemporary))
	require.Equal(t, two, back)
	requireClosedWith(t, stale, 4000, "replaced")
	require.Equal(t, dto.AgentKindTemporary, agentByName(t, alice, two.Name).Kind)

	// A reclaimed name whose agent was deleted meanwhile is a new temporary agent again.
	alice.Delete("/api/v1/agents/" + two.ID).RequireStatus(http.StatusNoContent)
	_, fresh := dialSession(t, tok, sessionHeaders("proj", "claude", "laptop", two.Name, dto.AgentModeTemporary))
	require.NotEqual(t, two.ID, fresh.ID)
	require.Equal(t, []string{two.Name, "Proj · Claude Code #2"}, []string{fresh.Name, fresh.DisplayName})
	require.Equal(t, dto.AgentKindTemporary, agentByName(t, alice, two.Name).Kind)
}

// ID-01 with the agent cap: a temporary session beyond MaxAgentsPerOwner (50) is refused with
// 409 "agent limit reached" and registers nothing; existing agents still connect.
func TestAgentWSTemporaryRespectsAgentCap(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	for i := 1; i <= 50; i++ {
		registerAgent(t, env, alice, fmt.Sprintf("a%d", i))
	}
	conn, resp := tok.DialAgentHeaders(sessionHeaders("proj", "claude", "laptop", "", ""))
	require.Nil(t, conn)
	e := resp.RequireError(http.StatusConflict, dto.ErrorCodeConflict)
	require.Equal(t, "agent limit reached", e.Message)
	require.Len(t, agentNames(t, alice), 50)
	_, info := dialSession(t, tok, sessionHeaders("proj", "claude", "laptop", "a7", ""))
	require.Equal(t, "a7", info.Name)
}

// Compatibility (contract notes): clients from before projects send no project header. In
// unique mode they keep the <name>[-N] choice; new agents are temporary in project <name>
// and an existing agent keeps its kind and gets that project when it had none. A plain name
// of a new agent is a fixed agent in the project its name derives.
func TestAgentWSClientsWithoutProjectGetProjectFromName(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")

	_, one := dialUnique(t, tok, "Proj")
	_, two := dialUnique(t, tok, "proj")
	require.Equal(t, []string{"proj", "proj-2"}, []string{one, two})
	a, b := agentByName(t, alice, "proj"), agentByName(t, alice, "proj-2")
	require.Equal(t, []any{dto.AgentKindTemporary, "proj", "Proj", "Proj · Agent"}, []any{a.Kind, a.Project.Slug, a.Project.Name, a.DisplayName})
	require.Equal(t, []any{dto.AgentKindTemporary, a.Project.ID, "Proj · Agent #2"}, []any{b.Kind, b.Project.ID, b.DisplayName})

	registerAgent(t, env, alice, "web")
	_, web := dialUnique(t, tok, "web")
	require.Equal(t, "web", web)
	w := agentByName(t, alice, "web")
	require.NotNil(t, w.Project)
	require.Equal(t, []any{dto.AgentKindFixed, "web", "web"}, []any{w.Kind, w.Project.Slug, w.DisplayName})

	conn, resp := tok.DialAgent("builder-3")
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	conn.Welcome(wsWait)
	c := agentByName(t, alice, "builder-3")
	require.NotNil(t, c.Project)
	require.Equal(t, []any{dto.AgentKindFixed, "builder", "builder-3"}, []any{c.Kind, c.Project.Slug, c.DisplayName})
}

// ID-01: identity headers are cleaned: harness aliases map to their id and unknown harnesses
// to "agent"; the machine loses control characters and is cut to 64 characters; the project
// slug follows the agent name rules and its name keeps the text with an upper-case first
// letter.
func TestAgentWSIdentityHeadersAreCleaned(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")

	_, alias := dialSession(t, tok, sessionHeaders("my app", "Claude-Code", "  my\tlaptop  ", "", ""))
	require.Equal(t, []string{"my-app-claude", "My app · Claude Code"}, []string{alias.Name, alias.DisplayName})
	a := agentByName(t, alice, "my-app-claude")
	require.Equal(t, []any{"claude", "mylaptop", "my-app", "My app"}, []any{a.Harness, a.Machine, a.Project.Slug, a.Project.Name})

	_, unknown := dialSession(t, tok, sessionHeaders("my app", "vim", strings.Repeat("x", 70), "", ""))
	require.Equal(t, []string{"my-app-agent", "My app · Agent"}, []string{unknown.Name, unknown.DisplayName})
	require.Equal(t, strings.Repeat("x", 64), agentByName(t, alice, "my-app-agent").Machine)
}

// ID-01: the WebSocket stamps the agent's connect time (the active session of a project);
// POST /agent/register resolves the same identity (a tools-only session without --name gets a
// temporary agent) and leaves the agent offline without a connect time.
func TestAgentConnectTimeAndRegisterIdentity(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	ctx := context.Background()

	_, info := dialSession(t, tok, sessionHeaders("proj", "claude", "laptop", "", ""))
	a, err := env.App.Store.R().GetAgent(ctx, info.ID)
	require.NoError(t, err)
	require.True(t, a.ConnectedAt.Valid, "connect time stamped")

	reg := tok.WithHeader(dto.HeaderProject, "proj").WithHeader(dto.HeaderHarness, "codex")
	got := testkit.Decode[dto.AgentInfo](reg.Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK))
	require.Equal(t, []string{"proj-codex", "Proj · Codex", "alice"}, []string{got.Name, got.DisplayName, got.Owner})
	r, err := env.App.Store.R().GetAgent(ctx, got.ID)
	require.NoError(t, err)
	require.False(t, r.ConnectedAt.Valid)
	listed := agentByName(t, alice, "proj-codex")
	require.Equal(t, []any{dto.AgentKindTemporary, false}, []any{listed.Kind, listed.Online})
}

// requireClosedWith waits for the server to close conn with code and reason.
func requireClosedWith(t *testing.T, conn *testkit.AgentConn, code int, reason string) {
	t.Helper()
	ce := closeError(t, conn.Closed(wsWait))
	require.Equal(t, websocket.StatusCode(code), ce.Code)
	require.Equal(t, reason, ce.Reason)
}
