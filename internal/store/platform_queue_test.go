package store

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
)

// platformFixture seeds the rows a queue item hangs on: user u1, its AI a1, connector k1 and
// chat c1 of k1 (and a second AI a2, connector k2 and chat c2 of k2 for the "only this one"
// assertions).
func platformFixture(t *testing.T) *schemaEnv {
	t.Helper()
	e := newSchemaEnv(t)
	e.must(`INSERT INTO users (id, username, password_hash, role, created_at) VALUES ('u1', 'alice', 'x', 'member', 1)`)
	e.must(`INSERT INTO agents (id, owner_id, name, created_at) VALUES ('a1', 'u1', 'atlas', 1), ('a2', 'u1', 'notes', 1)`)
	e.must(`INSERT INTO connectors (id, owner_id, kind, name, operator_iid, created_at, updated_at) VALUES
		('k1', 'u1', 'telegram', 'tg', NULL, 1, 1), ('k2', 'u1', 'telegram', 'tg2', NULL, 1, 1)`)
	e.must(`INSERT INTO chats (id, connector_id, external_id, created_at) VALUES ('c1', 'k1', '42', 1), ('c2', 'k2', '42', 1)`)
	return e
}

func (e *schemaEnv) item(id, agent, connector, chat, external string) error {
	return e.exec(`INSERT INTO platform_items (id, agent_id, connector_id, chat_id, external_id, received_at)
		VALUES (?, ?, ?, ?, ?, 1)`, id, agent, connector, chat, external)
}

// CID-08, CID-26, CID-27 (data): migration 0017 adds the three queue tables, the AI's dropped
// counter and the connector's delivery settings.
func TestPlatformQueueTablesAndColumnsExist(t *testing.T) {
	e := newSchemaEnv(t)

	require.Equal(t, []string{
		"seq", "id", "agent_id", "connector_id", "chat_id", "external_id", "sender_id", "sender_name",
		"trusted", "from_self", "kind", "text", "received_at", "acked_at",
	}, e.columnsOf("platform_items"))
	require.Equal(t, []string{"id", "item_id", "sha256", "name", "mime", "size", "status"}, e.columnsOf("platform_attachments"))
	require.Equal(t, []string{
		"id", "connector_id", "chat_id", "agent_id", "text", "reply_to_external_id", "status", "attempts",
		"next_attempt_at", "last_error", "external_id", "created_at", "updated_at",
	}, e.columnsOf("platform_sends"))
	require.Contains(t, e.columnsOf("agents"), "platform_dropped")
	require.Subset(t, e.columnsOf("connectors"), []string{"deliver", "groups_mentions_only"})
}

// CID-27: connections that exist before the upgrade, whatever their old catch-all scope, read
// deliver=all and groups_mentions_only on; the counter of dropped items of an AI starts at 0.
func TestPlatformQueueMigrationGivesExistingConnectorsTheDefaults(t *testing.T) {
	ctx := context.Background()
	s := openMigratedBefore(t, "0017")
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := s.write.ExecContext(ctx, query, args...)
		require.NoError(t, err, query)
	}
	exec(`INSERT INTO users (id, username, password_hash, role, created_at) VALUES ('u1', 'alice', 'x', 'member', 1)`)
	exec(`INSERT INTO agents (id, owner_id, name, created_at) VALUES ('a1', 'u1', 'atlas', 1)`)
	exec(`INSERT INTO connectors (id, owner_id, kind, name, auto_route_scope, created_at, updated_at) VALUES
		('k-off', 'u1', 'telegram', 'off', 'off', 1, 1),
		('k-dms', 'u1', 'telegram', 'dms', 'dms', 1, 1),
		('k-groups', 'u1', 'whatsapp', 'groups', 'groups', 1, 1),
		('k-all', 'u1', 'discord', 'all', 'all', 1, 1)`)

	require.NoError(t, s.Migrate(ctx))

	rows, err := s.write.QueryContext(ctx, `SELECT id, deliver, groups_mentions_only FROM connectors ORDER BY id`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	seen := 0
	for rows.Next() {
		var id, deliver string
		var mentionsOnly bool
		require.NoError(t, rows.Scan(&id, &deliver, &mentionsOnly))
		require.Equal(t, "all", deliver, id)
		require.True(t, mentionsOnly, id)
		seen++
	}
	require.NoError(t, rows.Err())
	require.Equal(t, 4, seen)
	var dropped int
	require.NoError(t, s.write.QueryRowContext(ctx, `SELECT platform_dropped FROM agents WHERE id = 'a1'`).Scan(&dropped))
	require.Zero(t, dropped)
}

