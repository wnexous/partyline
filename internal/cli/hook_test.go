package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// runHook runs `partyline hook <name>` with an empty environment and no server: the hook must not need either.
func runHook(name string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	std := stdio{
		stdin: strings.NewReader(`{"cwd":"/work/proj"}`), stdout: &out, stderr: &errOut,
		getenv: func(string) string { return "" },
	}
	code = execute(context.Background(), []string{"partyline", "hook", name}, std)
	return code, out.String(), errOut.String()
}

// AD-034: the turn-end hooks of Codex, Gemini and Antigravity are gone. One that setup has not removed yet
// still runs `partyline hook <name>`: it lets the harness stop as usual and prints nothing else, so the old
// session does not fail at the end of every turn.
func TestHookOfARemovedHarnessAllowsTheStop(t *testing.T) {
	for _, name := range []string{"codex-stop", "gemini-after-agent", "antigravity-stop"} {
		t.Run(name, func(t *testing.T) {
			code, out, errOut := runHook(name)
			require.Equal(t, 0, code)
			require.Equal(t, "{}\n", out)
			require.Empty(t, errOut)
		})
	}
}

// Any other hook name is a usage error that points at setup.
func TestHookRequiresAKnownName(t *testing.T) {
	for _, args := range [][]string{{"partyline", "hook"}, {"partyline", "hook", "claude-stop"}} {
		var out, errOut bytes.Buffer
		code := execute(context.Background(), args, stdio{stdout: &out, stderr: &errOut})
		require.Equal(t, 2, code, args)
		require.Empty(t, out.String())
		require.Contains(t, errOut.String(), "partyline setup")
	}
}

// AID-01, AID-04: the tools-only channels of pi sessions in two folders are two AIs, each acting as the AI of
// its own folder.
func TestToolsOnlyChannelActsAsTheAIOfItsFolder(t *testing.T) {
	e := newChannelEnv(t)
	session := func(folder string) *mcpClient {
		c := startChannel(t, []string{"--tools-only"}, e.vars("PARTYLINE_FOLDER", folder, "PARTYLINE_HARNESS", "pi"), "/tmp/src/anywhere")
		initialize(c)
		c.send(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "list_rooms", "arguments": map[string]any{}}})
		require.Equal(t, false, c.next()["result"].(map[string]any)["isError"])
		return c
	}
	session("/work/notes")
	session("/work/lab")
	notes, lab := e.registered(t, "/work/notes"), e.registered(t, "/work/lab")
	require.NotEqual(t, notes.ID, lab.ID)
	require.Equal(t, "pi", notes.AI.App)
	e.room(t, dto.CreateRoomRequest{Name: "Builds"}, notes.ID, lab.ID)
	require.Equal(t, []string{"/work/lab", "/work/notes"}, e.folders(t))
}

// AD-034: no session polls for messages any more; the tools-only server does not offer wait_for_messages.
func TestToolsOnlyChannelHasNoWaitTool(t *testing.T) {
	e := newChannelEnv(t)
	c := startChannel(t, []string{"--tools-only"}, e.launched("pi"), "/work/pi-box")
	initialize(c)
	c.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	var names []string
	for _, tool := range c.next()["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	require.Contains(t, names, "reply")
	require.NotContains(t, names, "wait_for_messages")
}
