package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
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
	// wantBusyChannel is wantBusy plus what a channel adds to it for its harness: it never gives up
	// on the AI, so it will come back on its own (SES-04).
	wantBusyChannel = wantBusy + " Partyline keeps trying and connects by itself when that session ends."
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
	"create_group":        toolArgs("name", "Ops", "members", []string{"@alice"}),
	"update_profile":      toolArgs("bio", "x"),
}

// heldAI is an older server in front of the app (the channel's compatibility branch, SES-04):
// while it holds the AI it answers the agent WebSocket with 409 identity_busy and the holder's
// details, and every other request reaches the app. The server this feature ships never refuses a
// session (SESS-10), so the refusal has to come from a server of the previous release.
// release lets the app answer the dial, as a new server does; holds changes the holder the
// refusal describes.
type heldAI struct {
	url     string
	mu      sync.Mutex
	held    bool
	details map[string]string
}

// holdAI fronts the app with an older server that holds alice's AI of /work/builder in OpenCode
// since holderStart.
func holdAI(t *testing.T, e *chanEnv) *heldAI {
	t.Helper()
	h := &heldAI{held: true, details: map[string]string{
		dto.BusyDetailDisplayName: "Builder", dto.BusyDetailApp: "opencode",
		dto.BusyDetailAppLabel: "OpenCode", dto.BusyDetailConnectedAt: holderStart.Format(time.RFC3339),
	}}
	proxy := httputil.NewSingleHostReverseProxy(mustURL(t, e.env.URL))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		held, details := h.held, maps.Clone(h.details)
		h.mu.Unlock()
		if held && r.URL.Path == "/api/v1/agent/ws" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(dto.ErrorBody{Error: dto.Error{Code: dto.ErrorCodeIdentityBusy, Message: "this intelligence is open in another session", Details: details}})
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	h.url = srv.URL
	return h
}

// holds makes the next refusals describe another holder.
func (h *heldAI) holds(details map[string]string) {
	h.mu.Lock()
	h.held, h.details = true, details
	h.mu.Unlock()
}

// release stops the refusal: the app answers, as the server of this feature does.
func (h *heldAI) release() {
	h.mu.Lock()
	h.held = false
	h.mu.Unlock()
}

// busyEnv is a server whose clock stands at holderStart in front of an older one that holds
// alice's AI of /work/builder.
func busyEnv(t *testing.T) (*chanEnv, *heldAI) {
	t.Helper()
	e := newChanEnv(t, testkit.WithClock(testkit.NewClock(holderStart).Now))
	return e, holdAI(t, e)
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
// and the channel tells its harness once, not once per poll, that it keeps trying.
func TestEveryToolAnswersTheBusyText(t *testing.T) {
	e, held := busyEnv(t)
	o := e.options()
	o.URL = held.url
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
	n := s.notification()
	require.Equal(t, "busy", n.Meta["reason"])
	require.Equal(t, "notice", n.Meta["type"])
	require.Equal(t, wantBusyChannel, n.Content)
	s.expectNoNotification(200 * time.Millisecond)
	require.False(t, e.online(), "the channel is not connected as the AI")
}

// SES-04: the retries do not change what a busy channel says, and the text follows the session
// that holds the AI: when the retries find another one, the text names it.
func TestTheBusyTextNamesTheSessionThatHoldsTheAI(t *testing.T) {
	e, held := busyEnv(t)
	o := e.options()
	o.URL = held.url
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
	held.holds(map[string]string{
		dto.BusyDetailDisplayName: "Builder", dto.BusyDetailApp: "pi",
		dto.BusyDetailAppLabel: "pi", dto.BusyDetailConnectedAt: holderStart.Format(time.RFC3339),
	})
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
	return startListenAt(t, e.env.URL, e, harness, tm)
}

// startListenAt is startListen against another base, such as an older server's refusal.
func startListenAt(t *testing.T, base string, e *chanEnv, harness string, tm timing) *listenRun {
	t.Helper()
	r := &listenRun{err: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		r.err <- Listen(ctx, ListenOptions{
			URL: base, Token: e.secret, Machine: builderFolder.Machine, Folder: builderFolder.Folder, Harness: harness,
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
	e, held := busyEnv(t)
	tm := fastTiming
	tm.busyRetries = []time.Duration{20 * time.Millisecond, 20 * time.Millisecond}
	r := startListenAt(t, held.url, e, "pi", tm)

	err := r.ended(t)
	require.True(t, errors.Is(err, ErrSessionLost), "listen ended with %v", err)
	require.Equal(t, []map[string]string{{
		"type": "notice", "reason": "busy", "author": "Partyline", "author_kind": "system", "sender_name": "", "trusted": "false", "kind": "system",
		"content": strings.Replace(wantBusy, `"partyline claude --take-over"`, `"partyline pi --take-over"`, 1),
	}}, r.lines())
}