// CID-27: a connector inserted without the settings reads the defaults too, and the column
// refuses a delivery other than dms, groups or all.
func TestConnectorDeliveryDefaultsAndChecks(t *testing.T) {
	e := platformFixture(t)

	var deliver string
	var mentionsOnly bool
	require.NoError(t, e.s.write.QueryRow(`SELECT deliver, groups_mentions_only FROM connectors WHERE id = 'k1'`).Scan(&deliver, &mentionsOnly))
	require.Equal(t, "all", deliver)
	require.True(t, mentionsOnly)

	for _, d := range []string{"dms", "groups", "all"} {
		e.must(`UPDATE connectors SET deliver = ? WHERE id = 'k1'`, d)
	}
	for _, d := range []string{"", "everyone", "off", "DMS"} {
		require.Error(t, e.exec(`UPDATE connectors SET deliver = ? WHERE id = 'k1'`, d), "deliver %q", d)
	}
	e.must(`UPDATE connectors SET groups_mentions_only = 0 WHERE id = 'k1'`)
}

// CID-26 (data): the same platform message of one chat of one connector is one queue row. An
// empty platform id (a platform that gives none) never collides; the same id in another chat
// or on another connection is another message.
func TestPlatformItemsDedupeOnConnectorChatAndPlatformID(t *testing.T) {
	e := platformFixture(t)
	e.must(`INSERT INTO chats (id, connector_id, external_id, created_at) VALUES ('c3', 'k1', '43', 1)`)

	require.NoError(t, e.item("i1", "a1", "k1", "c1", "wamid.1"))
	require.Error(t, e.item("i2", "a1", "k1", "c1", "wamid.1"), "a platform retry of the same message")
	require.Error(t, e.item("i2", "a2", "k1", "c1", "wamid.1"), "the operator does not matter: a move must not duplicate")
	require.NoError(t, e.item("i3", "a1", "k1", "c3", "wamid.1"), "same platform id, another chat")
	require.NoError(t, e.item("i4", "a1", "k2", "c2", "wamid.1"), "same platform id, another connection")
	require.NoError(t, e.item("i5", "a1", "k1", "c1", "wamid.2"), "another message")

	require.NoError(t, e.item("i6", "a1", "k1", "c1", ""))
	require.NoError(t, e.item("i7", "a1", "k1", "c1", ""), "empty platform ids are accepted twice")
	require.Equal(t, 2, e.count(`SELECT COUNT(*) FROM platform_items WHERE external_id = ''`))
	require.Error(t, e.item("i1", "a1", "k1", "c3", "wamid.9"), "ids are unique")
}

// CID-08 (data): rows are delivered in the order they were queued: seq grows with every insert
// and is never reused, also after the oldest rows are deleted.
func TestPlatformItemSeqOnlyGrows(t *testing.T) {
	e := platformFixture(t)
	for _, id := range []string{"i1", "i2", "i3"} {
		require.NoError(t, e.item(id, "a1", "k1", "c1", id))
	}
	e.must(`DELETE FROM platform_items WHERE id = 'i3'`)
	require.NoError(t, e.item("i4", "a1", "k1", "c1", "i4"))

	var seq1, seq2, seq4 int64
	require.NoError(t, e.s.write.QueryRow(`SELECT seq FROM platform_items WHERE id = 'i1'`).Scan(&seq1))
	require.NoError(t, e.s.write.QueryRow(`SELECT seq FROM platform_items WHERE id = 'i2'`).Scan(&seq2))
	require.NoError(t, e.s.write.QueryRow(`SELECT seq FROM platform_items WHERE id = 'i4'`).Scan(&seq4))
	require.Less(t, seq1, seq2)
	require.Greater(t, seq4, seq2+1, "the seq of a deleted row is not handed out again")
}

