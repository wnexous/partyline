package agents

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/events"
)

// presenceFixture is an AI of alice (folder /work/builder) and a bus subscription.
type presenceFixture struct {
	*fixture
	agent  struct{ ID string }
	owner  string
	events <-chan events.Event
}

func newPresenceFixture(t *testing.T) (*presenceFixture, func(s Session, liveID string, takeover bool) (bool, error)) {
	t.Helper()
	f := newFixture(t)
	alice := f.user(t, "alice")
	agent := mustAgent(t, f, f.upsert(t, alice, "builder"))
	ch, cancel := f.bus.Subscribe(64)
	t.Cleanup(cancel)
	p := &presenceFixture{fixture: f, owner: alice.UserID, events: ch}
	p.agent.ID = agent.ID
	attach := func(s Session, liveID string, takeover bool) (bool, error) {
		_, fresh := f.presence.AttachLabeled(context.Background(), agent.Agent, s, "claude", liveID, "", takeover)
		return fresh, nil
	}
	return p, attach
}

func (p *presenceFixture) online() events.Event {
	return events.AgentPresence{AgentID: p.agent.ID, OwnerID: p.owner, Online: true}
}

func (p *presenceFixture) offline() events.Event {
	return events.AgentPresence{AgentID: p.agent.ID, OwnerID: p.owner, Online: false}
}

// SESS-10: a connection from another process is a parallel session, not a refusal: the principal
// is untouched, both are live and the second gets the next free label.
func TestPresenceAttachesASecondProcess(t *testing.T) {
	p, attach := newPresenceFixture(t)
	first, second := &fakeSession{}, &fakeSession{}

	fresh, err := attach(first, "process-one", false)
	require.NoError(t, err)
	require.True(t, fresh)
	require.Equal(t, []events.Event{p.online()}, drain(p.events))
	live, ok := p.presence.Live(p.agent.ID)
	require.True(t, ok)
	require.Equal(t, t0, live.Since)

	fresh, err = attach(second, "process-two", false)
	require.NoError(t, err)
	require.True(t, fresh, "a second process starts a session")
	_, _, closed := first.state()
	require.Zero(t, closed, "the principal is untouched")
	require.Equal(t, []events.Event{p.online()}, drain(p.events), "a second session is a presence change")
	require.True(t, p.presence.Detach(context.Background(), p.agent.ID, second))
	require.True(t, p.presence.Online(p.agent.ID), "the principal keeps the AI online")
	require.Equal(t, []events.Event{p.online()}, drain(p.events), "the AI stays online while the principal remains")
	require.False(t, p.presence.Detach(context.Background(), p.agent.ID, second), "the parallel session already ended")
	require.Equal(t, []string{""}, labelsOf(p.presence.Sessions(p.agent.ID)))
}

// SESS-11, SESS-13: a connection that sends no process id is a process of its own, also when the
// principal sent none either: two such connections are two sessions, and a takeover replaces the
// principal (taken_over), never the silent reconnect of one process.
func TestPresenceTreatsTwoConnectionsWithoutAProcessIDAsTwoSessions(t *testing.T) {
	p, attach := newPresenceFixture(t)
	first, second, third := &fakeSession{}, &fakeSession{}, &fakeSession{}
	fresh, err := attach(first, "", false)
	require.NoError(t, err)
	require.True(t, fresh)
	drain(p.events)

	fresh, err = attach(second, "", false)
	require.NoError(t, err)
	require.True(t, fresh, "a connection that tells no process is a session of its own")
	_, _, closed := first.state()
	require.Zero(t, closed, "the principal is untouched")
	require.Equal(t, []string{"", "claude-2"}, labelsOf(p.presence.Sessions(p.agent.ID)))

	fresh, err = attach(third, "", true)
	require.NoError(t, err)
	require.True(t, fresh, "a takeover starts a new session")
	code, reason, closed := first.state()
	require.Equal(t, []any{1, CloseTakenOver, "taken_over"}, []any{closed, code, reason}, "closed as taken over, not as a reconnect")
	require.Equal(t, []string{"", "claude-2"}, labelsOf(p.presence.Sessions(p.agent.ID)), "the parallel session stays")
}

