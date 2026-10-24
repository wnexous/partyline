package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

// legacyColumns are the v0.3 identity columns migration 0016 drops (design, "Migration 0016"):
// names and archiving moved to the intelligence. agents.name and agents.project_id stay as
// documented dead columns (AD-012), so they are not listed.
var legacyColumns = map[string][]string{
	"users":  {"display_name"},
	"agents": {"display_name", "auto_name", "kind", "session_no", "archived_at", "session_key"},
}

// v03Fixture builds a database at schema 0014 holding testdata/v03_fixture.sql: users,
// agents (fixed, temporary, project sessions), projects with members, grants and cursors,
// connectors with a catch-all room, chats, identities, messages of every author kind,
// attachments, outbox rows, cursors and the auth tables.
func v03Fixture(t *testing.T) *schemaEnv {
	t.Helper()
	e := &schemaEnv{t: t, s: openMigratedBefore(t, "0015")}
	data, err := os.ReadFile("testdata/v03_fixture.sql")
	require.NoError(t, err)
	e.must(string(data))
	return e
}

// columnsOf returns the table's columns in order.
func (e *schemaEnv) columnsOf(table string) []string {
	e.t.Helper()
	rows, err := e.s.write.Query(`SELECT name FROM pragma_table_info(?)`, table)
	require.NoError(e.t, err)
	defer func() { _ = rows.Close() }()
	var cols []string
	for rows.Next() {
		var c string
		require.NoError(e.t, rows.Scan(&c))
		cols = append(cols, c)
	}
	require.NoError(e.t, rows.Err())
	return cols
}

// rowsOf renders every row of table restricted to cols, sorted.
func (e *schemaEnv) rowsOf(table string, cols []string) []string {
	e.t.Helper()
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = fmt.Sprintf("%q", c)
	}
	rows, err := e.s.write.Query(fmt.Sprintf(`SELECT %s FROM %q`, strings.Join(quoted, ", "), table))
	require.NoError(e.t, err)
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		require.NoError(e.t, rows.Scan(ptrs...))
		parts := make([]string, len(cols))
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			parts[i] = fmt.Sprintf("%s=%v", cols[i], v)
		}
		out = append(out, strings.Join(parts, " "))
	}
	require.NoError(e.t, rows.Err())
	sort.Strings(out)
	return out
}

