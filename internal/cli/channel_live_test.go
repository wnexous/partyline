package cli

import (
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/testkit"
)

// initialize performs Claude Code's handshake with the channel.
func (c *mcpClient) initialize() {
	c.t.Helper()
	c.send(map[string]any{"jsonrpc": "2.0", "id": 0, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25"}})
	c.next()
	c.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
}

// call runs a tool and returns its text and whether it is an error.
func (c *mcpClient) call(id int, tool string) (string, bool) {
	c.t.Helper()
	c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": map[string]any{}}})
	for {
		m := c.next()
		if m["id"] != float64(id) {
			continue // a notification
		}
		result := m["result"].(map[string]any)
		return result["content"].([]any)[0].(map[string]any)["text"].(string), result["isError"] == true
	}
}

// holder opens the live session of alice's AI of the folder, the way another process would.
func (e *channelEnv) holder(t *testing.T, folder string) *testkit.AgentConn {
	t.Helper()
	conn, resp := e.alice.WithBearer(e.secret).DialFolder(testkit.Folder{Machine: testMachine, Path: folder, Harness: "opencode"})
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	conn.Welcome(chanWait)
	return conn
}

// SESS-10, SESS-16: a channel started without PARTYLINE_TAKEOVER while the AI is live in another
// session attaches as a parallel session: the other session is left alone and its tools work.
func TestChannelStartsAsAParallelSessionWithoutTakeover(t *testing.T) {
	e := newChannelEnv(t)
	holder := e.holder(t, "/tmp/wd")
	c := startChannel(t, nil, map[string]string{"PARTYLINE_URL": e.env.URL, "PARTYLINE_TOKEN": e.secret, "PARTYLINE_HARNESS": "claude"}, "/tmp/wd")
	c.initialize()

	require.Eventually(t, func() bool {
		_, isErr := c.call(1, "list_rooms")
		return !isErr
	}, chanWait, 20*time.Millisecond)
	require.NoError(t, holder.Closed(100*time.Millisecond), "the live session was not taken over")
}

// SES-02: PARTYLINE_TAKEOVER=1, which the launcher sets after the person agreed, makes the
// channel's first connection take the AI over: the live session is closed with taken_over and
// the channel is the AI's session.
func TestChannelTakesOverWithTheEnvironmentVariable(t *testing.T) {
	e := newChannelEnv(t)
	holder := e.holder(t, "/tmp/wd")
	c := startChannel(t, nil, map[string]string{"PARTYLINE_URL": e.env.URL, "PARTYLINE_TOKEN": e.secret, "PARTYLINE_TAKEOVER": "1", "PARTYLINE_HARNESS": "claude"}, "/tmp/wd")
	c.initialize()

	ce := websocket.CloseStatus(holder.Closed(chanWait))
	require.Equal(t, websocket.StatusCode(4002), ce)
	prof := e.ai(t, "/tmp/wd")
	require.Equal(t, "claude", prof.AI.App, "the new session's app")
	text, isErr := c.call(2, "list_rooms")
	require.False(t, isErr, text)
}

// SESS-51, SESS-52: the channel of a Claude Code session sends CLAUDE_CODE_SESSION_ID as its harness session
// id, and the first session of the AI pins it; a second channel with another id is a parallel session.
func TestChannelSendsClaudeCodesSessionID(t *testing.T) {
	e := newChannelEnv(t)
	const first, second = "15cf4c1e-3a52-4a8e-9f6b-0123456789ab", "073e8d10-0000-4000-8000-000000000001"
	env := func(id string) map[string]string {
		return map[string]string{"PARTYLINE_URL": e.env.URL, "PARTYLINE_TOKEN": e.secret, "PARTYLINE_HARNESS": "claude", "CLAUDE_CODE_SESSION_ID": id}
	}
	a := startChannel(t, nil, env(first), "/tmp/wd")
	a.initialize()
	var id string
	require.Eventually(t, func() bool {
		prof := e.ai(t, "/tmp/wd")
		id = prof.ID
		return prof.AI != nil && prof.AI.Online
	}, chanWait, 20*time.Millisecond)
	agent, err := e.env.App.Store.R().GetAgent(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, first, agent.PrincipalSession, "the session id Claude Code gave the channel is pinned")

	b := startChannel(t, nil, env(second), "/tmp/wd")
	b.initialize()
	require.Eventually(t, func() bool { return len(e.ai(t, "/tmp/wd").AI.Sessions) == 2 }, chanWait, 20*time.Millisecond)
	agent, err = e.env.App.Store.R().GetAgent(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, first, agent.PrincipalSession, "a parallel session does not move the pin")
}
