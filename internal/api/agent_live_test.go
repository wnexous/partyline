package api_test

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/testkit"
)

// liveEnv is a server with a fake clock, alice, her agent token and a room "Support".
type liveEnv struct {
	env   *testkit.Env
	clock *testkit.Clock
	alice *testkit.Client
	tok   *testkit.Client
	room  dto.Room
	start time.Time
}

func newLiveEnv(t *testing.T) *liveEnv {
	t.Helper()
	start := time.Date(2026, 10, 21, 12, 30, 0, 0, time.UTC)
	clock := testkit.NewClock(start)
	env := testkit.New(t, testkit.WithClock(clock.Now))
	alice := env.NewUser("alice")
	return &liveEnv{env: env, clock: clock, alice: alice, tok: alice.Token(dto.TokenKindAgent, "laptop"), room: createRoom(alice, "Support"), start: start}
}

// dial connects a process (Live is the process, empty for a new one) and returns its AI's id.
func (l *liveEnv) dial(t *testing.T, f testkit.Folder) (*testkit.AgentConn, string) {
	t.Helper()
	conn, resp := l.tok.DialFolder(f)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	return conn, conn.Welcome(wsWait).Agent.ID
}

func (l *liveEnv) addMember(t *testing.T, agentID string) {
	t.Helper()
	l.alice.Post("/api/v1/rooms/"+l.room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: agentID}).RequireStatus(http.StatusCreated)
}

