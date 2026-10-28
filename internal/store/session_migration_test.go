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
	ownerLive  string
	createdAt  int64
	fellBackAt sql.NullInt64
	ackedAt    sql.NullInt64
}

func (e *schemaEnv) deliveryRow(messageID, agentID string) deliveryRow {
	e.t.Helper()
	var r deliveryRow
	require.NoError(e.t, e.s.write.QueryRowContext(context.Background(),
		`SELECT label, owner_live, created_at, fell_back_at, acked_at FROM agent_session_deliveries WHERE message_id = ? AND agent_id = ?`,
		messageID, agentID).Scan(&r.label, &r.ownerLive, &r.createdAt, &r.fellBackAt, &r.ackedAt))
	return r
}

func (e *schemaEnv) deliveryCount(messageID, agentID string) int {
	e.t.Helper()
	return e.count(`SELECT COUNT(*) FROM agent_session_deliveries WHERE message_id = ? AND agent_id = ?`, messageID, agentID)
}

// createDelivery runs CreateSessionDelivery in a write transaction; fellBackAt is invalid for a row
// pushed to a live connection (ownerLive) and set for a row born fallen back (ownerLive "").
func (e *schemaEnv) createDelivery(messageID, agentID, label, ownerLive string, fellBackAt sql.NullInt64) error {
	e.t.Helper()
	return e.s.WithTx(context.Background(), func(q *db.Queries) error {
		return q.CreateSessionDelivery(context.Background(), db.CreateSessionDeliveryParams{
			MessageID: messageID, AgentID: agentID, Label: label, OwnerLive: ownerLive, CreatedAt: 10, FellBackAt: fellBackAt,
		})
	})
}

func (e *schemaEnv) ackDelivery(messageID, agentID, ownerLive string, at int64) int64 {
	e.t.Helper()
	var n int64
	require.NoError(e.t, e.s.WithTx(context.Background(), func(q *db.Queries) error {
		var err error
		n, err = q.AckSessionDelivery(context.Background(), db.AckSessionDeliveryParams{
			AckedAt: sql.NullInt64{Int64: at, Valid: true}, MessageID: messageID, AgentID: agentID, OwnerLive: ownerLive,
		})
		return err
	}))
	return n
}

func (e *schemaEnv) ackFallback(messageID, agentID string, at int64) int64 {
	e.t.Helper()
	var n int64
	require.NoError(e.t, e.s.WithTx(context.Background(), func(q *db.Queries) error {
		var err error
		n, err = q.AckFallbackDelivery(context.Background(), db.AckFallbackDeliveryParams{
			AckedAt: sql.NullInt64{Int64: at, Valid: true}, MessageID: messageID, AgentID: agentID,
		})
		return err
	}))
	return n
}

// fallBack ends the connection ownerLive: the ids of the rows that fell back to the principal. The query
// also returns the AI's own pending rows, which it acknowledges instead and which have no fell_back_at;
// they are not part of the result here, as the gateway ignores them.
func (e *schemaEnv) fallBack(agentID, ownerLive string, at int64) []string {
	e.t.Helper()
	var ids []string
	require.NoError(e.t, e.s.WithTx(context.Background(), func(q *db.Queries) error {
		rows, err := q.FallBackOwnedDeliveries(context.Background(), db.FallBackOwnedDeliveriesParams{
			AgentID: agentID, OwnerLive: ownerLive, Now: sql.NullInt64{Int64: at, Valid: true},
		})
		for _, r := range rows {
			if r.FellBackAt.Valid {
				ids = append(ids, r.MessageID)
			}
		}
		return err
	}))
	return ids
}

func (e *schemaEnv) listFallbacks(agentID, roomID string) []db.MessageView {
	e.t.Helper()
	rows, err := e.s.R().ListSessionFallbacks(context.Background(), db.ListSessionFallbacksParams{AgentID: agentID, RoomID: roomID})
	require.NoError(e.t, err)
	return rows
}

func (e *schemaEnv) ownedPending(agentID, ownerLive string) []string {
	e.t.Helper()
	rows, err := e.s.R().ListOwnedPending(context.Background(), db.ListOwnedPendingParams{AgentID: agentID, OwnerLive: ownerLive})
	require.NoError(e.t, err)
	return messageIDs(rows)
}

