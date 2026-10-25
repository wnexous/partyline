package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// GAV-05, AD-012: migration 0022 adds rooms.avatar_sha256 with ADD COLUMN only. The column is the empty
// string for "no photo", like the avatar columns of intelligences and organizations; a group written before it
// keeps every other column and gets no photo.
func TestMigration0022AddsTheAvatarOfARoom(t *testing.T) {
	ctx := context.Background()
	s := openEmpty(t)
	require.NoError(t, s.MigrateBefore(ctx, "0022"))
	e := &schemaEnv{t: t, s: s}
	require.Zero(t, e.count(`SELECT COUNT(*) FROM pragma_table_info('rooms') WHERE name = 'avatar_sha256'`), "fixture sanity: the column is new")
	e.must(`INSERT INTO users (id, username, password_hash, role, created_at) VALUES ('u-alice', 'alice', 'x', 'member', 1)`)
	e.must(`INSERT INTO rooms (id, owner_id, name, description, retention_days, created_at, updated_at)
		VALUES ('r-old', 'u-alice', 'Launch', 'before the photo', 30, 7, 8)`)

	require.NoError(t, s.Migrate(ctx))
	require.Contains(t, versions(t, s), "0022_room_avatar")

	var typ, notNull, dflt string
	require.NoError(t, s.write.QueryRowContext(ctx,
		`SELECT type, CAST("notnull" AS TEXT), COALESCE(dflt_value, '') FROM pragma_table_info('rooms') WHERE name = 'avatar_sha256'`).
		Scan(&typ, &notNull, &dflt))
	require.Equal(t, []string{"TEXT", "1", "''"}, []string{typ, notNull, dflt}, "TEXT NOT NULL DEFAULT ''")

	var name, description, avatar string
	var retention, created, updated int
	require.NoError(t, s.write.QueryRowContext(ctx,
		`SELECT name, description, avatar_sha256, retention_days, created_at, updated_at FROM rooms WHERE id = 'r-old'`).
		Scan(&name, &description, &avatar, &retention, &created, &updated))
	require.Equal(t, []any{"Launch", "before the photo", "", 30, 7, 8}, []any{name, description, avatar, retention, created, updated},
		"the group keeps what it had and has no photo")

	// A row inserted without naming the column has no photo either: only the avatar routes set it.
	e.must(`INSERT INTO rooms (id, owner_id, name, created_at, updated_at) VALUES ('r-new', 'u-alice', 'New', 1, 1)`)
	require.Zero(t, e.count(`SELECT COUNT(*) FROM rooms WHERE avatar_sha256 <> ''`))
}
