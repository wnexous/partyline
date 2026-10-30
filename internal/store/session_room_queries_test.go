package store

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
)

// SN-01 (data): the join point of a session is the highest sequence the room has assigned, counted from
// next_seq, so a message retention deleted does not move it back.
func TestRoomHighSeqCountsWhatTheRoomAssigned(t *testing.T) {
	e := sessionRoomFixture(t)
	ctx := context.Background()
	join, err := e.s.R().GetRoomHighSeq(ctx, "r1")
	require.NoError(t, err)
	require.Zero(t, join, "a room with no message is at 0")

	for i := 1; i <= 3; i++ {
		sessionsMessage(e, fmt.Sprintf("m%d", i), "r1", int64(i), "user", "u1")
	}
	e.must(`UPDATE rooms SET next_seq = 4 WHERE id = 'r1'`)
	join, err = e.s.R().GetRoomHighSeq(ctx, "r1")
	require.NoError(t, err)
	require.Equal(t, int64(3), join)

	e.must(`DELETE FROM messages WHERE id = 'm3'`) // retention
	join, err = e.s.R().GetRoomHighSeq(ctx, "r1")
	require.NoError(t, err)
	require.Equal(t, int64(3), join, "the newest message may be gone; the room still assigned 3")
}

// SN-01, SN-02 (data): a state row is created once, at the session's join point and with the default of its
// role; joining again (a reconnect) changes neither the read position nor the setting, and a read position
// only moves forward.
func TestSessionRoomStateDefaultsAndForwardOnlyRead(t *testing.T) {
	e := sessionRoomFixture(t)
	ctx := context.Background()
	e.must(`UPDATE rooms SET next_seq = 21 WHERE id = 'r1'`) // 20 messages so far

	e.createState("harness-principal", "r1", 20, "on")
	e.createState("label:claude-2", "r1", 20, "mentions")
	e.createState("harness-principal", "r1", 0, "mentions") // the session comes back
	require.Equal(t, dbSessionRoomState{readSeq: 20, notify: "on"}, e.sessionState("harness-principal", "r1"),
		"a row that exists keeps its position and its setting")
	require.Equal(t, dbSessionRoomState{readSeq: 20, notify: "mentions"}, e.sessionState("label:claude-2", "r1"))

	// Two sessions and one session in two rooms are different rows.
	e.must(`UPDATE rooms SET next_seq = 6 WHERE id = 'r2'`)
	e.createState("harness-principal", "r2", 5, "on")
	states, err := e.s.R().ListSessionRoomStates(ctx, db.ListSessionRoomStatesParams{
		AgentID: "a1", SessionKey: "harness-principal",
	})
	require.NoError(t, err)
	require.Len(t, states, 2)
	require.Equal(t, "r1", states[0].RoomID)
	require.Equal(t, "r2", states[1].RoomID)
	require.Equal(t, int64(5), states[1].ReadSeq)

	// An acknowledgement older than the position changes nothing, a newer one moves it, and neither changes
	// the setting.
	e.advanceState("harness-principal", "r1", 10, "on")
	require.Equal(t, int64(20), e.sessionState("harness-principal", "r1").readSeq, "a late acknowledgement cannot unread")
	e.advanceState("harness-principal", "r1", 25, "on")
	state := e.sessionState("harness-principal", "r1")
	require.Equal(t, int64(25), state.readSeq)
	require.Equal(t, "on", state.notify, "advancing keeps the setting the session chose")

	// The advance creates the row when the session has none, with the setting it is given.
	e.advanceState("label:wt", "r1", 4, "mentions")
	require.Equal(t, dbSessionRoomState{readSeq: 4, notify: "mentions"}, e.sessionState("label:wt", "r1"))

	_, err = e.s.R().GetSessionRoomState(ctx, db.GetSessionRoomStateParams{
		AgentID: "a1", SessionKey: "label:wt", RoomID: "r2",
	})
	require.ErrorIs(t, err, sql.ErrNoRows, "a session has a state row only in the rooms it joined")
}

// SN-02 (data): the setting changes for one session and one room, and only for a session that has a state
// row there.
func TestSetSessionNotifyChangesOneSessionInOneRoom(t *testing.T) {
	e := sessionRoomFixture(t)
	ctx := context.Background()
	e.createState("harness-principal", "r1", 5, "on")
	e.createState("label:claude-2", "r1", 5, "mentions")

	set := func(key, room, notify string) int64 {
		t.Helper()
		var n int64
		require.NoError(t, e.s.WithTx(ctx, func(q *db.Queries) error {
			var err error
			n, err = q.SetSessionNotify(ctx, db.SetSessionNotifyParams{
				Notify: notify, UpdatedAt: 12, AgentID: "a1", SessionKey: key, RoomID: room,
			})
			return err
		}))
		return n
	}

	require.Equal(t, int64(1), set("label:claude-2", "r1", "on"))
	require.Equal(t, dbSessionRoomState{readSeq: 5, notify: "on"}, e.sessionState("label:claude-2", "r1"))
	require.Equal(t, "on", e.sessionState("harness-principal", "r1").notify, "the sibling's setting is its own")
	require.Equal(t, int64(0), set("harness-principal", "r2", "mentions"), "a session with no state row there is not created by the setting")
	require.Equal(t, int64(0), set("label:wt", "r1", "on"))
}

