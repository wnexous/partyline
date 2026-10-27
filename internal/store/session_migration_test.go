package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
)

// sessionsFixture inserts the owner, the AI with its profile and the two rooms every delivery test needs.
// The caller adds the messages, the members and the delivery rows.
func sessionsFixture(e *schemaEnv) {
	e.t.Helper()
	e.user("u1", "alice")
	e.must(`INSERT INTO agents (id, owner_id, name, created_at) VALUES ('a1', 'u1', 'atlas', 1)`)
	require.NoError(e.t, e.intelligence("a1", "ai", "atlas"))
}

// sessionsRoom inserts a group of the fixture's owner with the fixture's AI as a member.
func sessionsRoom(e *schemaEnv, id string) {
	e.t.Helper()
	e.must(`INSERT INTO rooms (id, owner_id, name, kind, created_at, updated_at) VALUES (?, 'u1', ?, 'group', 1, 1)`, id, id)
	e.must(`INSERT INTO room_members (id, room_id, kind, member_id, created_at) VALUES (?, ?, 'agent', 'a1', 1)`, "member-"+id, id)
}

// sessionsMessage inserts a message authored by the given kind and id, which is also its author_iid so the
// view's joins resolve the name.
func sessionsMessage(e *schemaEnv, id, room string, seq int64, kind, authorID string) {
	e.t.Helper()
	e.must(`INSERT INTO messages (id, room_id, seq, author_kind, author_id, author_iid, kind, text, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 'text', 'hello', ?)`, id, room, seq, kind, authorID, authorID, 1790000000000+seq)
}

// authorLabelOfMessage reads the author_label column the migration adds.
func (e *schemaEnv) authorLabelOfMessage(id string) string {
	e.t.Helper()
	var label string
	require.NoError(e.t, e.s.write.QueryRowContext(context.Background(),
		`SELECT author_label FROM messages WHERE id = ?`, id).Scan(&label))
	return label
}

// authorLabelOfView reads author_label from message_views.
func (e *schemaEnv) authorLabelOfView(id string) string {
	e.t.Helper()
	var label string
	require.NoError(e.t, e.s.write.QueryRowContext(context.Background(),
		`SELECT author_label FROM message_views WHERE id = ?`, id).Scan(&label))
	return label
}

// deliveryRow is what one delivery row holds.
type deliveryRow struct {
	label      string
	createdAt  int64
	fellBackAt sql.NullInt64
}

func (e *schemaEnv) deliveryRow(messageID, agentID string) deliveryRow {
	e.t.Helper()
	var r deliveryRow
	require.NoError(e.t, e.s.write.QueryRowContext(context.Background(),
		`SELECT label, created_at, fell_back_at FROM agent_session_deliveries WHERE message_id = ? AND agent_id = ?`,
		messageID, agentID).Scan(&r.label, &r.createdAt, &r.fellBackAt))
	return r
}

func (e *schemaEnv) deliveryCount(messageID, agentID string) int {
	e.t.Helper()
	return e.count(`SELECT COUNT(*) FROM agent_session_deliveries WHERE message_id = ? AND agent_id = ?`, messageID, agentID)
}

// createDelivery runs CreateSessionDelivery in a write transaction; fellBackAt is invalid for a live
// label.
func (e *schemaEnv) createDelivery(messageID, agentID, label string, fellBackAt sql.NullInt64) error {
	e.t.Helper()
	return e.s.WithTx(context.Background(), func(q *db.Queries) error {
		return q.CreateSessionDelivery(context.Background(), db.CreateSessionDeliveryParams{
			MessageID: messageID, AgentID: agentID, Label: label, CreatedAt: 10, FellBackAt: fellBackAt,
		})
	})
}

func (e *schemaEnv) ackDelivery(messageID, agentID string) int64 {
	e.t.Helper()
	var n int64
	require.NoError(e.t, e.s.WithTx(context.Background(), func(q *db.Queries) error {
		var err error
		n, err = q.AckSessionDelivery(context.Background(), db.AckSessionDeliveryParams{MessageID: messageID, AgentID: agentID})
		return err
	}))
	return n
}

func (e *schemaEnv) deleteDelivery(messageID, agentID string) error {
	e.t.Helper()
	return e.s.WithTx(context.Background(), func(q *db.Queries) error {
		return q.DeleteSessionDelivery(context.Background(), db.DeleteSessionDeliveryParams{MessageID: messageID, AgentID: agentID})
	})
}

