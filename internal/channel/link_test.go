package channel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// AGT-01, AGT-05, AGT-07: a pushed message becomes a notification line on stdout, then the
// ack advances the agent's cursor; the agent is online while connected.
func TestLinkDeliversNotificationThenAcks(t *testing.T) {
	e := newChanEnv(t)
	s := startSession(t, e.options())
	s.handshake()
	require.Eventually(t, e.online, wait, 10*time.Millisecond)

	m := e.post("deploy finished?")
	n := s.notification()
	require.Equal(t, "deploy finished?", n.Content)
	require.Equal(t, m.ID, n.Meta["message_id"])
	require.Equal(t, e.room.ID, n.Meta["room"])
	require.Equal(t, "true", n.Meta["trusted"]) // alice owns the agent
	require.Eventually(t, func() bool { return e.cursor() == m.Seq }, wait, 10*time.Millisecond)
}

// AGT-08 / goal: messages posted while the agent is offline arrive in order on connect.
func TestLinkDeliversBacklogOnConnect(t *testing.T) {
	e := newChanEnv(t)
	m1, m2, m3 := e.post("one"), e.post("two"), e.post("three")
	s := startSession(t, e.options())
	s.handshake()
	for _, want := range []dto.Message{m1, m2, m3} {
		require.Equal(t, want.ID, s.notification().Meta["message_id"])
	}
	require.Eventually(t, func() bool { return e.cursor() == m3.Seq }, wait, 10*time.Millisecond)
}

// fakeGateway is an agent WebSocket that sends welcome then the given frames and records acks.
func fakeGateway(t *testing.T, frames ...any) (url string, acks chan string) {
	t.Helper()
	acks = make(chan string, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.CloseNow() }()
		ctx := r.Context()
		for _, f := range append([]any{dto.AgentWelcomeFrame{Type: dto.AgentFrameTypeWelcome, Agent: dto.AgentInfo{ID: "a1", Name: "builder", Owner: "alice"}}}, frames...) {
			data, _ := json.Marshal(f)
			if ws.Write(ctx, websocket.MessageText, data) != nil {
				return
			}
		}
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var a dto.AgentAckFrame
			if json.Unmarshal(data, &a) == nil && a.Type == dto.AgentFrameTypeAck {
				acks <- a.MessageID
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, acks
}

// AGT-10: a message id already emitted is acknowledged again without a second notification.
func TestLinkDedupesRepeatedMessage(t *testing.T) {
	frame := dto.AgentMessageFrame{Type: dto.AgentFrameTypeMessage, AgentMessage: dto.AgentMessage{
		Room:    dto.RoomRef{ID: "r1", Name: "Support"},
		Message: dto.Message{ID: "m1", RoomID: "r1", Seq: 1, AuthorKind: dto.AuthorKindUser, AuthorName: "Alice", Kind: dto.MessageKindText, Text: "hi"},
	}}
	url, acks := fakeGateway(t, frame, frame)
	o := Options{URL: url, Token: "pl_agent_test", Agent: "builder", TempDir: t.TempDir(), timing: fastTiming}
	s := startSession(t, o)
	s.handshake()

	require.Equal(t, "m1", s.notification().Meta["message_id"])
	for range 2 {
		select {
		case id := <-acks:
			require.Equal(t, "m1", id)
		case <-time.After(wait):
			require.FailNow(t, "missing ack")
		}
	}
	s.expectNoNotification(300 * time.Millisecond)
}

// AGT-07: no ack is sent before the notification is written: before initialization the
// notification is held and the message stays unacknowledged.
func TestLinkAcksOnlyAfterNotificationWritten(t *testing.T) {
	frame := dto.AgentMessageFrame{Type: dto.AgentFrameTypeMessage, AgentMessage: dto.AgentMessage{
		Room:    dto.RoomRef{ID: "r1", Name: "Support"},
		Message: dto.Message{ID: "m1", RoomID: "r1", Seq: 1, AuthorKind: dto.AuthorKindUser, Kind: dto.MessageKindText, Text: "hi"},
	}}
	url, acks := fakeGateway(t, frame)
	s := startSession(t, Options{URL: url, Token: "pl_agent_test", Agent: "builder", TempDir: t.TempDir(), timing: fastTiming})
	select {
	case id := <-acks:
		require.FailNow(t, "ack before the notification was written", id)
	case <-time.After(300 * time.Millisecond):
	}
	s.handshake()
	require.Equal(t, "m1", s.notification().Meta["message_id"])
	select {
	case id := <-acks:
		require.Equal(t, "m1", id)
	case <-time.After(wait):
		require.FailNow(t, "missing ack")
	}
}

// AGT-03: after the connection drops (server restart) the channel reconnects; messages
// posted meanwhile are delivered and the server shows the agent offline, then online.
func TestLinkReconnectsAfterServerRestart(t *testing.T) {
	e := newChanEnv(t)
	p := newProxy(t, strings.TrimPrefix(e.env.URL, "http://"))
	o := e.options()
	o.URL = p.url()
	s := startSession(t, o)
	s.handshake()
	require.Eventually(t, e.online, wait, 10*time.Millisecond)

	p.stop()
	require.Eventually(t, func() bool { return !e.online() }, wait, 10*time.Millisecond)
	m := e.post("while you were away")
	time.Sleep(3 * fastTiming.minBackoff) // a few refused reconnects
	p.start()
	require.Equal(t, m.ID, s.notification().Meta["message_id"])
	require.True(t, e.online())
	require.Eventually(t, func() bool { return e.cursor() == m.Seq }, wait, 10*time.Millisecond)
}

// AGT-03: reconnect delays start at 1 s and double up to 30 s.
func TestBackoffDoublesToThirtySeconds(t *testing.T) {
	d := defaultTiming.minBackoff
	require.Equal(t, time.Second, d)
	var got []time.Duration
	for range 7 {
		got = append(got, d)
		d = nextBackoff(d, defaultTiming.maxBackoff)
	}
	require.Equal(t, []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}, got)
}