// CID-24 (data), CID-09: a queue item goes with its AI, its connection and its chat, and takes
// its attachment rows along.
func TestPlatformItemsCascade(t *testing.T) {
	for _, tc := range []struct{ name, delete string }{
		{"the AI is deleted", `DELETE FROM agents WHERE id = 'a1'`},
		{"the connection is deleted", `DELETE FROM connectors WHERE id = 'k1'`},
		{"the chat is deleted", `DELETE FROM chats WHERE id = 'c1'`},
		{"the item is deleted", `DELETE FROM platform_items WHERE id = 'i1'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := platformFixture(t)
			require.NoError(t, e.item("i1", "a1", "k1", "c1", "wamid.1"))
			require.NoError(t, e.item("i2", "a2", "k2", "c2", "wamid.1"))
			e.must(`INSERT INTO platform_attachments (id, item_id, sha256, name, mime, size, status) VALUES
				('f1', 'i1', 'aa', 'a.png', 'image/png', 3, 'stored'), ('f2', 'i2', 'bb', 'b.png', 'image/png', 3, 'stored')`)

			e.must(tc.delete)

			require.Zero(t, e.count(`SELECT COUNT(*) FROM platform_items WHERE id = 'i1'`))
			require.Zero(t, e.count(`SELECT COUNT(*) FROM platform_attachments WHERE id = 'f1'`), "its files go with it")
			require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM platform_items WHERE id = 'i2'`), "another AI's item stays")
			require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM platform_attachments WHERE id = 'f2'`))
		})
	}
}

// An item names rows that exist: an unknown AI, connection or chat is refused (foreign keys).
func TestPlatformItemsNeedTheirAIConnectionAndChat(t *testing.T) {
	e := platformFixture(t)
	require.Error(t, e.item("i1", "missing", "k1", "c1", "1"))
	require.Error(t, e.item("i2", "a1", "missing", "c1", "2"))
	require.Error(t, e.item("i3", "a1", "k1", "missing", "3"))
	require.Error(t, e.exec(`INSERT INTO platform_attachments (id, item_id, name, mime, size, status) VALUES ('f1', 'missing', 'a', 'b', 1, 'stored')`))
}

// CID-15, CID-18 (data): a platform send starts pending and moves through the outbox states; any
// other state is refused. Sends go with their AI, connection and chat.
func TestPlatformSendsStatusesAndCascade(t *testing.T) {
	e := platformFixture(t)
	insert := func(id string) error {
		return e.exec(`INSERT INTO platform_sends (id, connector_id, chat_id, agent_id, text, next_attempt_at, created_at, updated_at)
			VALUES (?, 'k1', 'c1', 'a1', 'hello', 1, 1, 1)`, id)
	}
	require.NoError(t, insert("s1"))
	var status string
	var attempts int
	require.NoError(t, e.s.write.QueryRow(`SELECT status, attempts FROM platform_sends WHERE id = 's1'`).Scan(&status, &attempts))
	require.Equal(t, "pending", status)
	require.Zero(t, attempts)

	for _, st := range []string{"sending", "sent", "failed", "pending"} {
		e.must(`UPDATE platform_sends SET status = ? WHERE id = 's1'`, st)
	}
	require.Error(t, e.exec(`UPDATE platform_sends SET status = 'queued' WHERE id = 's1'`))

	require.NoError(t, insert("s2"))
	e.must(`DELETE FROM chats WHERE id = 'c1'`)
	require.Zero(t, e.count(`SELECT COUNT(*) FROM platform_sends`), "sends go with their chat")
	e.must(`INSERT INTO chats (id, connector_id, external_id, created_at) VALUES ('c1', 'k1', '42', 1)`)
	require.NoError(t, insert("s3"))
	e.must(`DELETE FROM agents WHERE id = 'a1'`)
	require.Zero(t, e.count(`SELECT COUNT(*) FROM platform_sends`), "sends go with their AI")
	require.Error(t, insert("s4"), "an AI that does not exist sends nothing")
}

func (e *schemaEnv) insertItem(agent, connector, chat, external string) (int64, error) {
	e.t.Helper()
	var seq int64
	err := e.s.WithTx(context.Background(), func(q *db.Queries) (err error) {
		seq, err = q.InsertPlatformItem(context.Background(), db.InsertPlatformItemParams{
			ID: "item-" + external + "-" + chat, AgentID: agent, ConnectorID: connector, ChatID: chat, ExternalID: external,
			SenderID: "5511999990000", SenderName: "Pat", Trusted: true, Kind: "text", Text: "hello " + external, ReceivedAt: 1000,
		})
		return err
	})
	return seq, err
}

func (e *schemaEnv) unacked(agent string, after int64, limit int64) []db.ListUnackedPlatformItemsRow {
	e.t.Helper()
	rows, err := e.s.R().ListUnackedPlatformItems(context.Background(), db.ListUnackedPlatformItemsParams{AgentID: agent, AfterSeq: after, Limit: limit})
	require.NoError(e.t, err)
	return rows
}

func ids(rows []db.ListUnackedPlatformItemsRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

// CID-26, CID-08: the insert query reports the seq of a queued item and nothing for a repeat of
// the same platform message; the listing returns an AI's unacknowledged items oldest first, with
// the chat and the platform they came from.
func TestPlatformQueueInsertAndListQueries(t *testing.T) {
	e := platformFixture(t)
	e.must(`UPDATE chats SET title = 'Family', kind = 'group' WHERE id = 'c1'`)

	seq1, err := e.insertItem("a1", "k1", "c1", "m1")
	require.NoError(t, err)
	seq2, err := e.insertItem("a1", "k1", "c1", "m2")
	require.NoError(t, err)
	_, err = e.insertItem("a1", "k1", "c1", "m1")
	require.ErrorIs(t, err, sql.ErrNoRows, "a repeat of m1 queues nothing")
	_, err = e.insertItem("a2", "k2", "c2", "m1")
	require.NoError(t, err, "the same platform id on another connection is another message")
	require.Less(t, seq1, seq2)
	require.Equal(t, 2, e.count(`SELECT COUNT(*) FROM platform_items WHERE agent_id = 'a1'`), "the repeat left no row")

	list := e.unacked("a1", 0, 10)
	require.Equal(t, []string{"item-m1-c1", "item-m2-c1"}, ids(list), "oldest first, only this AI's")
	first := list[0]
	require.Equal(t, seq1, first.Seq)
	require.Equal(t, []string{"k1", "c1", "m1", "5511999990000", "Pat", "hello m1", "Family", "group", "telegram", "text"},
		[]string{first.ConnectorID, first.ChatID, first.ExternalID, first.SenderID, first.SenderName, first.Text, first.ChatTitle, first.ChatKind, first.Platform, first.Kind})
	require.True(t, first.Trusted)
	require.False(t, first.FromSelf)
	require.Equal(t, int64(1000), first.ReceivedAt)

	require.Equal(t, []string{"item-m2-c1"}, ids(e.unacked("a1", seq1, 10)), "after a seq")
	require.Equal(t, []string{"item-m1-c1"}, ids(e.unacked("a1", 0, 1)), "limited")
	require.Empty(t, e.unacked("a1", seq2, 10))
}

// CID-09: an acknowledgement clears the text and keeps the metadata, only for the item's own AI
// and only once; the item leaves the unacknowledged list.
func TestPlatformQueueAckQuery(t *testing.T) {
	ctx := context.Background()
	e := platformFixture(t)
	_, err := e.insertItem("a1", "k1", "c1", "m1")
	require.NoError(t, err)
	ack := func(agent, id string, at int64) int64 {
		var n int64
		require.NoError(t, e.s.WithTx(ctx, func(q *db.Queries) (err error) {
			n, err = q.AckPlatformItem(ctx, db.AckPlatformItemParams{AckedAt: sql.NullInt64{Int64: at, Valid: true}, ID: id, AgentID: agent})
			return err
		}))
		return n
	}

	require.Zero(t, ack("a2", "item-m1-c1", 5), "another AI's ack does nothing")
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM platform_items WHERE text = 'hello m1' AND acked_at IS NULL`))
	require.Equal(t, int64(1), ack("a1", "item-m1-c1", 5))
	require.Zero(t, ack("a1", "item-m1-c1", 9), "a second ack does nothing")

	var text, sender string
	var ackedAt int64
	require.NoError(t, e.s.write.QueryRow(`SELECT text, sender_name, acked_at FROM platform_items WHERE id = 'item-m1-c1'`).Scan(&text, &sender, &ackedAt))
	require.Equal(t, []any{"", "Pat", int64(5)}, []any{text, sender, ackedAt}, "text gone, metadata kept, first ack time kept")
	require.Empty(t, e.unacked("a1", 0, 10))
}

