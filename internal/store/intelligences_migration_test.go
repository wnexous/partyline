package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

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

// MIG-01, MIG-03: upgrading the v0.3 fixture keeps every row of every v0.3 table with all its
// v0.3 columns, except the project rows the conversion replaces: project members become
// agent members, project grants, projects and their cursors go, agents lose their project.
// Every room is a group whose history counts as read.
func TestUpgradeV03KeepsEveryRow(t *testing.T) {
	e := v03Fixture(t)
	tables := e.v03Tables()
	before := map[string][]string{}
	for table, cols := range tables {
		before[table] = e.rowsOf(table, cols)
	}
	require.Len(t, before["messages"], 13, "fixture sanity")
	agentCols := without(tables["agents"], func(c string) bool { return c == "project_id" })
	agentsBefore := e.rowsOf("agents", agentCols)

	require.NoError(t, e.s.Migrate(context.Background()))

	changed := map[string]bool{"room_members": true, "grants": true, "projects": true, "project_cursors": true, "agents": true, "agent_cursors": true}
	for table, cols := range tables {
		if changed[table] {
			continue
		}
		require.Equal(t, before[table], e.rowsOf(table, cols), "table %s keeps every row and column", table)
	}

	newMember := func(r string) bool { return strings.Contains(r, "room_id=r-ops kind=agent member_id=a-pc2 ") }
	require.Equal(t, without(before["room_members"], func(r string) bool { return strings.Contains(r, "kind=project") }),
		without(e.rowsOf("room_members", tables["room_members"]), newMember),
		"every non-project member keeps every column (the direct dev member of proj-claude-2 keeps its own settings)")
	settings := []string{"room_id", "kind", "member_id", "outbound", "added_by", "created_at", "display_name", "sign_outbound", "delivery", "keywords_json"}
	require.Equal(t, []string{
		`room_id=r-ops kind=agent member_id=a-pc2 outbound=0 added_by=u-alice created_at=1790000000025 display_name=Proj Bot sign_outbound=0 delivery=trusted keywords_json=["build","ci"]`,
	}, without(e.rowsOf("room_members", settings), func(r string) bool { return !newMember(r) }),
		"Proj's ops membership moves to proj-claude-2 (latest connected) with its settings")
	require.Equal(t, 11-3+1, e.count(`SELECT COUNT(*) FROM room_members`), "Empty's membership and Proj's dev membership go")

	require.Equal(t, without(before["grants"], func(r string) bool { return strings.Contains(r, "resource_kind=project") }),
		e.rowsOf("grants", tables["grants"]))
	require.Empty(t, e.rowsOf("projects", tables["projects"]))
	require.Empty(t, e.rowsOf("project_cursors", tables["project_cursors"]))

	require.Equal(t, agentsBefore, e.rowsOf("agents", agentCols), "agents keep every other column")
	require.Zero(t, e.count(`SELECT COUNT(*) FROM agents WHERE project_id IS NOT NULL OR folder <> ''`), "agents leave their project and stay unlinked")

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
	require.Equal(t, 3, e.count(`SELECT COUNT(*) FROM outbox WHERE purpose = 'room'`))
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