// SES-02: a session that takes over closes the old one with taken_over and becomes the live
// session; the AI stays online (no offline/online events) and its time restarts.
func TestPresenceTakeoverClosesTheOldSession(t *testing.T) {
	p, attach := newPresenceFixture(t)
	first, second := &fakeSession{}, &fakeSession{}
	_, err := attach(first, "process-one", false)
	require.NoError(t, err)
	drain(p.events)
	p.clock.Advance(time.Hour)

	fresh, err := attach(second, "process-two", true)
	require.NoError(t, err)
	require.True(t, fresh)
	code, reason, closed := first.state()
	require.Equal(t, []any{1, CloseTakenOver, "taken_over"}, []any{closed, code, reason})
	require.Equal(t, websocket.StatusCode(4002), CloseTakenOver)
	_, _, closed = second.state()
	require.Zero(t, closed)
	require.Equal(t, []events.Event{p.online()}, drain(p.events), "the new session is announced and the AI stays online")
	live, _ := p.presence.Live(p.agent.ID)
	require.Equal(t, t0.Add(time.Hour), live.Since, "the new session's time")

	require.False(t, p.presence.Detach(context.Background(), p.agent.ID, first), "the taken-over session no longer counts")
	require.True(t, p.presence.Online(p.agent.ID))
	require.True(t, p.presence.Detach(context.Background(), p.agent.ID, second))
	require.False(t, p.presence.Online(p.agent.ID))
	require.Equal(t, []events.Event{p.offline()}, drain(p.events))
}

// SES-02: the same process reconnecting (same live id) is accepted without takeover and
// silently: the old socket is closed normally, the AI never goes offline and its time stays.
func TestPresenceSameProcessReconnectsSilently(t *testing.T) {
	p, attach := newPresenceFixture(t)
	first, again := &fakeSession{}, &fakeSession{}
	_, err := attach(first, "process-one", false)
	require.NoError(t, err)
	drain(p.events)
	p.clock.Advance(time.Hour)

	fresh, err := attach(again, "process-one", false)
	require.NoError(t, err)
	require.False(t, fresh, "not a new session")
	code, reason, closed := first.state()
	require.Equal(t, []any{1, websocket.StatusNormalClosure, "reconnected"}, []any{closed, code, reason})
	require.Empty(t, drain(p.events))
	live, _ := p.presence.Live(p.agent.ID)
	require.Equal(t, t0, live.Since, "the session started when the process first connected")
	require.False(t, p.presence.Detach(context.Background(), p.agent.ID, first))
	require.True(t, p.presence.Detach(context.Background(), p.agent.ID, again))
}

// Spec edge case: sessions that take over at the same instant leave exactly one live session;
// every other one is closed with taken_over, and the AI is online throughout.
func TestPresenceConcurrentTakeoversLeaveExactlyOneSession(t *testing.T) {
	p, attach := newPresenceFixture(t)
	sessions := make([]*fakeSession, 12)
	for i := range sessions {
		sessions[i] = &fakeSession{}
	}
	var mu sync.Mutex
	var attached []*fakeSession
	errs := parallel(len(sessions), func(i int) error {
		_, err := attach(sessions[i], fmt.Sprintf("process-%d", i), true)
		if err == nil {
			mu.Lock()
			attached = append(attached, sessions[i])
			mu.Unlock()
		}
		return err
	})
	for _, err := range errs {
		require.NoError(t, err, "a takeover is never refused")
	}
	require.Len(t, attached, len(sessions))

	var open []*fakeSession
	for _, s := range sessions {
		code, reason, closed := s.state()
		if closed == 0 {
			open = append(open, s)
			continue
		}
		require.Equal(t, []any{1, CloseTakenOver, "taken_over"}, []any{closed, code, reason})
	}
	require.Len(t, open, 1, "exactly one session stays open")
	live, ok := p.presence.Session(p.agent.ID)
	require.True(t, ok)
	require.Same(t, open[0], live, "and it is the live session")
	require.True(t, p.presence.Online(p.agent.ID))
	announced := drain(p.events)
	require.Len(t, announced, len(sessions), "every new session is a presence change")
	for _, e := range announced {
		require.Equal(t, p.online(), e, "announced online, never offline: no flapping")
	}
}