// SN-05, SN-06 (data): the unread count of a session in each room counts the messages after its read position
// that it did not write itself: a user's and a sibling session's count, its own and a deleted one do not. A
// room with nothing unread has no row.
func TestCountSessionUnreadByRoom(t *testing.T) {
	e := sessionRoomFixture(t)
	ctx := context.Background()
	sessionsMessage(e, "m1", "r1", 1, "user", "u1")
	sessionsMessage(e, "m2", "r1", 2, "agent", "a1") // the session itself
	e.must(`UPDATE messages SET author_label = 'claude-2' WHERE id = 'm2'`)
	sessionsMessage(e, "m3", "r1", 3, "agent", "a1") // a sibling session
	e.must(`UPDATE messages SET author_label = 'wt' WHERE id = 'm3'`)
	sessionsMessage(e, "m4", "r1", 4, "user", "u1")
	e.must(`UPDATE messages SET deleted_at = 1790000000004 WHERE id = 'm4'`)
	sessionsMessage(e, "m5", "r1", 5, "user", "u1")
	sessionsMessage(e, "m6", "r1", 6, "agent", "a1") // the principal (no label) wrote it
	sessionsMessage(e, "m7", "r2", 1, "user", "u1")
	sessionsMessage(e, "m8", "r2", 2, "agent", "a1") // the session itself, in the other room

	e.createState("harness-principal", "r1", 2, "on")
	e.createState("harness-principal", "r2", 0, "on")
	counts, err := e.s.R().CountSessionUnreadByRoom(ctx, db.CountSessionUnreadByRoomParams{
		AgentID: "a1", SessionKey: "harness-principal", AuthorLabel: "",
	})
	require.NoError(t, err)
	require.Equal(t, []db.CountSessionUnreadByRoomRow{{RoomID: "r1", Unread: 2}, {RoomID: "r2", Unread: 1}}, counts,
		"a sibling's message counts and the session's own (or a deleted one) does not")

	// The sibling session, with its own read position: it did not write m3, and the principal's m6 counts.
	e.createState("label:claude-2", "r1", 2, "mentions")
	counts, err = e.s.R().CountSessionUnreadByRoom(ctx, db.CountSessionUnreadByRoomParams{
		AgentID: "a1", SessionKey: "label:claude-2", AuthorLabel: "claude-2",
	})
	require.NoError(t, err)
	require.Equal(t, []db.CountSessionUnreadByRoomRow{{RoomID: "r1", Unread: 3}, {RoomID: "r2", Unread: 2}}, counts)

	// A session that read everything has no row.
	e.createState("label:up-to-date", "r1", 6, "mentions")
	counts, err = e.s.R().CountSessionUnreadByRoom(ctx, db.CountSessionUnreadByRoomParams{
		AgentID: "a1", SessionKey: "label:up-to-date", AuthorLabel: "up-to-date",
	})
	require.NoError(t, err)
	require.Equal(t, []db.CountSessionUnreadByRoomRow{{RoomID: "r2", Unread: 2}}, counts)

	// The rooms of the AI are the ones it is a member of: another room's messages never count.
	other := newSchemaEnv(t)
	sessionsFixture(other)
	other.must(`INSERT INTO rooms (id, owner_id, name, kind, created_at, updated_at) VALUES ('r3', 'u1', 'other', 'group', 1, 1)`)
	sessionsMessage(other, "m9", "r3", 1, "user", "u1")
	counts, err = other.s.R().CountSessionUnreadByRoom(ctx, db.CountSessionUnreadByRoomParams{
		AgentID: "a1", SessionKey: "harness-principal", AuthorLabel: "",
	})
	require.NoError(t, err)
	require.Empty(t, counts)
}

// SN-03 (data): the catch-up of a session keeps the newest messages of the cap and returns them oldest first,
// after its read position, without its own messages and without deleted ones.
func TestListSessionUnreadKeepsTheNewestAndReturnsThemOldestFirst(t *testing.T) {
	e := sessionRoomFixture(t)
	ctx := context.Background()
	for i := 1; i <= 60; i++ {
		sessionsMessage(e, fmt.Sprintf("m%02d", i), "r1", int64(i), "user", "u1")
	}
	sessionsMessage(e, "own", "r1", 61, "agent", "a1")
	e.must(`UPDATE messages SET author_label = 'claude-2' WHERE id = 'own'`)

	list := func(after, limit int64, label string) []string {
		t.Helper()
		rows, err := e.s.R().ListSessionUnread(ctx, db.ListSessionUnreadParams{
			RoomID: "r1", AfterSeq: after, AgentID: "a1", AuthorLabel: label, Limit: limit,
		})
		require.NoError(t, err)
		return messageIDs(rows)
	}

	require.Equal(t, []string{"m56", "m57", "m58", "m59", "m60"}, list(5, 5, "claude-2"),
		"after the read position, oldest first")
	require.Equal(t, 10, len(list(0, 10, "claude-2")), "the cap")
	newest := list(0, 50, "claude-2")
	require.Len(t, newest, 50)
	require.Equal(t, "m11", newest[0], "the cap keeps the newest messages")
	require.Equal(t, "m60", newest[len(newest)-1])
	require.Equal(t, []string{"own"}, list(60, 10, "wt"), "another session reads what this one wrote")
	require.Len(t, list(0, 10, "wt"), 10, "a session that wrote nothing reads the newest ten")

	e.must(`UPDATE messages SET deleted_at = 1790000000060 WHERE id = 'm60'`)
	newest = list(0, 3, "claude-2")
	require.Equal(t, []string{"m57", "m58", "m59"}, newest, "a deleted message is not part of the batch")
}