func (e *schemaEnv) fallBack(agentID, label string, at int64) []string {
	e.t.Helper()
	var ids []string
	require.NoError(e.t, e.s.WithTx(context.Background(), func(q *db.Queries) error {
		var err error
		ids, err = q.FallBackSessionDeliveries(context.Background(), db.FallBackSessionDeliveriesParams{
			FellBackAt: sql.NullInt64{Int64: at, Valid: true}, AgentID: agentID, Label: label,
		})
		return err
	}))
	return ids
}

func (e *schemaEnv) deleteSelfDeliveries(agentID, label string) int64 {
	e.t.Helper()
	var n int64
	require.NoError(e.t, e.s.WithTx(context.Background(), func(q *db.Queries) error {
		var err error
		n, err = q.DeleteSelfSessionDeliveries(context.Background(), db.DeleteSelfSessionDeliveriesParams{AgentID: agentID, Label: label})
		return err
	}))
	return n
}

func (e *schemaEnv) listFallbacks(agentID, roomID string) []db.MessageView {
	e.t.Helper()
	rows, err := e.s.R().ListSessionFallbacks(context.Background(), db.ListSessionFallbacksParams{AgentID: agentID, RoomID: roomID})
	require.NoError(e.t, err)
	return rows
}

// messageIDs lists the ids of view rows in their order.
func messageIDs(rows []db.MessageView) []string {
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return ids
}

// SESS-24, SESS-28, SESS-30, SESS-37, SESS-38 (data): migration 0027 adds the author's session to the
// messages and the per-session delivery row to a database that existed before it and to a fresh one. A
// message stored before it was written by no session, the view exposes the label, the label CHECK refuses
// the empty string, and a delivery row goes with its message and with its AI.
func TestMigration0027AddsTheSessionLabelAndTheDeliveryRows(t *testing.T) {
	ctx := context.Background()
	e := &schemaEnv{t: t, s: openMigratedBefore(t, "0027")}
	require.NotContains(t, e.columnsOf("messages"), "author_label", "fixture sanity: schema before 0027")
	require.Empty(t, e.columnsOf("agent_session_deliveries"), "fixture sanity: the table does not exist before 0027")

	sessionsFixture(e)
	sessionsRoom(e, "r1")
	sessionsMessage(e, "m-old", "r1", 1, "agent", "a1")

	require.NoError(t, e.s.applyMigration(ctx, "0027_ai_sessions.sql"))
	require.Contains(t, versions(t, e.s), "0027_ai_sessions")

	var typ string
	var notNull int
	var dflt string
	require.NoError(t, e.s.write.QueryRowContext(ctx,
		`SELECT type, "notnull", dflt_value FROM pragma_table_info('messages') WHERE name = 'author_label'`).
		Scan(&typ, &notNull, &dflt))
	require.Equal(t, "TEXT", typ, "author_label is TEXT")
	require.Equal(t, 1, notNull, "author_label is NOT NULL")
	require.Equal(t, "''", dflt, "it defaults to the empty string")

	require.Equal(t, "", e.authorLabelOfMessage("m-old"), "a message stored before 0027 was written by no session")
	require.Equal(t, "", e.authorLabelOfView("m-old"), "and the view says so")

	// A statement that does not know the column (a CLI of an older build) still inserts; a session's post
	// stores its label and the view exposes it.
	e.must(`INSERT INTO messages (id, room_id, seq, author_kind, author_id, author_iid, kind, text, created_at)
		VALUES ('m-plain', 'r1', 2, 'agent', 'a1', 'a1', 'text', 'hi', 1790000000002)`)
	require.Equal(t, "", e.authorLabelOfView("m-plain"))
	e.must(`INSERT INTO messages (id, room_id, seq, author_kind, author_id, author_iid, kind, text, author_label, created_at)
		VALUES ('m-labelled', 'r1', 3, 'agent', 'a1', 'a1', 'text', 'hello', 'claude-2', 1790000000003)`)
	require.Equal(t, "claude-2", e.authorLabelOfView("m-labelled"))

	require.Equal(t, []string{"message_id", "agent_id", "label", "created_at", "fell_back_at"},
		e.columnsOf("agent_session_deliveries"), "a row is exactly the delivery state")
	require.Contains(t, e.indexes("agent_session_deliveries"), "agent_session_deliveries_pending")
	require.Contains(t, e.indexes("agent_session_deliveries"), "agent_session_deliveries_fallback")

	_, err := e.s.write.ExecContext(ctx,
		`INSERT INTO agent_session_deliveries (message_id, agent_id, label, created_at) VALUES ('m-old', 'a1', '', 10)`)
	require.Error(t, err, "the label CHECK refuses the empty string")

	e.must(`INSERT INTO agent_session_deliveries (message_id, agent_id, label, created_at, fell_back_at)
		VALUES ('m-old', 'a1', 'claude-2', 10, NULL), ('m-labelled', 'a1', 'claude-2', 11, NULL)`)
	require.Zero(t, e.count(`SELECT COUNT(*) FROM pragma_foreign_key_check`), "no foreign key is dangling")

	e.must(`DELETE FROM messages WHERE id = 'm-old'`)
	require.Zero(t, e.deliveryCount("m-old", "a1"), "the row goes with its message")
	require.Equal(t, 1, e.deliveryCount("m-labelled", "a1"))
	e.must(`DELETE FROM agents WHERE id = 'a1'`)
	require.Zero(t, e.deliveryCount("m-labelled", "a1"), "the row goes with its AI")

	// A fresh database gets the same column, table and indexes.
	fresh := newSchemaEnv(t)
	require.Contains(t, fresh.columnsOf("messages"), "author_label")
	require.Equal(t, []string{"message_id", "agent_id", "label", "created_at", "fell_back_at"},
		fresh.columnsOf("agent_session_deliveries"))
	require.Contains(t, fresh.indexes("agent_session_deliveries"), "agent_session_deliveries_pending")
	require.Contains(t, fresh.indexes("agent_session_deliveries"), "agent_session_deliveries_fallback")
}

