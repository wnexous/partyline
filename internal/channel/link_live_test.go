package channel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// wantTakenOver is the sentence SES-05 defines for a session another one took the AI from.
const wantTakenOver = "This intelligence was opened in another session; Partyline is disconnected here."

// dialLog records the headers of the dials a fake agent endpoint receives.
type dialLog struct {
	mu      sync.Mutex
	headers []http.Header
}

// add records a dial and returns how many there have been, this one included.
func (d *dialLog) add(h http.Header) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.headers = append(d.headers, h)
	return len(d.headers)
}

func (d *dialLog) all() []http.Header {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]http.Header(nil), d.headers...)
}

func (d *dialLog) count() int { return len(d.all()) }

// runLink runs a link against base until the test ends, with a sleep that does not wait but
// records the waits.
func runLink(t *testing.T, base string, tm timing, takeover bool) (*link, *syncBuffer, func() []time.Duration) {
	t.Helper()
	return runLinkAs(t, base, builderIdentity, tm, takeover)
}

// runLinkAs is runLink for a session with the identity id.
func runLinkAs(t *testing.T, base string, id Identity, tm timing, takeover bool) (*link, *syncBuffer, func() []time.Duration) {
	t.Helper()
	stderr := &syncBuffer{}
	l := newLink(mustURL(t, base), "pl_agent_x", id, http.DefaultClient, discard{}, stderr, tm)
	l.takeover = takeover
	var mu sync.Mutex
	var waits []time.Duration
	l.sleep = func(ctx context.Context, d time.Duration) bool {
		mu.Lock()
		waits = append(waits, d)
		mu.Unlock()
		return ctx.Err() == nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = l.run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return l, stderr, func() []time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return append([]time.Duration(nil), waits...)
	}
}

// refuseWith answers every dial with status and an error body of code and message.
func refuseWith(t *testing.T, log *dialLog, status int, code dto.ErrorCode, message string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.add(r.Header.Clone())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"code":"` + string(code) + `","message":"` + message + `","details":{"display_name":"Builder","app":"claude","app_label":"Claude Code","connected_at":"2026-10-21T12:30:00Z"}}}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// SES-04, SES-06: a connection refused because the AI is live in another session is retried 3
// times, after 2 s, 4 s and 4 s (that session may be ending), and then not any more; the tools
// report the session as busy meanwhile.
func TestLinkRetriesABusyAIThreeTimesThenStops(t *testing.T) {
	var log dialLog
	base := refuseWith(t, &log, http.StatusConflict, dto.ErrorCodeIdentityBusy, "this intelligence is open in another session")
	l, _, waits := runLink(t, base, defaultTiming, false)

	require.Eventually(t, func() bool { return log.count() == 4 }, wait, 5*time.Millisecond, "the first dial and three retries")
	require.Equal(t, []time.Duration{2 * time.Second, 4 * time.Second, 4 * time.Second}, waits())
	require.Never(t, func() bool { return log.count() > 4 }, 200*time.Millisecond, 10*time.Millisecond, "no retry after the third")
	require.Error(t, l.Err())
}

// SES-06: while the AI is busy the channel keeps trying; once the session that holds it ends the
// next retry connects, with no takeover.
func TestLinkConnectsOnceTheHolderEnds(t *testing.T) {
	e := newChanEnv(t)
	holder, resp := e.alice.WithBearer(e.secret).DialFolder(testkit.Folder{Machine: "testbox", Path: "/work/builder", Harness: "opencode"})
	require.NotNil(t, holder, "upgrade refused: %d %s", resp.Status, resp.Body)
	holder.Welcome(wait)

	tm := fastTiming
	tm.busyRetries = make([]time.Duration, 100)
	for i := range tm.busyRetries {
		tm.busyRetries[i] = 30 * time.Millisecond
	}
	l := linkFor(t, e, tm)
	require.Eventually(t, func() bool { return l.Err() != nil }, wait, 5*time.Millisecond, "busy while the holder is live")
	require.NoError(t, holder.Closed(100*time.Millisecond), "the holder keeps the AI")

	holder.Close()
	require.Eventually(t, func() bool { return l.Err() == nil && e.online() }, wait, 5*time.Millisecond, "the retry connects")
}

// SES-02, SES-05: a channel another session took the AI from reports it, does not reconnect and
// does not fight for the AI back.
func TestLinkStopsAfterTakenOver(t *testing.T) {
	e := newChanEnv(t)
	l := linkFor(t, e, fastTiming)
	require.Eventually(t, func() bool { return e.online() && l.Err() == nil }, wait, 10*time.Millisecond)

	f := testkit.Folder{Machine: "testbox", Path: "/work/builder", Harness: "opencode", Takeover: true}
	taker, resp := e.alice.WithBearer(e.secret).DialFolder(f)
	require.NotNil(t, taker, "upgrade refused: %d %s", resp.Status, resp.Body)
	taker.Welcome(wait)

	require.Eventually(t, func() bool { return l.Err() != nil }, wait, 10*time.Millisecond)
	require.EqualError(t, l.Err(), wantTakenOver)
	time.Sleep(4 * fastTiming.maxBackoff)
	require.NoError(t, taker.Closed(10*time.Millisecond), "the new session is left alone")
	require.True(t, e.online())
}

