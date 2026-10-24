package store

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
)

// org inserts an organization.
func (e *schemaEnv) org(id, slug string) error {
	return e.exec(`INSERT INTO organizations (id, name, slug, created_at, updated_at) VALUES (?, ?, ?, 1, 1)`, id, "Org "+id, slug)
}

// user inserts an account with its intelligence (the handle is the username).
func (e *schemaEnv) user(id, username string) {
	e.t.Helper()
	e.must(`INSERT INTO users (id, username, password_hash, role, created_at) VALUES (?, ?, 'x', 'member', 1)`, id, username)
	require.NoError(e.t, e.intelligence(id, "human", username))
}

// position inserts a position of the organization.
func (e *schemaEnv) position(id, org, name, parent string) error {
	var p any
	if parent != "" {
		p = parent
	}
	return e.exec(`INSERT INTO positions (id, org_id, name, parent_id, created_at) VALUES (?, ?, ?, ?, 1)`, id, org, name, p)
}

// member inserts a membership with no position and no inviter.
func (e *schemaEnv) member(org, iid, permission string) error {
	return e.exec(`INSERT INTO org_members (org_id, iid, permission, joined_at) VALUES (?, ?, ?, 1)`, org, iid, permission)
}

// indexes returns the names of the table's indexes with whether each is unique.
func (e *schemaEnv) indexes(table string) map[string]bool {
	e.t.Helper()
	rows, err := e.s.write.Query(`SELECT name, "unique" FROM pragma_index_list(?)`, table)
	require.NoError(e.t, err)
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		var unique bool
		require.NoError(e.t, rows.Scan(&name, &unique))
		out[name] = unique
	}
	require.NoError(e.t, rows.Err())
	return out
}

// ORG-02, OUPG-01 (schema): migration 0019 creates the four organization tables with the design's
// columns, adds the ownership columns to agents, rooms, connectors and users, and creates every index.
func TestOrganizationsSchemaHasEveryTableColumnAndIndex(t *testing.T) {
	e := newSchemaEnv(t)
	for table, cols := range map[string][]string{
		"organizations":   {"id", "name", "slug", "avatar_sha256", "created_by", "created_at", "updated_at"},
		"positions":       {"id", "org_id", "name", "parent_id", "created_at"},
		"org_members":     {"org_id", "iid", "permission", "position_id", "inviter_iid", "joined_at"},
		"org_invitations": {"id", "org_id", "invitee_iid", "permission", "position_id", "invited_by", "created_at", "expires_at"},
	} {
		require.Equal(t, cols, e.columnsOf(table), "table %s", table)
	}
	for table, col := range map[string]string{
		"agents": "org_id", "rooms": "org_id", "connectors": "org_id", "users": "default_org_id",
	} {
		require.Contains(t, e.columnsOf(table), col, "%s.%s", table, col)
	}
	require.Contains(t, e.columnsOf("agents"), "creator_iid")
	require.Contains(t, e.columnsOf("rooms"), "creator_iid")

	// index name → unique, per table
	for table, want := range map[string]map[string]bool{
		"organizations":   {"organizations_slug": true},
		"positions":       {"positions_name": true, "positions_parent": false},
		"org_members":     {"org_members_iid": false, "org_members_inviter": false, "org_members_position": false},
		"org_invitations": {"org_invitations_invitee": false},
		"agents":          {"agents_org": false},
		"rooms":           {"rooms_org": false},
		"connectors":      {"connectors_org": false},
	} {
		got := e.indexes(table)
		for name, unique := range want {
			require.Contains(t, got, name, "index %s on %s", name, table)
			require.Equal(t, unique, got[name], "index %s on %s is unique", name, table)
		}
	}
	// One membership per (organization, identity) and one pending invitation per invitee are
	// primary key and unique constraints, not indexes by name.
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM pragma_index_list('org_members') WHERE origin = 'pk'`))
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM pragma_index_list('org_invitations') WHERE origin = 'u'`))
}

