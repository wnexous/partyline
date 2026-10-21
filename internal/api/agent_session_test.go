package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
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

// ID-01 (T19): a session key holds an agent only for its owner and machine. Parent pid keys
// repeat across machines: the same key on another machine of the owner gets its own agent, the
// same key and machine gets its agent back, and another owner's same key never reaches the
// owner's agents. (A session without a machine still matches: see
// TestLiveSessionsAndToolsOnlySessionKeys.)
func TestSessionHoldsAreScopedByOwnerAndMachine(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	aliceTok := alice.Token(dto.TokenKindAgent, "laptop")
	bob := env.NewUser("bob")
	bobTok := bob.Token(dto.TokenKindAgent, "laptop")
	register := func(tok *testkit.Client, machine string) dto.AgentInfo {
		t.Helper()
		c := tok.WithHeader(dto.HeaderProject, "proj").WithHeader(dto.HeaderHarness, "codex").
			WithHeader(dto.HeaderSession, "4242").WithHeader(dto.HeaderMachine, machine)
		return testkit.Decode[dto.AgentInfo](c.Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK))
	}

	laptop := register(aliceTok, "laptop")
	desktop := register(aliceTok, "desktop")
	require.Equal(t, []string{"proj-codex", "proj-codex-2"}, []string{laptop.Name, desktop.Name},
		"the same key on another machine is another session")
	require.NotEqual(t, laptop.ID, desktop.ID)
	require.Equal(t, laptop, register(aliceTok, "laptop"), "same key and machine: the session's agent")
	require.Equal(t, desktop, register(aliceTok, "desktop"))

	bobs := register(bobTok, "laptop")
	require.Equal(t, "proj-codex", bobs.Name, "bob's session gets bob's own agent")
	require.NotEqual(t, laptop.ID, bobs.ID)
	require.Equal(t, laptop, register(aliceTok, "laptop"), "bob's registration took nothing of alice's")
}

// ID-01: a tools-only session gets back the agent it holds even when a lower number is free
// again. A live session takes proj-opencode, the tools-only session takes " #2"; once the
// live session ends, the tools-only session still gets " #2", not the freed first agent.
func TestToolsOnlySessionKeepsItsAgentWhenALowerOneFrees(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")

	liveConn, live := dialSession(t, tok, sessionHeaders("proj", "opencode", "laptop", "", ""))
	require.Equal(t, "proj-opencode", live.Name)
	tools := registerSession(t, tok, "proj", "opencode", "pl-tools")
	require.Equal(t, []string{"proj-opencode-2", "Proj · OpenCode #2"}, []string{tools.Name, tools.DisplayName})

	liveConn.Close()
	waitOffline(t, alice, live.Name)
	require.Equal(t, tools, registerSession(t, tok, "proj", "opencode", "pl-tools"),
		"the session's own agent, not the freed proj-opencode")
}

// ID-01: another owner's hold on the same session key never steers a session. Bob's own keys
// hold proj-codex and proj-codex-2; Alice's key K holds her proj-codex-2. Bob's session with
// key K holds nothing of Bob's, so it gets a new proj-codex-3, never an agent another of
// Bob's sessions holds.
func TestAnotherOwnersHoldDoesNotSteerASession(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	aliceTok := alice.Token(dto.TokenKindAgent, "laptop")
	bob := env.NewUser("bob")
	bobTok := bob.Token(dto.TokenKindAgent, "laptop")

	b1 := registerSession(t, bobTok, "proj", "codex", "pl-b1")
	b2 := registerSession(t, bobTok, "proj", "codex", "pl-b2")
	require.Equal(t, []string{"proj-codex", "proj-codex-2"}, []string{b1.Name, b2.Name})
	a1 := registerSession(t, aliceTok, "proj", "codex", "pl-a1")
	aK := registerSession(t, aliceTok, "proj", "codex", "pl-k")
	require.Equal(t, []string{"proj-codex", "proj-codex-2"}, []string{a1.Name, aK.Name})

	bK := registerSession(t, bobTok, "proj", "codex", "pl-k")
	require.Equal(t, []string{"proj-codex-3", "Proj · Codex #3"}, []string{bK.Name, bK.DisplayName})
	require.NotContains(t, []string{b1.ID, b2.ID, aK.ID}, bK.ID)
	require.Equal(t, b2, registerSession(t, bobTok, "proj", "codex", "pl-b2"), "bob's second session keeps its agent")
}

// ID-01: the server ignores a session key that is not 1–64 characters of A-Za-z0-9._- and
// applies the keyless rule (the lowest offline temporary agent, holds ignored) instead of
// giving the bad key an agent of its own.
func TestInvalidSessionKeyIsIgnored(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")

	one := registerSession(t, tok, "proj", "codex", "pl-one")
	for _, bad := range []string{"a/b", "../x", "with space", strings.Repeat("k", 65)} {
		require.Equal(t, one, registerSession(t, tok, "proj", "codex", bad), "key %q", bad)
	}
	require.Equal(t, one, registerSession(t, tok, "proj", "codex", "pl-one"), "the valid key still holds its agent")
}

// ID-01: a session key holds an agent only within its own project and harness. Keys are pids
// and holds last a day, so a recycled key can arrive for another harness or project: it then
// gets that family's agent (proj-gemini, other-codex), and still gets proj-codex back for
// proj and Codex.
func TestSessionHoldStaysInItsProjectAndHarness(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")

	codex := registerSession(t, tok, "proj", "codex", "4242")
	require.Equal(t, []string{"proj-codex", "Proj · Codex"}, []string{codex.Name, codex.DisplayName})
	gemini := registerSession(t, tok, "proj", "gemini", "4242")
	require.Equal(t, []string{"proj-gemini", "Proj · Gemini"}, []string{gemini.Name, gemini.DisplayName})
	other := registerSession(t, tok, "other", "codex", "4242")
	require.Equal(t, []string{"other-codex", "Other · Codex"}, []string{other.Name, other.DisplayName})
	require.NotContains(t, []string{codex.ID, gemini.ID}, other.ID)
	require.NotEqual(t, codex.ID, gemini.ID)
	require.Equal(t, codex, registerSession(t, tok, "proj", "codex", "4242"))
}
