package agents_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// stateOf reads the per-room state of one session of the AI.
func (f *sessionFixture) stateOf(t *testing.T, sessionKey string) db.SessionRoomState {
	t.Helper()
	state, err := f.env.App.Store.R().GetSessionRoomState(context.Background(), db.GetSessionRoomStateParams{
		AgentID: f.agentID, SessionKey: sessionKey, RoomID: f.room.ID,
	})
	require.NoError(t, err)
	return state
}

// waitReadSeq waits until the session's read position in the AI's room is seq.
func (f *sessionFixture) waitReadSeq(t *testing.T, sessionKey string, seq int64) {
	t.Helper()
	require.Eventually(t, func() bool {
		state, err := f.env.App.Store.R().GetSessionRoomState(context.Background(), db.GetSessionRoomStateParams{
			AgentID: f.agentID, SessionKey: sessionKey, RoomID: f.room.ID,
		})
		return err == nil && state.ReadSeq == seq
	}, 5*time.Second, 10*time.Millisecond, "the read position of %s", sessionKey)
}

// SN-01, SN-02: a session that connects joins each room of the AI caught up (its state row is created at the
// room's join point) with the default of its role; an acknowledgement moves the principal's read position
// together with its cursor, and a sibling that joined later counts the newer messages as unread.
func TestASessionJoinsCaughtUpAndAcksMoveTheReadPosition(t *testing.T) {
	ctx := context.Background()
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)

	joined := f.stateOf(t, "principal")
	require.Zero(t, joined.ReadSeq, "a session that connects to an empty room is caught up at 0")
	require.Equal(t, agents.NotifyOn, joined.Notify, "the principal is notified of everything its delivery mode wants")

	one, two := f.post(t, "one"), f.post(t, "two")
	require.Equal(t, one.ID, principal.Message(time.Minute).Message.ID)
	require.Equal(t, two.ID, principal.Message(time.Minute).Message.ID)
	principal.Ack(two.ID)
	f.waitReadSeq(t, "principal", two.Seq)
	cursor, err := f.env.App.Store.R().GetAgentCursor(ctx, db.GetAgentCursorParams{AgentID: f.agentID, RoomID: f.room.ID})
	require.NoError(t, err)
	require.Equal(t, two.Seq, cursor, "the cursor and the read position moved together")

	// A parallel session joins at the position of the room, not at its beginning, and is only notified of what
	// addresses it.
	parallel := f.dial(t)
	f.settle(t, parallel)
	sibling := f.stateOf(t, "label:claude-2")
	require.Equal(t, two.Seq, sibling.ReadSeq, "the sibling starts caught up")
	require.Equal(t, agents.NotifyMentions, sibling.Notify, "a parallel session is notified of mentions only")

	three := f.post(t, "three")
	require.Equal(t, three.ID, principal.Message(time.Minute).Message.ID)
	parallel.ExpectNone(300 * time.Millisecond)
	principal.Ack(three.ID)
	f.waitReadSeq(t, "principal", three.Seq)
	f.waitReadSeq(t, "label:claude-2", two.Seq)
	unread, err := f.env.App.Store.R().CountSessionUnreadByRoom(ctx, db.CountSessionUnreadByRoomParams{
		AgentID: f.agentID, SessionKey: "label:claude-2", AuthorLabel: "claude-2",
	})
	require.NoError(t, err)
	require.Equal(t, []db.CountSessionUnreadByRoomRow{{RoomID: f.room.ID, Unread: 1}}, unread,
		"the message the principal read is unread for the sibling")
	counted, err := f.env.App.Store.R().CountSessionUnreadByRoom(ctx, db.CountSessionUnreadByRoomParams{
		AgentID: f.agentID, SessionKey: "principal", AuthorLabel: "",
	})
	require.NoError(t, err)
	require.Empty(t, counted, "the principal read everything in the room")
}

// SN-01: a session that comes back with the same harness session id keeps its read position, also as a new
// process, instead of joining caught up again.
func TestAHarnessSessionKeepsItsReadPositionAcrossProcesses(t *testing.T) {
	f := newSessionFixture(t)
	dial := func() *testkit.AgentConn {
		t.Helper()
		folder := testkit.FolderOf("atlas")
		folder.HarnessSession = "claude-session-1"
		conn, resp := f.token.DialFolder(folder)
		require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
		conn.Welcome(time.Minute)
		return conn
	}

	first := dial()
	f.settle(t, first)
	one := f.post(t, "one")
	require.Equal(t, one.ID, first.Message(time.Minute).Message.ID)
	first.Ack(one.ID)
	f.waitReadSeq(t, "claude-session-1", one.Seq)
	first.Close()
	f.waitPrincipalGone(t)

	// A message arrives while the session is away: the backlog delivers it to the next process, which keeps the
	// position it had read instead of jumping to the join point.
	two := f.post(t, "two")
	again := dial()
	require.Equal(t, two.ID, again.Message(time.Minute).Message.ID)
	require.Equal(t, one.Seq+1, two.Seq, "the unread message is the next one in the stream")
	require.Equal(t, one.Seq, f.stateOf(t, "claude-session-1").ReadSeq, "the read position survives the process")
}