func (e *schemaEnv) backlog(agentID, roomID string) []string {
	e.t.Helper()
	rows, err := e.s.R().ListAgentBacklog(context.Background(), db.ListAgentBacklogParams{RoomID: roomID, AfterSeq: 0, AgentID: agentID, Limit: 10})
	require.NoError(e.t, err)
	return messageIDs(rows)
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

	require.Equal(t, []string{"message_id", "agent_id", "label", "owner_live", "created_at", "fell_back_at", "acked_at"},
		e.columnsOf("agent_session_deliveries"), "a row is exactly the delivery state")
	require.Contains(t, e.indexes("agent_session_deliveries"), "agent_session_deliveries_pending")
	require.Contains(t, e.indexes("agent_session_deliveries"), "agent_session_deliveries_fallback")

	_, err := e.s.write.ExecContext(ctx,
		`INSERT INTO agent_session_deliveries (message_id, agent_id, label, created_at) VALUES ('m-old', 'a1', '', 10)`)
	require.Error(t, err, "the label CHECK refuses the empty string")

	e.must(`INSERT INTO agent_session_deliveries (message_id, agent_id, label, created_at, fell_back_at)
		VALUES ('m-old', 'a1', 'claude-2', 10, NULL), ('m-labelled', 'a1', 'claude-2', 11, NULL)`)
	require.Equal(t, "", e.deliveryRow("m-old", "a1").ownerLive, "a row written without an owner is owned by no connection")
	require.False(t, e.deliveryRow("m-old", "a1").ackedAt.Valid)
	require.Zero(t, e.count(`SELECT COUNT(*) FROM pragma_foreign_key_check`), "no foreign key is dangling")

	e.must(`DELETE FROM messages WHERE id = 'm-old'`)
	require.Zero(t, e.deliveryCount("m-old", "a1"), "the row goes with its message")
	require.Equal(t, 1, e.deliveryCount("m-labelled", "a1"))
	e.must(`DELETE FROM agents WHERE id = 'a1'`)
	require.Zero(t, e.deliveryCount("m-labelled", "a1"), "the row goes with its AI")

	// A fresh database gets the same column, table and indexes.
	fresh := newSchemaEnv(t)
	require.Contains(t, fresh.columnsOf("messages"), "author_label")
	require.Equal(t, []string{"message_id", "agent_id", "label", "owner_live", "created_at", "fell_back_at", "acked_at"},
		fresh.columnsOf("agent_session_deliveries"))
	require.Contains(t, fresh.indexes("agent_session_deliveries"), "agent_session_deliveries_pending")
	require.Contains(t, fresh.indexes("agent_session_deliveries"), "agent_session_deliveries_fallback")
}

// SESS-24 (data): a retried push cannot duplicate a row, the first owner wins, an acknowledgement is a
// tombstone and not a delete, and the principal's queue keeps excluding the acknowledged message.
func TestSessionDeliveryIsIdempotentAndTombstoned(t *testing.T) {
	ctx := context.Background()
	e := newSchemaEnv(t)
	sessionsFixture(e)
	sessionsRoom(e, "r1")
	sessionsMessage(e, "m1", "r1", 1, "user", "u1")
	e.must(`INSERT INTO agent_cursors (agent_id, room_id, seq) VALUES ('a1', 'r1', 0)`)

	require.NoError(t, e.createDelivery("m1", "a1", "claude-2", "live-L", sql.NullInt64{}))
	require.NoError(t, e.createDelivery("m1", "a1", "claude-2", "live-L2", sql.NullInt64{Int64: 20, Valid: true}))
	require.Equal(t, 1, e.deliveryCount("m1", "a1"), "a retried push does not duplicate the row")
	row := e.deliveryRow("m1", "a1")
	require.Equal(t, "claude-2", row.label)
	require.Equal(t, "live-L", row.ownerLive, "the first owner wins")
	require.False(t, row.fellBackAt.Valid)
	require.False(t, row.ackedAt.Valid)
	require.Empty(t, e.backlog("a1", "r1"), "a pending row hides the message from the principal's queue")

	require.Equal(t, int64(1), e.ackDelivery("m1", "a1", "live-L", 50), "the owner's acknowledgement sets acked_at")
	require.Equal(t, 1, e.deliveryCount("m1", "a1"), "the row stays as a tombstone")
	row = e.deliveryRow("m1", "a1")
	require.True(t, row.ackedAt.Valid)
	require.Equal(t, int64(50), row.ackedAt.Int64)
	require.False(t, row.fellBackAt.Valid)
	require.Empty(t, e.backlog("a1", "r1"), "the acknowledged message does not come back to the principal")
	n, err := e.s.R().CountAgentBacklog(ctx, db.CountAgentBacklogParams{RoomID: "r1", AfterSeq: 0, AgentID: "a1"})
	require.NoError(t, err)
	require.Zero(t, n)
	cursor, err := e.s.R().GetAgentCursor(ctx, db.GetAgentCursorParams{AgentID: "a1", RoomID: "r1"})
	require.NoError(t, err)
	require.Zero(t, cursor, "the AI's cursor does not move: the principal never saw the message")

	require.Equal(t, int64(0), e.ackDelivery("m1", "a1", "live-L", 60), "a second acknowledgement changes nothing")
	require.Equal(t, int64(50), e.deliveryRow("m1", "a1").ackedAt.Int64)
	require.NoError(t, e.createDelivery("m1", "a1", "claude-2", "live-L3", sql.NullInt64{}))
	row = e.deliveryRow("m1", "a1")
	require.Equal(t, "live-L", row.ownerLive, "a push for an acknowledged message is a no-op")
	require.True(t, row.ackedAt.Valid)
	require.Empty(t, e.ownedPending("a1", "live-L3"))
}

