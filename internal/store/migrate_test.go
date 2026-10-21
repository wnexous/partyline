package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

// applyMigration applies one embedded migration file, with its hook, unless it is recorded.
func (s *Store) applyMigration(ctx context.Context, name string) error {
	body, err := fs.ReadFile(migrations, name)
	if err != nil {
		return err
	}
	return s.migrate(ctx, fstest.MapFS{name: {Data: body}}, hooks)
}

func openEmpty(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "partyline.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// snapshot renders the whole database: every schema object and every row of every table.
func snapshot(t *testing.T, s *Store) []string {
	t.Helper()
	ctx := context.Background()
	var out []string
	rows, err := s.write.QueryContext(ctx, `SELECT type, name, COALESCE(sql, '') FROM sqlite_master ORDER BY type, name`)
	require.NoError(t, err)
	var tables []string
	for rows.Next() {
		var typ, name, def string
		require.NoError(t, rows.Scan(&typ, &name, &def))
		out = append(out, typ+" "+name+": "+def)
		if typ == "table" {
			tables = append(tables, name)
		}
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	for _, table := range tables {
		out = append(out, tableRows(t, s, table)...)
	}
	return out
}

// tableRows renders every row of table, in a stable order.
func tableRows(t *testing.T, s *Store, table string) []string {
	t.Helper()
	rows, err := s.write.QueryContext(context.Background(), fmt.Sprintf(`SELECT * FROM %q`, table))
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	require.NoError(t, err)
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		require.NoError(t, rows.Scan(ptrs...))
		parts := make([]string, len(cols))
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			parts[i] = fmt.Sprintf("%s=%v", cols[i], v)
		}
		out = append(out, table+" | "+strings.Join(parts, ", "))
	}
	require.NoError(t, rows.Err())
	// SELECT * has no defined order; sort for a stable comparison.
	sort.Strings(out)
	return out
}

func versions(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.write.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var v string
		require.NoError(t, rows.Scan(&v))
		out = append(out, v)
	}
	require.NoError(t, rows.Err())
	return out
}

var baseMigration = fstest.MapFS{
	"0001_base.sql": {Data: []byte(`CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL);
INSERT INTO notes (body) VALUES ('kept');`)},
}

func withFiles(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for k, v := range baseMigration {
		fsys[k] = v
	}
	for name, body := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return fsys
}

// MIG-05: two pending migrations where the second fails leave schema_migrations, tables and
// rows exactly as before; the first one is not kept either.
func TestMigratePendingFilesRollBackTogether(t *testing.T) {
	ctx := context.Background()
	s := openEmpty(t)
	require.NoError(t, s.migrate(ctx, baseMigration, nil))
	before := snapshot(t, s)

	err := s.migrate(ctx, withFiles(map[string]string{
		"0002_tags.sql":   `CREATE TABLE tags (name TEXT); INSERT INTO notes (body) VALUES ('added'); UPDATE notes SET body = 'changed' WHERE body = 'kept';`,
		"0003_broken.sql": `CREATE TABLE broken (x TEXT); INSERT INTO missing_table VALUES (1);`,
	}), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "0003_broken.sql")

	require.Equal(t, before, snapshot(t, s))
	require.Equal(t, []string{"0001_base"}, versions(t, s))
}

// MIG-05: a failing hook rolls back its own file and every earlier pending file.
func TestMigrateFailingHookRollsBackEverything(t *testing.T) {
	ctx := context.Background()
	s := openEmpty(t)
	require.NoError(t, s.migrate(ctx, baseMigration, nil))
	before := snapshot(t, s)
	boom := errors.New("boom")

	err := s.migrate(ctx, withFiles(map[string]string{
		"0002_tags.sql": `CREATE TABLE tags (name TEXT); INSERT INTO notes (body) VALUES ('added');`,
		"0003_more.sql": `ALTER TABLE notes ADD COLUMN extra TEXT NOT NULL DEFAULT '';`,
	}), map[string]Hook{
		"0002_tags": func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO tags (name) VALUES ('from-hook')`)
			return err
		},
		"0003_more": func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `UPDATE notes SET extra = 'x'`); err != nil {
				return err
			}
			return boom
		},
	})
	require.ErrorIs(t, err, boom)
	require.Contains(t, err.Error(), "0003_more.sql hook")

	require.Equal(t, before, snapshot(t, s))
	require.Equal(t, []string{"0001_base"}, versions(t, s))
}

// A hook runs right after its own file: it sees that file's schema, and the next file sees
// what the hook wrote. Hooks of applied versions never run again.
func TestMigrateRunsHookAfterItsFile(t *testing.T) {
	ctx := context.Background()
	s := openEmpty(t)
	var calls []string
	hooks := map[string]Hook{
		"0002_tags": func(ctx context.Context, tx *sql.Tx) error {
			calls = append(calls, "0002_tags")
			_, err := tx.ExecContext(ctx, `INSERT INTO tags (name) SELECT 'tag-' || body FROM notes`)
			return err
		},
	}
	fsys := withFiles(map[string]string{
		"0002_tags.sql":   `CREATE TABLE tags (name TEXT NOT NULL);`,
		"0003_copies.sql": `CREATE TABLE copies AS SELECT name FROM tags;`,
	})
	require.NoError(t, s.migrate(ctx, fsys, hooks))
	require.Equal(t, []string{"0002_tags"}, calls)
	require.Equal(t, []string{"0001_base", "0002_tags", "0003_copies"}, versions(t, s))
	require.Equal(t, []string{"copies | name=tag-kept"}, tableRows(t, s, "copies"))

	require.NoError(t, s.migrate(ctx, fsys, hooks))
	require.Equal(t, []string{"0002_tags"}, calls, "an applied version's hook does not run again")
	require.Equal(t, []string{"tags | name=tag-kept"}, tableRows(t, s, "tags"))
}

// A fresh database gets every embedded migration; migrating again changes nothing.
func TestMigrateFreshAndAlreadyMigratedDatabases(t *testing.T) {
	ctx := context.Background()
	s := openEmpty(t)
	require.NoError(t, s.Migrate(ctx))
	files, err := migrationFiles(migrations)
	require.NoError(t, err)
	want := make([]string, 0, len(files))
	for _, f := range files {
		want = append(want, strings.TrimSuffix(f, ".sql"))
	}
	require.Equal(t, want, versions(t, s))

	before := snapshot(t, s)
	require.NoError(t, s.Migrate(ctx))
	require.Equal(t, before, snapshot(t, s))
}
