package cli

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// closeIdle closes the input (the harness exited) and requires a clean exit.
func closeIdle(t *testing.T, c *mcpClient) {
	t.Helper()
	require.NoError(t, c.in.Close())
	select {
	case code := <-c.code:
		require.Equal(t, 0, code, "stderr: %s", c.stderr.String())
	case <-time.After(chanWait):
		require.FailNow(t, "the idle channel did not exit", "stderr: %s", c.stderr.String())
	}
}

// initializeIdle performs the MCP handshake of a plain harness and checks the answer: no
// tools, no instructions and no channel capability.
func initializeIdle(t *testing.T, c *mcpClient) {
	t.Helper()
	c.send(map[string]any{"jsonrpc": "2.0", "id": 0, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25"}})
	res := c.next()["result"].(map[string]any)
	require.Equal(t, "partyline", res["serverInfo"].(map[string]any)["name"])
	require.Equal(t, map[string]any{"tools": map[string]any{}}, res["capabilities"], "no channel capability")
	require.NotContains(t, res, "instructions")
	c.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	c.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	require.Equal(t, []any{}, c.next()["result"].(map[string]any)["tools"], "a plain session shows no partyline tools")
}

// launch-only: a plain harness starts the MCP server `partyline setup` registered without
// `partyline <harness>`: it answers the handshake with no tools and no instructions and never
// reaches the server, so no AI appears for its folder.
func TestToolsOnlyChannelWithoutLauncherIsIdle(t *testing.T) {
	t.Run("reachable server", func(t *testing.T) {
		e := newChannelEnv(t)
		c := startChannel(t, []string{"--tools-only", "--harness", "pi"}, e.vars(), "/work/plain-pi")
		initializeIdle(t, c)
		closeIdle(t, c)
		require.Empty(t, c.stderr.String())
		require.Empty(t, e.folders(t), "nothing registered an AI")
	})

	t.Run("no credentials", func(t *testing.T) {
		c := startChannel(t, []string{"--tools-only"}, map[string]string{}, "/work/plain-pi")
		initializeIdle(t, c)
		closeIdle(t, c)
		require.Empty(t, c.stderr.String(), "no error without credentials")
	})
}

// launch-only (Claude Code, omp): the full channel of a plain session is idle in the same way.
func TestChannelWithoutLauncherIsIdle(t *testing.T) {
	e := newChannelEnv(t)
	c := startChannel(t, nil, e.vars(), "/work/plain-claude")
	initializeIdle(t, c)
	closeIdle(t, c)
	require.Empty(t, e.folders(t))
}

// setup --always: the server setup registered with the flag runs without the launcher.
func TestToolsOnlyChannelWithAlwaysRunsWithoutLauncher(t *testing.T) {
	e := newChannelEnv(t)
	c := startChannel(t, []string{"--tools-only", "--always"}, e.vars(), "/work/always-pi")
	initialize(c)
	c.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "list_rooms", "arguments": map[string]any{}}})
	require.Equal(t, false, c.next()["result"].(map[string]any)["isError"])
	require.Equal(t, []string{"/work/always-pi"}, e.folders(t))
}