// SESS-24 (data): only the owner connection acknowledges a pending row; another connection of the AI,
// even with the same label, acknowledges nothing.
func TestOnlyTheOwnerAcknowledgesAPendingRow(t *testing.T) {
	e := newSchemaEnv(t)
	sessionsFixture(e)
	sessionsRoom(e, "r1")
	sessionsMessage(e, "m1", "r1", 1, "user", "u1")
	require.NoError(t, e.createDelivery("m1", "a1", "claude-2", "live-L", sql.NullInt64{}))

	require.Equal(t, int64(0), e.ackDelivery("m1", "a1", "live-L2", 50), "another connection's acknowledgement is refused")
	require.Equal(t, int64(0), e.ackDelivery("m1", "a1", "", 50), "so is a connection with no live id")
	require.False(t, e.deliveryRow("m1", "a1").ackedAt.Valid, "the row is still pending")
	require.Equal(t, []string{"m1"}, e.ownedPending("a1", "live-L"))
	require.Empty(t, e.ownedPending("a1", "live-L2"))
}

// SESS-24 (data): the end of a connection hands the principal only the pending rows that connection
// owns; a row of a later connection that reused the label is untouched, a row that already fell back is
// not taken twice, and a late owner acknowledgement of a fallen-back row is refused.
func TestTheEndOfAConnectionFallsBackOnlyItsOwnRows(t *testing.T) {
	e := newSchemaEnv(t)
	sessionsFixture(e)
	sessionsRoom(e, "r1")
	for i, id := range []string{"m-old", "m-new", "m-acked", "m-born"} {
		sessionsMessage(e, id, "r1", int64(i+1), "user", "u1")
	}
	require.NoError(t, e.createDelivery("m-old", "a1", "claude-2", "live-L", sql.NullInt64{}))
	require.NoError(t, e.createDelivery("m-new", "a1", "claude-2", "live-L2", sql.NullInt64{}))
	require.NoError(t, e.createDelivery("m-acked", "a1", "claude-2", "live-L", sql.NullInt64{}))
	require.Equal(t, int64(1), e.ackDelivery("m-acked", "a1", "live-L", 5))
	require.NoError(t, e.createDelivery("m-born", "a1", "claude-2", "", sql.NullInt64{Int64: 7, Valid: true}))

	require.Equal(t, []string{"m-old"}, e.fallBack("a1", "live-L", 100), "only the ended connection's pending row falls back")
	row := e.deliveryRow("m-old", "a1")
	require.True(t, row.fellBackAt.Valid)
	require.Equal(t, int64(100), row.fellBackAt.Int64)
	require.False(t, row.ackedAt.Valid)

	row = e.deliveryRow("m-new", "a1")
	require.False(t, row.fellBackAt.Valid, "a row of another connection with the same label is untouched")
	require.False(t, row.ackedAt.Valid)
	require.Equal(t, []string{"m-new"}, e.ownedPending("a1", "live-L2"))
	require.Equal(t, int64(5), e.deliveryRow("m-acked", "a1").ackedAt.Int64, "an acknowledged row is not taken")
	require.False(t, e.deliveryRow("m-acked", "a1").fellBackAt.Valid)
	require.Equal(t, int64(7), e.deliveryRow("m-born", "a1").fellBackAt.Int64, "a row born fallen back is not touched")

	require.Empty(t, e.fallBack("a1", "live-L", 120), "a row that already fell back is not taken twice")
	require.Equal(t, int64(0), e.ackDelivery("m-old", "a1", "live-L", 130), "the dead connection's late acknowledgement is refused")
	require.False(t, e.deliveryRow("m-old", "a1").ackedAt.Valid)
}