// SES-06: the live session ending marks the AI offline at once and the next process attaches
// without takeover.
func TestPresenceNextProcessAttachesAfterTheLiveSessionEnds(t *testing.T) {
	p, attach := newPresenceFixture(t)
	first := &fakeSession{}
	_, err := attach(first, "process-one", false)
	require.NoError(t, err)
	drain(p.events)
	require.True(t, p.presence.Detach(context.Background(), p.agent.ID, first))
	require.False(t, p.presence.Online(p.agent.ID))
	require.Equal(t, []events.Event{p.offline()}, drain(p.events))
	_, ok := p.presence.Live(p.agent.ID)
	require.False(t, ok)

	_, err = attach(&fakeSession{}, "process-two", false)
	require.NoError(t, err)
	require.Equal(t, []events.Event{p.online()}, drain(p.events))
}

// SESS-32: every session change publishes AgentPresence, so a profile and an autocomplete refresh
// while the AI stays online: the second attach, a detach that leaves the principal, and the offline
// transition only when the last session ends.
func TestPresencePublishesEverySessionChange(t *testing.T) {
	p, _ := newPresenceFixture(t)
	first, second := &fakeSession{}, &fakeSession{}

	_, _ = p.attachLabeled(t, first, "claude", "process-one", "", false)
	require.Equal(t, []events.Event{p.online()}, drain(p.events))
	_, _ = p.attachLabeled(t, second, "claude", "process-two", "", false)
	require.Equal(t, []events.Event{p.online()}, drain(p.events), "the second session is announced while the AI stays online")

	require.True(t, p.presence.Detach(context.Background(), p.agent.ID, second))
	require.Equal(t, []events.Event{p.online()}, drain(p.events), "a detach that leaves a session announces the AI is still online")
	require.True(t, p.presence.Detach(context.Background(), p.agent.ID, first))
	require.Equal(t, []events.Event{p.offline()}, drain(p.events), "the offline event waits for the last session")
}

// attachLabeled makes s a session of the AI with the harness it reports and the label it prefers,
// the way the gateway attaches a connection (SESS-11).
func (p *presenceFixture) attachLabeled(t *testing.T, s Session, harness, liveID, preferred string, takeover bool) (Live, bool) {
	t.Helper()
	return p.presence.AttachLabeled(context.Background(), mustAgent(t, p.fixture, p.agent.ID).Agent, s, harness, liveID, preferred, takeover)
}

// labelsOf lists the labels of live sessions in the order presence returns them.
func labelsOf(lives []Live) []string {
	labels := make([]string, len(lives))
	for i, live := range lives {
		labels[i] = live.Label
	}
	return labels
}

// D2, SESS-11: every connection keeps the label it prefers while the label is free, the principal first
// in the list, and a label is free again as soon as its session ends.
func TestPresenceGivesEachSessionItsLabel(t *testing.T) {
	p, _ := newPresenceFixture(t)
	principal, second, third := &fakeSession{}, &fakeSession{}, &fakeSession{}

	live, fresh := p.attachLabeled(t, principal, "claude", "process-one", "", false)
	require.True(t, fresh)
	require.Equal(t, Live{Label: "", Harness: "claude", Since: t0}, live)
	live, fresh = p.attachLabeled(t, second, "claude", "process-two", "claude-2", false)
	require.True(t, fresh)
	require.Equal(t, Live{Label: "claude-2", Harness: "claude", Since: t0}, live)
	live, fresh = p.attachLabeled(t, third, "claude", "process-three", "claude-3", false)
	require.True(t, fresh)
	require.Equal(t, Live{Label: "claude-3", Harness: "claude", Since: t0}, live)

	require.Equal(t, []Live{
		{Label: "", Harness: "claude", Since: t0},
		{Label: "claude-2", Harness: "claude", Since: t0},
		{Label: "claude-3", Harness: "claude", Since: t0},
	}, p.presence.Sessions(p.agent.ID))
	require.True(t, p.presence.Online(p.agent.ID))
	byLabel, ok := p.presence.SessionByLabel(p.agent.ID, "claude-2")
	require.True(t, ok)
	require.Same(t, second, byLabel)
	main, ok := p.presence.Main(p.agent.ID)
	require.True(t, ok)
	require.Same(t, principal, main)

	require.True(t, p.presence.Detach(context.Background(), p.agent.ID, second))
	live, fresh = p.attachLabeled(t, &fakeSession{}, "claude", "process-four", "claude-2", false)
	require.True(t, fresh)
	require.Equal(t, "claude-2", live.Label, "the label its session freed")
}