func (l *liveEnv) post(t *testing.T, text string) dto.Message {
	t.Helper()
	return testkit.Decode[dto.Message](l.alice.Post("/api/v1/rooms/"+l.room.ID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
}

func (l *liveEnv) session(t *testing.T) dto.SessionState {
	t.Helper()
	return testkit.Decode[dto.SessionState](l.tok.AsFolder(acme).Get("/api/v1/agent/session").RequireStatus(http.StatusOK))
}

// offlineEvents counts the offline events of the AI on a bus subscription.
func offlineEvents(ch <-chan events.Event, agentID string) int {
	n := 0
	for {
		select {
		case e := <-ch:
			if p, ok := e.(events.AgentPresence); ok && p.AgentID == agentID && !p.Online {
				n++
			}
		default:
			return n
		}
	}
}

// SES-04: while an AI has a live session, a connection from another process is refused before
// the upgrade with 409 identity_busy naming the AI, its app and since when the session is live;
// the live session stays live and the refused connection changes nothing, not even the app.
func TestASecondSessionIsRefusedWhileTheFirstIsLive(t *testing.T) {
	l := newLiveEnv(t)
	first, id := l.dial(t, acme)
	l.addMember(t, id)

	l.clock.Advance(time.Hour)
	other := acme
	other.Harness = "opencode"
	conn, resp := l.tok.DialFolder(other)
	require.Nil(t, conn)
	e := resp.RequireError(http.StatusConflict, dto.ErrorCodeIdentityBusy)
	require.Equal(t, map[string]string{
		"display_name": "Acme", "app": "claude", "app_label": "Claude Code", "connected_at": l.start.Format(time.RFC3339),
	}, e.Details, "who is live, in which app and since when")
	require.NotEmpty(t, e.Message)

	require.True(t, l.env.App.Presence.Online(id))
	require.Equal(t, "claude", aiApp(t, l.alice, id), "a refused connection does not relabel the live session")
	m := l.post(t, "still here?")
	require.Equal(t, m.ID, first.Message(wsWait).Message.ID, "the first session keeps receiving")
	require.NoError(t, first.Closed(200*time.Millisecond), "and stays open")
}

// SES-04 (L-042): a connection that sends no X-Partyline-Live header is a process of its own: two
// of them for one AI are two processes, the second is refused with 409 identity_busy before the
// upgrade and the first stays live, receives and is not replaced.
func TestTwoConnectionsWithoutAProcessIDAreTwoProcesses(t *testing.T) {
	l := newLiveEnv(t)
	require.Empty(t, acme.Headers().Get(dto.HeaderLive), "the folder carries no process id")
	first, resp := l.tok.DialAgentHeaders(acme.Headers())
	require.NotNil(t, first, "upgrade refused: %d %s", resp.Status, resp.Body)
	id := first.Welcome(wsWait).Agent.ID
	l.addMember(t, id)

	second, resp := l.tok.DialAgentHeaders(acme.Headers())
	require.Nil(t, second)
	resp.RequireError(http.StatusConflict, dto.ErrorCodeIdentityBusy)

	require.True(t, l.env.App.Presence.Online(id))
	m := l.post(t, "still here?")
	require.Equal(t, m.ID, first.Message(wsWait).Message.ID, "the first connection keeps receiving")
	require.NoError(t, first.Closed(200*time.Millisecond), "and is not replaced")
}

// SES-04: the refusal is only for the AI that is held: another folder, or the same folder on
// another machine, is another AI and connects at the same time.
func TestBusyOnlyBlocksTheSameAI(t *testing.T) {
	l := newLiveEnv(t)
	_, id := l.dial(t, acme)
	conn, resp := l.tok.DialFolder(acme)
	require.Nil(t, conn)
	resp.RequireError(http.StatusConflict, dto.ErrorCodeIdentityBusy)

	for _, other := range []testkit.Folder{
		{Machine: "devbox", Path: "/home/alice/notes", Harness: "claude"},
		{Machine: "notebook", Path: acme.Path, Harness: "claude"},
	} {
		otherConn, otherID := l.dial(t, other)
		require.NotEqual(t, id, otherID)
		require.NoError(t, otherConn.Closed(100*time.Millisecond))
	}
}

// SES-02: a process that takes over closes the old session with 4002 taken_over and becomes the
// live session; the AI stays online (no offline event), its app and its time become the new
// session's.
func TestTakeoverMakesTheNewSessionLive(t *testing.T) {
	l := newLiveEnv(t)
	first, id := l.dial(t, acme)
	l.addMember(t, id)
	sub, cancel := l.env.App.Bus.Subscribe(64)
	defer cancel()

	l.clock.Advance(2 * time.Hour)
	takeover := acme
	takeover.Harness, takeover.Takeover = "opencode", true
	second, secondID := l.dial(t, takeover)
	require.Equal(t, id, secondID)
	requireClosedWith(t, first, 4002, "taken_over")

	require.True(t, l.env.App.Presence.Online(id))
	require.Zero(t, offlineEvents(sub, id), "the AI never went offline")
	s := l.session(t)
	require.True(t, s.Online)
	require.Equal(t, "opencode", s.App, "the app is the new session's")
	require.True(t, l.start.Add(2*time.Hour).Equal(*s.ConnectedAt), "and so is the time")
	m := l.post(t, "for the new session")
	require.Equal(t, m.ID, second.Message(wsWait).Message.ID)
	require.NoError(t, second.Closed(200*time.Millisecond))
}

// SES-02: the same process reconnecting (its live id) is accepted without takeover; the old
// socket goes away, the AI stays online and its time is the first connection's.
func TestAProcessReconnectingIsNotASecondSession(t *testing.T) {
	l := newLiveEnv(t)
	process := acme
	process.Live = testkit.NewLiveID()
	first, id := l.dial(t, process)
	sub, cancel := l.env.App.Bus.Subscribe(64)
	defer cancel()

	l.clock.Advance(time.Hour)
	again, againID := l.dial(t, process)
	require.Equal(t, id, againID)
	requireClosedWith(t, first, int(websocket.StatusNormalClosure), "reconnected")
	require.NoError(t, again.Closed(200*time.Millisecond))
	require.Zero(t, offlineEvents(sub, id))
	s := l.session(t)
	require.True(t, s.Online)
	require.True(t, l.start.Equal(*s.ConnectedAt), "still the time the process first connected")
}

// Spec edge case, SES-02: sessions that take over at the same instant leave exactly one live
// session; every other one is closed with taken_over.
func TestSimultaneousTakeoversLeaveExactlyOneLive(t *testing.T) {
	l := newLiveEnv(t)
	existing, id := l.dial(t, acme)

	const n = 8
	conns := make([]*testkit.AgentConn, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			f := acme
			f.Takeover, f.Harness = true, []string{"claude", "opencode", "pi", "omp"}[i%4]
			conns[i], _ = l.tok.DialFolder(f)
		}()
	}
	close(start)
	wg.Wait()
	for i, c := range conns {
		require.NotNil(t, c, "a takeover is never refused (%d)", i)
	}

	all := append([]*testkit.AgentConn{existing}, conns...)
	require.Eventually(t, func() bool {
		open := 0
		for _, c := range all {
			if c.Closed(20*time.Millisecond) == nil {
				open++
			}
		}
		return open == 1
	}, wsWait, 50*time.Millisecond, "exactly one session stays open")
	for i, c := range all {
		if err := c.Closed(20 * time.Millisecond); err != nil {
			ce := closeError(t, err)
			require.Equal(t, []any{websocket.StatusCode(4002), "taken_over"}, []any{ce.Code, ce.Reason}, "session %d", i)
		}
	}
	require.True(t, l.env.App.Presence.Online(id))
	live := l.session(t)
	require.True(t, live.Online)
}

// SES-06: when the live session ends, gracefully or by a dropped connection, the AI is offline
// within 5 seconds, and the next process connects without takeover.
func TestTheLiveSessionEndingFreesTheAI(t *testing.T) {
	l := newLiveEnv(t)
	sub, cancel := l.env.App.Bus.Subscribe(64)
	defer cancel()
	waitOfflineEvent := func(id string) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case e := <-sub:
				if p, ok := e.(events.AgentPresence); ok && p.AgentID == id && !p.Online {
					return
				}
			case <-deadline:
				require.FailNow(t, "no offline event within 5 seconds")
			}
		}
	}

	graceful, id := l.dial(t, acme)
	graceful.Close()
	waitOfflineEvent(id)
	dropped, _ := l.dial(t, acme)
	require.NoError(t, dropped.Closed(100*time.Millisecond), "the next process connected without takeover")
	require.NoError(t, dropped.Conn.CloseNow())
	waitOfflineEvent(id)
	require.False(t, l.session(t).Online)
	last, _ := l.dial(t, acme)
	require.NoError(t, last.Closed(100*time.Millisecond))
}

