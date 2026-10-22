package cli

import (
	"strings"
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

// SES-04, SES-07: a channel started without PARTYLINE_TAKEOVER finds the AI live in another
// session: it does not take it over and its tools say so.
func TestChannelStaysBusyWithoutTakeover(t *testing.T) {
	e := newChannelEnv(t)
	holder := e.holder(t, "/tmp/wd")
	c := startChannel(t, nil, map[string]string{"PARTYLINE_URL": e.env.URL, "PARTYLINE_TOKEN": e.secret}, "/tmp/wd")
	c.initialize()

	require.Eventually(t, func() bool {
		text, isErr := c.call(1, "list_rooms")
		return isErr && strings.Contains(text, "open in another session")
	}, chanWait, 20*time.Millisecond)
	require.NoError(t, holder.Closed(100*time.Millisecond), "the live session was not taken over")
}

// SES-02: PARTYLINE_TAKEOVER=1, which the launcher sets after the person agreed, makes the
// channel's first connection take the AI over: the live session is closed with taken_over and
// the channel is the AI's session.
func TestChannelTakesOverWithTheEnvironmentVariable(t *testing.T) {
	e := newChannelEnv(t)
	holder := e.holder(t, "/tmp/wd")
	c := startChannel(t, nil, map[string]string{"PARTYLINE_URL": e.env.URL, "PARTYLINE_TOKEN": e.secret, "PARTYLINE_TAKEOVER": "1"}, "/tmp/wd")
	c.initialize()

	ce := websocket.CloseStatus(holder.Closed(chanWait))
	require.Equal(t, websocket.StatusCode(4002), ce)
	prof := e.ai(t, "/tmp/wd")
	require.Equal(t, "claude", prof.AI.App, "the new session's app")
	text, isErr := c.call(2, "list_rooms")
	require.False(t, isErr, text)
}
