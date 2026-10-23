package store

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// v04Fixture builds what v0.4 leaves behind: the v0.3 data set (testdata/v03_fixture.sql) upgraded
// to schema 0016 by the intelligences feature, plus what v0.4 itself wrote and the fixture predates:
// a connector operated by an AI next to the owner-operated ones, connectors of every catch-all scope,
// the chat a user chose for copies of AI DMs, and a platform DM between an AI and a person, with its
// chat as a member and a message the chat sent.
func v04Fixture(t *testing.T) *schemaEnv {
	t.Helper()
	e := v03Fixture(t)
	require.NoError(t, e.s.MigrateBefore(context.Background(), "0017"))
	require.Equal(t, "0016_drop_v03_identity", versions(t, e.s)[len(versions(t, e.s))-1], "fixture sanity: schema 0016")

	maria := e.personOf("k-tg", "100")
	e.must(`UPDATE connectors SET operator_iid = 'a-atlas' WHERE id = 'k-tg'`) // k-wa stays owner-operated (NULL)
	e.must(`INSERT INTO connectors (id, owner_id, kind, name, auto_route_scope, created_at, updated_at) VALUES
		('k-dc', 'u-alice', 'discord', 'Guild', 'groups', 1790000000080, 1790000000080),
		('k-hook', 'u-bob', 'webhook', 'CI', 'all', 1790000000081, 1790000000081)`)
	e.must(`UPDATE users SET notify_chat_id = 'c-tgdm' WHERE id = 'u-bob'`)

	a, b := "a-atlas", maria
	if a > b {
		a, b = b, a
	}
	e.must(`INSERT INTO rooms (id, owner_id, name, kind, dm_a, dm_b, next_seq, read_baseline, created_at, updated_at)
		VALUES ('r-dm-maria', 'u-alice', '', 'dm', ?, ?, 2, 0, 1790000000090, 1790000000090)`, a, b)
	e.must(`INSERT INTO room_members (id, room_id, kind, member_id, added_by, created_at) VALUES
		('rm-dm-atlas', 'r-dm-maria', 'agent', 'a-atlas', 'u-alice', 1790000000091),
		('rm-dm-chat', 'r-dm-maria', 'chat', 'c-tgdm', 'u-alice', 1790000000092)`)
	e.must(`INSERT INTO messages (id, room_id, seq, author_kind, author_id, author_user_id, author_iid, sender_id, sender_alt_id,
			sender_name, kind, text, reply_to, recipients_json, external_id, created_at)
		VALUES ('m-dm1', 'r-dm-maria', 1, 'chat', 'c-tgdm', '', ?, '100', '', 'Maria Silva', 'text', 'oi, tudo bem?', '', '[]', 'tg-9', 1790000000093)`, maria)
	return e
}

// What migration 0018 removes from the schema (design, "Migration 0018"), and the columns it keeps but
// empties.
var (
	droppedTables  = []string{"outbox", "auto_route_skips"}
	droppedColumns = map[string][]string{"connectors": {"auto_route_scope"}, "rooms": {"bridge_chats", "broadcast"}}
	clearedColumns = map[string][]string{"connectors": {"auto_route_room_id"}, "users": {"notify_chat_id"}}
)

// column returns the first column of every row of the query.
func (e *schemaEnv) column(query string, args ...any) []string {
	e.t.Helper()
	rows, err := e.s.write.Query(query, args...)
	require.NoError(e.t, err)
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var v string
		require.NoError(e.t, rows.Scan(&v))
		out = append(out, v)
	}
	require.NoError(e.t, rows.Err())
	return out
}

