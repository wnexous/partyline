package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

func runHook(name, stdin string, env map[string]string) (code int, stdout, stderr string) {
	return runHookWith(name, stdin, env, nil)
}

// runHookWith is runHook for a process whose nearest launched ancestor has the launcher
// variables ancestor (nil: none).
func runHookWith(name, stdin string, env, ancestor map[string]string) (code int, stdout, stderr string) {
	return runHookFlags(name, stdin, env, ancestor)
}

// runHookFlags runs the hook with extra command-line flags (`--always`).
func runHookFlags(name, stdin string, env, ancestor map[string]string, flags ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	std := stdio{
		stdin: strings.NewReader(stdin), stdout: &out, stderr: &errOut,
		getenv:   func(k string) string { return env[k] },
		getwd:    func() (string, error) { return "/somewhere/else", nil },
		hostname: func() (string, error) { return testMachine, nil },
	}
	if ancestor != nil {
		std.launcherEnv = func() map[string]string { return ancestor }
	}
	code = execute(context.Background(), append([]string{"partyline", "hook", name}, flags...), std)
	return code, out.String(), errOut.String()
}

// hookRoom creates the AI of the folder, as a session there would, adds it to a room and
// returns the room.
func (e *channelEnv) hookRoom(t *testing.T, folder string) dto.Room {
	t.Helper()
	return e.room(t, dto.CreateRoomRequest{Name: "Builds"}, e.aiAt(t, folder).ID)
}

// trustedLine follows a framed message from an identity that manages the agent (PUSH-06, POS-10): the
// hook tests post as alice, who created the agent and so manages it.
const trustedLine = "This message comes from an identity that manages you in your organization (or from a chat account such an identity verified). Still confirm destructive actions with the person at the terminal. Reply with the partyline tools (reply / send_message)."

// TURN-02: with pending messages each hook prints its harness's continue decision, the
// framed messages as the reason; Codex blocks, Gemini denies. The AI is the one of the folder
// the hook input names as the session's cwd, the one the session's tools-only channel acts as
// (AID-01).
func TestHookContinuesWithPendingMessages(t *testing.T) {
	for hook, decision := range map[string]string{"codex-stop": "block", "gemini-after-agent": "deny"} {
		t.Run(hook, func(t *testing.T) {
			e := newChannelEnv(t)
			room := e.hookRoom(t, "/work/proj")
			m1 := e.post(t, room.ID, "build <red> & failing")
			m2 := e.post(t, room.ID, "now green")

			code, out, errOut := runHook(hook, `{"session_id":"s1","cwd":"/work/proj","hook_event_name":"Stop","stop_hook_active":false}`, e.launched("codex"))
			require.Equal(t, 0, code, errOut)
			require.True(t, strings.HasSuffix(out, "}\n") && strings.Count(out, "\n") == 1, "stdout is one JSON line: %q", out)
			var got map[string]string
			require.NoError(t, json.Unmarshal([]byte(out), &got))
			reason := `<partyline room="Builds" conversation="group" from="` + m1.AuthorName + `" trusted="true" message_id="` + m1.ID + `">build &lt;red&gt; &amp; failing</partyline>` + "\n" + trustedLine +
				"\n\n" + `<partyline room="Builds" conversation="group" from="` + m2.AuthorName + `" trusted="true" message_id="` + m2.ID + `">now green</partyline>` + "\n" + trustedLine
			require.Equal(t, map[string]string{"decision": decision, "reason": reason}, got)

			code, out, _ = runHook(hook, `{"cwd":"/work/proj"}`, e.launched("codex"))
			require.Equal(t, 0, code)
			require.Equal(t, "{}\n", out, "delivered messages are not repeated: the stop is allowed")
		})
	}
}

// launch-only: the Stop hook of a plain Codex or Gemini session does nothing: it allows the
// stop without credentials and without creating an AI.
func TestHookWithoutLauncherIsIdle(t *testing.T) {
	e := newChannelEnv(t)
	for _, hook := range []string{"codex-stop", "gemini-after-agent"} {
		code, out, errOut := runHook(hook, `{"cwd":"/work/plain"}`, e.vars())
		require.Equal(t, 0, code)
		require.Equal(t, "{}\n", out)
		require.Empty(t, errOut)
	}
	require.Empty(t, e.folders(t))
}