// OMEM-03: one identity has at most one membership, so at most one position, per organization; it
// can be a member of several organizations.
func TestOrgMembershipIsUniquePerIdentityAndOrganization(t *testing.T) {
	e := newSchemaEnv(t)
	e.user("u1", "alice")
	e.user("u2", "bob")
	require.NoError(t, e.org("o1", "acme"))
	require.NoError(t, e.org("o2", "globex"))

	require.NoError(t, e.member("o1", "u1", "admin"))
	require.Error(t, e.member("o1", "u1", "member"), "a second membership of the same identity in the same organization")
	require.Error(t, e.member("o1", "u1", "admin"), "not even with the same permission")
	require.NoError(t, e.member("o2", "u1", "guest"), "the same identity in another organization")
	require.NoError(t, e.member("o1", "u2", "member"), "another identity in the first organization")
	require.Equal(t, 3, e.count(`SELECT COUNT(*) FROM org_members`))
}

// OMEM-04 (data): a membership and an invitation carry admin, member or guest and nothing else.
func TestOrgPermissionsAreAdminMemberOrGuest(t *testing.T) {
	e := newSchemaEnv(t)
	require.NoError(t, e.org("o1", "acme"))
	for i, p := range []string{"admin", "member", "guest"} {
		id := "u" + string(rune('1'+i))
		e.user(id, "user"+id)
		require.NoError(t, e.member("o1", id, p), "membership as %s", p)
		require.NoError(t, e.exec(`INSERT INTO org_invitations (id, org_id, invitee_iid, permission, invited_by, created_at, expires_at)
			VALUES (?, 'o1', ?, ?, 'u1', 1, 2)`, "inv-"+p, id, p), "invitation as %s", p)
	}
	e.user("u9", "nine")
	for _, bad := range []string{"owner", "Admin", "", "viewer"} {
		require.Error(t, e.member("o1", "u9", bad), "membership as %q", bad)
		require.Error(t, e.exec(`INSERT INTO org_invitations (id, org_id, invitee_iid, permission, invited_by, created_at, expires_at)
			VALUES ('bad', 'o1', 'u9', ?, 'u1', 1, 2)`, bad), "invitation as %q", bad)
	}
	require.Error(t, e.exec(`UPDATE org_members SET permission = 'owner' WHERE iid = 'u1'`))
}

// OMEM-01: inviting again updates the pending invitation instead of adding a second one.
func TestOrgInvitationIsUniquePerInvitee(t *testing.T) {
	e := newSchemaEnv(t)
	e.user("u1", "alice")
	e.user("u2", "bob")
	require.NoError(t, e.org("o1", "acme"))
	require.NoError(t, e.org("o2", "globex"))
	invite := func(id, org string) error {
		return e.exec(`INSERT INTO org_invitations (id, org_id, invitee_iid, permission, invited_by, created_at, expires_at)
			VALUES (?, ?, 'u2', 'member', 'u1', 1, 2)`, id, org)
	}
	require.NoError(t, invite("i1", "o1"))
	require.Error(t, invite("i2", "o1"), "a second invitation of the same invitee to the same organization")
	require.NoError(t, invite("i3", "o2"), "the same invitee, another organization")
}

// OMEM-01 (data): a guest's inviter is recorded, and it empties when the inviter's intelligence is
// deleted; the guest stays a member.
func TestOrgMemberInviterBecomesNullWhenTheInviterIsDeleted(t *testing.T) {
	e := newSchemaEnv(t)
	e.user("u1", "inviter")
	e.user("u2", "guest")
	e.user("u3", "other")
	require.NoError(t, e.org("o1", "acme"))
	require.NoError(t, e.member("o1", "u1", "member"))
	e.must(`INSERT INTO org_members (org_id, iid, permission, inviter_iid, joined_at) VALUES ('o1', 'u2', 'guest', 'u1', 1)`)
	e.must(`INSERT INTO org_members (org_id, iid, permission, inviter_iid, joined_at) VALUES ('o1', 'u3', 'guest', 'u1', 1)`)
	require.Equal(t, "u1", e.str(`SELECT inviter_iid FROM org_members WHERE iid = 'u2'`), "fixture sanity")
	require.Error(t, e.exec(`UPDATE org_members SET inviter_iid = 'missing' WHERE iid = 'u2'`), "the inviter must be an intelligence")

	e.must(`DELETE FROM users WHERE id = 'u1'`) // the trigger deletes its intelligence

	require.Equal(t, 2, e.count(`SELECT COUNT(*) FROM org_members WHERE org_id = 'o1'`), "the guests stay, the inviter's own membership goes")
	require.Zero(t, e.count(`SELECT COUNT(*) FROM org_members WHERE iid = 'u1'`))
	require.Equal(t, 2, e.count(`SELECT COUNT(*) FROM org_members WHERE inviter_iid IS NULL AND permission = 'guest'`))
}