// CID-11: an AI keeps its newest items up to the cap; the older unacknowledged ones go, acked
// items and other AIs' items are left alone, and the dropped counter adds up and resets.
func TestPlatformQueueCapQueries(t *testing.T) {
	ctx := context.Background()
	e := platformFixture(t)
	for i := range 5 {
		_, err := e.insertItem("a1", "k1", "c1", fmt.Sprintf("m%d", i))
		require.NoError(t, err)
	}
	_, err := e.insertItem("a2", "k2", "c2", "other")
	require.NoError(t, err)
	e.must(`UPDATE platform_items SET acked_at = 7, text = '' WHERE id = 'item-m0-c1'`) // the oldest is acked already
	over := func(agent string, keep int64) int64 {
		var n int64
		require.NoError(t, e.s.WithTx(ctx, func(q *db.Queries) (err error) {
			n, err = q.DeleteUnackedPlatformItemsOverCap(ctx, db.DeleteUnackedPlatformItemsOverCapParams{AgentID: agent, Cap: keep})
			return err
		}))
		return n
	}

	require.Zero(t, over("a1", 4), "4 unacked items fit a cap of 4")
	require.Equal(t, int64(1), over("a1", 3), "the oldest unacked item goes")
	require.Equal(t, []string{"item-m2-c1", "item-m3-c1", "item-m4-c1"}, ids(e.unacked("a1", 0, 10)))
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM platform_items WHERE id = 'item-m0-c1'`), "an acked item is not counted against the cap")
	require.Equal(t, int64(3), over("a1", 0), "a cap of zero empties the unacked list")
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM platform_items WHERE agent_id = 'a2'`), "another AI keeps its item")
	n, err := e.s.R().CountUnackedPlatformItems(ctx, "a2")
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	add := func(agent string, n int64) {
		require.NoError(t, e.s.WithTx(ctx, func(q *db.Queries) error {
			return q.AddPlatformDropped(ctx, db.AddPlatformDroppedParams{N: n, AgentID: agent})
		}))
	}
	add("a1", 2)
	add("a1", 3)
	dropped, err := e.s.R().GetPlatformDropped(ctx, "a1")
	require.NoError(t, err)
	require.Equal(t, int64(5), dropped)
	other, err := e.s.R().GetPlatformDropped(ctx, "a2")
	require.NoError(t, err)
	require.Zero(t, other)
	require.NoError(t, e.s.WithTx(ctx, func(q *db.Queries) error { return q.ResetPlatformDropped(ctx, "a1") }))
	dropped, err = e.s.R().GetPlatformDropped(ctx, "a1")
	require.NoError(t, err)
	require.Zero(t, dropped)
}