// SESS-38 (data): a message the AI wrote is never handed back to it. The end of the connection
// acknowledges its pending row instead of marking it for the principal, and the fallback list never shows
// it, while a user's message of the same connection still falls back.
func TestAnAIsOwnMessageNeverFallsBack(t *testing.T) {
	e := newSchemaEnv(t)
	sessionsFixture(e)
	sessionsRoom(e, "r1")
	sessionsMessage(e, "m-self", "r1", 1, "agent", "a1")
	e.must(`UPDATE messages SET author_label = 'claude-2' WHERE id = 'm-self'`)
	sessionsMessage(e, "m-user", "r1", 2, "user", "u1")
	sessionsMessage(e, "m-self-other", "r1", 3, "agent", "a1")
	require.NoError(t, e.createDelivery("m-self", "a1", "claude-2", "live-L", sql.NullInt64{}))
	require.NoError(t, e.createDelivery("m-user", "a1", "claude-2", "live-L", sql.NullInt64{}))
	require.NoError(t, e.createDelivery("m-self-other", "a1", "wt", "live-L2", sql.NullInt64{}))

	require.Equal(t, []string{"m-user"}, e.fallBack("a1", "live-L", 100), "only the user's message falls back")
	row := e.deliveryRow("m-self", "a1")
	require.False(t, row.fellBackAt.Valid, "the AI's own row never falls back")
	require.True(t, row.ackedAt.Valid, "it is acknowledged instead")
	require.Equal(t, int64(100), row.ackedAt.Int64)
	require.Equal(t, []string{"m-user"}, messageIDs(e.listFallbacks("a1", "r1")), "the fallback list never shows the AI's own message")
	require.Empty(t, e.backlog("a1", "r1"), "and the principal's queue never gets it either")

	row = e.deliveryRow("m-self-other", "a1")
	require.False(t, row.fellBackAt.Valid, "a row of another connection is untouched")
	require.False(t, row.ackedAt.Valid)

	// A self-authored row born fallen back is never listed for the AI either.
	sessionsMessage(e, "m-self-born", "r1", 4, "agent", "a1")
	require.NoError(t, e.createDelivery("m-self-born", "a1", "claude-2", "", sql.NullInt64{Int64: 9, Valid: true}))
	require.Equal(t, []string{"m-user"}, messageIDs(e.listFallbacks("a1", "r1")))
	labels, err := e.s.R().ListSessionFallbackLabels(context.Background(), db.ListSessionFallbackLabelsParams{AgentID: "a1", RoomID: "r1"})
	require.NoError(t, err)
	require.Len(t, labels, 1)
	require.Equal(t, "m-user", labels[0].MessageID)
}

