package cli

import (
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/channel"
	"github.com/wnexous/partyline/internal/testkit"
)

const liveTestID = "AAAAAAAAAAAAAAAAAAAAAA"

// SESS-45: PARTYLINE_LIVE is "<pid>:<id>"; anything else is no live id.
func TestParseLive(t *testing.T) {
	pid, id, ok := parseLive(" 4242:" + liveTestID + " ")
	require.True(t, ok)
	require.Equal(t, 4242, pid)
	require.Equal(t, liveTestID, id)
	for _, bad := range []string{"", liveTestID, "x:" + liveTestID, "0:" + liveTestID, "-3:" + liveTestID, "4242:short", "4242:" + liveTestID + " x", "4242:bad id bad id bad id"} {
		_, _, ok := parseLive(bad)
		require.False(t, ok, bad)
	}
}

// SESS-45: the id is tied to the harness process that owns it. The environment of an ancestor gives it only
// when that ancestor is the owner, and a process reads its own copy only when its parent is the owner: a pi
// started by a tool of another pi inherits the variable and must not share the id.
func TestLiveIDBelongsToItsOwner(t *testing.T) {
	proc := newFakeProc(t)
	proc.add(40, 30, "partyline", []string{"PATH=/usr/bin"}) // the MCP server: stripped
	proc.add(30, 20, "sh", nil)
	proc.add(20, 10, "pi", []string{"PARTYLINE_FOLDER=/work/pi", "PARTYLINE_HARNESS=pi", liveEnvVar + "=20:" + liveTestID})
	proc.add(10, 1, "bash", nil)
	require.Equal(t, "20:"+liveTestID, ancestorLauncherEnv(proc.root, 40)[liveEnvVar], "the owner's environment gives it")

	nested := newFakeProc(t)
	nested.add(40, 30, "partyline", []string{"PATH=/usr/bin"})
	nested.add(30, 1, "pi", []string{"PARTYLINE_FOLDER=/work/pi", "PARTYLINE_HARNESS=pi", liveEnvVar + "=20:" + liveTestID})
	got := ancestorLauncherEnv(nested.root, 40)
	require.Equal(t, "/work/pi", got["PARTYLINE_FOLDER"])
	require.NotContains(t, got, liveEnvVar, "an id inherited from the pi that started this one is not this session's")

	std := func(own map[string]string, ppid int) stdio {
		return stdio{getenv: func(k string) string { return own[k] }, ppid: func() int { return ppid }}
	}
	require.Equal(t, liveTestID, sessionLiveID(withLauncherEnv(std(map[string]string{liveEnvVar: "77:" + liveTestID}, 77))))
	require.Empty(t, sessionLiveID(withLauncherEnv(std(map[string]string{liveEnvVar: "77:" + liveTestID}, 78))), "its parent is not the owner")
	require.Empty(t, sessionLiveID(withLauncherEnv(std(nil, 77))))

	fromAncestor := withLauncherEnv(stdio{
		getenv: func(string) string { return "" }, ppid: func() int { return 1 },
		launcherEnv: func() map[string]string {
			return map[string]string{"PARTYLINE_FOLDER": "/work/pi", liveEnvVar: "20:" + liveTestID}
		},
	})
	require.Equal(t, liveTestID, sessionLiveID(fromAncestor), "the owner's value, checked where it was read")
}

// call runs a tool with arguments and returns its text and whether it is an error.
func (c *mcpClient) callWith(id int, tool string, args map[string]any) (string, bool) {
	c.t.Helper()
	c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	for {
		m := c.next()
		if m["id"] != float64(id) {
			continue
		}
		result := m["result"].(map[string]any)
		return result["content"].([]any)[0].(map[string]any)["text"].(string), result["isError"] == true
	}
}

// parallelPi opens a pi session next to a principal that is already live: the `partyline listen` of pi with
// the live id of the session, returning the room both are members of and the label the server gave pi.
func (e *channelEnv) parallelPi(t *testing.T, folder, live string) (dto.Room, string, *listenProc) {
	t.Helper()
	e.holder(t, folder) // the principal, in another harness
	listen := startListen(t, e.launched("pi", liveEnvVar, live), folder, nil)
	prof := e.ai(t, folder)
	var label string
	require.Eventually(t, func() bool {
		for _, s := range e.env.App.Agents.Sessions(prof.ID) {
			if s.Label != "" {
				label = s.Label
			}
		}
		return label != ""
	}, chanWait, 20*time.Millisecond, "the pi session became a parallel session")
	return e.room(t, dto.CreateRoomRequest{Name: "Support"}, prof.ID), label, listen
}

