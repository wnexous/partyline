package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
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

// AGT-10 (design: dedupe window of 1,000 ids): the link remembers the last 1,000 delivered
// message ids; the 1,001st evicts only the oldest, which would then be notified again.
func TestLinkDedupeWindowHoldsThousandIDs(t *testing.T) {
	l := newLink(mustURL(t, "http://127.0.0.1"), "pl_agent_test", "builder", http.DefaultClient, discard{}, io.Discard, fastTiming)
	id := func(i int) string { return fmt.Sprintf("m%d", i) }
	for i := range 1000 {
		l.remember(id(i))
	}
	for i := range 1000 {
		require.True(t, l.seenBefore(id(i)), "id %d of 1000 is remembered", i)
	}
	l.remember(id(1000))
	require.False(t, l.seenBefore(id(0)), "the 1,001st id evicts the oldest")
	for i := 1; i <= 1000; i++ {
		require.True(t, l.seenBefore(id(i)), "id %d stays remembered", i)
	}
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

// S5, AGT-07: before notifications/initialized the link keeps reading the socket: a server
// ping is answered and a close is seen while pushed messages wait. After initialization the
// held messages arrive in order, each acknowledged after its notification.
func TestLinkReadsSocketBeforeInitialized(t *testing.T) {
	frame := func(id string, seq int64) dto.AgentMessageFrame {
		return dto.AgentMessageFrame{Type: dto.AgentFrameTypeMessage, AgentMessage: dto.AgentMessage{
			Room:    dto.RoomRef{ID: "r1", Name: "Support"},
			Message: dto.Message{ID: id, RoomID: "r1", Seq: seq, AuthorKind: dto.AuthorKindUser, Kind: dto.MessageKindText, Text: "hi " + id},
		}}
	}
	// gateway sends welcome and two messages, then closes with closeWith when it is set, or
	// else pings (pinged gets the result) and stays open.
	gateway := func(t *testing.T, closeWith websocket.StatusCode) (url string, pinged chan error, acks chan string) {
		pinged, acks = make(chan error, 1), make(chan string, 16)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ws, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer func() { _ = ws.CloseNow() }()
			ctx := r.Context()
			go func() { // the server reads acks (and the pong) concurrently
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
			}()
			welcome := dto.AgentWelcomeFrame{Type: dto.AgentFrameTypeWelcome, Agent: dto.AgentInfo{ID: "a1", Name: "builder", Owner: "alice"}}
			for _, f := range []any{welcome, frame("m1", 1), frame("m2", 2)} {
				data, _ := json.Marshal(f)
				if ws.Write(ctx, websocket.MessageText, data) != nil {
					return
				}
			}
			if closeWith != 0 {
				_ = ws.Close(closeWith, "deleted")
				return
			}
			pctx, cancel := context.WithTimeout(ctx, wait)
			pinged <- ws.Ping(pctx)
			cancel()
			<-ctx.Done()
		}))
		t.Cleanup(srv.Close)
		return srv.URL, pinged, acks
	}
	options := func(url string) Options {
		return Options{URL: url, Token: "pl_agent_test", Agent: "builder", TempDir: t.TempDir(), timing: fastTiming}
	}

	t.Run("ping answered, messages held until initialized", func(t *testing.T) {
		url, pinged, acks := gateway(t, 0)
		s := startSession(t, options(url))
		select {
		case err := <-pinged:
			require.NoError(t, err, "the ping before initialization gets its pong")
		case <-time.After(wait):
			require.FailNow(t, "the ping before initialization was not answered")
		}
		require.Empty(t, acks, "nothing is acknowledged before initialization")
		s.handshake()
		for _, id := range []string{"m1", "m2"} {
			require.Equal(t, id, s.notification().Meta["message_id"])
			select {
			case got := <-acks:
				require.Equal(t, id, got)
			case <-time.After(wait):
				require.FailNow(t, "missing ack", id)
			}
		}
	})

	t.Run("close seen before initialized", func(t *testing.T) {
		url, _, _ := gateway(t, closeDeleted)
		s := startSession(t, options(url))
		require.Eventually(t, func() bool { return strings.Contains(s.stderr.String(), "agent builder was deleted") }, wait, 10*time.Millisecond)
	})
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

func (discard) Initialized() <-chan struct{} { return closedChan }

// closedChan is an Initialized channel of a notifier that is ready at once.
var closedChan = func() chan struct{} { c := make(chan struct{}); close(c); return c }()

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

// discardNotifier drops notifications (the link never connects in the backoff test).
type discardNotifier struct{}

func (discardNotifier) Notify(string, any) error { return nil }

func (discardNotifier) Initialized() <-chan struct{} { return closedChan }

// AGT-03: while the server is unreachable the link retries after 1 s, doubling up to 30 s.
// The waits are recorded instead of slept.
func TestLinkBacksOffWhileUnreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	base, err := url.Parse("http://" + ln.Addr().String())
	require.NoError(t, err)
	require.NoError(t, ln.Close()) // nothing listens there now: every dial fails at once

	l := newLink(base, "pl_agent_x", "builder", &http.Client{}, discardNotifier{}, io.Discard, defaultTiming)
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	var waits []time.Duration
	l.sleep = func(ctx context.Context, d time.Duration) bool {
		waits = append(waits, d)
		if len(waits) == 8 {
			cancel()
		}
		return ctx.Err() == nil
	}
	require.NoError(t, l.run(ctx))
	require.Equal(t, []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 30 * time.Second, 30 * time.Second, 30 * time.Second}, waits)
	require.ErrorContains(t, l.Err(), "server unreachable")
}