// SESS-24 (data): the fallback list hides acknowledged rows, and the principal's acknowledgement of a
// fallen-back row tombstones it without bringing the message back to the principal's queue.
func TestTheFallbackListHidesAcknowledgedRows(t *testing.T) {
	e := newSchemaEnv(t)
	sessionsFixture(e)
	sessionsRoom(e, "r1")
	sessionsMessage(e, "m1", "r1", 1, "user", "u1")
	sessionsMessage(e, "m2", "r1", 2, "user", "u1")
	sessionsMessage(e, "m3", "r1", 3, "user", "u1")
	require.NoError(t, e.createDelivery("m1", "a1", "wt", "", sql.NullInt64{Int64: 10, Valid: true}))
	require.NoError(t, e.createDelivery("m2", "a1", "wt", "", sql.NullInt64{Int64: 10, Valid: true}))
	require.NoError(t, e.createDelivery("m3", "a1", "wt", "live-L", sql.NullInt64{}))
	require.Equal(t, []string{"m1", "m2"}, messageIDs(e.listFallbacks("a1", "r1")))

	require.Equal(t, int64(0), e.ackFallback("m3", "a1", 20), "a pending row is not a fallback to acknowledge")
	require.Equal(t, int64(1), e.ackFallback("m1", "a1", 20))
	require.Equal(t, int64(0), e.ackFallback("m1", "a1", 30), "once")
	require.Equal(t, int64(20), e.deliveryRow("m1", "a1").ackedAt.Int64)
	require.Equal(t, []string{"m2"}, messageIDs(e.listFallbacks("a1", "r1")), "the acknowledged fallback is hidden")
	labels, err := e.s.R().ListSessionFallbackLabels(context.Background(), db.ListSessionFallbackLabelsParams{AgentID: "a1", RoomID: "r1"})
	require.NoError(t, err)
	require.Len(t, labels, 1)
	require.Equal(t, "m2", labels[0].MessageID)
	require.Empty(t, e.backlog("a1", "r1"), "the tombstone keeps the message out of the principal's queue")

	row, err := e.s.R().GetSessionDelivery(context.Background(), db.GetSessionDeliveryParams{MessageID: "m1", AgentID: "a1"})
	require.NoError(t, err)
	require.Equal(t, "wt", row.Label)
	require.True(t, row.FellBackAt.Valid)
	require.True(t, row.AckedAt.Valid)
	_, err = e.s.R().GetSessionDelivery(context.Background(), db.GetSessionDeliveryParams{MessageID: "none", AgentID: "a1"})
	require.ErrorIs(t, err, sql.ErrNoRows)
}

// SESS-24 (data): what a connection owns and has not acknowledged, in sequence order: the rows a
// reconnecting session of the same live id receives again.
func TestOwnedPendingListsTheConnectionsUnackedRowsInOrder(t *testing.T) {
	e := newSchemaEnv(t)
	sessionsFixture(e)
	sessionsRoom(e, "r1")
	sessionsRoom(e, "r2")
	sessionsMessage(e, "m-c", "r1", 3, "user", "u1")
	sessionsMessage(e, "m-a", "r1", 1, "user", "u1")
	sessionsMessage(e, "m-b", "r2", 1, "user", "u1")
	sessionsMessage(e, "m-acked", "r1", 4, "user", "u1")
	sessionsMessage(e, "m-fell", "r1", 5, "user", "u1")
	sessionsMessage(e, "m-other", "r1", 6, "user", "u1")
	for _, id := range []string{"m-c", "m-a", "m-b", "m-acked", "m-fell"} {
		require.NoError(t, e.createDelivery(id, "a1", "wt", "live-L", sql.NullInt64{}))
	}
	require.NoError(t, e.createDelivery("m-other", "a1", "wt", "live-L2", sql.NullInt64{}))
	require.Equal(t, int64(1), e.ackDelivery("m-acked", "a1", "live-L", 5))
	e.must(`UPDATE agent_session_deliveries SET fell_back_at = 9 WHERE message_id = 'm-fell'`)

	got := e.ownedPending("a1", "live-L")
	require.Len(t, got, 3)
	require.ElementsMatch(t, []string{"m-a", "m-b", "m-c"}, got, "only the unacked, not-fallen-back rows of that connection")
	require.Equal(t, []string{"m-a", "m-c"}, onlyRoom(t, e, "r1", got), "in sequence order within a room")
	require.Equal(t, []string{"m-other"}, e.ownedPending("a1", "live-L2"))
	require.Empty(t, e.ownedPending("a1", ""), "a row born fallen back, or a connection with no live id, owns nothing")
}

// onlyRoom keeps, in order, the ids of got that belong to roomID.
func onlyRoom(t *testing.T, e *schemaEnv, roomID string, got []string) []string {
	t.Helper()
	var out []string
	for _, id := range got {
		var room string
		require.NoError(t, e.s.write.QueryRowContext(context.Background(), `SELECT room_id FROM messages WHERE id = ?`, id).Scan(&room))
		if room == roomID {
			out = append(out, id)
		}
	}
	return out
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
	require.NoError(t, e.createDelivery("m-fb", "a1", "wt", "", sql.NullInt64{Int64: 100, Valid: true}))
	require.NoError(t, e.createDelivery("m-fb-other-room", "a1", "wt", "", sql.NullInt64{Int64: 100, Valid: true}))
	require.NoError(t, e.createDelivery("m-pending", "a1", "wt", "live-L", sql.NullInt64{}))

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