// POS-01 (data): a position name is unique within its organization ignoring case, so --position
// <name> is unambiguous; another organization may reuse the name.
func TestPositionNamesAreUniquePerOrganizationIgnoringCase(t *testing.T) {
	e := newSchemaEnv(t)
	require.NoError(t, e.org("o1", "acme"))
	require.NoError(t, e.org("o2", "globex"))

	require.NoError(t, e.position("p1", "o1", "Lead", ""))
	require.Error(t, e.position("p2", "o1", "Lead", ""), "the same name")
	require.Error(t, e.position("p3", "o1", "lead", ""), "the same name in lower case")
	require.Error(t, e.position("p4", "o1", "LEAD", "p1"), "the same name below another position")
	require.NoError(t, e.position("p5", "o1", "Leader", "p1"), "a different name")

	require.NoError(t, e.position("a1", "o1", "Admin", ""))
	require.NoError(t, e.position("a2", "o2", "Admin", ""), "two organizations can each have Admin")
	n, err := e.s.R().CountPositionName(context.Background(), db.CountPositionNameParams{OrgID: "o1", Name: "ADMIN"})
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "a lookup by name ignores case")
}

// ORG-01 (data): an organization address (slug) is unique across the instance ignoring case.
func TestOrganizationSlugsAreUniqueIgnoringCase(t *testing.T) {
	e := newSchemaEnv(t)
	require.NoError(t, e.org("o1", "acme"))
	require.Error(t, e.org("o2", "acme"))
	require.Error(t, e.org("o3", "ACME"))
	require.NoError(t, e.org("o4", "acme-2"))

	o, err := e.s.R().GetOrganizationBySlug(context.Background(), "Acme-2")
	require.NoError(t, err)
	require.Equal(t, "o4", o.ID, "a lookup by slug ignores case")
}

// POS-09 (data): deleting a position empties the references to it: its children become roots and
// its holders lose the position. The service moves them to the parent first; this is the net under it.
func TestDeletingAPositionEmptiesItsReferences(t *testing.T) {
	e := newSchemaEnv(t)
	e.user("u1", "alice")
	require.NoError(t, e.org("o1", "acme"))
	require.NoError(t, e.position("root", "o1", "Admin", ""))
	require.NoError(t, e.position("child", "o1", "Lead", "root"))
	e.must(`INSERT INTO org_members (org_id, iid, permission, position_id, joined_at) VALUES ('o1', 'u1', 'admin', 'root', 1)`)
	e.must(`INSERT INTO org_invitations (id, org_id, invitee_iid, permission, position_id, invited_by, created_at, expires_at)
		VALUES ('i1', 'o1', 'u1', 'member', 'root', 'u1', 1, 2)`)

	e.must(`DELETE FROM positions WHERE id = 'root'`)

	require.Zero(t, e.count(`SELECT COUNT(*) FROM positions WHERE id = 'child' AND parent_id IS NOT NULL`), "the child became a root")
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM org_members WHERE iid = 'u1' AND position_id IS NULL`), "the holder lost the position, not the membership")
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM org_invitations WHERE id = 'i1' AND position_id IS NULL`), "a pending invitation keeps its permission and loses the position")
}