// SESS-12: two connections racing for the principal label end with two different labels, exactly one of
// them "", and neither is refused.
func TestTwoConnectionsRaceForThePrincipal(t *testing.T) {
	p, _ := newPresenceFixture(t)
	sessions := []*fakeSession{{}, {}}
	lives := make([]Live, len(sessions))
	errs := parallel(len(sessions), func(i int) error {
		lives[i], _ = p.attachLabeled(t, sessions[i], "claude", fmt.Sprintf("process-%d", i), "", false)
		return nil
	})
	for _, err := range errs {
		require.NoError(t, err)
	}
	require.ElementsMatch(t, []string{"", "claude-2"}, labelsOf(lives), "one principal and one parallel session")
	require.ElementsMatch(t, []string{"", "claude-2"}, labelsOf(p.presence.Sessions(p.agent.ID)))
	require.True(t, p.presence.Online(p.agent.ID))

	_, ok := p.presence.Main(p.agent.ID)
	require.True(t, ok, "exactly one session holds the principal label")
	announced := drain(p.events)
	require.Len(t, announced, len(sessions), "both attaches are presence changes")
	for _, e := range announced {
		require.Equal(t, p.online(), e, "announced online, never offline: no flapping")
	}
}

// SESS-13: takeover replaces the principal session only; a parallel or worktree label ignores it.
func TestTakeoverReplacesOnlyThePrincipal(t *testing.T) {
	p, _ := newPresenceFixture(t)
	first := &fakeSession{}
	_, _ = p.attachLabeled(t, first, "claude", "process-one", "", false)
	drain(p.events)
	p.clock.Advance(time.Hour)

	second := &fakeSession{}
	live, fresh := p.attachLabeled(t, second, "claude", "process-two", "", true)
	require.True(t, fresh)
	require.Equal(t, "", live.Label)
	require.Equal(t, t0.Add(time.Hour), live.Since, "the new principal's time")
	code, reason, closed := first.state()
	require.Equal(t, []any{1, CloseTakenOver, "taken_over"}, []any{closed, code, reason})

	third := &fakeSession{}
	live, fresh = p.attachLabeled(t, third, "pi", "process-three", "wt", true)
	require.True(t, fresh)
	require.Equal(t, "wt", live.Label)
	_, _, closed = second.state()
	require.Zero(t, closed, "a takeover of a worktree label closes nothing")
	require.True(t, p.presence.Online(p.agent.ID))
	main, ok := p.presence.Main(p.agent.ID)
	require.True(t, ok)
	require.Same(t, second, main, "the principal is still live")
}

// SESS-14: a process that lost its socket keeps its label and start time for ten minutes, then the label
// is a new session's and is the same again only because it is free.
func TestReconnectKeepsTheLabel(t *testing.T) {
	p, _ := newPresenceFixture(t)
	first := &fakeSession{}
	live, _ := p.attachLabeled(t, first, "claude", "P", "claude-2", false)
	require.Equal(t, Live{Label: "claude-2", Harness: "claude", Since: t0}, live)
	require.True(t, p.presence.Detach(context.Background(), p.agent.ID, first))
	p.clock.Advance(5 * time.Minute)

	again := &fakeSession{}
	live, fresh := p.attachLabeled(t, again, "claude", "P", "other-label", false)
	require.True(t, fresh, "the session ended")
	require.Equal(t, "claude-2", live.Label, "the memory wins over the preferred label")
	require.Equal(t, t0, live.Since, "the start time stays")

	require.True(t, p.presence.Detach(context.Background(), p.agent.ID, again))
	p.clock.Advance(11 * time.Minute) // the memory of the drop at t0+5 expired after ten minutes

	later := &fakeSession{}
	live, _ = p.attachLabeled(t, later, "claude", "P", "claude-2", false)
	require.Equal(t, "claude-2", live.Label, "free again")
	require.Equal(t, t0.Add(16*time.Minute), live.Since, "no memory: this is a new session")

	live, _ = p.attachLabeled(t, &fakeSession{}, "claude", "Q", "claude-2", false)
	require.Equal(t, "claude-3", live.Label, "the label is taken")
}

