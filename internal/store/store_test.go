package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
)

func openMigrated(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "partyline.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	require.NoError(t, s.Migrate(context.Background()))
	return s
}

func newUser(id, username string) db.CreateUserParams {
	return db.CreateUserParams{ID: id, Username: username, PasswordHash: "x", Role: "member", CreatedAt: 1}
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := openMigrated(t)

	require.NoError(t, s.Migrate(ctx))

	var applied int
	require.NoError(t, s.write.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&applied))
	files, err := migrationFiles()
	require.NoError(t, err)
	require.Equal(t, len(files), applied)

	n, err := s.R().CountUsers(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestMigrateCreatesEveryDesignTable(t *testing.T) {
	s := openMigrated(t)
	for _, table := range []string{
		"users", "sessions", "invites", "api_tokens", "agents", "connectors", "chats", "rooms",
		"room_members", "grants", "messages", "attachments", "agent_cursors", "outbox",
		"identities", "pairing_codes", "audit_log",
	} {
		var name string
		err := s.write.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name)
		require.NoError(t, err, table)
	}
}

func TestPragmasApplyToBothPools(t *testing.T) {
	s := openMigrated(t)
	var mode string
	var fk int
	require.NoError(t, s.read.QueryRow("PRAGMA journal_mode").Scan(&mode))
	require.Equal(t, "wal", mode)
	require.NoError(t, s.read.QueryRow("PRAGMA foreign_keys").Scan(&fk))
	require.Equal(t, 1, fk)
	require.NoError(t, s.write.QueryRow("PRAGMA foreign_keys").Scan(&fk))
	require.Equal(t, 1, fk)
}