// SES-02: Options.Takeover is a first connection that takes the AI over; the session that held
// it is closed and the channel receives from then on.
func TestRunWithTakeoverReplacesTheLiveSession(t *testing.T) {
	e := newChanEnv(t)
	holder, resp := e.alice.WithBearer(e.secret).DialFolder(testkit.Folder{Machine: "testbox", Path: "/work/builder", Harness: "opencode"})
	require.NotNil(t, holder, "upgrade refused: %d %s", resp.Status, resp.Body)
	holder.Welcome(wait)

	o := e.options()
	o.Takeover = true
	s := startSession(t, o)
	s.handshake()
	err := holder.Closed(wait)
	require.Equal(t, websocket.StatusCode(4002), websocket.CloseStatus(err), "the holder was taken over")

	m := e.post("for the new session")
	require.Equal(t, m.ID, s.notification().Meta["message_id"])
}

// SES-02: the takeover is asked for on the first connection that succeeds and never again, so a
// reconnect after a drop does not flip the live session; the process id stays the same across
// connections, as a reconnect of one process must be told from another session.
func TestLinkTakesOverOnlyOnTheFirstSuccessfulDial(t *testing.T) {
	var log dialLog
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch log.add(r.Header.Clone()) {
		case 1: // unreachable for a moment
			w.WriteHeader(http.StatusServiceUnavailable)
		case 2: // connects, then the connection drops
			c, err := websocket.Accept(w, r, nil)
			if err == nil {
				_ = c.Close(websocket.StatusGoingAway, "restart")
			}
		default:
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer func() { _ = c.CloseNow() }()
			_, _, _ = c.Read(r.Context())
		}
	}))
	t.Cleanup(srv.Close)
	runLink(t, srv.URL, fastTiming, true)

	require.Eventually(t, func() bool { return log.count() >= 3 }, wait, 5*time.Millisecond)
	dials := log.all()
	require.Equal(t, []string{"1", "1", ""}, []string{
		dials[0].Get(dto.HeaderTakeover), dials[1].Get(dto.HeaderTakeover), dials[2].Get(dto.HeaderTakeover),
	}, "asked on the dial that failed and on the first that succeeded, not after")
	live := dials[0].Get(dto.HeaderLive)
	require.Regexp(t, regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`), live)
	for i, h := range dials[:3] {
		require.Equal(t, live, h.Get(dto.HeaderLive), "dial %d is the same process", i+1)
	}
}

// SES-02: without the takeover option no dial asks for it.
func TestLinkDoesNotTakeOverUnlessAsked(t *testing.T) {
	var log dialLog
	base := refuseWith(t, &log, http.StatusServiceUnavailable, dto.ErrorCodeUnavailable, "later")
	runLink(t, base, fastTiming, false)
	require.Eventually(t, func() bool { return log.count() >= 3 }, wait, 5*time.Millisecond)
	for i, h := range log.all() {
		require.Empty(t, h.Values(dto.HeaderTakeover), "dial %d", i+1)
	}
}

// SES-04: the race the server detects after the upgrade closes the socket with 4003; the channel
// handles it like the refusal before the upgrade: busy, retried as many times, then stopped.
func TestLinkTreatsALostRaceAsBusy(t *testing.T) {
	var log dialLog
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.add(r.Header.Clone())
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = c.Close(4003, "identity_busy")
	}))
	t.Cleanup(srv.Close)
	tm := fastTiming
	tm.busyRetries = []time.Duration{20 * time.Millisecond, 20 * time.Millisecond}
	l, _, _ := runLink(t, srv.URL, tm, false)

	require.Eventually(t, func() bool { return log.count() == 3 && l.Err() != nil }, wait, 5*time.Millisecond)
	require.Never(t, func() bool { return log.count() > 3 }, 200*time.Millisecond, 10*time.Millisecond)
}

// AID-05: a server that cannot use the machine or folder is not retried: tools report the
// reason, once on stderr.
func TestLinkReportsAnUnusableFolderWithoutRetrying(t *testing.T) {
	var log dialLog
	base := refuseWith(t, &log, http.StatusUnprocessableEntity, dto.ErrorCodeFolderInvalid, "the folder must be an absolute path")
	l, stderr, _ := runLink(t, base, fastTiming, false)

	require.Eventually(t, func() bool { return l.Err() != nil }, wait, 5*time.Millisecond)
	require.EqualError(t, l.Err(), "partyline: the server refused this session: the folder must be an absolute path")
	time.Sleep(4 * fastTiming.maxBackoff)
	require.Equal(t, 1, log.count(), "no retry")
	require.Contains(t, stderr.String(), "the folder must be an absolute path")
}
