package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// seedBeforeSimpleIdentity fills a database migrated up to 0039 with everything migration 0040 converts:
//
//	acme (o1): positions Admin > Lead; ann admin holding Admin, ben member holding Lead, gil a guest ben
//	invited; three AIs ann hosts: a1 (created by ann, member, holding Lead), a2 (created by a1 with
//	--parent, a guest of a1) and a3 (created by a2, a guest of a2); a4, which ann hosts and ben created;
//	pending invitations: a guest one with a position and an admin one.
//	globex (o2): ann admin; a5, which ann hosts, created by a1, which is not a member of globex.
func seedBeforeSimpleIdentity(e *schemaEnv) {
	e.t.Helper()
	for _, u := range [][2]string{{"u-ann", "ann"}, {"u-ben", "ben"}, {"u-gil", "gil"}, {"u-ida", "ida"}, {"u-joy", "joy"}} {
		e.user(u[0], u[1])
	}
	require.NoError(e.t, e.org("o1", "acme"))
	require.NoError(e.t, e.org("o2", "globex"))
	e.must(`INSERT INTO positions (id, org_id, name, parent_id, created_at) VALUES ('p-admin', 'o1', 'Admin', NULL, 1), ('p-lead', 'o1', 'Lead', 'p-admin', 1)`)
	for i, a := range [][3]string{{"a1", "u-ann", "o1"}, {"a2", "a1", "o1"}, {"a3", "a2", "o1"}, {"a4", "u-ben", "o1"}, {"a5", "a1", "o2"}} {
		e.must(`INSERT INTO agents (id, owner_id, name, created_at, org_id, creator_iid) VALUES (?, 'u-ann', ?, ?, ?, ?)`,
			a[0], a[0], 100+i, a[2], a[1])
		require.NoError(e.t, e.intelligence(a[0], "ai", "ai-"+a[0]))
	}
	e.must(`INSERT INTO org_members (org_id, iid, permission, position_id, inviter_iid, joined_at) VALUES
		('o1', 'u-ann', 'admin', 'p-admin', NULL, 1),
		('o1', 'u-ben', 'member', 'p-lead', NULL, 2),
		('o1', 'u-gil', 'guest', NULL, 'u-ben', 3),
		('o1', 'a1', 'member', 'p-lead', NULL, 4),
		('o1', 'a2', 'guest', NULL, 'a1', 5),
		('o1', 'a3', 'guest', NULL, 'a2', 6),
		('o1', 'a4', 'member', NULL, NULL, 7),
		('o2', 'u-ann', 'admin', NULL, NULL, 8),
		('o2', 'a5', 'guest', NULL, 'u-ann', 9)`)
	e.must(`INSERT INTO org_invitations (id, org_id, invitee_iid, permission, position_id, invited_by, created_at, expires_at) VALUES
		('i-guest', 'o1', 'u-ida', 'guest', 'p-lead', 'u-ben', 10, 20),
		('i-admin', 'o1', 'u-joy', 'admin', 'p-admin', 'u-ann', 11, 21)`)
}

