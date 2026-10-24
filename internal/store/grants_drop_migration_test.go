package store

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// grantObjects lists the schema objects (tables, indexes, triggers, views) whose definition names the grants
// table, by name: the table itself and whatever hangs on it.
func (e *schemaEnv) grantObjects() []string {
	e.t.Helper()
	rows, err := e.s.write.Query(`SELECT name FROM sqlite_master WHERE sql LIKE '%grants%' ORDER BY name`)
	require.NoError(e.t, err)
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var name string
		require.NoError(e.t, rows.Scan(&name))
		out = append(out, name)
	}
	require.NoError(e.t, rows.Err())
	return out
}

// OUPG-01, ORG-02, AD-016: the grants table is gone once the database is migrated, with its index, and no
// other table, index, trigger or view names it. Until 0020 it is exactly the table and its index that do:
// nothing references the table, so dropping it takes nothing else with it.
func TestMigration0020DropsTheGrantsTable(t *testing.T) {
	ctx := context.Background()
	e := newSchemaEnv(t)
	require.Contains(t, versions(t, e.s), "0020_drop_grants", "the migration is applied by Migrate")
	require.Empty(t, e.grantObjects(), "no table, index, trigger or view names grants any more")
	require.Zero(t, e.count(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'grants'`))
	require.Zero(t, e.count(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'grants_subject'`))
	_, err := e.s.write.ExecContext(ctx, `SELECT 1 FROM grants`)
	require.ErrorContains(t, err, "no such table")

	before := openEmpty(t)
	require.NoError(t, before.MigrateBefore(ctx, "0020"))
	old := &schemaEnv{t: t, s: before}
	require.Equal(t, []string{"grants", "grants_subject"}, old.grantObjects(), "before 0020 only the table and its index name grants")
}

// OUPG-01 (design, "Go hook"): 0020 only drops the table. Run on a v0.5 database after 0019, with a row still in
// the table, it removes the table and its index and changes nothing else: every other table keeps every row
// (foreign keys are on, so a table that referenced grants would lose rows through the implicit delete of the drop).
func TestMigration0020ChangesNothingButTheGrantsTable(t *testing.T) {
	ctx := context.Background()
	e := orgFixture(t)
	require.NoError(t, e.s.MigrateBefore(ctx, "0020")) // 0019 and its hook: the grants are consumed
	require.Zero(t, e.count(`SELECT COUNT(*) FROM grants`))
	e.must(`INSERT INTO grants (resource_kind, resource_id, subject, level, created_by, created_at)
		VALUES ('room', 'r-ops', 'u-bob', 'post', 'u-alice', 1790000000999)`) // a row the drop must take along
	before := snapshot(t, e.s)

	require.NoError(t, e.s.applyMigration(ctx, "0020_drop_grants.sql"))
	after := snapshot(t, e.s)

	// What is left is what was there, minus the table, its indexes, its row and the version record of 0020.
	var kept []string
	for _, line := range before {
		switch {
		case strings.HasPrefix(line, "table grants: "), strings.HasPrefix(line, "index grants_subject: "),
			strings.HasPrefix(line, "index sqlite_autoindex_grants_"), strings.HasPrefix(line, "grants | "):
			continue // the table, its index, the index of its primary key and its row
		case strings.HasPrefix(line, "schema_migrations | "):
			continue
		}
		kept = append(kept, line)
	}
	var left []string
	for _, line := range after {
		if !strings.HasPrefix(line, "schema_migrations | ") {
			left = append(left, line)
		}
	}
	require.Equal(t, kept, left, "nothing but the grants table and its index changed")
	require.Equal(t, 5, e.count(`SELECT COUNT(*) FROM organizations`), "the organizations of 0019 are untouched")
	require.Equal(t, "0020_drop_grants", versions(t, e.s)[len(versions(t, e.s))-1])
}

// MIG-05, OUPG-01: a v0.5 database goes to organizations and loses its grants table in one transaction. When
// 0020 fails after dropping the table, 0019 - its tables, its columns and its hook's rows - is rolled back
// with it: the database is the v0.5 one again, grants included, and the next start migrates it in full.
func TestMigrations0019And0020ApplyInOneTransaction(t *testing.T) {
	ctx := context.Background()
	e := orgFixture(t)
	before := snapshot(t, e.s)
	require.Equal(t, 14, e.count(`SELECT COUNT(*) FROM grants`), "fixture sanity: a v0.5 database with its grants")

	failing := overlayMigration(t, "0020_drop_grants.sql", func(body string) string {
		return body + "\nINSERT INTO a_table_that_is_not_there VALUES (1);\n"
	})
	err := e.s.migrate(ctx, failing, hooks)
	require.ErrorContains(t, err, "0020_drop_grants.sql")

	require.Equal(t, before, snapshot(t, e.s), "schema, schema_migrations and every row are as they were")
	for _, table := range []string{"organizations", "positions", "org_members", "org_invitations"} {
		require.Empty(t, e.columnsOf(table), "0019 was rolled back: table %s is gone", table)
	}
	require.NotContains(t, versions(t, e.s), "0019_organizations")
	require.NotContains(t, versions(t, e.s), "0020_drop_grants")
	require.Equal(t, 14, e.count(`SELECT COUNT(*) FROM grants`), "the grants are back")

	require.NoError(t, e.s.Migrate(ctx))
	require.Equal(t, 5, e.count(`SELECT COUNT(*) FROM organizations`), "0019 applied")
	require.Empty(t, e.grantObjects(), "and 0020 dropped the table")
	require.Equal(t, []string{"0019_organizations", "0020_drop_grants"}, versions(t, e.s)[len(versions(t, e.s))-2:])
}
