package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
)

// sessionRoomFixture is an AI of alice, with its profile, and two groups it is a member of: the caller adds
// the messages and the state rows.
func sessionRoomFixture(t *testing.T) *schemaEnv {
	t.Helper()
	e := newSchemaEnv(t)
	sessionsFixture(e)
	sessionsRoom(e, "r1")
	sessionsRoom(e, "r2")
	return e
}

// sessionState reads the state row of a session in a room.
func (e *schemaEnv) sessionState(sessionKey, roomID string) dbSessionRoomState {
	e.t.Helper()
	var state dbSessionRoomState
	require.NoError(e.t, e.s.write.QueryRowContext(context.Background(),
		`SELECT read_seq, notify FROM session_room_state WHERE agent_id = 'a1' AND session_key = ? AND room_id = ?`,
		sessionKey, roomID).Scan(&state.readSeq, &state.notify))
	return state
}

// dbSessionRoomState is what a state row holds, for the assertions.
type dbSessionRoomState struct {
	readSeq int64
	notify  string
}

// createState creates a state row the way EnsureRoomState does.
func (e *schemaEnv) createState(sessionKey, roomID string, readSeq int64, notify string) {
	e.t.Helper()
	require.NoError(e.t, e.s.WithTx(context.Background(), func(q *db.Queries) error {
		return q.CreateSessionRoomState(context.Background(), db.CreateSessionRoomStateParams{
			AgentID: "a1", SessionKey: sessionKey, RoomID: roomID, ReadSeq: readSeq, Notify: notify, UpdatedAt: 10,
		})
	}))
}

// advanceState moves a read position the way the gateway and the tools do.
func (e *schemaEnv) advanceState(sessionKey, roomID string, readSeq int64, notify string) {
	e.t.Helper()
	require.NoError(e.t, e.s.WithTx(context.Background(), func(q *db.Queries) error {
		return q.AdvanceSessionReadSeq(context.Background(), db.AdvanceSessionReadSeqParams{
			AgentID: "a1", SessionKey: sessionKey, RoomID: roomID, ReadSeq: readSeq, Notify: notify, UpdatedAt: 11,
		})
	}))
}