// v03Tables returns every table of the v0.3 schema with its columns and rows.
func (e *schemaEnv) v03Tables() map[string][]string {
	e.t.Helper()
	rows, err := e.s.write.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name <> 'schema_migrations'`)
	require.NoError(e.t, err)
	var tables []string
	for rows.Next() {
		var n string
		require.NoError(e.t, rows.Scan(&n))
		tables = append(tables, n)
	}
	require.NoError(e.t, rows.Err())
	require.NoError(e.t, rows.Close())
	out := map[string][]string{}
	for _, table := range tables {
		out[table] = e.columnsOf(table)
	}
	return out
}

func without(rows []string, drop func(string) bool) []string {
	out := []string{}
	for _, r := range rows {
		if !drop(r) {
			out = append(out, r)
		}
	}
	return out
}

// MIG-01, MIG-03, MIG-05: upgrading the v0.3 fixture (0015 to 0019 apply in one transaction)
// keeps every row of every v0.3 table with all its columns, except what the upgrade replaces on
// purpose: the project rows the conversion replaces (project members become agent members,
// project grants, projects and their cursors go, agents lose their project), the legacy
// identity columns 0016 drops, whose values live in the intelligences
// (TestUpgradeV03Identities), what the connection proxy retires (0018, CID-21): the chats in
// rooms, the delivery tables and the catch-all and room switches
// (TestConnectionProxyUpgradeKeepsHistoryAndDropsTheRest), and the grants, which the organizations
// upgrade (0019, OUPG-01) turns into memberships (TestOrganizationsUpgrade...) before 0020 drops their
// table. Every room is a group whose history counts as read.
func TestUpgradeV03KeepsEveryRow(t *testing.T) {
	e := v03Fixture(t)
	all := e.v03Tables()
	for table, legacy := range legacyColumns {
		require.Subset(t, all[table], legacy, "fixture sanity: %s has its legacy columns before the upgrade", table)
	}
	gone := append(slices.Clone(droppedTables), "grants") // 0018 drops the delivery tables, 0020 the grants table
	// The columns the upgrade keeps: every v0.3 column but the legacy ones, the ones the proxy
	// upgrade drops and the ones it empties.
	tables := map[string][]string{}
	for table, cols := range all {
		if slices.Contains(gone, table) {
			continue
		}
		tables[table] = without(cols, func(c string) bool {
			return slices.Contains(legacyColumns[table], c) || slices.Contains(droppedColumns[table], c) || slices.Contains(clearedColumns[table], c)
		})
	}
	before := map[string][]string{}
	for table, cols := range tables {
		before[table] = e.rowsOf(table, cols)
	}
	require.Len(t, before["messages"], 13, "fixture sanity")
	agentCols := without(tables["agents"], func(c string) bool { return c == "project_id" })
	agentsBefore := e.rowsOf("agents", agentCols)

	require.NotContains(t, versions(t, e.s), "0015_intelligences", "fixture sanity: the database is a v0.3 one")
	require.NoError(t, e.s.Migrate(context.Background()))
	require.Subset(t, versions(t, e.s), []string{"0015_intelligences", "0016_drop_v03_identity", "0018_connection_proxy", "0019_organizations", "0020_drop_grants"}, "the fixture upgrades through 0015, 0016, 0018, 0019 and 0020")

	// 0016 drops the legacy columns and project_cursors, 0018 the delivery tables and the catch-all
	// and room switches, and no other column or table of v0.3.
	for table, cols := range all {
		if table == "project_cursors" || slices.Contains(gone, table) {
			continue
		}
		after := e.columnsOf(table)
		require.Subset(t, after, tables[table], "table %s keeps its other v0.3 columns", table)
		dropped := without(cols, func(c string) bool { return slices.Contains(after, c) })
		require.ElementsMatch(t, append(slices.Clone(legacyColumns[table]), droppedColumns[table]...), dropped, "table %s loses its legacy columns and no other", table)
	}
	for _, table := range gone {
		require.Empty(t, e.columnsOf(table), "table %s is dropped", table)
	}
	require.Empty(t, e.columnsOf("project_cursors"), "the project cursor table is dropped")
	require.NotEmpty(t, e.columnsOf("projects"), "the empty projects table stays (agents.project_id references it, AD-012)")
	require.Subset(t, e.columnsOf("agents"), []string{"name", "project_id"}, "dead columns SQLite cannot drop stay (AD-012)")

	changed := map[string]bool{"room_members": true, "projects": true, "project_cursors": true, "agents": true, "agent_cursors": true}
	for table, cols := range tables {
		if changed[table] {
			continue
		}
		require.Equal(t, before[table], e.rowsOf(table, cols), "table %s keeps every row and column", table)
	}

	newMember := func(r string) bool { return strings.Contains(r, "room_id=r-ops kind=agent member_id=a-pc2 ") }
	require.Equal(t, without(before["room_members"], func(r string) bool { return strings.Contains(r, "kind=project") || strings.Contains(r, "kind=chat ") }),
		without(e.rowsOf("room_members", tables["room_members"]), newMember),
		"every non-project, non-chat member keeps every column (the direct dev member of proj-claude-2 keeps its own settings)")
	settings := []string{"room_id", "kind", "member_id", "outbound", "added_by", "created_at", "display_name", "sign_outbound", "delivery", "keywords_json"}
	require.Equal(t, []string{
		`room_id=r-ops kind=agent member_id=a-pc2 outbound=0 added_by=u-alice created_at=1790000000025 display_name=Proj Bot sign_outbound=0 delivery=trusted keywords_json=["build","ci"]`,
	}, without(e.rowsOf("room_members", settings), func(r string) bool { return !newMember(r) }),
		"Proj's ops membership moves to proj-claude-2 (latest connected) with its settings")
	require.Equal(t, 11-3+1-3, e.count(`SELECT COUNT(*) FROM room_members`), "Empty's membership and Proj's dev membership go, and the three chats")

	require.Empty(t, e.rowsOf("projects", tables["projects"]))

	require.Equal(t, agentsBefore, e.rowsOf("agents", agentCols), "agents keep every other column")
	require.Zero(t, e.count(`SELECT COUNT(*) FROM agents WHERE project_id IS NOT NULL OR folder <> ''`), "agents leave their project and stay unlinked")

	require.Zero(t, e.count(`SELECT COUNT(*) FROM pragma_foreign_key_check`), "no foreign key is left dangling")
	require.Equal(t, "ok", e.str(`SELECT integrity_check FROM pragma_integrity_check`))

	require.Equal(t, []string{
		"agent_id=a-atlas room_id=r-inbox seq=3",
		"agent_id=a-atlas room_id=r-ops seq=7",
		"agent_id=a-bob-atlas room_id=r-bob seq=1",
		"agent_id=a-pc2 room_id=r-dev seq=2",
		"agent_id=a-pc2 room_id=r-ops seq=5",
	}, e.rowsOf("agent_cursors", tables["agent_cursors"]), "Proj's cursor moves to its latest session where it was further")

	baselines := map[string]int{}
	rows, err := e.s.write.Query(`SELECT id, read_baseline FROM rooms WHERE kind = 'group' AND dm_a IS NULL AND dm_b IS NULL`)
	require.NoError(t, err)
	for rows.Next() {
		var id string
		var seq int
		require.NoError(t, rows.Scan(&id, &seq))
		baselines[id] = seq
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Equal(t, map[string]int{"r-ops": 7, "r-inbox": 3, "r-dev": 2, "r-bob": 1}, baselines, "every room is a group; its history counts as read")
	require.Zero(t, e.count(`SELECT COUNT(*) FROM rooms WHERE kind = 'dm'`), "no DMs yet")
	require.Zero(t, e.count(`SELECT COUNT(*) FROM read_cursors`))
}

// MIG-01, MIG-03, MIG-04 on the fixture: handles and names, the converted project member and
// the people and authors.
func TestUpgradeV03Identities(t *testing.T) {
	e := v03Fixture(t)
	require.NoError(t, e.s.Migrate(context.Background()))
	got := e.intelligences()
	for id, want := range map[string]intel{
		"u-alice":     {"human", "Alice A.", "alice"},
		"u-bob":       {"human", "Bob.Smith", "bob-smith"},
		"u-pl":        {"human", "partyline", "partyline-2"},
		"a-atlas":     {"ai", "atlas", "atlas"},
		"a-pc":        {"ai", "Proj · Claude", "proj-claude"},
		"a-pc2":       {"ai", "Proj · Claude #2", "proj-claude-2"},
		"a-pc0":       {"ai", "Proj · Codex", "proj-codex"},
		"a-agent":     {"ai", "Scratch", "agent"},
		"a-bob-atlas": {"ai", "atlas", "atlas-2"},
	} {
		require.Equal(t, want, got[id], id)
	}
	maria := e.personOf("k-tg", "100")
	ze := e.personOf("k-wa", "100000000000002@lid")
	require.Equal(t, ze, e.personOf("k-wa", "5511999990002@s.whatsapp.net"))
	require.Equal(t, "Maria Silva", got[maria].Display)
	require.Equal(t, "Zé", got[ze].Display)
	require.Equal(t, 3, e.count(`SELECT COUNT(*) FROM people`), "maria, bob (linked) and zé")
	require.Equal(t, 3+6+3, len(got))

	authors := map[string]string{}
	rows, err := e.s.write.Query(`SELECT id, author_iid FROM messages`)
	require.NoError(t, err)
	for rows.Next() {
		var id, iid string
		require.NoError(t, rows.Scan(&id, &iid))
		authors[id] = iid
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Equal(t, map[string]string{
		"m-ops1": "u-alice", "m-ops2": "a-atlas", "m-ops3": "u-bob", "m-ops4": ze, "m-ops5": "", "m-ops6": "",
		"m-ops7": "a-pc2", "m-in1": maria, "m-in2": "a-atlas", "m-in3": maria, "m-dev1": "u-alice", "m-dev2": "a-pc2",
		"m-bob1": "u-bob",
	}, authors)
	require.Equal(t, maria, e.str(`SELECT person_id FROM chats WHERE id = 'c-tgdm'`))
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM chat_people WHERE chat_id = 'c-tgdm' AND person_id = ?`, maria))

	// The converted member keeps the name the project member gave the session's posts.
	require.Equal(t, "Proj Bot", e.str(`SELECT author_name FROM message_views WHERE id = 'm-ops7'`))
}

