package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/testkit"
)

// holderStart is when the session that holds the AI connected, on the server's clock; the
// person's zone is UTC-3, so the channel says 09:30.
var holderStart = time.Date(2026, 10, 21, 12, 30, 0, 0, time.UTC)

var personZone = time.FixedZone("UTC-3", -3*60*60)

const (
	// wantBusy is SES-04's text for an AI that OpenCode holds since 09:30, told to a channel that
	// runs in Claude Code.
	wantBusy = `Builder is open in another session (OpenCode, since 09:30). Partyline is not connected here. To take over, restart with "partyline claude --take-over" or set PARTYLINE_TAKEOVER=1.`
	// wantBusyUnknown is what a channel can say when the server only closed its socket with
	// identity_busy and so told nothing about the session that holds the AI.
	wantBusyUnknown = `This intelligence is open in another session. Partyline is not connected here. To take over, restart with "partyline claude --take-over" or set PARTYLINE_TAKEOVER=1.`
)

// allTools are the tools of the channel with arguments that need no server answer.
var allTools = map[string]map[string]any{
	"reply":               toolArgs("message_id", "m", "text", "x"),
	"react":               toolArgs("message_id", "m", "emoji", "👍"),
	"send_message":        toolArgs("room", "Support", "text", "x"),
	"room_info":           toolArgs("room", "Support"),
	"list_rooms":          {},
	"history":             toolArgs("room", "Support"),
	"download_attachment": toolArgs("attachment_id", "a"),
	"send_file":           toolArgs("room", "Support", "path", "/etc/hostname"),
}

// busyEnv is a server whose clock stands at holderStart with OpenCode holding alice's AI of
// /work/builder.
func busyEnv(t *testing.T) (*chanEnv, *testkit.AgentConn) {
	t.Helper()
	e := newChanEnv(t, testkit.WithClock(testkit.NewClock(holderStart).Now))
	holder, resp := e.alice.WithBearer(e.secret).DialFolder(testkit.Folder{Machine: "testbox", Path: "/work/builder", Harness: "opencode"})
	require.NotNil(t, holder, "upgrade refused: %d %s", resp.Status, resp.Body)
	holder.Welcome(wait)
	return e, holder
}

// takeOver opens a session of OpenCode that takes the AI of /work/builder over.
func (e *chanEnv) takeOver() *testkit.AgentConn {
	e.t.Helper()
	f := testkit.Folder{Machine: "testbox", Path: "/work/builder", Harness: "opencode", Takeover: true}
	taker, resp := e.alice.WithBearer(e.secret).DialFolder(f)
	require.NotNil(e.t, taker, "upgrade refused: %d %s", resp.Status, resp.Body)
	taker.Welcome(wait)
	return taker
}

// SES-04: while another session holds the AI, every tool answers that it is open elsewhere, in
// which app and since when (local HH:MM), and how to take over; none of them reaches the server
// and the channel pushes no notification.
func TestEveryToolAnswersTheBusyText(t *testing.T) {
	e, holder := busyEnv(t)
	o := e.options()
	o.zone = personZone
	o.timing.busyRetries = []time.Duration{} // no retry: the state stays busy
	s := startSession(t, o)
	s.handshake()
	require.Eventually(t, func() bool {
		text, isErr := s.call("list_rooms", map[string]any{})
		return isErr && text == wantBusy
	}, wait, 20*time.Millisecond)

	for tool, args := range allTools {
		text, isErr := s.call(tool, args)
		require.True(t, isErr, tool)
		require.Equal(t, wantBusy, text, tool)
	}
	require.Empty(t, e.messages(e.room.ID), "the busy channel posted nothing as the AI")
	s.expectNoNotification(200 * time.Millisecond)
	require.NoError(t, holder.Closed(50*time.Millisecond), "the holder keeps the AI")
}

// SES-04: the retries do not change what a busy channel says, and the text follows the session
// that holds the AI: when the retries find another one, the text names it.
func TestTheBusyTextNamesTheSessionThatHoldsTheAI(t *testing.T) {
	e, holder := busyEnv(t)
	o := e.options()
	o.zone = personZone
	o.timing.busyRetries = make([]time.Duration, 100) // retries for as long as the test needs
	for i := range o.timing.busyRetries {
		o.timing.busyRetries[i] = 50 * time.Millisecond
	}
	s := startSession(t, o)
	s.handshake()
	require.Eventually(t, func() bool {
		text, isErr := s.call("list_rooms", map[string]any{})
		return isErr && text == wantBusy
	}, wait, 10*time.Millisecond)

	// A session of pi takes the AI over from the holder: the next retry finds it instead.
	next, resp := e.alice.WithBearer(e.secret).DialFolder(testkit.Folder{Machine: "testbox", Path: "/work/builder", Harness: "pi", Takeover: true})
	require.NotNil(t, next, "upgrade refused: %d %s", resp.Status, resp.Body)
	next.Welcome(wait)
	require.Equal(t, websocket.StatusCode(4002), websocket.CloseStatus(holder.Closed(wait)))
	require.Eventually(t, func() bool {
		text, _ := s.call("list_rooms", map[string]any{})
		return strings.Contains(text, "(pi, since 09:30)")
	}, wait, 10*time.Millisecond)
}

