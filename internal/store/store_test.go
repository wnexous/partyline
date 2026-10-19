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