// SESS-45: the tools half of pi acts as the session its listen process holds. With the live id both got, a
// message it writes carries that session's label; without it (Codex, a value another process owns) it posts
// as the AI's principal.
func TestTheToolsHalfOfPiPostsAsItsListenSession(t *testing.T) {
	e := newChannelEnv(t)
	live := strconv.Itoa(os.Getppid()) + ":" + channel.NewLiveID()
	room, label, _ := e.parallelPi(t, "/work/pi-box", live)
	require.Equal(t, "pi-2", label)

	post := func(c *mcpClient, id int, text string) dto.Message {
		t.Helper()
		out, isErr := c.callWith(id, "send_message", map[string]any{"room": room.ID, "text": text})
		require.False(t, isErr, out)
		msgs := testkit.Decode[dto.Page[dto.Message]](e.alice.Get("/api/v1/rooms/" + room.ID + "/messages?limit=1").RequireStatus(http.StatusOK)).Items
		require.Len(t, msgs, 1)
		require.Equal(t, text, msgs[0].Text)
		return msgs[0]
	}

	tools := startChannel(t, []string{"--tools-only", "--harness", "pi"}, e.launched("pi", liveEnvVar, live), "/work/pi-box")
	tools.initialize()
	require.Equal(t, label, post(tools, 1, "from the pi tools").AuthorLabel)

	// Its environment is stripped: the id comes from the launched ancestor that owns it.
	owner := os.Getppid()
	stripped := startChannelWith(t, []string{"--tools-only", "--harness", "pi"}, e.vars(), "/tmp/elsewhere",
		map[string]string{"PARTYLINE_FOLDER": "/work/pi-box", "PARTYLINE_HARNESS": "pi", liveEnvVar: strconv.Itoa(owner) + ":" + live[len(strconv.Itoa(owner))+1:]})
	stripped.initialize()
	require.Equal(t, label, post(stripped, 2, "from the stripped pi tools").AuthorLabel)

	// An id some other process owns is ignored: the tools post as the principal, as before.
	foreign := startChannel(t, []string{"--tools-only", "--harness", "pi"}, e.launched("pi", liveEnvVar, "1:"+live[len(strconv.Itoa(os.Getppid()))+1:]), "/work/pi-box")
	foreign.initialize()
	require.Empty(t, post(foreign, 3, "from a foreign id").AuthorLabel)

	// Codex has no listen process and sends no live id, whatever the environment holds.
	codex := startChannel(t, []string{"--tools-only", "--harness", "codex"}, e.launched("codex", liveEnvVar, live), "/work/pi-box")
	codex.initialize()
	require.Empty(t, post(codex, 4, "from codex").AuthorLabel)
}

// SESS-45: listen connects with the id it is given, and a restart with the same id is the same session: it keeps
// the label the first run got.
func TestListenKeepsTheLabelOfItsLiveIDAcrossRestarts(t *testing.T) {
	e := newChannelEnv(t)
	live := strconv.Itoa(os.Getppid()) + ":" + channel.NewLiveID()
	_, label, first := e.parallelPi(t, "/work/pi-box", live)
	require.Equal(t, "pi-2", label)

	require.NoError(t, first.in.Close())
	require.Equal(t, 0, first.exit())
	prof := e.ai(t, "/work/pi-box")
	require.Eventually(t, func() bool { return len(e.env.App.Agents.Sessions(prof.ID)) == 1 }, chanWait, 20*time.Millisecond)

	startListen(t, e.launched("pi", liveEnvVar, live), "/work/pi-box", nil)
	require.Eventually(t, func() bool {
		sessions := e.env.App.Agents.Sessions(prof.ID)
		return len(sessions) == 2 && sessions[1].Label == "pi-2"
	}, chanWait, 20*time.Millisecond, "the restart is pi-2 again")
}