// SES-04: a socket the server only closed with identity_busy tells nothing about the session
// that holds the AI; the text still says that it is busy and how to take over.
func TestTheBusyTextWithoutDetails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err == nil {
			_ = c.Close(4003, "identity_busy")
		}
	}))
	t.Cleanup(srv.Close)
	l, _, _ := runLink(t, srv.URL, timing{busyRetries: []time.Duration{}}, false)
	require.Eventually(t, func() bool { return l.Err() != nil }, wait, 5*time.Millisecond)
	require.EqualError(t, l.Err(), wantBusyUnknown)
}

// SES-05: a channel another session took the AI from tells its harness once, in exactly these
// words, and delivers nothing more: the messages go to the new session, every tool answers the
// same sentence.
func TestTakenOverChannelNotifiesOnceAndDeliversNothingMore(t *testing.T) {
	e := newChanEnv(t)
	s := e.connected(e.options())
	before := e.post("before the takeover")
	require.Equal(t, before.ID, s.notification().Meta["message_id"])
	require.Eventually(t, func() bool { return e.cursor() == before.Seq }, wait, 10*time.Millisecond, "its ack is recorded")

	taker := e.takeOver()
	n := s.notification()
	require.Equal(t, wantTakenOver, n.Content)
	require.Equal(t, map[string]string{
		"type": "notice", "reason": "taken_over", "author": "Partyline", "author_kind": "system",
		"sender_name": "", "trusted": "false", "kind": "system",
	}, n.Meta)

	after := e.post("after the takeover")
	require.Equal(t, after.ID, taker.Message(wait).Message.ID, "the new session receives it")
	s.expectNoNotification(500 * time.Millisecond)
	for tool, args := range allTools {
		text, isErr := s.call(tool, args)
		require.True(t, isErr, tool)
		require.Equal(t, wantTakenOver, text, tool)
	}
	s.expectNoNotification(100 * time.Millisecond)
}

// listenOnce runs Listen against the server until it returns and reports what it wrote.
type listenRun struct {
	out bytes.Buffer
	mu  sync.Mutex
	err chan error
}

func (r *listenRun) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.out.Write(p)
}

func (r *listenRun) lines() []map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(r.out.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]string
		if json.Unmarshal([]byte(line), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

func startListen(t *testing.T, e *chanEnv, harness string, tm timing) *listenRun {
	t.Helper()
	r := &listenRun{err: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		r.err <- Listen(ctx, ListenOptions{
			URL: e.env.URL, Token: e.secret, Machine: builderFolder.Machine, Folder: builderFolder.Folder, Harness: harness,
			Stdout: r, Stderr: &syncBuffer{}, timing: tm, zone: personZone,
		})
	}()
	return r
}

func (r *listenRun) ended(t *testing.T) error {
	t.Helper()
	select {
	case err := <-r.err:
		return err
	case <-time.After(wait):
		require.FailNow(t, "listen did not end")
		return nil
	}
}

// SES-05: listen of a session another one took the AI from prints the notice as one JSON line
// and ends with the error the command turns into exit code 3, so the adapters stop restarting it.
func TestListenPrintsTheNoticeAndEndsWhenTakenOver(t *testing.T) {
	e := newChanEnv(t)
	r := startListen(t, e, "pi", fastTiming)
	require.Eventually(t, e.online, wait, 10*time.Millisecond)
	m := e.post("one message first")
	require.Eventually(t, func() bool { return len(r.lines()) == 1 }, wait, 10*time.Millisecond)
	require.Equal(t, m.ID, r.lines()[0]["message_id"])

	e.takeOver()
	err := r.ended(t)
	require.ErrorIs(t, err, ErrSessionLost)
	lines := r.lines()
	require.Len(t, lines, 2, "the message and the notice")
	require.Equal(t, map[string]string{
		"type": "notice", "reason": "taken_over", "content": wantTakenOver, "author": "Partyline", "author_kind": "system",
		"sender_name": "", "trusted": "false", "kind": "system",
	}, lines[1])
}

// SES-04: listen of a session that finds the AI held by another one retries, then prints the
// busy text as one JSON line and ends the same way.
func TestListenPrintsTheBusyNoticeAndEnds(t *testing.T) {
	e, _ := busyEnv(t)
	tm := fastTiming
	tm.busyRetries = []time.Duration{20 * time.Millisecond, 20 * time.Millisecond}
	r := startListen(t, e, "pi", tm)

	err := r.ended(t)
	require.True(t, errors.Is(err, ErrSessionLost), "listen ended with %v", err)
	require.Equal(t, []map[string]string{{
		"type": "notice", "reason": "busy", "author": "Partyline", "author_kind": "system", "sender_name": "", "trusted": "false", "kind": "system",
		"content": strings.Replace(wantBusy, `"partyline claude --take-over"`, `"partyline pi --take-over"`, 1),
	}}, r.lines())
}
