package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// addUser inserts an account with its intelligence (display is the intelligence's display name),
// the way a database of schema 0018 holds one.
func (e *schemaEnv) addUser(id, username, display string, created int64) {
	e.t.Helper()
	e.must(`INSERT INTO users (id, username, password_hash, role, created_at) VALUES (?, ?, 'x', 'member', ?)`, id, username, created)
	e.must(`INSERT INTO intelligences (id, kind, display_name, handle, created_at, updated_at) VALUES (?, 'human', ?, ?, ?, ?)`,
		id, display, "h-"+id, created, created)
}

// orgFixture is the database of a v0.5 installation (schema 0018, the v0.4 data set of the
// connection proxy tests) plus what the organizations upgrade has to move: users who only hold
// grants or group memberships on other users' resources, grants of every kind and level, public
// grants, grants that point at nothing, and grants the owner holds on their own group and connection.
//
// Users: alice (admin of the instance, five AIs, three groups, three connections), Bob.Smith (one
// AI, one group, one connection), partyline (disabled), carol and dave (no resources of their own;
// dave owns one AI).
func orgFixture(t *testing.T) *schemaEnv {
	t.Helper()
	ctx := context.Background()
	e := v04Fixture(t)
	require.NoError(t, e.s.MigrateBefore(ctx, "0019"))
	require.Equal(t, "0018_connection_proxy", versions(t, e.s)[len(versions(t, e.s))-1], "fixture sanity: schema 0018")

	e.addUser("u-carol", "carol", "carol", 1790000000101)
	e.addUser("u-dave", "dave", "dave", 1790000000102)
	e.must(`INSERT INTO agents (id, owner_id, name, created_at, harness, machine, folder) VALUES ('a-dave', 'u-dave', 'a-dave', 1790000000103, 'claude', 'devbox', '/work/dave')`)
	e.must(`INSERT INTO intelligences (id, kind, display_name, handle, created_at, updated_at) VALUES ('a-dave', 'ai', 'Dave notes', 'dave-notes', 1790000000103, 1790000000103)`)

	// Group memberships next to the fixture's own: dave and Bob's AI are in alice's dev group, dave's AI in Bob's group.
	e.must(`INSERT INTO room_members (id, room_id, kind, member_id, added_by, created_at) VALUES
		('rm-dev-dave', 'r-dev', 'user', 'u-dave', 'u-alice', 1790000000120),
		('rm-dev-bobatlas', 'r-dev', 'agent', 'a-bob-atlas', 'u-alice', 1790000000121),
		('rm-bob-davebot', 'r-bob', 'agent', 'a-dave', 'u-bob', 1790000000122)`)

	// The fixture already holds: bob post on r-ops, bob view on r-inbox, everyone use on a-atlas, bob use on k-tg.
	e.must(`INSERT INTO grants (resource_kind, resource_id, subject, level, created_by, created_at) VALUES
		('room', 'r-bob', 'u-carol', 'manage', 'u-bob', 1790000000110),
		('agent', 'a-bob-atlas', 'u-carol', 'use', 'u-bob', 1790000000111),
		('connector', 'k-hook', 'u-alice', 'use', 'u-bob', 1790000000112),
		('room', 'r-bob', '*', 'view', 'u-bob', 1790000000113),
		('room', 'r-dm-maria', 'u-carol', 'post', 'u-alice', 1790000000114),
		('room', 'r-missing', 'u-dave', 'post', 'u-alice', 1790000000115),
		('room', 'r-ops', 'u-ghost', 'post', 'u-alice', 1790000000116),
		('connector', 'k-wa', 'u-alice', 'use', 'u-alice', 1790000000117),
		('room', 'r-dev', 'u-carol', 'post', 'u-alice', 1790000000118),
		('room', 'r-ops', 'u-alice', 'manage', 'u-alice', 1790000000119)`)
	require.Equal(t, 14, e.count(`SELECT COUNT(*) FROM grants`), "fixture sanity: four grants of the fixture and ten added")
	return e
}