// CID-05: moving a connection hands over every item of that connection, acked or not, and no
// other.
func TestPlatformQueueMoveQuery(t *testing.T) {
	ctx := context.Background()
	e := platformFixture(t)
	for _, m := range []string{"m1", "m2", "m3"} {
		_, err := e.insertItem("a1", "k1", "c1", m)
		require.NoError(t, err)
	}
	_, err := e.insertItem("a1", "k2", "c2", "m1")
	require.NoError(t, err)
	e.must(`UPDATE platform_items SET acked_at = 7, text = '' WHERE id = 'item-m1-c1'`)
	move := func(connector, to string) int64 {
		var n int64
		require.NoError(t, e.s.WithTx(ctx, func(q *db.Queries) (err error) {
			n, err = q.MovePlatformItems(ctx, db.MovePlatformItemsParams{AgentID: to, ConnectorID: connector})
			return err
		}))
		return n
	}

	require.Equal(t, int64(3), move("k1", "a2"))
	require.Equal(t, 3, e.count(`SELECT COUNT(*) FROM platform_items WHERE agent_id = 'a2' AND connector_id = 'k1'`), "the acked item moved too")
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM platform_items WHERE agent_id = 'a1' AND connector_id = 'k2'`), "the other connection stays")
	require.Equal(t, []string{"item-m2-c1", "item-m3-c1"}, ids(e.unacked("a2", 0, 10)))
	require.Zero(t, move("k1", "a2"), "moving to the AI that holds them changes nothing")
}

// CID-06: the files of an item list with it, in the order they were stored.
func TestPlatformAttachmentListQuery(t *testing.T) {
	ctx := context.Background()
	e := platformFixture(t)
	_, err := e.insertItem("a1", "k1", "c1", "m1")
	require.NoError(t, err)
	_, err = e.insertItem("a1", "k1", "c1", "m2")
	require.NoError(t, err)
	for _, a := range []struct{ id, item, name string }{
		{"f-1", "item-m1-c1", "first.png"}, {"f-2", "item-m1-c1", "second.png"}, {"f-3", "item-m2-c1", "third.png"},
	} {
		require.NoError(t, e.s.WithTx(ctx, func(q *db.Queries) error {
			return q.InsertPlatformAttachment(ctx, db.InsertPlatformAttachmentParams{
				ID: a.id, ItemID: a.item, Sha256: "aa", Name: a.name, Mime: "image/png", Size: 3, Status: "stored",
			})
		}))
	}

	got, err := e.s.R().ListPlatformAttachmentsForItems(ctx, []string{"item-m2-c1", "item-m1-c1"})
	require.NoError(t, err)
	names := make([]string, len(got))
	for i, a := range got {
		names[i] = a.Name
	}
	require.Equal(t, []string{"first.png", "second.png", "third.png"}, names)
	none, err := e.s.R().ListPlatformAttachmentsForItems(ctx, []string{"item-none"})
	require.NoError(t, err)
	require.Empty(t, none)
}
