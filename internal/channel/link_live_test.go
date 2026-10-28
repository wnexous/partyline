package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
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
	return runLinkWith(t, base, id, tm, takeover, nil)
}

// runLinkWith is runLinkAs with a chance to set the link's fields before it starts connecting.
func runLinkWith(t *testing.T, base string, id Identity, tm timing, takeover bool, setup func(*link)) (*link, *syncBuffer, func() []time.Duration) {
	t.Helper()
	stderr := &syncBuffer{}
	l := newLink(mustURL(t, base), "pl_agent_x", id, http.DefaultClient, discard{}, stderr, tm)
	l.takeover = takeover
	if setup != nil {
		setup(l)
	}
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

// SES-04, SES-06: the retries of a connection refused because the AI is live in another session,
// after 2 s, 4 s, 4 s, 10 s, 10 s and 15 s, add up to 45 s: more than the 35 s it takes the server's
// ping to drop a session that went silent. listen stops after the last retry; a channel keeps polling
// every 30 s and never gives up.
func TestLinkBusyRetriesOutlastThePing(t *testing.T) {
	var stopped dialLog
	base := refuseWith(t, &stopped, http.StatusConflict, dto.ErrorCodeIdentityBusy, "this intelligence is open in another session")
	l, _, waits := runLink(t, base, defaultTiming, false)

	retries := []time.Duration{2 * time.Second, 4 * time.Second, 4 * time.Second, 10 * time.Second, 10 * time.Second, 15 * time.Second}
	require.Eventually(t, func() bool { return len(waits()) >= len(retries) }, wait, 5*time.Millisecond)
	require.Equal(t, retries, waits(), "six retries, no poll: listen gives up after them")
	require.GreaterOrEqual(t, sumWaits(retries), 35*time.Second, "the retries outlast the 35 s of SES-06")
	require.Eventually(t, func() bool { return stopped.count() == 1+len(retries) }, wait, 5*time.Millisecond, "the first dial and six retries")
	require.Never(t, func() bool { return stopped.count() > 1+len(retries) }, 200*time.Millisecond, 10*time.Millisecond, "no retry after the sixth")
	require.Error(t, l.Err())

	// The channel keeps trying: the same six retries, then a poll every 30 s.
	var polling dialLog
	pollBase := refuseWith(t, &polling, http.StatusConflict, dto.ErrorCodeIdentityBusy, "this intelligence is open in another session")
	kept, _, keptWaits := runLinkWith(t, pollBase, builderIdentity, defaultTiming, false, func(l *link) { l.keepTryingWhenBusy = true })
	require.Eventually(t, func() bool { return len(keptWaits()) >= len(retries)+3 }, wait, 5*time.Millisecond)
	require.Equal(t, append(retries, 30*time.Second, 30*time.Second, 30*time.Second), keptWaits()[:len(retries)+3])
	require.Error(t, kept.Err())
}

// sumWaits adds up waits.
func sumWaits(waits []time.Duration) time.Duration {
	var sum time.Duration
	for _, d := range waits {
		sum += d
	}
	return sum
}

// SES-04: a channel that finds the AI open in another session tells its harness once, with the busy
// text and the promise that Partyline connects by itself, keeps polling while the holder is live, and
// when the holder ends it connects, tells the harness that it is back, and delivers again.
func TestChannelKeepsTryingAfterBusyAndSaysSo(t *testing.T) {
	e, held := busyEnv(t)

	o := e.options()
	o.URL = held.url
	o.timing.busyRetries = []time.Duration{20 * time.Millisecond, 20 * time.Millisecond}
	o.timing.busyPoll = 50 * time.Millisecond
	s := startSession(t, o)
	s.handshake()

	n := s.notification()
	require.Equal(t, "notice", n.Meta["type"])
	require.Equal(t, "busy", n.Meta["reason"])
	require.Contains(t, n.Content, "is open in another session")
	require.Contains(t, n.Content, "connects by itself", "the harness is told that Partyline comes back on its own")
	s.expectNoNotification(200 * time.Millisecond) // one notice, not one per poll

	held.release()
	back := s.notification()
	require.Equal(t, reconnectText, back.Content)
	require.Equal(t, "notice", back.Meta["type"])
	require.Equal(t, "connected", back.Meta["reason"])
	require.True(t, e.online(), "the channel is the AI's live session")

	m := e.post("after the reconnect")
	require.Equal(t, m.ID, s.notification().Meta["message_id"], "messages reach the session again")
	s.expectNoNotification(200 * time.Millisecond)
}

// SES-06: while the AI is busy the channel keeps trying; once the session that holds it ends the
// next retry connects, with no takeover.
func TestLinkConnectsOnceTheHolderEnds(t *testing.T) {
	e := newChanEnv(t)
	held := holdAI(t, e)

	tm := fastTiming
	tm.busyRetries = make([]time.Duration, 100)
	for i := range tm.busyRetries {
		tm.busyRetries[i] = 30 * time.Millisecond
	}
	l := linkAt(t, held.url, e, tm)
	require.Eventually(t, func() bool { return l.Err() != nil }, wait, 5*time.Millisecond, "busy while the older server holds the AI")

	held.release()
	require.Eventually(t, func() bool { return l.Err() == nil && e.online() }, wait, 5*time.Millisecond, "the retry connects")
}

// linkAt is linkFor against another base, such as a server of the previous release that refuses a
// session while another one holds the AI.
func linkAt(t *testing.T, base string, e *chanEnv, tm timing) *link {
	t.Helper()
	l := newLink(mustURL(t, base), e.secret, builderIdentity, http.DefaultClient, discard{}, &syncBuffer{}, tm)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = l.run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return l
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

// noticeLog records the notifications a link sends, as a harness would receive them.
type noticeLog struct {
	mu    sync.Mutex
	notes []Notification
}

func (n *noticeLog) Notify(_ string, params any) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if note, ok := params.(Notification); ok {
		n.notes = append(n.notes, note)
	}
	return nil
}

func (n *noticeLog) Initialized() <-chan struct{} { return closedChan }

func (n *noticeLog) all() []Notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Notification(nil), n.notes...)
}