// memberships renders every membership as "slug|identity|permission|position|inviter", sorted.
func (e *schemaEnv) memberships() []string {
	e.t.Helper()
	rows, err := e.s.write.Query(`SELECT o.slug, m.iid, m.permission, COALESCE(p.name, ''), COALESCE(m.inviter_iid, '')
		FROM org_members m JOIN organizations o ON o.id = m.org_id LEFT JOIN positions p ON p.id = m.position_id`)
	require.NoError(e.t, err)
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var slug, iid, permission, position, inviter string
		require.NoError(e.t, rows.Scan(&slug, &iid, &permission, &position, &inviter))
		out = append(out, strings.Join([]string{slug, iid, permission, position, inviter}, "|"))
	}
	require.NoError(e.t, rows.Err())
	slices.Sort(out)
	return out
}

// OUPG-01, ORG-02: the upgrade gives every user a personal organization where they are the admin
// and hold the root position "Admin", and moves everything they owned into it.
func TestOrganizationsUpgradeCreatesAPersonalOrganizationPerUser(t *testing.T) {
	e := orgFixture(t)
	require.NoError(t, e.s.Migrate(context.Background()))

	type org struct{ Name, Slug, By string }
	want := map[string]org{ // by owner
		"u-alice": {"Alice A.", "alice", "u-alice"},
		"u-bob":   {"Bob.Smith", "bob-smith", "u-bob"},
		"u-pl":    {"partyline", "partyline", "u-pl"}, // a disabled user has one too
		"u-carol": {"carol", "carol", "u-carol"},
		"u-dave":  {"dave", "dave", "u-dave"},
	}
	require.Equal(t, len(want), e.count(`SELECT COUNT(*) FROM organizations`), "one organization per user and no other")
	for user, w := range want {
		orgID := e.str(`SELECT default_org_id FROM users WHERE id = ?`, user)
		require.NotEmpty(t, orgID, "%s: the personal organization is the user's default", user)
		require.Equal(t, w.Name, e.str(`SELECT name FROM organizations WHERE id = ?`, orgID), user)
		require.Equal(t, w.Slug, e.str(`SELECT slug FROM organizations WHERE id = ?`, orgID), user)
		require.Equal(t, w.By, e.str(`SELECT created_by FROM organizations WHERE id = ?`, orgID), user)
		require.Zero(t, e.count(`SELECT COUNT(*) FROM organizations WHERE id = ? AND avatar_sha256 <> ''`, orgID))

		require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM positions WHERE org_id = ?`, orgID), "%s: one position", user)
		require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM positions WHERE org_id = ? AND name = 'Admin' AND parent_id IS NULL`, orgID), "%s: the root Admin", user)
		require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM org_members m JOIN positions p ON p.id = m.position_id
			WHERE m.org_id = ? AND m.iid = ? AND m.permission = 'admin' AND p.name = 'Admin' AND m.inviter_iid IS NULL`, orgID, user),
			"%s is admin of its organization and holds Admin", user)
	}
	require.Zero(t, e.count(`SELECT COUNT(*) FROM users WHERE default_org_id IS NULL`))
}

// OUPG-01, ORG-02: every AI, group and connection belongs to the organization of its owner and
// every AI and group to its owner as creator; a DM belongs to none. An AI becomes a member of its
// organization with no position, so its owner manages it as its creator (POS-03).
func TestOrganizationsUpgradeMovesEveryResourceIntoTheOwnersOrganization(t *testing.T) {
	e := orgFixture(t)
	require.NoError(t, e.s.Migrate(context.Background()))
	org := func(user string) string {
		id := e.str(`SELECT default_org_id FROM users WHERE id = ?`, user)
		require.NotEmpty(t, id, "%s has a personal organization (a missing one would equal a missing org_id)", user)
		return id
	}

	owners := map[string]string{ // AI → owner
		"a-atlas": "u-alice", "a-pc": "u-alice", "a-pc2": "u-alice", "a-pc0": "u-alice", "a-agent": "u-alice",
		"a-bob-atlas": "u-bob", "a-dave": "u-dave",
	}
	require.Equal(t, len(owners), e.count(`SELECT COUNT(*) FROM agents`), "fixture sanity: every AI is listed")
	for agent, owner := range owners {
		require.Equal(t, org(owner), e.str(`SELECT org_id FROM agents WHERE id = ?`, agent), "%s belongs to the organization of %s", agent, owner)
		require.Equal(t, owner, e.str(`SELECT creator_iid FROM agents WHERE id = ?`, agent), "%s was created by its owner", agent)
		require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM org_members WHERE org_id = ? AND iid = ? AND permission = 'member'
			AND position_id IS NULL AND inviter_iid IS NULL`, org(owner), agent), "%s is a member with no position", agent)
		require.Equal(t, e.count(`SELECT created_at FROM agents WHERE id = ?`, agent),
			e.count(`SELECT joined_at FROM org_members WHERE org_id = ? AND iid = ?`, org(owner), agent), "%s joined when it was created", agent)
	}

	groups := map[string]string{"r-ops": "u-alice", "r-inbox": "u-alice", "r-dev": "u-alice", "r-bob": "u-bob"}
	require.Equal(t, len(groups), e.count(`SELECT COUNT(*) FROM rooms WHERE kind = 'group'`), "fixture sanity: every group is listed")
	for room, owner := range groups {
		require.Equal(t, org(owner), e.str(`SELECT org_id FROM rooms WHERE id = ?`, room), room)
		require.Equal(t, owner, e.str(`SELECT creator_iid FROM rooms WHERE id = ?`, room), room)
	}
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM rooms WHERE kind = 'dm'`), "fixture sanity: the platform DM")
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM rooms WHERE kind = 'dm' AND org_id IS NULL AND creator_iid IS NULL`), "a DM belongs to no organization")

	connections := map[string]string{"k-tg": "u-alice", "k-wa": "u-alice", "k-dc": "u-alice", "k-hook": "u-bob"}
	require.Equal(t, len(connections), e.count(`SELECT COUNT(*) FROM connectors`), "fixture sanity: every connection is listed")
	for connection, owner := range connections {
		require.Equal(t, org(owner), e.str(`SELECT org_id FROM connectors WHERE id = ?`, connection), connection)
	}
	require.Equal(t, "a-atlas", e.str(`SELECT operator_iid FROM connectors WHERE id = 'k-tg'`), "an operator is an AI of the connection's organization (ORG-07)")
	require.Zero(t, e.count(`SELECT COUNT(*) FROM connectors c JOIN agents a ON a.id = c.operator_iid WHERE a.org_id <> c.org_id`))
	require.Zero(t, e.count(`SELECT COUNT(*) FROM agents WHERE org_id IS NULL`)+e.count(`SELECT COUNT(*) FROM connectors WHERE org_id IS NULL`)+
		e.count(`SELECT COUNT(*) FROM rooms WHERE kind = 'group' AND org_id IS NULL`), "exactly one organization for each AI, group and connection")
}