// ORG-06 (data): the foreign keys of the ownership columns have no ON DELETE action, so deleting an
// organization that still owns an AI, a group or a connection fails in the database as well as in
// the service. Each resource alone is enough.
func TestAnOrganizationThatOwnsSomethingCannotBeDeleted(t *testing.T) {
	owns := map[string]string{
		"an AI":   `INSERT INTO agents (id, owner_id, name, created_at, org_id) VALUES ('a1', 'u1', 'notes', 1, 'o1')`,
		"a group": `INSERT INTO rooms (id, owner_id, name, created_at, updated_at, org_id) VALUES ('r1', 'u1', 'ops', 1, 1, 'o1')`,
		"a connection": `INSERT INTO connectors (id, owner_id, kind, name, created_at, updated_at, org_id)
			VALUES ('k1', 'u1', 'telegram', 'bot', 1, 1, 'o1')`,
	}
	for what, insert := range owns {
		t.Run(what, func(t *testing.T) {
			e := newSchemaEnv(t)
			e.user("u1", "alice")
			require.NoError(t, e.org("o1", "acme"))
			require.NoError(t, e.position("p1", "o1", "Admin", ""))
			require.NoError(t, e.member("o1", "u1", "admin"))
			e.must(insert)

			require.Error(t, e.exec(`DELETE FROM organizations WHERE id = 'o1'`), "the organization owns %s", what)

			require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM organizations WHERE id = 'o1'`))
			require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM positions WHERE org_id = 'o1'`), "nothing cascaded")
			require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM org_members WHERE org_id = 'o1'`), "nothing cascaded")
		})
	}
}

// ORG-06, OMEM-06 (data): an organization that owns nothing deletes with its positions, members and
// invitations; the identities themselves and a user's default organization pointing at it stay.
func TestAnEmptyOrganizationDeletesWithItsPositionsMembersAndInvitations(t *testing.T) {
	e := newSchemaEnv(t)
	e.user("u1", "alice")
	e.user("u2", "bob")
	require.NoError(t, e.org("o1", "acme"))
	require.NoError(t, e.org("o2", "globex"))
	require.NoError(t, e.position("p1", "o1", "Admin", ""))
	require.NoError(t, e.position("p2", "o1", "Lead", "p1"))
	require.NoError(t, e.position("q1", "o2", "Admin", ""))
	e.must(`INSERT INTO org_members (org_id, iid, permission, position_id, joined_at) VALUES ('o1', 'u1', 'admin', 'p1', 1), ('o2', 'u1', 'member', 'q1', 1)`)
	e.must(`INSERT INTO org_invitations (id, org_id, invitee_iid, permission, invited_by, created_at, expires_at) VALUES ('i1', 'o1', 'u2', 'guest', 'u1', 1, 2)`)
	e.must(`UPDATE users SET default_org_id = 'o1' WHERE id = 'u1'`)

	e.must(`DELETE FROM organizations WHERE id = 'o1'`)

	require.Zero(t, e.count(`SELECT COUNT(*) FROM positions WHERE org_id = 'o1'`))
	require.Zero(t, e.count(`SELECT COUNT(*) FROM org_members WHERE org_id = 'o1'`))
	require.Zero(t, e.count(`SELECT COUNT(*) FROM org_invitations WHERE org_id = 'o1'`))
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM org_members WHERE org_id = 'o2'`), "another organization is untouched")
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM positions WHERE org_id = 'o2'`))
	require.Equal(t, 2, e.count(`SELECT COUNT(*) FROM users`), "the identities stay")
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM users WHERE id = 'u1' AND default_org_id IS NULL`), "the default organization is cleared")
}