// AGT-02: an invalid token writes one stderr line (not repeated on retries), MCP keeps
// answering and the link reports "partyline: not connected (invalid token)".
func TestLinkInvalidToken(t *testing.T) {
	e := newChanEnv(t)
	o := e.options()
	o.Token = "pl_agent_invalid"
	s := startSession(t, o)
	init := s.handshake()
	require.Equal(t, "2025-11-25", init["result"].(map[string]any)["protocolVersion"])

	require.Eventually(t, func() bool { return strings.Contains(s.stderr.String(), "rejected PARTYLINE_TOKEN") }, wait, 10*time.Millisecond)
	time.Sleep(4 * fastTiming.invalidRetry) // several retries
	require.Equal(t, 1, strings.Count(s.stderr.String(), "\n"), "stderr: %q", s.stderr.String())
	require.NotContains(t, s.stderr.String(), "pl_agent_invalid")
	require.False(t, e.online())

	s.send(map[string]any{"jsonrpc": "2.0", "id": 9, "method": "ping"})
	require.Equal(t, float64(9), s.response()["id"])
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}

func linkFor(t *testing.T, e *chanEnv, tm timing) *link {
	t.Helper()
	l := newLink(mustURL(t, e.env.URL), e.secret, "builder", http.DefaultClient, discard{}, &syncBuffer{}, tm)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = l.run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return l
}

type discard struct{}

func (discard) Notify(string, any) error { return nil }

// AGT-02: link error text for an invalid token.
func TestLinkErrInvalidToken(t *testing.T) {
	e := newChanEnv(t)
	e.secret = "pl_agent_invalid"
	l := linkFor(t, e, fastTiming)
	require.Eventually(t, func() bool { return l.Err() != nil }, wait, 10*time.Millisecond)
	require.EqualError(t, l.Err(), "partyline: not connected (invalid token)")
}