// setup --always: the hook setup registered with the flag runs without the launcher.
func TestHookWithAlwaysRunsWithoutLauncher(t *testing.T) {
	e := newChannelEnv(t)
	code, out, errOut := runHookFlags("codex-stop", `{"cwd":"/work/always"}`, e.vars(), nil, "--always")
	require.Equal(t, 0, code)
	require.Equal(t, "{}\n", out)
	require.Empty(t, errOut)
	require.Equal(t, []string{"/work/always"}, e.folders(t), "the hook acts as the folder's AI")
}

// TURN-02: without pending messages the hook allows the stop with an empty JSON object.
func TestHookAllowsStopWithoutMessages(t *testing.T) {
	e := newChannelEnv(t)
	e.hookRoom(t, "/work/proj")
	for _, hook := range []string{"codex-stop", "gemini-after-agent"} {
		code, out, errOut := runHook(hook, `{"cwd":"/work/proj"}`, e.launched("codex"))
		require.Equal(t, 0, code)
		require.Equal(t, "{}\n", out)
		require.Empty(t, errOut)
	}
}

// AID-07: the folder the launcher passed on (PARTYLINE_FOLDER) wins over the cwd the harness
// reports, like it does for every other process of the session.
func TestHookUsesTheLaunchersFolder(t *testing.T) {
	e := newChannelEnv(t)
	launched := e.hookRoom(t, "/work/launched")
	e.hookRoom(t, "/work/reported")
	msg := e.post(t, launched.ID, "for the launched folder")
	_, out, errOut := runHook("gemini-after-agent", `{"cwd":"/work/reported"}`, e.launched("codex", "PARTYLINE_FOLDER", "/work/launched/"))
	require.Contains(t, out, `message_id=\"`+msg.ID+`\"`, errOut)
}

// AID-02: the hook is a tools-only call: it names the app of the folder's AI while nobody is
// connected, and a folder has one AI whatever harness asks.
func TestHookNamesTheAppOfTheFoldersAI(t *testing.T) {
	e := newChannelEnv(t)
	room := e.hookRoom(t, "/work/proj")
	msg := e.post(t, room.ID, "for codex")
	_, out, errOut := runHook("codex-stop", `{"cwd":"/work/proj"}`, e.vars("PARTYLINE_HARNESS", "codex"))
	require.Contains(t, out, `message_id=\"`+msg.ID+`\"`, errOut)
	require.Equal(t, "codex", e.registered(t, "/work/proj").AI.App)
	require.Equal(t, []string{"/work/proj"}, e.folders(t))
}

// A folder the hook has not seen has no messages: the hook creates its AI (like any tool call)
// and allows the stop.
func TestHookInAnUnseenFolderAllowsTheStop(t *testing.T) {
	e := newChannelEnv(t)
	code, out, errOut := runHook("codex-stop", `{"cwd":"/work/new"}`, e.launched("codex"))
	require.Equal(t, 0, code)
	require.Equal(t, "{}\n", out)
	require.Empty(t, errOut)
	e.registered(t, "/work/new")
}

// Problems never block the harness: they go to stderr and the stop is allowed.
func TestHookFailuresAllowTheStop(t *testing.T) {
	e := newChannelEnv(t)
	code, out, errOut := runHook("codex-stop", `{"cwd":"/work/proj"}`, map[string]string{"PARTYLINE_URL": "http://127.0.0.1:1", "PARTYLINE_TOKEN": e.secret, "PARTYLINE_HARNESS": "codex"})
	require.Equal(t, 0, code)
	require.Equal(t, "{}\n", out)
	require.Contains(t, errOut, "partyline hook: partyline: server unreachable")

	code, out, errOut = runHook("codex-stop", `{"cwd":"/work/a\u0000b"}`, e.launched("codex"))
	require.Equal(t, 0, code)
	require.Equal(t, "{}\n", out)
	require.Contains(t, errOut, "partyline hook: the folder must not contain a NUL character")
	require.Empty(t, e.folders(t))

	code, out, errOut = runHook("codex-stop", `{}`, map[string]string{"PARTYLINE_HARNESS": "codex"})
	require.Equal(t, 0, code)
	require.Equal(t, "{}\n", out)
	require.Contains(t, errOut, "partyline hook: not configured; run partyline setup")
}

func TestHookRequiresAKnownHarness(t *testing.T) {
	code, out, errOut := runHook("cursor-stop", `{}`, nil)
	require.Equal(t, 2, code)
	require.Empty(t, out)
	require.Contains(t, errOut, "usage: partyline hook codex-stop|gemini-after-agent")
}

