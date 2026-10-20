package api_test

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

func dialUnique(t *testing.T, c *testkit.Client, name string) (*testkit.AgentConn, string) {
	t.Helper()
	conn, resp := c.DialAgentUnique(name)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	return conn, conn.Welcome(wsWait).Agent.Name
}

func agentNames(t *testing.T, c *testkit.Client) map[string]bool {
	t.Helper()
	list := testkit.Decode[dto.List[dto.Agent]](c.Get("/api/v1/agents").RequireStatus(http.StatusOK))
	names := map[string]bool{}
	for _, a := range list.Items {
		names[a.Name] = a.Online
	}
	return names
}

// NAME-02: a derived name already online in another session registers as the next free
// <name>-N (N ≥ 2); the welcome frame carries the final name and the older session stays.
func TestAgentWSUniqueModeAssignsFreeSuffix(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")

	first, name1 := dialUnique(t, tok, "proj")
	require.Equal(t, "proj", name1)
	second, name2 := dialUnique(t, tok, "proj")
	require.Equal(t, "proj-2", name2)
	_, name3 := dialUnique(t, tok, "Proj")
	require.Equal(t, "proj-3", name3, "the requested name is slugified first")

	require.NoError(t, first.Closed(300*time.Millisecond), "the first session is not replaced")
	require.NoError(t, second.Closed(300*time.Millisecond), "the second session is not replaced")
	require.Equal(t, map[string]bool{"proj": true, "proj-2": true, "proj-3": true}, agentNames(t, alice))
}

// NAME-02: the first offline name is reused; a free gap is filled before a new suffix.
func TestAgentWSUniqueModeReusesOfflineName(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")

	first, _ := dialUnique(t, tok, "proj")
	second, _ := dialUnique(t, tok, "proj")
	_, _ = dialUnique(t, tok, "proj")

	second.Close()
	require.Eventually(t, func() bool { return !agentNames(t, alice)["proj-2"] }, wsWait, 20*time.Millisecond)
	_, again := dialUnique(t, tok, "proj")
	require.Equal(t, "proj-2", again)

	first.Close()
	require.Eventually(t, func() bool { return !agentNames(t, alice)["proj"] }, wsWait, 20*time.Millisecond)
	_, base := dialUnique(t, tok, "proj")
	require.Equal(t, "proj", base)
	require.Len(t, agentNames(t, alice), 3, "no agent beyond proj, proj-2, proj-3")
}

// NAME-02: names are per owner: bob's derived "proj" is not suffixed because alice's is online.
func TestAgentWSUniqueModeIsPerOwner(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	bob := env.NewUser("bob")
	_, aliceName := dialUnique(t, alice.Token(dto.TokenKindAgent, "laptop"), "proj")
	_, bobName := dialUnique(t, bob.Token(dto.TokenKindAgent, "desktop"), "proj")
	require.Equal(t, "proj", aliceName)
	require.Equal(t, "proj", bobName)
}

// NAME-02: sessions connecting at the same time all get distinct names; none replaces another.
func TestAgentWSUniqueModeConcurrentSessionsGetDistinctNames(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")

	const n = 6
	conns := make([]*testkit.AgentConn, n)
	names := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			conn, resp := tok.DialAgentUnique("proj")
			if conn == nil {
				t.Errorf("upgrade refused: %d %s", resp.Status, resp.Body)
				return
			}
			conns[i] = conn
			names[i] = conn.Welcome(wsWait).Agent.Name
		})
	}
	wg.Wait()
	want := map[string]bool{"proj": true}
	for i := 2; i <= n; i++ {
		want[fmt.Sprintf("proj-%d", i)] = true
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

// NAME-03: an explicit name keeps "newest wins", also over a session that connected under a
// derived name.
func TestAgentWSExplicitNameStillReplaces(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")

	derived, _ := dialUnique(t, tok, "proj")
	explicit, resp := tok.DialAgent("proj")
	require.NotNil(t, explicit, "upgrade refused: %d %s", resp.Status, resp.Body)
	require.Equal(t, "proj", explicit.Welcome(wsWait).Agent.Name)

	ce := closeError(t, derived.Closed(wsWait))
	require.Equal(t, websocket.StatusCode(4000), ce.Code)
	require.Equal(t, "replaced", ce.Reason)
	require.Equal(t, map[string]bool{"proj": true}, agentNames(t, alice))
}

// NAME-02 with the agent cap (agents.MaxAgentsPerOwner = 50): a suffix that would be a new
// agent beyond the cap answers 409 "agent limit reached" and registers nothing; an offline
// existing name still connects.
func TestAgentWSUniqueModeKeepsAgentCap(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	for i := range 48 {
		registerAgent(t, env, alice, fmt.Sprintf("agent-%02d", i))
	}
	proj, _ := dialUnique(t, tok, "proj")   // 49th agent
	_, second := dialUnique(t, tok, "proj") // 50th agent
	require.Equal(t, "proj-2", second)

	conn, resp := tok.DialAgentUnique("proj")
	require.Nil(t, conn)
	e := resp.RequireError(http.StatusConflict, dto.ErrorCodeConflict)
	require.Equal(t, "agent limit reached", e.Message)
	names := agentNames(t, alice)
	require.Len(t, names, 50)
	require.NotContains(t, names, "proj-3")

	proj.Close()
	require.Eventually(t, func() bool { return !agentNames(t, alice)["proj"] }, wsWait, 20*time.Millisecond)
	_, again := dialUnique(t, tok, "proj")
	require.Equal(t, "proj", again, "an offline existing agent connects at the cap")
}

// NAME-02: a suffixed name still fits the 48-character agent name limit.
func TestAgentWSUniqueModeSuffixFitsNameLimit(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	long := strings.Repeat("a", 48)

	_, first := dialUnique(t, tok, long)
	require.Equal(t, long, first)
	_, second := dialUnique(t, tok, long)
	require.Equal(t, strings.Repeat("a", 46)+"-2", second)
	require.Regexp(t, regexp.MustCompile(dto.AgentNamePattern), second)
}