// CID-21, CID-27 (data): the upgrade from a v0.4 database takes the chats out of every room and keeps
// everything else: the rooms with their settings and history, their AI and user members, the DMs
// (platform DMs included, now history), the people and every message, intelligence and grant. Every
// connection reads deliver=all and groups_mentions_only=1 whatever its catch-all scope was, an
// operator stays and a missing one stays missing, and what the retired features stored is gone: the
// delivery tables, the routing scope, the room switches, the room a connection routed to and the chat
// a user chose for copies.
func TestConnectionProxyUpgradeKeepsHistoryAndDropsTheRest(t *testing.T) {
	ctx := context.Background()
	e := v04Fixture(t)
	all := e.v03Tables()

	// Fixture sanity: every retired feature has data, so that the assertions below can fail.
	require.Equal(t, 4, e.count(`SELECT COUNT(*) FROM room_members WHERE kind = 'chat'`), "three chats in rooms and one in the platform DM")
	require.Equal(t, 3, e.count(`SELECT COUNT(*) FROM outbox`))
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM auto_route_skips`))
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM connectors WHERE auto_route_room_id IS NOT NULL`))
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM users WHERE notify_chat_id IS NOT NULL`))
	require.Equal(t, 3, e.count(`SELECT COUNT(*) FROM connectors WHERE operator_iid IS NULL`))
	require.ElementsMatch(t, []string{"dms", "off", "groups", "all"}, e.column(`SELECT auto_route_scope FROM connectors`), "every scope is represented")
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM rooms WHERE bridge_chats = 1`))
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM rooms WHERE broadcast = 0`))

	// What must survive, per table: every row, on the columns that neither go nor are emptied.
	compared := map[string][]string{}
	before := map[string][]string{}
	for table, cols := range all {
		if slices.Contains(droppedTables, table) {
			continue
		}
		compared[table] = without(cols, func(c string) bool {
			return slices.Contains(droppedColumns[table], c) || slices.Contains(clearedColumns[table], c)
		})
		before[table] = e.rowsOf(table, compared[table])
	}
	require.NotEmpty(t, before["people"], "fixture sanity: people")
	require.NotEmpty(t, before["intelligences"], "fixture sanity: intelligences")

	require.NoError(t, e.s.MigrateBefore(ctx, "0019")) // 0017 and 0018 alone: 0019 turns the grants into memberships and has tests of its own
	require.Subset(t, versions(t, e.s), []string{"0017_platform_queue", "0018_connection_proxy"})
	require.NotContains(t, versions(t, e.s), "0019_organizations")

	// The chats leave every room; every AI and user member stays, with every column.
	require.Zero(t, e.count(`SELECT COUNT(*) FROM room_members WHERE kind = 'chat'`), "no chat is a member of any room")
	isChat := func(row string) bool { return strings.Contains(row, "kind=chat ") }
	require.Equal(t, without(before["room_members"], isChat), e.rowsOf("room_members", compared["room_members"]),
		"the AI and user members keep every column, the stored outbound and signature switches included")
	require.Equal(t, []string{"a-atlas"}, e.column(`SELECT member_id FROM room_members WHERE room_id = 'r-dm-maria'`), "the platform DM keeps its AI")

	// Everything else is history now and untouched: rooms with their settings, messages, people,
	// intelligences, identities, grants, cursors, files.
	for table, rows := range before {
		if table == "room_members" {
			continue
		}
		require.Equal(t, rows, e.rowsOf(table, compared[table]), "table %s keeps every row and every surviving column", table)
	}
	require.Equal(t, 14, e.count(`SELECT COUNT(*) FROM messages`), "the thirteen messages of the fixture and the platform DM's")
	require.Equal(t, 4, e.count(`SELECT COUNT(*) FROM rooms WHERE kind = 'group'`))
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM rooms WHERE kind = 'dm'`), "the platform DM stays, with its two participants")
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM rooms r JOIN intelligences a ON a.id = r.dm_a JOIN intelligences b ON b.id = r.dm_b WHERE r.id = 'r-dm-maria'`))
	require.Equal(t, "oi, tudo bem?", e.str(`SELECT text FROM message_views WHERE id = 'm-dm1'`))
	require.Equal(t, "Maria Silva", e.str(`SELECT author_name FROM message_views WHERE id = 'm-dm1'`))
	require.Equal(t, 3, e.count(`SELECT COUNT(*) FROM people`))

	// Connections: the proxy defaults whatever the old scope, operators untouched (CID-27, CID-02).
	for _, id := range []string{"k-tg", "k-wa", "k-dc", "k-hook"} {
		require.Equal(t, "all", e.str(`SELECT deliver FROM connectors WHERE id = ?`, id), id)
		require.Equal(t, 1, e.count(`SELECT groups_mentions_only FROM connectors WHERE id = ?`, id), id)
	}
	require.Equal(t, "a-atlas", e.str(`SELECT operator_iid FROM connectors WHERE id = 'k-tg'`), "an AI keeps its connection")
	require.Equal(t, 3, e.count(`SELECT COUNT(*) FROM connectors WHERE operator_iid IS NULL`), "an owner-operated connection has no operator: it idles until one is chosen")
	require.Zero(t, e.count(`SELECT COUNT(*) FROM connectors WHERE auto_route_room_id IS NOT NULL`), "no connection routes to a room")
	require.Zero(t, e.count(`SELECT COUNT(*) FROM users WHERE notify_chat_id IS NOT NULL`), "no user has a chat to notify")

	// The retired tables and columns are gone; the dead columns the design keeps are still there.
	for _, table := range droppedTables {
		require.Empty(t, e.columnsOf(table), "table %s is dropped", table)
	}
	for table, cols := range droppedColumns {
		for _, col := range cols {
			require.NotContains(t, e.columnsOf(table), col, "%s.%s is dropped", table, col)
		}
	}
	require.Subset(t, e.columnsOf("connectors"), []string{"auto_route_room_id", "operator_iid", "deliver", "groups_mentions_only"})
	require.Contains(t, e.columnsOf("users"), "notify_chat_id")
	require.Subset(t, e.columnsOf("room_members"), []string{"outbound", "sign_outbound"})
	require.Zero(t, e.count(`SELECT COUNT(*) FROM pragma_foreign_key_check`), "no foreign key is left dangling")
	require.Equal(t, "ok", e.str(`SELECT integrity_check FROM pragma_integrity_check`))
}

// MIG-05 (CID-21): the upgrade is atomic. A statement of 0018 that fails after the chats were deleted and
// the tables dropped (a view somebody made on a column it drops) leaves the database exactly as it was,
// 0017 included, and the same upgrade works once the cause is gone.
func TestConnectionProxyUpgradeRollsBackOnFailure(t *testing.T) {
	ctx := context.Background()
	e := v04Fixture(t)
	e.must(`CREATE VIEW room_switches AS SELECT id, bridge_chats FROM rooms`)
	before := snapshot(t, e.s)

	err := e.s.Migrate(ctx)
	require.Error(t, err)
	require.ErrorContains(t, err, "0018_connection_proxy")
	require.Equal(t, before, snapshot(t, e.s), "schema, schema_migrations and every row are unchanged")
	require.Equal(t, 4, e.count(`SELECT COUNT(*) FROM room_members WHERE kind = 'chat'`), "the chats are still in their rooms")
	require.Equal(t, 3, e.count(`SELECT COUNT(*) FROM outbox`))
	require.Empty(t, e.columnsOf("platform_items"), "0017 did not stay either")

	e.must(`DROP VIEW room_switches`)
	require.NoError(t, e.s.Migrate(ctx))
	require.Zero(t, e.count(`SELECT COUNT(*) FROM room_members WHERE kind = 'chat'`))
	require.Empty(t, e.columnsOf("outbox"))
	require.NotEmpty(t, e.columnsOf("platform_items"))
}

// A database that is already at 0018 is not touched by a second start.
func TestConnectionProxyUpgradeRunsOnce(t *testing.T) {
	ctx := context.Background()
	e := v04Fixture(t)
	require.NoError(t, e.s.Migrate(ctx))
	// Rows written afterwards survive another start: the upgrade deleted chat members once.
	e.must(`INSERT INTO room_members (id, room_id, kind, member_id, added_by, created_at)
		VALUES ('rm-late', 'r-dev', 'chat', 'c-tgdm', 'u-alice', 1790000000099)`)
	before := snapshot(t, e.s)

	require.NoError(t, e.s.Migrate(ctx))

	require.Equal(t, before, snapshot(t, e.s))
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM room_members WHERE id = 'rm-late'`))
}
