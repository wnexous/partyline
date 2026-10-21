package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
)

// PROJ-01, PROJ-06 (data): migration 0011 runs on a database with agents, members, grants,
// cursors and messages. Every member and grant row survives the table rebuilds with all its
// columns, existing agents stay fixed and join the project their name derives from, and the
// rebuilt CHECKs accept the new 'project' kind while still refusing unknown values.
func TestAgentIdentityMigrationKeepsDataAndAcceptsProjects(t *testing.T) {
	ctx := context.Background()
	s := openMigratedBefore(t, "0011")
	exec := func(query string, args ...any) error {
		_, err := s.write.ExecContext(ctx, query, args...)
		return err
	}
	require.NoError(t, s.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateUser(ctx, newUser("u1", "alice")); err != nil {
			return err
		}
		return q.CreateUser(ctx, newUser("u2", "bob"))
	}))
	for _, stmt := range []string{
		`INSERT INTO rooms (id, owner_id, name, created_at, updated_at) VALUES ('r1', 'u1', 'ops', 1, 1), ('r2', 'u1', 'dev', 1, 1)`,
		`INSERT INTO agents (id, owner_id, name, created_at) VALUES
			('a1', 'u1', 'proj', 10), ('a2', 'u1', 'proj-2', 20), ('a3', 'u1', 'web-12', 30),
			('a4', 'u1', 'agent', 40), ('a5', 'u2', 'proj', 50), ('a6', 'u1', 'v-1', 60)`,
		`INSERT INTO connectors (id, owner_id, kind, name, created_at, updated_at) VALUES ('k1', 'u1', 'telegram', 'tg', 1, 1)`,
		`INSERT INTO chats (id, connector_id, external_id, title, kind, created_at) VALUES ('c1', 'k1', 'x', 'g', 'group', 1)`,
		`INSERT INTO room_members (id, room_id, kind, member_id, outbound, added_by, created_at, display_name, sign_outbound, delivery, keywords_json)
			VALUES ('m1', 'r1', 'agent', 'a1', 0, 'u1', 5, 'Builder', 0, 'mentions', '["deploy"]'),
			       ('m2', 'r1', 'chat', 'c1', 1, 'u1', 6, '', 1, 'all', '[]'),
			       ('m3', 'r1', 'user', 'u2', 1, 'u1', 7, '', 1, 'all', '[]')`,
		`INSERT INTO grants (resource_kind, resource_id, subject, level, created_by, created_at)
			VALUES ('room', 'r1', 'u2', 'post', 'u1', 8), ('agent', 'a1', '*', 'use', 'u1', 9), ('connector', 'k1', 'u2', 'use', 'u1', 10)`,
		`INSERT INTO messages (id, room_id, seq, author_kind, author_id, text, created_at) VALUES ('msg1', 'r1', 1, 'agent', 'a1', 'hi', 11)`,
		`INSERT INTO agent_cursors (agent_id, room_id, seq) VALUES ('a1', 'r1', 1)`,
	} {
		require.NoError(t, exec(stmt), stmt)
	}

	// Up to 0014: 0015 replaces projects with agent members (see projects_conversion_test.go).
	require.NoError(t, s.migrate(ctx, migrationsBefore("0015"), hooks))

	// Members and grants keep every column.
	type member struct {
		ID, Room, Kind, MemberID, AddedBy, Display, Delivery, Keywords string
		Outbound, Sign                                                 bool
		Created                                                        int64
	}
	rows, err := s.write.QueryContext(ctx, `SELECT id, room_id, kind, member_id, added_by, display_name, delivery, keywords_json,
		outbound, sign_outbound, created_at FROM room_members ORDER BY id`)
	require.NoError(t, err)
	var members []member
	for rows.Next() {
		var m member
		require.NoError(t, rows.Scan(&m.ID, &m.Room, &m.Kind, &m.MemberID, &m.AddedBy, &m.Display, &m.Delivery, &m.Keywords, &m.Outbound, &m.Sign, &m.Created))
		members = append(members, m)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []member{
		{ID: "m1", Room: "r1", Kind: "agent", MemberID: "a1", AddedBy: "u1", Display: "Builder", Delivery: "mentions", Keywords: `["deploy"]`, Outbound: false, Sign: false, Created: 5},
		{ID: "m2", Room: "r1", Kind: "chat", MemberID: "c1", AddedBy: "u1", Display: "", Delivery: "all", Keywords: "[]", Outbound: true, Sign: true, Created: 6},
		{ID: "m3", Room: "r1", Kind: "user", MemberID: "u2", AddedBy: "u1", Display: "", Delivery: "all", Keywords: "[]", Outbound: true, Sign: true, Created: 7},
	}, members)

	var grants []string
	grows, err := s.write.QueryContext(ctx, `SELECT resource_kind || '/' || resource_id || '/' || subject || '/' || level || '/' || created_by || '/' || created_at
		FROM grants ORDER BY resource_kind`)
	require.NoError(t, err)
	for grows.Next() {
		var g string
		require.NoError(t, grows.Scan(&g))
		grants = append(grants, g)
	}
	require.NoError(t, grows.Err())
	require.Equal(t, []string{"agent/a1/*/use/u1/9", "connector/k1/u2/use/u1/10", "room/r1/u2/post/u1/8"}, grants)

	// Existing agents are fixed, keep their names and join the project of their name.
	type project struct{ Owner, Slug, Name string }
	projectOf := func(agentID string) *project {
		var p project
		err := s.write.QueryRowContext(ctx, `SELECT p.owner_id, p.slug, p.name FROM agents a JOIN projects p ON p.id = a.project_id WHERE a.id = ?`, agentID).
			Scan(&p.Owner, &p.Slug, &p.Name)
		if err == sql.ErrNoRows {
			return nil
		}
		require.NoError(t, err)
		return &p
	}
	require.Equal(t, &project{"u1", "proj", "Proj"}, projectOf("a1"))
	require.Equal(t, &project{"u1", "proj", "Proj"}, projectOf("a2"), "the unique-mode suffix -2 is not part of the project")
	require.Equal(t, &project{"u1", "web", "Web"}, projectOf("a3"), "a two-digit suffix is dropped too")
	require.Nil(t, projectOf("a4"), "the default name agent derives no project")
	require.Equal(t, &project{"u2", "proj", "Proj"}, projectOf("a5"), "projects are per owner")
	require.Equal(t, &project{"u1", "v-1", "V-1"}, projectOf("a6"), "-1 is never a unique-mode suffix")
	var sameProject int
	require.NoError(t, s.write.QueryRowContext(ctx, `SELECT COUNT(DISTINCT project_id) FROM agents WHERE id IN ('a1', 'a2')`).Scan(&sameProject))
	require.Equal(t, 1, sameProject)
	var projects int
	require.NoError(t, s.write.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&projects))
	require.Equal(t, 4, projects, "one project per (owner, slug)")

	var kind, harness, machine, display string
	var archived, connected sql.NullInt64
	require.NoError(t, s.write.QueryRowContext(ctx, `SELECT kind, harness, machine, display_name, archived_at, connected_at FROM agents WHERE id = 'a2'`).
		Scan(&kind, &harness, &machine, &display, &archived, &connected))
	require.Equal(t, []any{"fixed", "agent", "", "", false, false}, []any{kind, harness, machine, display, archived.Valid, connected.Valid})

	// The message view still names agent authors (member display name first).
	var author string
	require.NoError(t, s.write.QueryRowContext(ctx, `SELECT author_name FROM message_views WHERE id = 'msg1'`).Scan(&author))
	require.Equal(t, "Builder", author)

	// The rebuilt CHECKs accept 'project' and still refuse unknown values.
	require.NoError(t, exec(`INSERT INTO room_members (id, room_id, kind, member_id, created_at) VALUES ('m4', 'r1', 'project', 'p1', 12)`))
	require.Error(t, exec(`INSERT INTO room_members (id, room_id, kind, member_id, created_at) VALUES ('m5', 'r1', 'team', 'x', 12)`))
	require.NoError(t, exec(`INSERT INTO grants (resource_kind, resource_id, subject, level, created_at) VALUES ('project', 'p1', 'u2', 'use', 12)`))
	require.Error(t, exec(`INSERT INTO grants (resource_kind, resource_id, subject, level, created_at) VALUES ('team', 'p1', 'u2', 'use', 12)`))
	require.Error(t, exec(`UPDATE agents SET kind = 'session' WHERE id = 'a1'`))
	require.NoError(t, exec(`UPDATE agents SET kind = 'temporary' WHERE id = 'a1'`))

	// Keys, foreign keys and indexes survive the rebuild.
	require.Error(t, exec(`INSERT INTO room_members (id, room_id, kind, member_id, created_at) VALUES ('m6', 'r1', 'agent', 'a1', 12)`), "one row per (room, kind, member)")
	require.Error(t, exec(`INSERT INTO room_members (id, room_id, kind, member_id, created_at) VALUES ('m7', 'missing', 'agent', 'a2', 12)`), "the room must exist")
	require.Error(t, exec(`INSERT INTO grants (resource_kind, resource_id, subject, level, created_at) VALUES ('room', 'r1', 'u2', 'view', 13)`), "one grant per (kind, resource, subject)")
	for _, index := range []string{"room_members_member", "grants_subject"} {
		var name string
		require.NoError(t, s.write.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&name), index)
	}
	require.NoError(t, exec(`DELETE FROM rooms WHERE id = 'r1'`))
	var left int
	require.NoError(t, s.write.QueryRowContext(ctx, `SELECT COUNT(*) FROM room_members`).Scan(&left))
	require.Zero(t, left, "members go with their room")

	// Projects: unique per owner and slug; deleting one leaves its agents without a project.
	require.Error(t, exec(`INSERT INTO projects (id, owner_id, slug, name, created_at) VALUES ('px', 'u1', 'proj', 'Again', 1)`))
	require.NoError(t, exec(`INSERT INTO project_cursors (project_id, room_id, seq) SELECT project_id, 'r2', 3 FROM agents WHERE id = 'a1'`))
	require.NoError(t, exec(`DELETE FROM projects WHERE id = (SELECT project_id FROM agents WHERE id = 'a1')`))
	require.Nil(t, projectOf("a1"))
	var cursors int
	require.NoError(t, s.write.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_cursors`).Scan(&cursors))
	require.Zero(t, cursors, "cursors go with their project")
}
