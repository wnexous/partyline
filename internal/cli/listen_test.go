package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/cliconfig"
	"github.com/wnexous/partyline/internal/testkit"
)

// listenProc drives `partyline listen` the way the pi and OpenCode adapters do.
type listenProc struct {
	t      *testing.T
	in     *io.PipeWriter
	lines  chan string
	stderr *lockedBuffer
	code   chan int
}

func startListen(t *testing.T, env map[string]string, wd string, stdout io.Writer) *listenProc {
	t.Helper()
	inR, inW := io.Pipe()
	p := &listenProc{t: t, in: inW, lines: make(chan string, 64), stderr: &lockedBuffer{}, code: make(chan int, 1)}
	var outW *io.PipeWriter
	if stdout == nil {
		var outR *io.PipeReader
		outR, outW = io.Pipe()
		stdout = outW
		go func() {
			sc := bufio.NewScanner(outR)
			sc.Buffer(make([]byte, 1<<20), 4<<20)
			for sc.Scan() {
				p.lines <- sc.Text()
			}
		}()
	}
	std := stdio{
		stdin: inR, stdout: stdout, stderr: p.stderr,
		getenv:   func(k string) string { return env[k] },
		getwd:    func() (string, error) { return wd, nil },
		hostname: func() (string, error) { return testMachine, nil },
	}
	go func() {
		p.code <- execute(context.Background(), []string{"partyline", "listen"}, std)
		if outW != nil {
			_ = outW.Close()
		}
	}()
	t.Cleanup(func() { _ = inW.Close() })
	return p
}

// line returns the next output line decoded as a JSON object of strings.
func (p *listenProc) line() map[string]string {
	p.t.Helper()
	select {
	case l := <-p.lines:
		var m map[string]string
		require.NoError(p.t, json.Unmarshal([]byte(l), &m), "line %q", l)
		return m
	case <-time.After(chanWait):
		require.FailNow(p.t, "no line from listen", "stderr: %s", p.stderr.String())
		return nil
	}
}

func (p *listenProc) exit() int {
	p.t.Helper()
	select {
	case code := <-p.code:
		return code
	case <-time.After(chanWait):
		require.FailNow(p.t, "listen did not exit", "stderr: %s", p.stderr.String())
		return -1
	}
}

// room creates a room of alice with the given agents as members.
func (e *channelEnv) room(t *testing.T, req dto.CreateRoomRequest, agentIDs ...string) dto.Room {
	t.Helper()
	room := testkit.Decode[dto.Room](e.alice.Post("/api/v1/rooms", req).RequireStatus(http.StatusCreated))
	for _, id := range agentIDs {
		e.alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: id}).RequireStatus(http.StatusCreated)
	}
	return room
}