// Contract (close 4000 replaced): the replaced channel stops reconnecting for the pause,
// reports the replacement through Err, then connects again.
func TestLinkPausesAfterReplaced(t *testing.T) {
	e := newChanEnv(t)
	tm := fastTiming
	tm.replacedPause = 3 * time.Second
	l := linkFor(t, e, tm)
	require.Eventually(t, func() bool { return e.online() && l.Err() == nil }, wait, 10*time.Millisecond)

	other, resp := e.alice.WithBearer(e.secret).DialAgent("builder")
	require.NotNil(t, other, "upgrade refused: %d", resp.Status)
	other.Welcome(wait)
	require.Eventually(t, func() bool { return l.Err() != nil }, wait, 10*time.Millisecond)
	require.EqualError(t, l.Err(), "partyline: replaced by another session using agent name builder")

	// No fight during the pause: the newer connection stays open.
	require.NoError(t, other.Closed(time.Second))

	// After the pause the channel connects again and replaces it.
	err := other.Closed(wait)
	require.Error(t, err)
	require.Equal(t, closeReplaced, websocket.CloseStatus(err))
	require.Eventually(t, func() bool { return l.Err() == nil }, wait, 10*time.Millisecond)
}

// Contract (close 4001 deleted): after the agent is deleted the channel stops reconnecting.
func TestLinkStopsAfterDeleted(t *testing.T) {
	e := newChanEnv(t)
	l := linkFor(t, e, fastTiming)
	require.Eventually(t, e.online, wait, 10*time.Millisecond)

	e.alice.Delete("/api/v1/agents/" + e.agentID).RequireStatus(http.StatusNoContent)
	require.Eventually(t, func() bool { return l.Err() != nil }, wait, 10*time.Millisecond)
	require.EqualError(t, l.Err(), "partyline: agent builder was deleted; restart Claude Code to register it again")

	time.Sleep(4 * fastTiming.maxBackoff)
	var list dto.List[dto.Agent]
	e.alice.Get("/api/v1/agents").RequireStatus(http.StatusOK).Decode(&list)
	require.Empty(t, list.Items, "the channel registered the agent again")
}

// AGT-03 edge case: when stdin closes the channel closes the WebSocket and returns nil
// within 2 seconds; the server marks the agent offline.
func TestRunReturnsOnStdinEOF(t *testing.T) {
	e := newChanEnv(t)
	s := startSession(t, e.options())
	s.handshake()
	require.Eventually(t, e.online, wait, 10*time.Millisecond)

	took, err := s.closeStdin()
	require.NoError(t, err)
	require.Less(t, took, 2*time.Second)
	require.Eventually(t, func() bool { return !e.online() }, wait, 10*time.Millisecond)
}

// Design (error handling): when the notification output is gone, Run returns the error.
func TestRunFailsWhenOutputCloses(t *testing.T) {
	e := newChanEnv(t)
	o := e.options()
	inR, inW := io.Pipe()
	o.Stdin, o.Stdout, o.Stderr = inR, &failingWriter{after: 1}, &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), o) }()
	t.Cleanup(func() { _ = inW.Close() })

	// The initialize answer is written; the first notification write fails.
	_, err := inW.Write([]byte(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}` + "\n" + `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"))
	require.NoError(t, err)
	require.Eventually(t, e.online, wait, 10*time.Millisecond)
	e.post("lost output")
	select {
	case err := <-done:
		require.ErrorIs(t, err, errBrokenPipe)
	case <-time.After(wait):
		require.FailNow(t, "Run kept going without an output")
	}
}

var errBrokenPipe = errors.New("broken pipe")

// failingWriter accepts `after` writes, then fails.
type failingWriter struct {
	after int64
	n     atomic.Int64
}

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.n.Add(1) > w.after {
		return 0, errBrokenPipe
	}
	return len(p), nil
}