func TestForeignKeysAreEnforced(t *testing.T) {
	s := openMigrated(t)
	err := s.WithTx(context.Background(), func(q *db.Queries) error {
		return q.CreateSession(context.Background(), db.CreateSessionParams{
			ID: "s1", TokenHash: "h1", UserID: "missing-user", CreatedAt: 1, ExpiresAt: 2, LastSeenAt: 1,
		})
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "FOREIGN KEY")
}

func TestWithTxCommitsOnSuccess(t *testing.T) {
	ctx := context.Background()
	s := openMigrated(t)
	require.NoError(t, s.WithTx(ctx, func(q *db.Queries) error {
		return q.CreateUser(ctx, newUser("u1", "alice"))
	}))
	n, err := s.R().CountUsers(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
}

func TestWithTxRollsBackOnError(t *testing.T) {
	ctx := context.Background()
	s := openMigrated(t)
	boom := errors.New("boom")

	err := s.WithTx(ctx, func(q *db.Queries) error {
		require.NoError(t, q.CreateUser(ctx, newUser("u1", "alice")))
		return boom
	})
	require.ErrorIs(t, err, boom)

	n, err := s.R().CountUsers(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
}

// MSG-01 relies on a unique (room_id, seq); CON-10 relies on the partial dedupe index.
func TestMessageSequenceAndDedupeConstraints(t *testing.T) {
	ctx := context.Background()
	s := openMigrated(t)
	require.NoError(t, s.WithTx(ctx, func(q *db.Queries) error { return q.CreateUser(ctx, newUser("u1", "alice")) }))
	_, err := s.write.Exec(`INSERT INTO rooms (id, owner_id, name, created_at, updated_at) VALUES ('r1', 'u1', 'Room', 1, 1)`)
	require.NoError(t, err)

	insert := func(id string, seq int, externalID any) error {
		_, err := s.write.Exec(`INSERT INTO messages (id, room_id, seq, author_kind, author_id, external_id, created_at)
			VALUES (?, 'r1', ?, 'chat', 'c1', ?, 1)`, id, seq, externalID)
		return err
	}
	require.NoError(t, insert("m1", 1, "ext-1"))
	require.Error(t, insert("m2", 1, nil), "duplicate seq in a room must fail")
	require.Error(t, insert("m3", 2, "ext-1"), "duplicate external id for the same author must fail")
	require.NoError(t, insert("m4", 2, nil))
	require.NoError(t, insert("m5", 3, nil), "messages without external id are never deduplicated")
}

// Edge case (ROOM-02): users are never deleted by the app (only disabled); if an owner
// account is removed anyway, its rooms go with it, with their members, messages,
// attachments, cursors and outbox entries.
func TestDeletingOwnerDeletesTheirRooms(t *testing.T) {
	ctx := context.Background()
	s := openMigrated(t)
	require.NoError(t, s.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateUser(ctx, newUser("u1", "alice")); err != nil {
			return err
		}
		return q.CreateUser(ctx, newUser("u2", "bob"))
	}))
	for _, stmt := range []string{
		`INSERT INTO rooms (id, owner_id, name, created_at, updated_at) VALUES ('r1', 'u1', 'Doomed', 1, 1), ('r2', 'u2', 'Kept', 1, 1)`,
		`INSERT INTO connectors (id, owner_id, kind, name, created_at, updated_at) VALUES ('k1', 'u2', 'telegram', 'tg', 1, 1)`,
		`INSERT INTO chats (id, connector_id, external_id, title, kind, created_at) VALUES ('c1', 'k1', 'x', 'g', 'group', 1)`,
		`INSERT INTO agents (id, owner_id, name, created_at) VALUES ('a1', 'u2', 'builder', 1)`,
		`INSERT INTO room_members (id, room_id, kind, member_id, created_at) VALUES ('rm1', 'r1', 'chat', 'c1', 1), ('rm2', 'r2', 'chat', 'c1', 1)`,
		`INSERT INTO messages (id, room_id, seq, author_kind, author_id, created_at) VALUES ('m1', 'r1', 1, 'user', 'u1', 1), ('m2', 'r2', 1, 'user', 'u2', 1)`,
		`INSERT INTO attachments (id, message_id, sha256, mime, name, size, created_at) VALUES ('f1', 'm1', 'abc', 'text/plain', 'a', 1, 1)`,
		`INSERT INTO outbox (id, message_id, chat_id, next_attempt_at, updated_at) VALUES ('o1', 'm1', 'c1', 1, 1), ('o2', 'm2', 'c1', 1, 1)`,
		`INSERT INTO agent_cursors (agent_id, room_id, seq) VALUES ('a1', 'r1', 1)`,
		`DELETE FROM users WHERE id = 'u1'`,
	} {
		_, err := s.write.ExecContext(ctx, stmt)
		require.NoError(t, err, stmt)
	}
	count := func(query string) int {
		var n int
		require.NoError(t, s.write.QueryRowContext(ctx, query).Scan(&n))
		return n
	}
	require.Equal(t, map[string]int{"rooms": 1, "members": 1, "messages": 1, "attachments": 0, "outbox": 1, "cursors": 0}, map[string]int{
		"rooms":       count(`SELECT COUNT(*) FROM rooms`),
		"members":     count(`SELECT COUNT(*) FROM room_members`),
		"messages":    count(`SELECT COUNT(*) FROM messages`),
		"attachments": count(`SELECT COUNT(*) FROM attachments`),
		"outbox":      count(`SELECT COUNT(*) FROM outbox`),
		"cursors":     count(`SELECT COUNT(*) FROM agent_cursors`),
	})
	require.Equal(t, 1, count(`SELECT COUNT(*) FROM rooms WHERE id = 'r2'`), "rooms of other owners stay")
}

// IDN-02: identities linked before they were scoped to a connector are dropped by the
// migration (they cannot say which connector vouched for them); users link again.
func TestIdentitiesPerConnectorMigrationDropsOldLinks(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "partyline.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	_, err = s.write.ExecContext(ctx, `CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`)
	require.NoError(t, err)
	for _, name := range []string{"0001_init.sql", "0002_message_views.sql"} {
		require.NoError(t, s.applyMigration(ctx, name))
	}
	require.NoError(t, s.WithTx(ctx, func(q *db.Queries) error { return q.CreateUser(ctx, newUser("u1", "alice")) }))
	_, err = s.write.ExecContext(ctx, `INSERT INTO identities (id, user_id, connector_kind, external_id, created_at) VALUES ('i1', 'u1', 'webhook', 'alice-id', 1)`)
	require.NoError(t, err)

	require.NoError(t, s.Migrate(ctx))
	var n int
	require.NoError(t, s.write.QueryRowContext(ctx, "SELECT COUNT(*) FROM identities").Scan(&n))
	require.Zero(t, n)
	var hasConnector int
	require.NoError(t, s.write.QueryRowContext(ctx, "SELECT COUNT(*) FROM pragma_table_info('identities') WHERE name = 'connector_id'").Scan(&hasConnector))
	require.Equal(t, 1, hasConnector)
}

// IDN-01 (edge case): pairing codes issued before migration 0005 name no connector, so the
// migration drops them (users request a new code), and every code from then on must name
// one (connector_id NOT NULL).
func TestPairingCodesPerConnectorMigrationDropsOldCodes(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "partyline.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	_, err = s.write.ExecContext(ctx, `CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`)
	require.NoError(t, err)
	files, err := migrationFiles()
	require.NoError(t, err)
	for _, name := range files {
		if name >= "0005" {
			break
		}
		require.NoError(t, s.applyMigration(ctx, name))
	}
	require.NoError(t, s.WithTx(ctx, func(q *db.Queries) error { return q.CreateUser(ctx, newUser("u1", "alice")) }))
	_, err = s.write.ExecContext(ctx, `INSERT INTO pairing_codes (code_hash, user_id, expires_at) VALUES ('h1', 'u1', 32503680000000)`)
	require.NoError(t, err, "the 0004 schema has codes without a connector")

	require.NoError(t, s.Migrate(ctx))
	var n int
	require.NoError(t, s.write.QueryRowContext(ctx, "SELECT COUNT(*) FROM pairing_codes").Scan(&n))
	require.Zero(t, n, "codes without a connector are dropped")
	var notNull int
	require.NoError(t, s.write.QueryRowContext(ctx,
		`SELECT "notnull" FROM pragma_table_info('pairing_codes') WHERE name = 'connector_id'`).Scan(&notNull))
	require.Equal(t, 1, notNull, "connector_id is NOT NULL")
}

// openMigratedBefore applies the migrations older than version (e.g. "0006").
func openMigratedBefore(t *testing.T, version string) *Store {
	t.Helper()
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "partyline.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	_, err = s.write.ExecContext(ctx, `CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`)
	require.NoError(t, err)
	files, err := migrationFiles()
	require.NoError(t, err)
	for _, name := range files {
		if name >= version {
			break
		}
		require.NoError(t, s.applyMigration(ctx, name))
	}
	return s
}

// MEM-01..05, INS-01, ROUTE-01..04 (data): migration 0006 gives existing members, rooms and
// connectors the defaults the spec names (signature on, delivery all, no display name, no
// keywords, no instructions, catch-all off) and its CHECKs refuse values outside the enums.
func TestRoutingContextMigrationDefaultsAndChecks(t *testing.T) {
	ctx := context.Background()
	s := openMigratedBefore(t, "0006")
	exec := func(query string, args ...any) error {
		_, err := s.write.ExecContext(ctx, query, args...)
		return err
	}
	require.NoError(t, s.WithTx(ctx, func(q *db.Queries) error { return q.CreateUser(ctx, newUser("u1", "alice")) }))
	require.NoError(t, exec(`INSERT INTO rooms (id, owner_id, name, created_at, updated_at) VALUES ('r1', 'u1', 'ops', 1, 1)`))
	require.NoError(t, exec(`INSERT INTO agents (id, owner_id, name, created_at) VALUES ('a1', 'u1', 'builder', 1)`))
	require.NoError(t, exec(`INSERT INTO room_members (id, room_id, kind, member_id, created_at) VALUES ('m1', 'r1', 'agent', 'a1', 1)`))
	require.NoError(t, exec(`INSERT INTO connectors (id, owner_id, kind, name, created_at, updated_at) VALUES ('c1', 'u1', 'telegram', 'tg', 1, 1)`))

	require.NoError(t, s.Migrate(ctx))

	var displayName, delivery, keywords string
	var signOutbound bool
	require.NoError(t, s.write.QueryRowContext(ctx,
		`SELECT display_name, sign_outbound, delivery, keywords_json FROM room_members WHERE id = 'm1'`).
		Scan(&displayName, &signOutbound, &delivery, &keywords))
	require.Equal(t, "", displayName)
	require.True(t, signOutbound, "signature stays on for existing members")
	require.Equal(t, "all", delivery)
	require.Equal(t, "[]", keywords)
	var instructions string
	require.NoError(t, s.write.QueryRowContext(ctx, `SELECT instructions FROM rooms WHERE id = 'r1'`).Scan(&instructions))
	require.Equal(t, "", instructions)
	var scope string
	var routeRoom *string
	require.NoError(t, s.write.QueryRowContext(ctx, `SELECT auto_route_scope, auto_route_room_id FROM connectors WHERE id = 'c1'`).Scan(&scope, &routeRoom))
	require.Equal(t, "off", scope)
	require.Nil(t, routeRoom)

	for _, d := range []string{"mentions", "trusted", "all"} {
		require.NoError(t, exec(`UPDATE room_members SET delivery = ? WHERE id = 'm1'`, d), d)
	}
	require.Error(t, exec(`UPDATE room_members SET delivery = 'loud' WHERE id = 'm1'`))
	for _, sc := range []string{"dms", "groups", "all", "off"} {
		require.NoError(t, exec(`UPDATE connectors SET auto_route_scope = ? WHERE id = 'c1'`, sc), sc)
	}
	require.Error(t, exec(`UPDATE connectors SET auto_route_scope = 'everyone' WHERE id = 'c1'`))
}

// ROUTE-03 (data): deleting the catch-all room clears the connector's auto_route_room_id;
// skip rows go with their chat.
func TestRoutingContextMigrationRoomAndChatReferences(t *testing.T) {
	ctx := context.Background()
	s := openMigrated(t)
	exec := func(query string, args ...any) error {
		_, err := s.write.ExecContext(ctx, query, args...)
		return err
	}
	require.NoError(t, s.WithTx(ctx, func(q *db.Queries) error { return q.CreateUser(ctx, newUser("u1", "alice")) }))
	require.NoError(t, exec(`INSERT INTO rooms (id, owner_id, name, created_at, updated_at) VALUES ('r1', 'u1', 'ops', 1, 1)`))
	require.NoError(t, exec(`INSERT INTO connectors (id, owner_id, kind, name, auto_route_scope, auto_route_room_id, created_at, updated_at)
		VALUES ('c1', 'u1', 'telegram', 'tg', 'dms', 'r1', 1, 1)`))
	require.Error(t, exec(`UPDATE connectors SET auto_route_room_id = 'missing' WHERE id = 'c1'`), "the room must exist")
	require.NoError(t, exec(`INSERT INTO chats (id, connector_id, external_id, created_at) VALUES ('h1', 'c1', '42', 1)`))
	require.NoError(t, exec(`INSERT INTO auto_route_skips (connector_id, chat_id, until) VALUES ('c1', 'h1', 99)`))

	require.NoError(t, exec(`DELETE FROM rooms WHERE id = 'r1'`))
	var routeRoom *string
	require.NoError(t, s.write.QueryRowContext(ctx, `SELECT auto_route_room_id FROM connectors WHERE id = 'c1'`).Scan(&routeRoom))
	require.Nil(t, routeRoom)

	require.NoError(t, exec(`DELETE FROM chats WHERE id = 'h1'`))
	var skips int
	require.NoError(t, s.write.QueryRowContext(ctx, `SELECT COUNT(*) FROM auto_route_skips`).Scan(&skips))
	require.Zero(t, skips)
}