// SR2-15, SR2-01, SR2-04, SR2-06, SR2-08: migration 0040 turns guests and guest invitations into members,
// drops positions, creates agent_instructors and makes every creator up the chain (a member of the AI's home
// organization, not its host) an instructor, so no AI loses a manager it had through creation.
func TestSR215Migration0040ConvertsGuestsPositionsAndCreators(t *testing.T) {
	ctx := context.Background()
	e := &schemaEnv{t: t, s: openMigratedBefore(t, "0040")}
	seedBeforeSimpleIdentity(e)

	require.NoError(t, e.s.applyMigration(ctx, "0040_simple_identity.sql"))

	// SR2-04: positions are gone, from an upgraded database and from a fresh one.
	fresh := newSchemaEnv(t)
	for _, env := range []*schemaEnv{e, fresh} {
		require.Zero(t, env.count(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'positions'`))
		require.NotContains(t, env.columnsOf("org_members"), "position_id")
		require.NotContains(t, env.columnsOf("org_members"), "inviter_iid", "SR2-03: the inviter grants nothing and goes")
		require.NotContains(t, env.columnsOf("org_invitations"), "position_id")
		require.Contains(t, env.columnsOf("agent_instructors"), "instructor_iid")
	}

	// SR2-01: every membership and invitation is admin or member; nobody lost one.
	require.Equal(t, []string{
		"o1 a1 member", "o1 a2 member", "o1 a3 member", "o1 a4 member", "o1 u-ann admin", "o1 u-ben member", "o1 u-gil member",
		"o2 a5 member", "o2 u-ann admin",
	}, e.column(`SELECT org_id || ' ' || iid || ' ' || permission FROM org_members ORDER BY org_id, iid`))
	require.Equal(t, []string{"i-admin u-joy admin", "i-guest u-ida member"},
		e.column(`SELECT id || ' ' || invitee_iid || ' ' || permission FROM org_invitations ORDER BY id`))
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM org_members WHERE iid = 'u-gil' AND joined_at = 3`), "join times stay")

	// SR2-08: the creators up the chain become instructors; the host never does (it is trusted anyway), and a
	// creator that is not a member of the AI's home organization does not either, as it managed nothing there.
	require.Equal(t, []string{
		"a2 a1 u-ann", // created by a1
		"a3 a1 u-ann", // a1 created a2, which created a3: a1 managed a3
		"a3 a2 u-ann", // created by a2
		"a4 u-ben u-ann",
	}, e.column(`SELECT agent_iid || ' ' || instructor_iid || ' ' || created_by FROM agent_instructors ORDER BY agent_iid, instructor_iid`))
	require.Equal(t, "a1", e.str(`SELECT creator_iid FROM agents WHERE id = 'a5'`), "the creator column stays as history")

	// The schema still holds its constraints.
	require.Error(t, e.exec(`INSERT INTO org_members (org_id, iid, permission, joined_at) VALUES ('o2', 'u-gil', 'guest', 1)`))
	require.Error(t, e.exec(`INSERT INTO org_invitations (id, org_id, invitee_iid, permission, invited_by, created_at, expires_at) VALUES ('x', 'o2', 'u-gil', 'guest', 'u-ann', 1, 2)`))
	require.Error(t, e.exec(`INSERT INTO org_members (org_id, iid, permission, joined_at) VALUES ('o1', 'u-ann', 'member', 1)`), "one membership per identity")
	require.Error(t, e.exec(`INSERT INTO agent_instructors (agent_iid, instructor_iid, created_at) VALUES ('a2', 'a1', 1)`), "one link per pair")
	require.Zero(t, e.count(`SELECT COUNT(*) FROM pragma_foreign_key_check`), "no foreign key is left dangling")

	// The links follow the identities they name.
	e.must(`DELETE FROM intelligences WHERE id = 'u-ben'`)
	require.Zero(t, e.count(`SELECT COUNT(*) FROM agent_instructors WHERE instructor_iid = 'u-ben'`))
	e.must(`DELETE FROM agents WHERE id = 'a3'`)
	require.Zero(t, e.count(`SELECT COUNT(*) FROM agent_instructors WHERE agent_iid = 'a3'`))
}

// SR2-15: migration 0040 runs after the automatic pre-migration backup, which keeps the guests and positions,
// and in one transaction with the other migrations: when it fails, nothing of it stays.
func TestSR215Migration0040IsBackedUpAndAtomic(t *testing.T) {
	ctx := context.Background()

	t.Run("backed up", func(t *testing.T) {
		dataDir := t.TempDir()
		s := openStore(t, filepath.Join(dataDir, "partyline.db"))
		require.NoError(t, s.MigrateBefore(ctx, "0040"))
		e := &schemaEnv{t: t, s: s}
		seedBeforeSimpleIdentity(e)

		require.NoError(t, s.Migrate(ctx))

		paths := backupPaths(t, dataDir)
		require.Len(t, paths, 1)
		backup := openBackup(t, paths[0])
		var guests, positions int
		require.NoError(t, backup.QueryRowContext(ctx, `SELECT COUNT(*) FROM org_members WHERE permission = 'guest'`).Scan(&guests))
		require.NoError(t, backup.QueryRowContext(ctx, `SELECT COUNT(*) FROM positions`).Scan(&positions))
		require.Equal(t, 4, guests, "the copy has the guests the migration converted")
		require.Equal(t, 2, positions, "and the positions it dropped")
		require.Zero(t, e.count(`SELECT COUNT(*) FROM org_members WHERE permission = 'guest'`), "the live database was converted")
	})

	t.Run("one transaction", func(t *testing.T) {
		s := openMigratedBefore(t, "0040")
		e := &schemaEnv{t: t, s: s}
		seedBeforeSimpleIdentity(e)
		// A membership that names an intelligence that does not exist, written with the foreign keys off: the
		// rebuilt table refuses it, so the migration fails half way through.
		conn, err := s.write.Conn(ctx)
		require.NoError(t, err)
		_, err = conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`)
		require.NoError(t, err)
		_, err = conn.ExecContext(ctx, `INSERT INTO org_members (org_id, iid, permission, joined_at) VALUES ('o1', 'ghost', 'member', 1)`)
		require.NoError(t, err)
		_, err = conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
		require.NoError(t, err)
		require.NoError(t, conn.Close())

		require.Error(t, s.applyMigration(ctx, "0040_simple_identity.sql"))

		require.NotContains(t, versions(t, s), "0040_simple_identity")
		require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'positions'`), "positions are still there")
		require.Zero(t, e.count(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'agent_instructors'`), "no instructors table")
		require.Equal(t, 4, e.count(`SELECT COUNT(*) FROM org_members WHERE permission = 'guest'`), "no guest was converted")
		require.Equal(t, 3, e.count(`SELECT COUNT(*) FROM org_members WHERE position_id IS NOT NULL`))
	})
}