// welcomeSessionServer answers the agent WebSocket with a welcome that carries session and drops the
// first connection, so the link reconnects with the same process; it reports how many dials it saw.
func welcomeSessionServer(t *testing.T, session dto.SessionInfo) (string, func() int) {
	t.Helper()
	var mu sync.Mutex
	dials := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agent/ws" {
			http.NotFound(w, r)
			return
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		dials++
		dial := dials
		mu.Unlock()
		hello, _ := json.Marshal(dto.AgentWelcomeFrame{
			Type:         dto.AgentFrameTypeWelcome,
			Intelligence: dto.IntelligenceRef{Kind: dto.IntelligenceKindAI, DisplayName: "Builder", Handle: "builder"},
			Session:      session,
		})
		_ = ws.Write(r.Context(), websocket.MessageText, hello)
		if dial == 1 {
			_ = ws.Close(websocket.StatusInternalError, "drop") // the channel reconnects
			return
		}
		defer func() { _ = ws.CloseNow() }()
		for {
			if _, _, err := ws.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() int {
		mu.Lock()
		defer mu.Unlock()
		return dials
	}
}

// SESS-15: a welcome with a label makes the channel say which parallel session it became, once in the
// identity notice and once on stderr, and a reconnect of the same process says neither again. The
// principal prints neither.
func TestChannelAnnouncesItsParallelSessionOnce(t *testing.T) {
	const line = "partyline: opened as parallel session claude-2\n"
	cases := map[string]struct {
		label, notice string
		announced     bool
	}{
		"principal": {label: "", notice: "You are Builder (@builder)."},
		"parallel":  {label: "claude-2", notice: "You are Builder (@builder). This session is :claude-2.", announced: true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			base, dials := welcomeSessionServer(t, dto.SessionInfo{Label: c.label, Harness: "claude"})
			notes, stderr := &noticeLog{}, &syncBuffer{}
			l := newLink(mustURL(t, base), "pl_agent_x", builderIdentity, http.DefaultClient, notes, stderr, fastTiming)
			l.announce = true
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { _ = l.run(ctx); close(done) }()
			t.Cleanup(func() { cancel(); <-done })

			require.Eventually(t, func() bool { return dials() >= 2 }, wait, 5*time.Millisecond, "the channel reconnects")
			require.Eventually(t, func() bool { return len(notes.all()) >= 1 }, wait, 5*time.Millisecond, "the identity notice")
			require.Never(t, func() bool { return len(notes.all()) > 1 }, 200*time.Millisecond, 10*time.Millisecond,
				"a reconnect of the same process says nothing again")
			require.Len(t, notes.all(), 1)
			require.Equal(t, c.notice, notes.all()[0].Content)

			if !c.announced {
				require.Empty(t, stderr.String(), "the principal prints no session line")
				return
			}
			require.Eventually(t, func() bool { return strings.Count(stderr.String(), line) == 1 }, wait, 5*time.Millisecond)
			require.Never(t, func() bool { return strings.Count(stderr.String(), line) > 1 }, 200*time.Millisecond, 10*time.Millisecond,
				"the line is printed once across two connections")
		})
	}
}

// SESS-51, SESS-57: the harness session id is sent on every dial, and --principal on the first connection that
// succeeds only, like the takeover: a reconnect never takes the role again.
func TestLinkSendsTheHarnessSessionAndAsksForThePrincipalOnce(t *testing.T) {
	var log dialLog
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch log.add(r.Header.Clone()) {
		case 1: // connects, then the connection drops
			c, err := websocket.Accept(w, r, nil)
			if err == nil {
				_ = c.Close(websocket.StatusCode(4004), "role_changed")
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
	id := builderIdentity
	id.HarnessSession = "15cf4c1e-3a52-4a8e-9f6b-0123456789ab"
	runLinkWith(t, srv.URL, id, fastTiming, false, func(l *link) { l.principal = true })

	require.Eventually(t, func() bool { return log.count() >= 2 }, wait, 5*time.Millisecond)
	dials := log.all()
	require.Equal(t, []string{"1", ""}, []string{dials[0].Get(dto.HeaderPrincipal), dials[1].Get(dto.HeaderPrincipal)})
	for i, h := range dials[:2] {
		require.Equal(t, id.HarnessSession, h.Get(dto.HeaderHarnessSession), "dial %d", i+1)
	}
}

// SESS-51: a session whose harness has no session id sends no header.
func TestLinkSendsNoHarnessSessionWhenThereIsNone(t *testing.T) {
	var log dialLog
	base := refuseWith(t, &log, http.StatusServiceUnavailable, dto.ErrorCodeUnavailable, "later")
	runLink(t, base, fastTiming, false)
	require.Eventually(t, func() bool { return log.count() >= 1 }, wait, 5*time.Millisecond)
	require.Empty(t, log.all()[0].Values(dto.HeaderHarnessSession))
	require.Empty(t, log.all()[0].Values(dto.HeaderPrincipal))
}