// MIG-05: a failure in the upgrade's Go hook (after its SQL and its own writes) leaves the
// v0.3 database exactly as it was; the next start migrates it normally.
func TestUpgradeV03RollsBackOnHookFailure(t *testing.T) {
	ctx := context.Background()
	e := v03Fixture(t)
	before := snapshot(t, e.s)
	boom := errors.New("forced failure")
	failing := map[string]Hook{}
	for v, h := range hooks {
		failing[v] = h
	}
	ran := false
	failing["0015_intelligences"] = func(ctx context.Context, tx *sql.Tx) error {
		if err := hooks["0015_intelligences"](ctx, tx); err != nil {
			return err
		}
		ran = true
		return boom
	}

	err := e.s.migrate(ctx, migrations, failing)
	require.ErrorIs(t, err, boom)
	require.True(t, ran, "the real hook ran before the failure")
	require.Equal(t, before, snapshot(t, e.s), "schema, schema_migrations and every row are unchanged")

	require.NoError(t, e.s.Migrate(ctx))
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM schema_migrations WHERE version = '0015_intelligences'`))
	require.Equal(t, 12, e.count(`SELECT COUNT(*) FROM intelligences`))
}

// v03AuthorNames renders message_views.author_name per message id.
func (e *schemaEnv) v03AuthorNames() map[string]string {
	e.t.Helper()
	out := map[string]string{}
	rows, err := e.s.write.Query(`SELECT id, author_name FROM message_views`)
	require.NoError(e.t, err)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, name string
		require.NoError(e.t, rows.Scan(&id, &name))
		out[id] = name
	}
	require.NoError(e.t, rows.Err())
	return out
}

// Design test-plan assertion 6, MIG-01: after 0016 recreated message_views without its
// transitional fallbacks, every fixture message is named as the v0.3 view named it. Two
// authors change on purpose: a chat sender linked to a user is that user (PEOP-02), and a
// person has one name, the latest the platform reported (MIG-04).
func TestUpgradeV03AuthorNamesSurvive(t *testing.T) {
	e := v03Fixture(t)
	want := map[string]string{
		"m-ops1": "Alice A.", "m-ops2": "Atlas Ops", "m-ops3": "Bobby", "m-ops4": "Zé", "m-ops5": "Team",
		"m-ops6": "Partyline", "m-ops7": "Proj Bot", "m-in1": "Maria", "m-in2": "atlas", "m-in3": "Maria Silva",
		"m-dev1": "Alice A.", "m-dev2": "Mine", "m-bob1": "Bob.Smith",
	}
	require.Equal(t, want, e.v03AuthorNames(), "fixture sanity: the v0.3 view names")

	require.NoError(t, e.s.Migrate(context.Background()))
	want["m-ops3"] = "Bob.Smith"  // the sender linked to bob is bob
	want["m-in1"] = "Maria Silva" // the person's latest name
	require.Equal(t, want, e.v03AuthorNames())

	type author struct{ Handle, Kind string }
	got := map[string]author{}
	rows, err := e.s.write.Query(`SELECT id, author_handle, author_ikind FROM message_views`)
	require.NoError(t, err)
	for rows.Next() {
		var id string
		var a author
		require.NoError(t, rows.Scan(&id, &a.Handle, &a.Kind))
		got[id] = a
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Equal(t, author{"alice", "human"}, got["m-ops1"])
	require.Equal(t, author{"atlas", "ai"}, got["m-ops2"])
	require.Equal(t, author{"proj-claude-2", "ai"}, got["m-ops7"])
	require.Equal(t, author{"bob-smith", "human"}, got["m-ops3"])
	require.Equal(t, author{}, got["m-ops5"], "a chat message without a sender has no intelligence")
	require.Equal(t, author{}, got["m-ops6"], "a system message has none")
}

// MIG-01: nothing the dropped name columns held is lost; the name every user and agent went by
// in v0.3 is the display name of its intelligence after the upgrade.
func TestUpgradeV03KeepsEveryNameInTheIntelligence(t *testing.T) {
	e := v03Fixture(t)
	v03Names := map[string]string{}
	for _, query := range []string{
		`SELECT id, COALESCE(NULLIF(display_name, ''), username) FROM users`,
		`SELECT id, COALESCE(NULLIF(display_name, ''), NULLIF(auto_name, ''), name) FROM agents`,
	} {
		rows, err := e.s.write.Query(query)
		require.NoError(t, err)
		for rows.Next() {
			var id, name string
			require.NoError(t, rows.Scan(&id, &name))
			v03Names[id] = name
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
	}
	require.Len(t, v03Names, 3+6, "fixture sanity: every user and agent")
	require.Equal(t, "Scratch", v03Names["a-agent"], "fixture sanity: a rename wins over the generated name")

	require.NoError(t, e.s.Migrate(context.Background()))
	got := e.intelligences()
	for id, name := range v03Names {
		require.Equal(t, name, got[id].Display, "the v0.3 name of %s", id)
	}
}

// MIG-01: an agent that v0.3 archived or named after a project session is listed like any other
// after the upgrade, with its handle (archiving is gone).
func TestUpgradeV03ListsEveryAgent(t *testing.T) {
	e := v03Fixture(t)
	require.NoError(t, e.s.Migrate(context.Background()))
	rows, err := e.s.R().ListVisibleAgents(context.Background(), "u-alice")
	require.NoError(t, err)
	got := map[string]string{}
	for _, r := range rows {
		got[r.Agent.ID] = r.Handle
	}
	require.Equal(t, map[string]string{
		"a-atlas": "atlas", "a-pc": "proj-claude", "a-pc2": "proj-claude-2", "a-pc0": "proj-codex",
		"a-agent": "agent", // v0.3 archived it (archived_at)
	}, got)
}

// overlayMigration returns the embedded migrations with one file's body replaced.
func overlayMigration(t *testing.T, name string, edit func(body string) string) fs.FS {
	t.Helper()
	files, err := migrationFiles(migrations)
	require.NoError(t, err)
	out := fstest.MapFS{}
	for _, f := range files {
		body, err := fs.ReadFile(migrations, f)
		require.NoError(t, err)
		if f == name {
			body = []byte(edit(string(body)))
		}
		out[f] = &fstest.MapFile{Data: body}
	}
	require.Contains(t, out, name, "the migration exists")
	return out
}

// MIG-05: a failure in 0016 - in its SQL after the columns were dropped, or in a step that runs
// right after it - leaves the v0.3 database exactly as it was: 0015 and its hook are rolled
// back too, because the whole upgrade is one transaction. The next start migrates normally.
func TestUpgradeV03RollsBackWhenTheCleanupFails(t *testing.T) {
	const cleanup = "0016_drop_v03_identity"
	boom := errors.New("forced failure")
	for name, tc := range map[string]struct {
		files fs.FS
		hooks func(t *testing.T) map[string]Hook
	}{
		"a statement after the drops fails": {
			files: overlayMigration(t, cleanup+".sql", func(body string) string {
				return body + "\nINSERT INTO no_such_table (id) VALUES (1);\n"
			}),
			hooks: func(*testing.T) map[string]Hook { return hooks },
		},
		"a step after the file fails": {
			files: migrations,
			hooks: func(t *testing.T) map[string]Hook {
				failing := map[string]Hook{}
				for v, h := range hooks {
					failing[v] = h
				}
				failing[cleanup] = func(ctx context.Context, tx *sql.Tx) error {
					var n int
					require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('agents') WHERE name = 'auto_name'`).Scan(&n))
					require.Zero(t, n, "the cleanup ran before the failure")
					return boom
				}
				return failing
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			e := v03Fixture(t)
			before := snapshot(t, e.s)

			err := e.s.migrate(ctx, tc.files, tc.hooks(t))
			require.Error(t, err)
			require.Contains(t, err.Error(), cleanup)
			require.Equal(t, before, snapshot(t, e.s), "schema, schema_migrations and every row are unchanged")
			require.NotContains(t, versions(t, e.s), "0015_intelligences")
			require.Positive(t, e.count(`SELECT COUNT(*) FROM pragma_table_info('agents') WHERE name = 'auto_name'`), "the legacy columns are back")

			require.NoError(t, e.s.Migrate(ctx))
			require.Subset(t, versions(t, e.s), []string{"0015_intelligences", cleanup})
			require.Zero(t, e.count(`SELECT COUNT(*) FROM pragma_table_info('agents') WHERE name = 'auto_name'`))
		})
	}
}