// SESS-24 (data): a retried push cannot duplicate a row, the first owner wins, only a pending row is an
// acknowledgement and only a pending row falls back: a late acknowledgement of a fallen-back message is
// ignored instead of consuming it twice.
func TestSessionDeliveryIsIdempotentAndOwnedOnce(t *testing.T) {
	ctx := context.Background()
	e := newSchemaEnv(t)
	sessionsFixture(e)
	sessionsRoom(e, "r1")
	sessionsMessage(e, "m1", "r1", 1, "user", "u1")
	e.must(`INSERT INTO agent_cursors (agent_id, room_id, seq) VALUES ('a1', 'r1', 0)`)

	require.NoError(t, e.createDelivery("m1", "a1", "claude-2", sql.NullInt64{}))
	require.NoError(t, e.createDelivery("m1", "a1", "claude-2", sql.NullInt64{Int64: 20, Valid: true}))
	require.Equal(t, 1, e.deliveryCount("m1", "a1"), "a retried push does not duplicate the row")
	row := e.deliveryRow("m1", "a1")
	require.Equal(t, "claude-2", row.label)
	require.False(t, row.fellBackAt.Valid, "the first owner wins")

	require.Equal(t, int64(1), e.ackDelivery("m1", "a1"), "a live label's acknowledgement deletes its pending row")
	require.Zero(t, e.deliveryCount("m1", "a1"))
	cursor, err := e.s.R().GetAgentCursor(ctx, db.GetAgentCursorParams{AgentID: "a1", RoomID: "r1"})
	require.NoError(t, err)
	require.Zero(t, cursor, "the AI's cursor does not move: the principal never saw the message")

	require.NoError(t, e.createDelivery("m1", "a1", "claude-2", sql.NullInt64{}))
	require.Equal(t, []string{"m1"}, e.fallBack("a1", "claude-2", 100))
	row = e.deliveryRow("m1", "a1")
	require.True(t, row.fellBackAt.Valid)
	require.Equal(t, int64(100), row.fellBackAt.Int64, "the end of the session marks the row for the principal")
	require.Empty(t, e.fallBack("a1", "claude-2", 120), "a row that already fell back is not taken twice")
	require.Equal(t, int64(0), e.ackDelivery("m1", "a1"), "a late labelled acknowledgement is ignored")
	require.Equal(t, 1, e.deliveryCount("m1", "a1"))

	sessionsMessage(e, "m2", "r1", 2, "user", "u1")
	require.NoError(t, e.createDelivery("m2", "a1", "claude-2", sql.NullInt64{Int64: 50, Valid: true}))
	row = e.deliveryRow("m2", "a1")
	require.True(t, row.fellBackAt.Valid)
	require.Equal(t, int64(50), row.fellBackAt.Int64, "a label with no session is the principal's from the start")
	require.NoError(t, e.deleteDelivery("m2", "a1"))
	require.Zero(t, e.deliveryCount("m2", "a1"), "the principal's consumption deletes the row")
}