// SESS-19, POS-04: ending the AI's session closes every label with the same code and reason and marks
// the AI offline once.
func TestKickClosesEverySession(t *testing.T) {
	p, _ := newPresenceFixture(t)
	first, second := &fakeSession{}, &fakeSession{}
	_, _ = p.attachLabeled(t, first, "claude", "process-one", "", false)
	_, _ = p.attachLabeled(t, second, "pi", "process-two", "claude-2", false)
	require.Equal(t, []events.Event{p.online(), p.online()}, drain(p.events))

	p.presence.Kick(context.Background(), p.agent.ID, CloseTakenOver, "ended by @lead")
	for _, s := range []*fakeSession{first, second} {
		code, reason, closed := s.state()
		require.Equal(t, []any{1, CloseTakenOver, "ended by @lead"}, []any{closed, code, reason})
	}
	require.False(t, p.presence.Online(p.agent.ID))
	require.Empty(t, p.presence.Sessions(p.agent.ID))
	require.Equal(t, []events.Event{p.offline()}, drain(p.events))
}

// SESS-14: the same process reconnecting keeps its label, harness and start time while its old socket is
// closed as a reconnect, even when it prefers another label.
func TestSameProcessReplacesItsSocket(t *testing.T) {
	p, _ := newPresenceFixture(t)
	first, again := &fakeSession{}, &fakeSession{}
	live, _ := p.attachLabeled(t, first, "claude", "P", "claude-2", false)
	drain(p.events)
	p.clock.Advance(time.Hour)

	reconnected, fresh := p.attachLabeled(t, again, "pi", "P", "wt", false)
	require.False(t, fresh, "not a new session")
	require.Equal(t, live, reconnected, "the label, the harness and the start time stay")
	code, reason, closed := first.state()
	require.Equal(t, []any{1, websocket.StatusNormalClosure, "reconnected"}, []any{closed, code, reason})
	require.Equal(t, []string{"claude-2"}, labelsOf(p.presence.Sessions(p.agent.ID)))
	require.Empty(t, drain(p.events), "a reconnect is no session change")
}

// SESS-19, SESS-24: Kick gives every session it closes to the end hook, after the AI is offline, so the
// rows each connection owned can be swept; Drop, for an AI whose row is gone, never calls it.
func TestKickHandsEachClosedSessionToTheEndHook(t *testing.T) {
	p, _ := newPresenceFixture(t)
	first, second := &fakeSession{}, &fakeSession{}
	_, _ = p.attachLabeled(t, first, "claude", "process-one", "", false)
	_, _ = p.attachLabeled(t, second, "pi", "process-two", "wt", false)

	var mu sync.Mutex
	var ended []Session
	var onlineWhenCalled []bool
	p.presence.OnEnded(func(_ context.Context, agentID string, s Session) {
		mu.Lock()
		defer mu.Unlock()
		require.Equal(t, p.agent.ID, agentID)
		ended = append(ended, s)
		onlineWhenCalled = append(onlineWhenCalled, p.presence.Online(agentID))
	})
	p.presence.Kick(context.Background(), p.agent.ID, CloseTakenOver, "ended")
	require.ElementsMatch(t, []Session{first, second}, ended)
	require.Equal(t, []bool{false, false}, onlineWhenCalled)

	ended = nil
	third := &fakeSession{}
	_, _ = p.attachLabeled(t, third, "claude", "process-three", "", false)
	p.presence.Drop(p.agent.ID, CloseDeleted, ReasonDeleted)
	require.Empty(t, ended, "a dropped AI has no rows to sweep and never waits for the database")
}

// SESS-14: a process holds its live id while one of its sockets is attached; the lookup finds the
// session of an owner and the replaced socket's end owns nothing.
func TestPresenceHoldsALiveIDWhileOneOfItsSocketsIsAttached(t *testing.T) {
	p, _ := newPresenceFixture(t)
	first, again := &fakeSession{}, &fakeSession{}
	_, _ = p.attachLabeled(t, first, "claude", "P", "wt", false)
	require.True(t, p.presence.HoldsLive(p.agent.ID, "P"))
	require.False(t, p.presence.HoldsLive(p.agent.ID, "other"))
	require.False(t, p.presence.HoldsLive(p.agent.ID, ""), "no live id is held by anyone")

	_, _ = p.attachLabeled(t, again, "claude", "P", "wt", false)
	require.False(t, p.presence.Detach(context.Background(), p.agent.ID, first), "the replaced socket is no longer a session")
	require.True(t, p.presence.HoldsLive(p.agent.ID, "P"), "the new socket holds it")
	got, ok := p.presence.Find(p.agent.ID, func(s Session) bool { return s == again })
	require.True(t, ok)
	require.Same(t, again, got)

	require.True(t, p.presence.Detach(context.Background(), p.agent.ID, again))
	require.False(t, p.presence.HoldsLive(p.agent.ID, "P"))
}
