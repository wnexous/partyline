package agents

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// hookedSession is a live session whose Close runs a test hook: the first thing Delete does once the AI
// is gone from the database is end its session, so the hook stands for whatever can go wrong after the commit.
type hookedSession struct {
	fakeSession
	onClose func()
}

func (s *hookedSession) Close(code websocket.StatusCode, reason string) {
	s.fakeSession.Close(code, reason)
	if s.onClose != nil {
		s.onClose()
	}
}

// worldOfAHostedAI is an AI of alice's with a group, a DM with another user and a live session.
type worldOfAHostedAI struct {
	f             *fixture
	alice         authn.Principal
	ai, group, dm string
	home          string
	session       *hookedSession
	events        <-chan events.Event
}

func newWorldOfAHostedAI(t *testing.T, f *fixture) *worldOfAHostedAI {
	t.Helper()
	ctx := context.Background()
	w := &worldOfAHostedAI{f: f, alice: f.user(t, "alice")}
	bob := f.user(t, "bobby")
	w.ai = f.upsert(t, w.alice, "builder")
	w.home = f.orgOf(t, w.alice)
	w.group = f.room(t, w.alice)
	f.exec(t, "INSERT INTO room_members (id, room_id, kind, member_id, added_by, created_at) VALUES (?, ?, 'agent', ?, ?, ?)",
		store.NewID(), w.group, w.ai, w.alice.UserID, t0.UnixMilli())
	// The DM of the AI with bob goes with it: the AI's intelligence is a participant.
	lo, hi := w.ai, bob.UserID
	if lo > hi {
		lo, hi = hi, lo
	}
	w.dm = store.NewID()
	f.exec(t, "INSERT INTO rooms (id, owner_id, name, kind, dm_a, dm_b, created_at, updated_at) VALUES (?, ?, '', 'dm', ?, ?, ?, ?)",
		w.dm, bob.UserID, lo, hi, t0.UnixMilli(), t0.UnixMilli())
	f.exec(t, "INSERT INTO room_members (id, room_id, kind, member_id, added_by, created_at) VALUES (?, ?, 'agent', ?, ?, ?)",
		store.NewID(), w.dm, w.ai, bob.UserID, t0.UnixMilli())
	f.exec(t, "INSERT INTO room_members (id, room_id, kind, member_id, added_by, created_at) VALUES (?, ?, 'user', ?, ?, ?)",
		store.NewID(), w.dm, bob.UserID, bob.UserID, t0.UnixMilli())

	w.session = &hookedSession{}
	_, err := f.presence.Attach(ctx, mustAgent(t, f, w.ai).Agent, w.session, "process-one", false)
	require.NoError(t, err)
	var cancel func()
	w.events, cancel = f.bus.Subscribe(32)
	t.Cleanup(cancel)
	return w
}

// requireDeleted checks what the deletion did: the AI is gone with its rows, its live session was ended as
// that of a deleted AI, and the clients were told - the group lost a member, its DM is gone, the
// organization's chart changed.
func (w *worldOfAHostedAI) requireDeleted(t *testing.T) {
	t.Helper()
	require.Zero(t, w.f.count(t, "SELECT COUNT(*) FROM agents WHERE id = ?", w.ai))
	require.Zero(t, w.f.count(t, "SELECT COUNT(*) FROM intelligences WHERE id = ?", w.ai))
	require.Zero(t, w.f.count(t, "SELECT COUNT(*) FROM rooms WHERE id = ?", w.dm), "its DM went with it")
	require.Equal(t, 1, w.f.count(t, "SELECT COUNT(*) FROM rooms WHERE id = ?", w.group), "its group stays")
	code, reason, closed := w.session.state()
	require.Equal(t, 1, closed)
	require.Equal(t, CloseDeleted, code)
	require.Equal(t, ReasonDeleted, reason)
	require.False(t, w.f.presence.Online(w.ai))
	require.ElementsMatch(t, []events.Event{
		events.AgentPresence{AgentID: w.ai, OwnerID: w.alice.UserID, Online: false},
		events.MembersChanged{RoomID: w.group},
		events.RoomChanged{RoomID: w.dm, Deleted: true},
		events.OrgChanged{OrgID: w.home},
	}, drain(w.events))
}