// SN-01, SN-02 (data): migration 0031 creates session_room_state with the design's columns, refuses a setting
// outside on/mentions and an empty session key, and takes the rows of an AI or a room with it. A database
// that existed before it gets the table, and a fresh one has it.
func TestMigration0031CreatesSessionRoomState(t *testing.T) {
	ctx := context.Background()
	e := &schemaEnv{t: t, s: openMigratedBefore(t, "0031")}
	require.Empty(t, e.columnsOf("session_room_state"), "fixture sanity: the table does not exist before 0031")

	sessionsFixture(e)
	sessionsRoom(e, "r1")
	require.NoError(t, e.s.applyMigration(ctx, "0031_session_room_state.sql"))
	require.Contains(t, versions(t, e.s), "0031_session_room_state")

	require.Equal(t, []string{"agent_id", "session_key", "room_id", "read_seq", "notify", "updated_at"},
		e.columnsOf("session_room_state"), "a row is exactly the session's position and setting")
	require.Contains(t, e.indexes("session_room_state"), "session_room_state_session")

	e.must(`INSERT INTO session_room_state (agent_id, session_key, room_id, read_seq, notify, updated_at)
		VALUES ('a1', 'harness-session', 'r1', 7, 'on', 1)`)
	require.Equal(t, dbSessionRoomState{readSeq: 7, notify: "on"}, e.sessionState("harness-session", "r1"))

	err := e.exec(`INSERT INTO session_room_state (agent_id, session_key, room_id, read_seq, notify, updated_at)
		VALUES ('a1', 'other', 'r1', 0, 'all', 1)`)
	require.Error(t, err, "notify has only two values")
	require.Contains(t, err.Error(), "CHECK")
	require.Error(t, e.exec(`INSERT INTO session_room_state (agent_id, session_key, room_id, read_seq, notify, updated_at)
		VALUES ('a1', '', 'r1', 0, 'on', 1)`), "the session key is never empty")
	require.Error(t, e.exec(`INSERT INTO session_room_state (agent_id, session_key, room_id, read_seq, notify, updated_at)
		VALUES ('missing', 'harness-session', 'r1', 0, 'on', 1)`), "a row needs its AI")
	require.Error(t, e.exec(`INSERT INTO session_room_state (agent_id, session_key, room_id, read_seq, notify, updated_at)
		VALUES ('a1', 'harness-session', 'missing', 0, 'on', 1)`), "a row needs its room")
	require.Zero(t, e.count(`SELECT COUNT(*) FROM pragma_foreign_key_check`), "no foreign key is dangling")

	// One row per session and room: a second insert of the same triple collides.
	require.Error(t, e.exec(`INSERT INTO session_room_state (agent_id, session_key, room_id, read_seq, notify, updated_at)
		VALUES ('a1', 'harness-session', 'r1', 0, 'mentions', 2)`))

	e.must(`INSERT INTO session_room_state (agent_id, session_key, room_id, read_seq, notify, updated_at)
		VALUES ('a1', 'label:wt', 'r1', 0, 'mentions', 1)`)
	e.must(`DELETE FROM agents WHERE id = 'a1'`)
	require.Zero(t, e.count(`SELECT COUNT(*) FROM session_room_state`), "the rows go with their AI")

	e.must(`INSERT INTO agents (id, owner_id, name, created_at) VALUES ('a1', 'u1', 'atlas', 1)`)
	e.must(`INSERT INTO session_room_state (agent_id, session_key, room_id, read_seq, notify, updated_at)
		VALUES ('a1', 'harness-session', 'r1', 7, 'on', 1)`)
	e.must(`DELETE FROM rooms WHERE id = 'r1'`)
	require.Zero(t, e.count(`SELECT COUNT(*) FROM session_room_state`), "the rows go with their room")

	// A fresh database has the same table and index, and the column 0040 adds (SN-11).
	fresh := newSchemaEnv(t)
	require.Equal(t, []string{"agent_id", "session_key", "room_id", "read_seq", "notify", "updated_at", "read_from"},
		fresh.columnsOf("session_room_state"))
	require.Contains(t, fresh.indexes("session_room_state"), "session_room_state_session")
}

// SN-11 (data): migration 0040 adds read_from, where what a state row has read starts. A row that existed
// before it gets read_from = read_seq, so nothing it read before is taken as read by another session; a row
// created afterwards by a reading starts at 0, and one created at the session's join point starts there.
func TestMigration0040AddsSessionReadFrom(t *testing.T) {
	ctx := context.Background()
	e := &schemaEnv{t: t, s: openMigratedBefore(t, "0040")}
	sessionsFixture(e)
	sessionsRoom(e, "r1")
	e.must(`INSERT INTO session_room_state (agent_id, session_key, room_id, read_seq, notify, updated_at)
		VALUES ('a1', 'old-session', 'r1', 7, 'on', 1)`)
	require.NoError(t, e.s.applyMigration(ctx, "0040_session_read_from.sql"))
	require.Contains(t, versions(t, e.s), "0040_session_read_from")
	readFrom := func(key string) int {
		return e.count(`SELECT read_from FROM session_room_state WHERE agent_id = 'a1' AND session_key = ? AND room_id = 'r1'`, key)
	}
	require.Equal(t, 7, readFrom("old-session"), "what an existing row read is not known")

	e.advanceState("reader", "r1", 5, "mentions")
	require.Equal(t, 0, readFrom("reader"), "a row created by a reading has read the room up to it")
	e.createState("joiner", "r1", 9, "mentions")
	require.Equal(t, 9, readFrom("joiner"), "a row created at the join point has read nothing before it")
	e.advanceState("joiner", "r1", 12, "mentions")
	require.Equal(t, 9, readFrom("joiner"), "a reading keeps where the row's reading starts")
}