// Agent cap (S2/W2): when the server refuses the agent because its owner is at the limit,
// tools report it plainly (not "server unreachable") and stderr says so once.
func TestLinkReportsAgentLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"conflict","message":"agent limit reached"}}`))
	}))
	t.Cleanup(srv.Close)
	stderr := &syncBuffer{}
	tm := fastTiming
	tm.replacedPause = 50 * time.Millisecond
	l := newLink(mustURL(t, srv.URL), "pl_agent_x", "builder", http.DefaultClient, discard{}, stderr, tm)
	var waitsMu sync.Mutex
	var waits []time.Duration
	l.sleep = func(ctx context.Context, d time.Duration) bool {
		waitsMu.Lock()
		waits = append(waits, d)
		waitsMu.Unlock()
		return sleepCtx(ctx, d)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = l.run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	require.Eventually(t, func() bool { return l.Err() != nil }, wait, 10*time.Millisecond)
	require.EqualError(t, l.Err(), "partyline: agent limit reached for this account; delete unused agents in the web app")
	time.Sleep(4 * tm.replacedPause) // several retries
	require.Equal(t, 1, strings.Count(stderr.String(), "agent limit reached"), "stderr: %q", stderr.String())
	waitsMu.Lock()
	defer waitsMu.Unlock()
	require.NotEmpty(t, waits)
	for _, d := range waits {
		require.Equal(t, tm.replacedPause, d, "retries after a refusal wait the long pause, not the backoff")
	}
}

// W1: the delivery queue is bounded; a full queue refuses new frames so the link drops the
// connection instead of growing without limit.
func TestFrameQueueIsBounded(t *testing.T) {
	q := newFrameQueue(maxQueuedFrames)
	for i := range maxQueuedFrames {
		require.True(t, q.push([]byte{byte(i)}), "frame %d", i)
	}
	require.False(t, q.push([]byte("one too many")))
	data, ok := q.pop(context.Background())
	require.True(t, ok)
	require.Equal(t, []byte{0}, data, "oldest first")
	require.True(t, q.push([]byte("room again")))
}

// fakeAgentServer accepts the agent WebSocket, runs send on it and reports how the client
// closed the connection.
func fakeAgentServer(t *testing.T, send func(ctx context.Context, c *websocket.Conn)) (*url.URL, <-chan websocket.StatusCode) {
	t.Helper()
	closed := make(chan websocket.StatusCode, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		send(r.Context(), c)
		for {
			if _, _, err := c.Read(r.Context()); err != nil {
				select {
				case closed <- websocket.CloseStatus(err):
				default:
				}
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return mustURL(t, srv.URL), closed
}

// neverReady is a notifier Claude Code never initializes, so frames stay queued.
type neverReady struct{}

func (neverReady) Notify(string, any) error { return nil }

func (neverReady) Initialized() <-chan struct{} { return nil }

// W1: when the delivery queue is full the link drops the connection (policy violation)
// instead of discarding frames while staying connected, so nothing is acked past the cap.
func TestLinkDropsConnectionWhenQueueIsFull(t *testing.T) {
	base, closed := fakeAgentServer(t, func(ctx context.Context, c *websocket.Conn) {
		for i := range 5 {
			_ = c.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"type":"notice","text":"n%d"}`, i)))
		}
	})
	l := newLink(base, "pl_agent_x", "builder", http.DefaultClient, neverReady{}, &syncBuffer{}, fastTiming)
	l.queueCap = 3
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = l.run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	select {
	case code := <-closed:
		require.Equal(t, websocket.StatusPolicyViolation, code)
	case <-time.After(wait):
		t.Fatal("the link kept the connection open with a full queue")
	}
}

// W1: a frame above the read limit closes the connection (1009) instead of being buffered.
func TestLinkRefusesOversizedFrame(t *testing.T) {
	base, closed := fakeAgentServer(t, func(ctx context.Context, c *websocket.Conn) {
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"notice","text":"`+strings.Repeat("x", 4096)+`"}`))
	})
	l := newLink(base, "pl_agent_x", "builder", http.DefaultClient, discard{}, &syncBuffer{}, fastTiming)
	l.readLimit = 1024
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = l.run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	select {
	case code := <-closed:
		require.Equal(t, websocket.StatusMessageTooBig, code)
	case <-time.After(wait):
		t.Fatal("an oversized frame did not close the connection")
	}
}

// The production limits stay at their documented values.
func TestLinkDefaultLimits(t *testing.T) {
	l := newLink(mustURL(t, "http://127.0.0.1:1"), "t", "a", http.DefaultClient, discard{}, &syncBuffer{}, fastTiming)
	require.Equal(t, 2000, l.queueCap)
	require.Equal(t, int64(1<<20), l.readLimit)
}