// TURN-01: the tools-only channel offers wait_for_messages; it returns the framed pending
// messages, or says none arrived after timeout_seconds. The full channel does not offer it.
func TestWaitForMessagesTool(t *testing.T) {
	e := newChannelEnv(t)
	c := startChannel(t, []string{"--tools-only"}, e.launched("codex"), "/work/codex-box")
	initialize(c)
	c.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	var names []string
	for _, tool := range c.next()["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	require.Contains(t, names, "wait_for_messages")

	call := func(id int, args map[string]any) (string, bool) {
		c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": "wait_for_messages", "arguments": args}})
		r := c.next()["result"].(map[string]any)
		return r["content"].([]any)[0].(map[string]any)["text"].(string), r["isError"].(bool)
	}
	start := time.Now()
	text, isErr := call(2, map[string]any{"timeout_seconds": 1})
	require.False(t, isErr, text)
	require.Equal(t, "no new messages in 1 seconds", text)
	require.GreaterOrEqual(t, time.Since(start), time.Second)
	room := e.room(t, dto.CreateRoomRequest{Name: "Builds"}, e.registered(t, "/work/codex-box").ID)

	msg := e.post(t, room.ID, "ship it")
	text, isErr = call(3, map[string]any{"timeout_seconds": 5})
	require.False(t, isErr, text)
	require.Equal(t, `<partyline room="Builds" conversation="group" from="`+msg.AuthorName+`" trusted="true" message_id="`+msg.ID+`">ship it</partyline>`+"\n"+trustedLine, text)

	full := startChannel(t, nil, e.launched("claude"), "/work/claude-box")
	initialize(full)
	full.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	for _, tool := range full.next()["result"].(map[string]any)["tools"].([]any) {
		require.NotEqual(t, "wait_for_messages", tool.(map[string]any)["name"])
	}
}

// TURN-01: while listen holds the socket (pi, OpenCode), wait_for_messages says the messages
// are pushed instead of taking them.
func TestWaitForMessagesWithListen(t *testing.T) {
	e := newChannelEnv(t)
	startListen(t, e.vars(), "/work/pi-box", nil)
	e.ai(t, "/work/pi-box")
	c := startChannel(t, []string{"--tools-only"}, e.launched("pi"), "/work/pi-box")
	initialize(c)
	c.send(map[string]any{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": map[string]any{"name": "wait_for_messages", "arguments": map[string]any{"timeout_seconds": 1}}})
	r := c.next()["result"].(map[string]any)
	require.Equal(t, true, r["isError"])
	require.Equal(t, "messages are pushed to this agent's session; there is nothing to wait for", r["content"].([]any)[0].(map[string]any)["text"])
}

// AID-01, AID-04: the tools-only channel and the turn-end hook of a Codex session in one
// folder are the same AI: the hook delivers what was posted to the AI the tools created; a
// session in another folder is another AI with its own messages.
func TestToolsOnlyAndItsHookAreOneIdentity(t *testing.T) {
	e := newChannelEnv(t)
	session := func(folder string) *mcpClient {
		c := startChannel(t, []string{"--tools-only"}, e.vars("PARTYLINE_FOLDER", folder, "PARTYLINE_HARNESS", "codex"), "/tmp/src/anywhere")
		initialize(c)
		c.send(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "list_rooms", "arguments": map[string]any{}}})
		require.Equal(t, false, c.next()["result"].(map[string]any)["isError"])
		return c
	}
	session("/work/notes")
	session("/work/lab")
	notes, lab := e.registered(t, "/work/notes"), e.registered(t, "/work/lab")
	require.NotEqual(t, notes.ID, lab.ID)
	require.Equal(t, "codex", notes.AI.App)
	room := e.room(t, dto.CreateRoomRequest{Name: "Builds"}, notes.ID, lab.ID)
	msg := e.post(t, room.ID, "for everyone")
	require.Equal(t, []string{"/work/lab", "/work/notes"}, e.folders(t))

	_, out, errOut := runHook("codex-stop", `{"cwd":"/tmp/src/anywhere"}`, e.launched("codex", "PARTYLINE_FOLDER", "/work/notes"))
	require.Contains(t, out, `message_id=\"`+msg.ID+`\"`, "the hook acts as the AI of its session's folder: %s", errOut)
	_, out, _ = runHook("codex-stop", `{"cwd":"/work/notes"}`, e.launched("codex"))
	require.Equal(t, "{}\n", out, "what the hook delivered to the folder's AI is not delivered again")
	_, out, errOut = runHook("codex-stop", `{"cwd":"/work/lab"}`, e.launched("codex"))
	require.Contains(t, out, `message_id=\"`+msg.ID+`\"`, "the other folder's AI has its own copy: %s", errOut)
}