func (e *channelEnv) post(t *testing.T, roomID, text string) dto.Message {
	t.Helper()
	return testkit.Decode[dto.Message](e.alice.Post("/api/v1/rooms/"+roomID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
}

func (e *channelEnv) vars(extra ...string) map[string]string {
	env := map[string]string{"PARTYLINE_URL": e.env.URL, "PARTYLINE_TOKEN": e.secret}
	for i := 0; i+1 < len(extra); i += 2 {
		env[extra[i]] = extra[i+1]
	}
	return env
}

// PUSH-01: each incoming message is one JSON line with the channel meta fields plus content.
func TestListenPrintsOneLinePerMessage(t *testing.T) {
	e := newChannelEnv(t)
	p := startListen(t, e.vars(), "/work/pi-box", nil)
	agent := e.ai(t, "/work/pi-box")
	room := e.room(t, dto.CreateRoomRequest{Name: "Support"}, agent.ID)

	msg := e.post(t, room.ID, "first line\nsecond line")
	require.Equal(t, map[string]string{
		"content":     "first line\nsecond line",
		"room":        room.ID,
		"room_name":   "Support",
		"message_id":  msg.ID,
		"author":      msg.AuthorName,
		"author_kind": "user",
		"sender_name": msg.SenderName,
		"trusted":     "true",
		"kind":        string(msg.Kind),
	}, p.line())

	next := e.post(t, room.ID, "again")
	require.Equal(t, next.ID, p.line()["message_id"], "one line per message, in order")
}

// PUSH-01: notices (here the instructions notice on connect) are lines of the same shape.
func TestListenPrintsNotices(t *testing.T) {
	e := newChannelEnv(t)
	a := e.aiAt(t, "/work/pi-box")
	room := e.room(t, dto.CreateRoomRequest{Name: "Ops", Instructions: "be brief"}, a.ID)

	p := startListen(t, e.vars(), "/work/pi-box", nil)
	line := p.line()
	require.Equal(t, room.ID, line["room"])
	require.Equal(t, "Ops", line["room_name"])
	require.Equal(t, "system", line["author_kind"])
	require.Equal(t, "false", line["trusted"])
	require.Equal(t, "This room has instructions for its agents; call the room_info tool to read them.", line["content"])
	_, hasID := line["message_id"]
	require.False(t, hasID, "a notice has no message id")
}

// PUSH-01: a message is acknowledged after its line is written, so the next listen does not get
// it again.
func TestListenAcksAfterWriting(t *testing.T) {
	e := newChannelEnv(t)
	env := e.vars()
	first := startListen(t, env, "/work/pi-box", nil)
	agent := e.ai(t, "/work/pi-box")
	room := e.room(t, dto.CreateRoomRequest{Name: "Support"}, agent.ID)
	delivered := e.post(t, room.ID, "one")
	require.Equal(t, delivered.ID, first.line()["message_id"])
	require.Eventually(t, func() bool {
		states, err := e.env.App.Agents.Rooms(context.Background(), agent.ID)
		return err == nil && len(states) == 1 && states[0].Cursor > 0
	}, chanWait, 20*time.Millisecond, "the cursor moved: the message was acknowledged")

	require.NoError(t, first.in.Close())
	require.Equal(t, 0, first.exit(), "stdin closing ends listen: %s", first.stderr.String())
	require.Eventually(t, func() bool { return !e.env.App.Gateway.Online(agent.ID) }, chanWait, 20*time.Millisecond)

	later := e.post(t, room.ID, "two")
	second := startListen(t, env, "/work/pi-box", nil)
	require.Equal(t, later.ID, second.line()["message_id"], "the acknowledged message is not repeated")
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

// PUSH-01: when the line cannot be written the message is not acknowledged: listen exits 1 and
// the next listen gets the message again.
func TestListenDoesNotAckUnwrittenLines(t *testing.T) {
	e := newChannelEnv(t)
	env := e.vars()
	a := e.aiAt(t, "/work/pi-box")
	room := e.room(t, dto.CreateRoomRequest{Name: "Support"}, a.ID)
	msg := e.post(t, room.ID, "lost?")

	broken := startListen(t, env, "/work/pi-box", failingWriter{})
	require.Equal(t, 1, broken.exit())
	require.Contains(t, broken.stderr.String(), "partyline listen: write message line: broken pipe")

	again := startListen(t, env, "/work/pi-box", nil)
	require.Equal(t, msg.ID, again.line()["message_id"])
}

// Edge case (ONB-05): listen refuses a config file that is not 0600, like channel and doctor.
// The file names a working server and token, so only the permission check can stop listen: it
// exits 1 with the chmod fix before connecting, prints no line and never shows the token.
func TestListenRefusesConfigFileNot0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
	e := newChannelEnv(t)
	home := t.TempDir()
	path := writeConfig(t, home, cliconfig.Config{URL: e.env.URL, Token: e.secret})
	require.NoError(t, os.Chmod(path, 0o640)) // group-readable only

	var out lockedBuffer
	p := startListen(t, map[string]string{"HOME": home}, "/work/pi-box", &out)
	require.Equal(t, 1, p.exit())
	require.Contains(t, p.stderr.String(), "partyline listen: config file "+path+" has mode 0640, want 0600")
	require.Contains(t, p.stderr.String(), "run chmod 600 "+path)
	require.NotContains(t, p.stderr.String(), e.secret)
	require.Empty(t, out.String())
	require.Empty(t, e.folders(t), "listen did not connect")
}

// PUSH-02, AID-04: a tools-only channel serves the tools without the push socket: the AI of its
// folder is created by its first call, never comes online, and messages are not pushed to it.
func TestChannelToolsOnlyOpensNoSocket(t *testing.T) {
	e := newChannelEnv(t)
	c := startChannel(t, []string{"--tools-only"}, e.vars(), "/work/codex-box")
	initialize(c)
	callListRooms := func(id int) string {
		t.Helper()
		c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": "list_rooms", "arguments": map[string]any{}}})
		r := c.next()
		require.Equal(t, float64(id), r["id"])
		result := r["result"].(map[string]any)
		require.Equal(t, false, result["isError"], "result: %v", result)
		return result["content"].([]any)[0].(map[string]any)["text"].(string)
	}

	require.Empty(t, e.folders(t), "nothing is created before a tool is called")
	callListRooms(5)
	agent := e.registered(t, "/work/codex-box")
	room := e.room(t, dto.CreateRoomRequest{Name: "Builds"}, agent.ID)
	require.Contains(t, callListRooms(6), "Builds")

	e.post(t, room.ID, "not pushed")
	select {
	case m := <-c.lines:
		require.FailNow(t, "a tools-only channel got output it did not ask for", "%v", m)
	case <-time.After(500 * time.Millisecond):
	}
	require.False(t, e.env.App.Gateway.Online(agent.ID), "no push socket")
}

// PUSH-02, AID-01: listen and the tools-only channel of a harness process started in one folder
// are the same AI (the v0.3 OpenCode split is gone): the tools post as the AI listen is online
// as. A tools-only channel started in another folder is another AI.
func TestListenAndToolsOnlyAreOneIdentity(t *testing.T) {
	e := newChannelEnv(t)
	wd := "/tmp/src/proj"
	listen := startListen(t, e.vars("PARTYLINE_HARNESS", "pi"), wd, nil)
	proj := e.ai(t, wd)

	post := func(c *mcpClient, id int, text string) {
		t.Helper()
		c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{
			"name": "send_message", "arguments": map[string]any{"room": "Shared", "text": text},
		}})
		r := c.next()
		require.Equal(t, false, r["result"].(map[string]any)["isError"], "result: %v", r)
	}
	tools := startChannel(t, []string{"--tools-only"}, e.vars("PARTYLINE_HARNESS", "pi"), wd)
	initialize(tools)
	elsewhere := startChannel(t, []string{"--tools-only"}, e.vars("PARTYLINE_HARNESS", "pi"), "/tmp/src/other")
	initialize(elsewhere)
	other := e.aiAt(t, "/tmp/src/other")
	room := e.room(t, dto.CreateRoomRequest{Name: "Shared"}, proj.ID, other.ID)
	post(tools, 7, "from the sibling")
	post(elsewhere, 8, "from the other folder")

	page := testkit.Decode[dto.Page[dto.Message]](e.alice.Get("/api/v1/rooms/" + room.ID + "/messages").RequireStatus(http.StatusOK))
	authors := map[string]string{}
	for _, m := range page.Items {
		authors[m.Text] = m.AuthorID
	}
	require.Equal(t, proj.ID, authors["from the sibling"])
	require.Equal(t, other.ID, authors["from the other folder"])
	require.Equal(t, []string{"/tmp/src/other", "/tmp/src/proj"}, e.folders(t), "one AI per folder, whatever the process")
	require.Equal(t, "pi", e.registered(t, wd).AI.App)

	require.NoError(t, listen.in.Close())
	require.Equal(t, 0, listen.exit())
}

// listen needs a URL and a token like the channel.
func TestListenRequiresURLAndToken(t *testing.T) {
	p := startListen(t, map[string]string{"PARTYLINE_URL": "http://127.0.0.1:1"}, "/tmp/wd", io.Discard)
	require.Equal(t, 2, p.exit())
	require.Contains(t, p.stderr.String(), "partyline listen: set PARTYLINE_URL and PARTYLINE_TOKEN")
}