// A delete whose row is gone is a success whatever happens afterwards (the answer of DELETE /agents/{id} is
// 204): the last thing Delete does is the cleanup, and the cleanup is best effort - a failure is logged and
// the steps after it still run. Here the live session blows up when it is ended.
func TestDeleteSucceedsEvenIfEndingTheSessionPanics(t *testing.T) {
	f := newFixture(t)
	var logs bytes.Buffer
	f.reg = NewRegistry(Deps{
		Store: f.st, Access: f.az, Bus: f.bus, Presence: f.presence, Audit: audit.New(f.st, f.clock.Now), Now: f.clock.Now,
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
	})
	w := newWorldOfAHostedAI(t, f)
	w.session.onClose = func() { panic("the socket library failed") }

	require.NoError(t, f.reg.Delete(context.Background(), w.alice, w.ai), "the AI is deleted, whatever its session does")
	require.Zero(t, f.count(t, "SELECT COUNT(*) FROM agents WHERE id = ?", w.ai))
	require.Zero(t, f.count(t, "SELECT COUNT(*) FROM rooms WHERE id = ?", w.dm))
	require.False(t, f.presence.Online(w.ai), "and it is offline")
	require.ElementsMatch(t, []events.Event{
		events.AgentPresence{AgentID: w.ai, OwnerID: w.alice.UserID, Online: false},
		events.MembersChanged{RoomID: w.group},
		events.RoomChanged{RoomID: w.dm, Deleted: true},
		events.OrgChanged{OrgID: w.home},
	}, drain(w.events), "the steps after the failed one ran")
	require.Contains(t, logs.String(), "level=ERROR", "the failure is logged")
	require.Contains(t, logs.String(), w.ai, "with the AI it was about")
}

// The row is gone, so nothing after the commit may wait for the database: the answer of a delete that
// took effect must not hang on a busy writer (a proxy in front answers its own error when the upstream is
// late, and the AI is deleted all the same). Here the single writer is taken as soon as the session is ended,
// which is right after the commit.
func TestDeleteDoesNotWaitForTheDatabaseAfterTheCommit(t *testing.T) {
	f := newFixture(t)
	w := newWorldOfAHostedAI(t, f)
	held, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	releaseWriter := func() { once.Do(func() { close(release) }) }
	t.Cleanup(releaseWriter)
	w.session.onClose = func() {
		go func() {
			_ = f.st.WithTx(context.Background(), func(*db.Queries) error {
				close(held)
				<-release
				return nil
			})
		}()
		<-held
	}

	done := make(chan error, 1)
	go func() { done <- f.reg.Delete(context.Background(), w.alice, w.ai) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		releaseWriter()
		require.Fail(t, "Delete waited for the writer after the commit")
	}
	releaseWriter()
	w.requireDeleted(t)
}

// The request can end at any time - the client or a proxy in front hangs up - and the cleanup of a delete
// that committed must not depend on it: the DM that went with the AI is still announced as deleted, and not as
// a room that changed, because the request's context is gone when the cleanup reads.
func TestDeleteCleansUpWhenTheRequestEndsRightAfterTheCommit(t *testing.T) {
	f := newFixture(t)
	w := newWorldOfAHostedAI(t, f)
	ctx, hangUp := context.WithCancel(context.Background())
	defer hangUp()
	w.session.onClose = hangUp

	require.NoError(t, f.reg.Delete(ctx, w.alice, w.ai))
	require.Error(t, ctx.Err(), "the request ended")
	w.requireDeleted(t)
}
