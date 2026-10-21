package api_test

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// registerSession registers a tools-only session (POST /agent/register) of project proj and
// harness with the session key key (none when empty) and returns its agent.
func registerSession(t *testing.T, tok *testkit.Client, project, harness, key string) dto.AgentInfo {
	t.Helper()
	c := tok.WithHeader(dto.HeaderProject, project).WithHeader(dto.HeaderHarness, harness)
	if key != "" {
		c = c.WithHeader(dto.HeaderSession, key)
	}
	return testkit.Decode[dto.AgentInfo](c.Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK))
}

// keyedHeaders are sessionHeaders without a name plus the session key.
func keyedHeaders(project, harness, key string) http.Header {
	h := sessionHeaders(project, harness, "laptop", "", "")
	h.Set(dto.HeaderSession, key)
	return h
}

// ID-01 (tools-only edge, PROJ-07 precision): two tools-only sessions of one project and
// harness are never online, yet each gets its own temporary agent (" #2" for the second);
// the same session registering again (a restart of its tools, or each turn-end hook) gets its
// own agent back. A client that sends no session key keeps the old rule: the lowest offline
// temporary agent of the project and harness.
func TestToolsOnlySessionsOfOneProjectGetTheirOwnAgents(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")

	one := registerSession(t, tok, "proj", "codex", "pl-one")
	two := registerSession(t, tok, "proj", "codex", "pl-two")
	require.Equal(t, []string{"proj-codex", "Proj · Codex"}, []string{one.Name, one.DisplayName})
	require.Equal(t, []string{"proj-codex-2", "Proj · Codex #2"}, []string{two.Name, two.DisplayName})
	require.NotEqual(t, one.ID, two.ID)

	require.Equal(t, two, registerSession(t, tok, "proj", "codex", "pl-two"), "the second session gets its own agent again")
	require.Equal(t, one, registerSession(t, tok, "proj", "codex", "pl-one"), "the first session gets its own agent again")

	require.Equal(t, one, registerSession(t, tok, "proj", "codex", ""), "no session key: the lowest offline temporary agent, as before")
	require.Equal(t, one, registerSession(t, tok, "proj", "codex", ""))
	require.Equal(t, two, registerSession(t, tok, "proj", "codex", "pl-two"), "a keyless registration does not take an agent over")
}

// ID-01 edge case: tools-only sessions registering at the same instant get distinct numbers.
func TestConcurrentToolsOnlySessionsGetDistinctNumbers(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")

	const n = 6
	names := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			c := tok.WithHeader(dto.HeaderProject, "proj").WithHeader(dto.HeaderHarness, "gemini").
				WithHeader(dto.HeaderSession, fmt.Sprintf("pl-%d", i))
			resp := c.Post("/api/v1/agent/register", nil)
			if resp.Status != http.StatusOK {
				t.Errorf("register: %d %s", resp.Status, resp.Body)
				return
			}
			names[i] = testkit.Decode[dto.AgentInfo](resp).DisplayName
		})
	}
	wg.Wait()
	want := map[string]bool{"Proj · Gemini": true}
	for i := 2; i <= n; i++ {
		want[fmt.Sprintf("Proj · Gemini #%d", i)] = true
	}
	got := map[string]bool{}
	for _, name := range names {
		got[name] = true
	}
	require.Equal(t, want, got)
}

// ID-01, PROJ-02: a tools-only session holds its agent while it keeps registering; once the
// agent is archived (24 hours without a registration) another session may take it, which
// unarchives it.
func TestToolsOnlySessionHoldsItsAgentUntilArchived(t *testing.T) {
	clock := testkit.NewClock(time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC))
	env := testkit.New(t, testkit.WithClock(clock.Now))
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	ctx := context.Background()

	one := registerSession(t, tok, "proj", "codex", "pl-one")
	clock.Advance(23 * time.Hour)
	require.Equal(t, one, registerSession(t, tok, "proj", "codex", "pl-one"), "the hook of the session registers again")
	clock.Advance(2 * time.Hour)
	n, err := env.App.Agents.Archive(ctx)
	require.NoError(t, err)
	require.Zero(t, n, "a registration counts as activity")
	other := registerSession(t, tok, "proj", "codex", "pl-other")
	require.Equal(t, "proj-codex-2", other.Name, "a held agent is not taken over")

	clock.Advance(25 * time.Hour)
	n, err = env.App.Agents.Archive(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	again := registerSession(t, tok, "proj", "codex", "pl-new")
	require.Equal(t, one, again, "an archived agent is free again")
	requireUnarchived(t, alice, one.Name, "taking an archived agent unarchives it")
}

// ID-01, PROJ-07: a live session (listen) that sends a session key joins the agent its own
// tools-only half registered, and skips one held by another tools-only session. A live
// session does not hold its agent once it is gone: the next live session reuses it, so live
// sessions keep the list short as before.
func TestLiveSessionsAndToolsOnlySessionKeys(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")

	tools := registerSession(t, tok, "proj", "opencode", "pl-one")
	require.Equal(t, "proj-opencode", tools.Name)

	otherConn, other := dialSession(t, tok, keyedHeaders("proj", "opencode", "pl-two"))
	require.Equal(t, []string{"proj-opencode-2", "Proj · OpenCode #2"}, []string{other.Name, other.DisplayName},
		"another session's tools-only agent is not taken over")
	ownConn, own := dialSession(t, tok, keyedHeaders("proj", "opencode", "pl-one"))
	require.Equal(t, tools, own, "listen joins the agent of its session's tools")

	ownConn.Close()
	waitOffline(t, alice, own.Name)
	otherConn.Close()
	waitOffline(t, alice, other.Name)
	_, next := dialSession(t, tok, keyedHeaders("proj", "opencode", "pl-three"))
	require.Equal(t, tools, next, "a live session does not hold its agent after it ends")
}