// SESS-38 (data): a message the AI wrote is never handed back to it. The end-of-session sweep deletes its
// pending row instead of marking it for the principal, while a user's message of the same label still
// falls back and a row of another label is untouched.
func TestAnAIMsOwnMessageNeverFallsBack(t *testing.T) {
	e := newSchemaEnv(t)
	sessionsFixture(e)
	sessionsRoom(e, "r1")
	sessionsMessage(e, "m-self", "r1", 1, "agent", "a1")
	e.must(`UPDATE messages SET author_label = 'claude-2' WHERE id = 'm-self'`)
	require.NoError(t, e.createDelivery("m-self", "a1", "claude-2", sql.NullInt64{}))

	require.Empty(t, e.fallBack("a1", "claude-2", 100), "the AI is never handed its own message")
	require.False(t, e.deliveryRow("m-self", "a1").fellBackAt.Valid, "the row stays pending until the session ends")
	require.Equal(t, int64(1), e.deleteSelfDeliveries("a1", "claude-2"), "the end of the session drops the row instead")
	require.Equal(t, int64(0), e.deleteSelfDeliveries("a1", "claude-2"))

	sessionsMessage(e, "m-user", "r1", 2, "user", "u1")
	require.NoError(t, e.createDelivery("m-user", "a1", "claude-2", sql.NullInt64{}))
	sessionsMessage(e, "m-self-wt", "r1", 3, "agent", "a1")
	require.NoError(t, e.createDelivery("m-self-wt", "a1", "wt", sql.NullInt64{}))

	require.Equal(t, []string{"m-user"}, e.fallBack("a1", "claude-2", 100), "a user's message of the same label still falls back")
	require.Equal(t, int64(0), e.deleteSelfDeliveries("a1", "claude-2"), "the sweep only drops the AI's own pending rows")
	require.Equal(t, 1, e.deliveryCount("m-self-wt", "a1"), "a row of another label is untouched")
	require.False(t, e.deliveryRow("m-self-wt", "a1").fellBackAt.Valid)
}

// SESS-24 (data): the fallback list is per AI and per room, a message with any delivery row is not read
// by the principal's backlog or pending list, and the three queue reads exclude every delivery row.
func TestFallbacksArePerRoomAndOrdered(t *testing.T) {
	ctx := context.Background()
	e := newSchemaEnv(t)
	sessionsFixture(e)
	sessionsRoom(e, "r1")
	sessionsRoom(e, "r2")
	sessionsMessage(e, "m-fb", "r1", 1, "user", "u1")
	sessionsMessage(e, "m-new", "r1", 2, "user", "u1")
	sessionsMessage(e, "m-pending", "r1", 3, "user", "u1")
	sessionsMessage(e, "m-fb-other-room", "r2", 1, "user", "u1")
	require.NoError(t, e.createDelivery("m-fb", "a1", "wt", sql.NullInt64{Int64: 100, Valid: true}))
	require.NoError(t, e.createDelivery("m-fb-other-room", "a1", "wt", sql.NullInt64{Int64: 100, Valid: true}))
	require.NoError(t, e.createDelivery("m-pending", "a1", "wt", sql.NullInt64{}))

	require.Equal(t, []string{"m-fb"}, messageIDs(e.listFallbacks("a1", "r1")), "a fallback is read in its room only")
	require.Equal(t, []string{"m-fb-other-room"}, messageIDs(e.listFallbacks("a1", "r2")))

	backlog, err := e.s.R().ListAgentBacklog(ctx, db.ListAgentBacklogParams{RoomID: "r1", AfterSeq: 0, AgentID: "a1", Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{"m-new"}, messageIDs(backlog), "only the message without a delivery row is the principal's queue")

	n, err := e.s.R().CountAgentBacklog(ctx, db.CountAgentBacklogParams{RoomID: "r1", AfterSeq: 0, AgentID: "a1"})
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	pending, err := e.s.R().ListAgentPending(ctx, db.ListAgentPendingParams{RoomID: "r1", AfterSeq: 0, AgentID: "a1", Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{"m-new"}, messageIDs(pending), "a pending row of a live label is not the principal's")
}