// ORG-02 (data): a DM belongs to no organization; AIs and groups point at one and at the identity that
// created them, which empties when that identity is deleted.
func TestOwnershipColumnsAreOptionalAndReleaseTheirCreator(t *testing.T) {
	e := newSchemaEnv(t)
	e.user("u1", "alice")
	e.user("u2", "bob")
	require.NoError(t, e.org("o1", "acme"))

	e.must(`INSERT INTO rooms (id, owner_id, name, created_at, updated_at, org_id, creator_iid) VALUES ('g1', 'u1', 'ops', 1, 1, 'o1', 'u2')`)
	require.NoError(t, e.dm("d1", "u1", "u1", "u2"), "a DM is created without an organization")
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM rooms WHERE id = 'd1' AND org_id IS NULL AND creator_iid IS NULL`))
	e.must(`INSERT INTO agents (id, owner_id, name, created_at, org_id, creator_iid) VALUES ('a1', 'u1', 'notes', 1, 'o1', 'u2')`)
	require.Error(t, e.exec(`UPDATE rooms SET org_id = 'missing' WHERE id = 'g1'`), "the organization must exist")
	require.Error(t, e.exec(`UPDATE agents SET creator_iid = 'missing' WHERE id = 'a1'`), "the creator must be an intelligence")
	require.Error(t, e.exec(`UPDATE users SET default_org_id = 'missing' WHERE id = 'u1'`), "the default organization must exist")

	e.must(`DELETE FROM users WHERE id = 'u2'`)

	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM rooms WHERE id = 'g1' AND org_id = 'o1' AND creator_iid IS NULL`), "the group stays with its organization")
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM agents WHERE id = 'a1' AND org_id = 'o1' AND creator_iid IS NULL`), "the AI stays with its organization")
}

// OUPG-01, AD-012: migration 0019 is additive. Applied to a v0.5 database without its Go step it
// keeps every row of every table on every column it had, and leaves a consistent database.
func TestOrganizationsMigrationKeepsEveryRowOfAV05Database(t *testing.T) {
	ctx := context.Background()
	e := v03Fixture(t)
	require.NoError(t, e.s.MigrateBefore(ctx, "0019"))
	require.Equal(t, "0018_connection_proxy", versions(t, e.s)[len(versions(t, e.s))-1], "fixture sanity: schema 0018")
	tables := e.v03Tables()
	before := map[string][]string{}
	for table, cols := range tables {
		before[table] = e.rowsOf(table, cols)
	}
	require.NotEmpty(t, before["rooms"], "fixture sanity")
	require.NotEmpty(t, before["agents"], "fixture sanity")
	require.NotEmpty(t, before["connectors"], "fixture sanity")

	require.NoError(t, e.s.migrate(ctx, migrationsBefore("0020"), nil), "the schema step of 0019 alone")

	require.Contains(t, versions(t, e.s), "0019_organizations")
	for table, cols := range tables {
		after := e.columnsOf(table)
		require.Subset(t, after, cols, "table %s keeps its columns", table)
		require.Equal(t, before[table], e.rowsOf(table, cols), "table %s keeps every row", table)
	}
	for _, table := range []string{"agents", "rooms", "connectors"} {
		require.Zero(t, e.count(`SELECT COUNT(*) FROM `+table+` WHERE org_id IS NOT NULL`), "no %s row has an organization before the Go step", table)
	}
	require.Zero(t, e.count(`SELECT COUNT(*) FROM pragma_foreign_key_check`), "no foreign key is left dangling")
	require.Equal(t, "ok", e.str(`SELECT integrity_check FROM pragma_integrity_check`))
	require.True(t, slices.Contains(e.columnsOf("users"), "default_org_id"))
}

// The generated queries write and read what the schema holds: an organization by id and by slug, a
// position, a membership with and without a position and inviter.
func TestOrganizationQueriesRoundTrip(t *testing.T) {
	ctx := context.Background()
	e := newSchemaEnv(t)
	e.user("u1", "alice")
	e.user("u2", "bob")
	q := e.s.R()
	require.NoError(t, e.s.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateOrganization(ctx, db.CreateOrganizationParams{ID: "o1", Name: "Acme", Slug: "acme", CreatedBy: "u1", CreatedAt: 10, UpdatedAt: 11}); err != nil {
			return err
		}
		if err := q.CreatePosition(ctx, db.CreatePositionParams{ID: "p1", OrgID: "o1", Name: "Admin", CreatedAt: 12}); err != nil {
			return err
		}
		if err := q.CreateOrgMember(ctx, db.CreateOrgMemberParams{
			OrgID: "o1", Iid: "u1", Permission: "admin", PositionID: nullStr("p1"), JoinedAt: 13,
		}); err != nil {
			return err
		}
		return q.CreateOrgMember(ctx, db.CreateOrgMemberParams{
			OrgID: "o1", Iid: "u2", Permission: "guest", InviterIid: nullStr("u1"), JoinedAt: 14,
		})
	}))

	byID, err := q.GetOrganization(ctx, "o1")
	require.NoError(t, err)
	require.Equal(t, db.Organization{ID: "o1", Name: "Acme", Slug: "acme", CreatedBy: "u1", CreatedAt: 10, UpdatedAt: 11}, byID)
	bySlug, err := q.GetOrganizationBySlug(ctx, "ACME")
	require.NoError(t, err)
	require.Equal(t, byID, bySlug)
	_, err = q.GetOrganization(ctx, "missing")
	require.ErrorIs(t, err, sql.ErrNoRows)

	admin, err := q.GetOrgMember(ctx, db.GetOrgMemberParams{OrgID: "o1", Iid: "u1"})
	require.NoError(t, err)
	require.Equal(t, db.OrgMember{OrgID: "o1", Iid: "u1", Permission: "admin", PositionID: nullStr("p1"), JoinedAt: 13}, admin)
	guest, err := q.GetOrgMember(ctx, db.GetOrgMemberParams{OrgID: "o1", Iid: "u2"})
	require.NoError(t, err)
	require.Equal(t, db.OrgMember{OrgID: "o1", Iid: "u2", Permission: "guest", InviterIid: nullStr("u1"), JoinedAt: 14}, guest)
	_, err = q.GetOrgMember(ctx, db.GetOrgMemberParams{OrgID: "o2", Iid: "u1"})
	require.ErrorIs(t, err, sql.ErrNoRows, "a membership is per organization")
}