// SES-08: messages posted while the AI has no live session wait for the next one, which gets
// the last 50 of them and a notice for the older ones, in order.
func TestMessagesWhileOfflineArriveAsBacklog(t *testing.T) {
	l := newLiveEnv(t)
	first, id := l.dial(t, acme)
	l.addMember(t, id)
	first.Close()
	waitForOffline(t, l.env, id)

	var posted []dto.Message
	for i := 1; i <= 55; i++ {
		posted = append(posted, l.post(t, fmt.Sprintf("message %d", i)))
	}
	second, _ := l.tok.DialFolder(acme)
	require.NotNil(t, second)
	second.Welcome(wsWait)
	n := second.Notice(wsWait)
	require.Equal(t, "5 older messages skipped; use the history tool", n.Text)
	for _, m := range posted[5:] {
		require.Equal(t, m.ID, second.Message(wsWait).Message.ID)
	}
	second.ExpectNone(300 * time.Millisecond)
}

// waitForOffline waits until the server sees the AI offline.
func waitForOffline(t *testing.T, env *testkit.Env, id string) {
	t.Helper()
	require.Eventually(t, func() bool { return !env.App.Presence.Online(id) }, wsWait, 20*time.Millisecond)
}

// requireClosedWith waits for the server to close conn with code and reason.
func requireClosedWith(t *testing.T, conn *testkit.AgentConn, code int, reason string) {
	t.Helper()
	ce := closeError(t, conn.Closed(wsWait))
	require.Equal(t, websocket.StatusCode(code), ce.Code)
	require.Equal(t, reason, ce.Reason)
}