// OUPG-01: a grantee joins the granter's organization as a member with no position, whatever the
// level and the resource; a manage or post room grantee also becomes a member of the room, a view one
// does not; a public grant, a grant on a DM and grants that point at nothing add nobody; a group
// member of another user joins the group's organization. Group memberships stay.
func TestOrganizationsUpgradeTurnsGrantsAndGroupMembersIntoMemberships(t *testing.T) {
	e := orgFixture(t)
	roomMembersBefore := e.rowsOf("room_members", e.columnsOf("room_members"))
	require.NoError(t, e.s.Migrate(context.Background()))

	// The whole membership table: every personal admin, the owners' AIs, and exactly the extra
	// members the grants and the group members explain.
	require.ElementsMatch(t, []string{
		"alice|a-agent|member||", "alice|a-atlas|member||", "alice|a-bob-atlas|member||", "alice|a-pc|member||",
		"alice|a-pc0|member||", "alice|a-pc2|member||",
		"alice|u-alice|admin|Admin|",
		"alice|u-bob|member||",                                        // post on ops, view on inbox, use on a connection, user member of ops
		"alice|u-carol|member||",                                      // post on dev
		"alice|u-dave|member||",                                       // only a user member of the dev group
		"bob-smith|a-bob-atlas|member||", "bob-smith|a-dave|member||", // the owner's AI; dave's AI, a member of Bob's group
		"bob-smith|u-alice|member||", // use on Bob's connection
		"bob-smith|u-bob|admin|Admin|",
		"bob-smith|u-carol|member||", // manage on Bob's group, use on Bob's AI
		"carol|u-carol|admin|Admin|",
		"dave|a-dave|member||", "dave|u-dave|admin|Admin|",
		"partyline|u-pl|admin|Admin|",
	}, e.memberships(), "no one gets a membership from a public grant, a DM grant or a grant that points at nothing")
	require.Zero(t, e.count(`SELECT COUNT(*) FROM org_members WHERE iid = 'u-ghost'`), "a grant to a user that does not exist adds nobody")

	// Room members: the post and manage grantees join the room as users; a view grantee does not.
	rows := e.rowsOf("room_members", e.columnsOf("room_members"))
	added := without(rows, func(r string) bool { return slices.Contains(roomMembersBefore, r) })
	require.Len(t, added, 2, "the only room members the upgrade adds are carol, a manage grantee of Bob's group and a post grantee of dev: %v", added)
	slices.SortFunc(added, func(a, b string) int {
		return strings.Compare(a[strings.Index(a, "room_id="):], b[strings.Index(b, "room_id="):])
	})
	require.Contains(t, added[0], "room_id=r-bob kind=user member_id=u-carol outbound=1 added_by=u-bob created_at=1790000000110 display_name= sign_outbound=1 delivery=all keywords_json=[]",
		"added by the granter when granted, with the column defaults")
	require.Contains(t, added[1], "room_id=r-dev kind=user member_id=u-carol outbound=1 added_by=u-alice created_at=1790000000118 display_name= sign_outbound=1 delivery=all keywords_json=[]")
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM room_members WHERE room_id = 'r-ops' AND kind = 'user' AND member_id = 'u-bob'`), "bob posts on ops: he was a member already, and stays one row")
	require.Zero(t, e.count(`SELECT COUNT(*) FROM room_members WHERE room_id = 'r-inbox' AND kind = 'user'`), "a view grantee is an organization member but not a room member")
	require.Zero(t, e.count(`SELECT COUNT(*) FROM room_members WHERE room_id = 'r-dm-maria' AND kind = 'user'`), "a DM takes no grantee")
	require.Zero(t, e.count(`SELECT COUNT(*) FROM room_members WHERE room_id = 'r-bob' AND member_id = 'u-alice'`))
	require.Zero(t, e.count(`SELECT COUNT(*) FROM room_members WHERE room_id = 'r-ops' AND member_id = 'u-alice'`), "the owner who granted herself manage is the creator, not a room member")
	for _, kept := range roomMembersBefore {
		require.Contains(t, rows, kept, "group memberships stay")
	}

	// The grants are consumed: the table stays (0020 drops it) but holds nothing.
	require.Zero(t, e.count(`SELECT COUNT(*) FROM grants`))
}

// OUPG-01, MIG-01: the upgrade moves ownership and nothing else. Every row of every table keeps
// every column it had (messages, attachments, cursors, people, identities and the rest), the new
// columns are the only difference, and the database is consistent.
func TestOrganizationsUpgradeKeepsHistoryAndEveryOtherRow(t *testing.T) {
	e := orgFixture(t)
	tables := e.v03Tables()
	before := map[string][]string{}
	for table, cols := range tables {
		before[table] = e.rowsOf(table, cols)
	}
	require.Len(t, before["messages"], 14, "fixture sanity: the thirteen messages of the fixture and the platform DM's")
	require.Len(t, before["attachments"], 2, "fixture sanity")
	require.Len(t, before["agent_cursors"], 5, "fixture sanity")

	require.NoError(t, e.s.Migrate(context.Background()))

	for table, cols := range tables {
		if table == "grants" || table == "room_members" {
			continue // consumed, and one added member: asserted on their own
		}
		require.Equal(t, before[table], e.rowsOf(table, cols), "table %s keeps every row and every column it had", table)
	}
	require.Equal(t, 14, e.count(`SELECT COUNT(*) FROM messages`))
	require.Equal(t, 2, e.count(`SELECT COUNT(*) FROM attachments`))
	require.Equal(t, 5, e.count(`SELECT COUNT(*) FROM agent_cursors`))
	require.Zero(t, e.count(`SELECT COUNT(*) FROM pragma_foreign_key_check`), "no foreign key is left dangling")
	require.Equal(t, "ok", e.str(`SELECT integrity_check FROM pragma_integrity_check`))
	require.Subset(t, versions(t, e.s), []string{"0018_connection_proxy", "0019_organizations"})
}

// A start after the upgrade changes nothing: the Go step belongs to the version that is recorded.
func TestOrganizationsUpgradeRunsOnce(t *testing.T) {
	ctx := context.Background()
	e := orgFixture(t)
	require.NoError(t, e.s.Migrate(ctx))
	before := snapshot(t, e.s)
	require.NoError(t, e.s.Migrate(ctx))
	require.Equal(t, before, snapshot(t, e.s))
}

// OUPG-01: a database with no user, such as a fresh install, gets no organization.
func TestOrganizationsUpgradeOfAnEmptyDatabaseCreatesNothing(t *testing.T) {
	e := newSchemaEnv(t)
	for _, table := range []string{"organizations", "positions", "org_members", "org_invitations"} {
		require.Zero(t, e.count(`SELECT COUNT(*) FROM `+table), table)
	}
}

// OUPG-01, ORG-01: the personal organization is named after the user - the display name, else the
// username - cut to 100 characters (code points, not bytes) with control characters dropped; its slug
// is the first free one derived from the username: bob, bob-2, bob-3 for the usernames bob, Bob_ and
// BOB__, "org" when no Latin letter is left, a cut at 36 bytes so a suffix fits, ties by creation time
// and then by id. The upgrade never fails on such names.
func TestOrganizationsUpgradeNamesAndAddressesPersonalOrganizations(t *testing.T) {
	e := &schemaEnv{t: t, s: openMigratedBefore(t, "0019")}
	long := strings.Repeat("x", 40)
	for i, u := range []struct{ id, username, display string }{
		{"u-1", "bob", "Bobby"},
		{"u-2", "Bob_", ""}, // a blank display name falls back to the username
		{"u-3", "BOB__", "Bob Three"},
		{"u-4", long, long},
		{"u-5", long + "y", "Second long"},
		{"u-6", "李雷", "李雷"},
		{"u-7", "韩梅梅", "韩梅梅"},
		{"u-8", "lengthy", strings.Repeat("é", 150)}, // 150 code points, 300 bytes
		{"u-9", "controlled", "Ana\x00 Maria\n"},
		{"u-10", "42", "42"},
	} {
		e.addUser(u.id, u.username, u.display, 1790000000200+int64(i))
	}
	// Two users created at the same moment: the smaller id comes first.
	e.addUser("u-tie-b", "sam", "Sam B", 1790000000300)
	e.addUser("u-tie-a", "Sam_", "Sam A", 1790000000300)

	require.NoError(t, e.s.Migrate(context.Background()))

	type org struct{ name, slug string }
	got := map[string]org{}
	rows, err := e.s.write.Query(`SELECT u.id, o.name, o.slug FROM users u JOIN organizations o ON o.id = u.default_org_id`)
	require.NoError(t, err)
	for rows.Next() {
		var id string
		var o org
		require.NoError(t, rows.Scan(&id, &o.name, &o.slug))
		got[id] = o
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())

	require.Equal(t, org{"Bobby", "bob"}, got["u-1"])
	require.Equal(t, org{"Bob_", "bob-2"}, got["u-2"])
	require.Equal(t, org{"Bob Three", "bob-3"}, got["u-3"])
	stem := strings.Repeat("x", 36)
	require.Equal(t, org{long, stem}, got["u-4"], "the slug is cut to 36 bytes, the name is kept")
	require.Equal(t, org{"Second long", stem + "-2"}, got["u-5"], "a suffix after the cut")
	require.Equal(t, org{"李雷", "org"}, got["u-6"])
	require.Equal(t, org{"韩梅梅", "org-2"}, got["u-7"])
	require.Equal(t, strings.Repeat("é", 100), got["u-8"].name, "a name is cut to 100 code points")
	require.Equal(t, 100, utf8.RuneCountInString(got["u-8"].name))
	require.Equal(t, "lengthy", got["u-8"].slug)
	require.Equal(t, org{"Ana Maria", "controlled"}, got["u-9"], "control characters are dropped")
	require.Equal(t, org{"42", "42"}, got["u-10"], "a slug may start with a digit")
	require.Equal(t, org{"Sam A", "sam"}, got["u-tie-a"])
	require.Equal(t, org{"Sam B", "sam-2"}, got["u-tie-b"])
	require.Equal(t, len(got), e.count(`SELECT COUNT(DISTINCT slug) FROM organizations`), "every slug is unique")
}

// MIG-05: a failure in the Go step of 0019 - after its SQL and after its own writes - leaves the v0.5
// database exactly as it was: no organization table or column remains, and the next start migrates it.
func TestOrganizationsUpgradeRollsBackOnHookFailure(t *testing.T) {
	ctx := context.Background()
	e := orgFixture(t)
	before := snapshot(t, e.s)
	boom := errors.New("forced failure")
	failing := map[string]Hook{}
	for v, h := range hooks {
		failing[v] = h
	}
	ran := false
	require.Contains(t, hooks, "0019_organizations", "the upgrade has its Go step")
	failing["0019_organizations"] = func(ctx context.Context, tx *sql.Tx) error {
		if err := hooks["0019_organizations"](ctx, tx); err != nil {
			return err
		}
		var n int
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM organizations`).Scan(&n))
		require.Positive(t, n, "the real step created the organizations before the failure")
		ran = true
		return boom
	}

	err := e.s.migrate(ctx, migrations, failing)
	require.ErrorIs(t, err, boom)
	require.ErrorContains(t, err, "0019_organizations.sql hook")
	require.True(t, ran)
	require.Equal(t, before, snapshot(t, e.s), "schema, schema_migrations and every row are unchanged")
	for _, table := range []string{"organizations", "positions", "org_members", "org_invitations"} {
		require.Empty(t, e.columnsOf(table), "table %s is gone", table)
	}
	for table, col := range map[string]string{"agents": "org_id", "rooms": "org_id", "connectors": "org_id", "users": "default_org_id"} {
		require.NotContains(t, e.columnsOf(table), col, "%s.%s is gone", table, col)
	}
	require.Equal(t, 14, e.count(`SELECT COUNT(*) FROM grants`), "the grants are back")

	require.NoError(t, e.s.Migrate(ctx))
	require.Equal(t, 5, e.count(`SELECT COUNT(*) FROM organizations`))
	require.Zero(t, e.count(`SELECT COUNT(*) FROM grants`))
}