// MIG-01, MIG-05: 0016 on its own - the database of an earlier build that applied 0015 - drops
// the legacy columns and the empty project cursor table and changes nothing else: every other
// column of every table keeps every row (a rebuilt table would lose the rows that reference it,
// foreign_keys being on), and the database is consistent afterwards.
func TestCleanupAloneKeepsEveryRow(t *testing.T) {
	ctx := context.Background()
	e := v03Fixture(t)
	require.NoError(t, e.s.MigrateBefore(ctx, "0016"))
	require.Contains(t, versions(t, e.s), "0015_intelligences")
	require.NotContains(t, versions(t, e.s), "0016_drop_v03_identity")

	tables := e.v03Tables()
	kept := map[string][]string{}
	before := map[string][]string{}
	for table, cols := range tables {
		kept[table] = without(cols, func(c string) bool { return slices.Contains(legacyColumns[table], c) })
		before[table] = e.rowsOf(table, kept[table])
	}
	require.Len(t, before["messages"], 13, "fixture sanity")
	require.Len(t, before["agents"], 6, "fixture sanity")
	viewBefore := e.v03AuthorNames()

	require.NoError(t, e.s.MigrateBefore(ctx, "0017")) // 0016 alone: 0017 and 0018 have tests of their own

	for table := range tables {
		if table == "project_cursors" {
			continue
		}
		require.Equal(t, before[table], e.rowsOf(table, kept[table]), "table %s keeps every row", table)
	}
	require.Equal(t, viewBefore, e.v03AuthorNames(), "the recreated view names every author as before")
	require.Zero(t, e.count(`SELECT COUNT(*) FROM pragma_foreign_key_check`), "no foreign key is left dangling")
	require.Equal(t, "ok", e.str(`SELECT integrity_check FROM pragma_integrity_check`))
}
