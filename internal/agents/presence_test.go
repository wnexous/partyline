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
		return f.presence.Attach(context.Background(), agent.Agent, s, liveID, takeover)
	}
	return p, attach
}

func (p *presenceFixture) online() events.Event {
	return events.AgentPresence{AgentID: p.agent.ID, OwnerID: p.owner, Online: true}
}

func (p *presenceFixture) offline() events.Event {
	return events.AgentPresence{AgentID: p.agent.ID, OwnerID: p.owner, Online: false}
}

// SES-04: an AI has one live session. A connection from another process is refused while it is
// held, the held session is not touched, and the refused one never counts as attached.
func TestPresenceRefusesASecondProcess(t *testing.T) {
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
	require.ErrorIs(t, err, ErrBusy)
	require.False(t, fresh)
	_, _, closed := first.state()
	require.Zero(t, closed, "the live session is untouched")
	require.Empty(t, drain(p.events), "a refusal changes nothing")
	require.False(t, p.presence.Detach(context.Background(), p.agent.ID, second), "the refused session was never attached")
	require.True(t, p.presence.Online(p.agent.ID))

	busy, ok := p.presence.Busy(p.agent.ID, "process-two")
	require.True(t, ok, "another process is blocked")
	require.Equal(t, t0, busy.Since)
	_, ok = p.presence.Busy(p.agent.ID, "process-one")
	require.False(t, ok, "the holder itself is not blocked")
	_, ok = p.presence.Busy("someone-else", "process-two")
	require.False(t, ok, "an AI nobody holds blocks nobody")
	_, ok = p.presence.Busy(p.agent.ID, "")
	require.True(t, ok, "a connection that tells no process is another process")
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
	require.Empty(t, drain(p.events), "taking over keeps the AI online")
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
	require.Equal(t, []events.Event{p.online()}, drain(p.events), "one online event, no flapping")
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
