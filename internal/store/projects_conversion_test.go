package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type member struct {
	Room, Kind, MemberID, AddedBy, Display, Delivery, Keywords string
	Outbound, Sign                                             bool
	Created                                                    int64
}

func (e *schemaEnv) members() []member {
	e.t.Helper()
	rows, err := e.s.write.Query(`SELECT room_id, kind, member_id, added_by, display_name, delivery, keywords_json,
		outbound, sign_outbound, created_at FROM room_members ORDER BY room_id, kind, member_id`)
	require.NoError(e.t, err)
	defer func() { _ = rows.Close() }()
	var out []member
	for rows.Next() {
		var m member
		require.NoError(e.t, rows.Scan(&m.Room, &m.Kind, &m.MemberID, &m.AddedBy, &m.Display, &m.Delivery, &m.Keywords,
			&m.Outbound, &m.Sign, &m.Created))
		out = append(out, m)
	}
	require.NoError(e.t, rows.Err())
	return out
}

// MIG-03: each project room member becomes a direct member of the project's most recently
// connected agent with the member's settings; an existing direct membership keeps its own
// settings; a project without agents loses its membership. Project cursors move to that
// agent (the furthest wins); no project, project member or project grant remains.
func TestProjectsBecomeAgentMembers(t *testing.T) {
	ctx := context.Background()
	e := &schemaEnv{t: t, s: openMigratedBefore(t, "0015")}
	for _, stmt := range []string{
		`INSERT INTO users (id, username, password_hash, role, created_at) VALUES ('u1', 'alice', 'x', 'admin', 1), ('u2', 'bob', 'x', 'member', 2)`,
		`INSERT INTO projects (id, owner_id, slug, name, created_at) VALUES
			('p-web', 'u1', 'web', 'Web', 1), ('p-empty', 'u1', 'empty', 'Empty', 1),
			('p-tie', 'u1', 'tie', 'Tie', 1), ('p-never', 'u1', 'never', 'Never', 1)`,
		// p-web: the latest connected_at wins over an older agent whose id sorts first and over
		// a never-connected one; ties fall to the smaller id; never-connected agents sort last.
		`INSERT INTO agents (id, owner_id, name, project_id, kind, connected_at, created_at) VALUES
			('a-1-old', 'u1', 'web-claude', 'p-web', 'temporary', 100, 1),
			('a-2-new', 'u1', 'web-claude-2', 'p-web', 'temporary', 200, 2),
			('a-0', 'u1', 'web-codex', 'p-web', 'temporary', NULL, 3),
			('a-tie-b', 'u1', 'tie-claude', 'p-tie', 'temporary', 300, 4),
			('a-tie-a', 'u1', 'tie-claude-2', 'p-tie', 'temporary', 300, 5),
			('a-never-b', 'u1', 'never-claude', 'p-never', 'temporary', NULL, 6),
			('a-never-a', 'u1', 'never-claude-2', 'p-never', 'temporary', NULL, 7),
			('a-free', 'u1', 'free', NULL, 'fixed', 50, 8)`,
		`INSERT INTO rooms (id, owner_id, name, created_at, updated_at) VALUES ('r1', 'u1', 'ops', 1, 1), ('r2', 'u1', 'dev', 1, 1)`,
		`INSERT INTO room_members (id, room_id, kind, member_id, outbound, added_by, created_at, display_name, sign_outbound, delivery, keywords_json) VALUES
			('m1', 'r1', 'project', 'p-web', 0, 'u2', 10, 'Web Bot', 0, 'trusted', '["build"]'),
			('m2', 'r2', 'project', 'p-web', 1, 'u1', 11, 'Web Dev', 0, 'mentions', '["x"]'),
			('m3', 'r2', 'agent', 'a-2-new', 1, 'u1', 12, 'Mine', 1, 'all', '[]'),
			('m4', 'r1', 'project', 'p-empty', 1, 'u1', 13, '', 1, 'all', '[]'),
			('m5', 'r1', 'project', 'p-tie', 1, 'u1', 14, 'Tie', 1, 'all', '[]'),
			('m6', 'r1', 'project', 'p-never', 1, 'u1', 15, 'Never', 1, 'mentions', '[]'),
			('m7', 'r1', 'agent', 'a-free', 1, 'u1', 16, 'Free', 1, 'all', '[]')`,
		`INSERT INTO grants (resource_kind, resource_id, subject, level, created_by, created_at) VALUES
			('project', 'p-web', 'u2', 'use', 'u1', 1), ('room', 'r1', 'u2', 'post', 'u1', 1)`,
		`INSERT INTO project_cursors (project_id, room_id, seq) VALUES ('p-web', 'r1', 5), ('p-web', 'r2', 1)`,
		`INSERT INTO agent_cursors (agent_id, room_id, seq) VALUES ('a-2-new', 'r1', 3), ('a-2-new', 'r2', 2)`,
	} {
		e.must(stmt)
	}

	require.NoError(t, e.s.Migrate(ctx))

	require.Equal(t, []member{
		{Room: "r1", Kind: "agent", MemberID: "a-2-new", AddedBy: "u2", Display: "Web Bot", Delivery: "trusted", Keywords: `["build"]`, Outbound: false, Sign: false, Created: 10},
		{Room: "r1", Kind: "agent", MemberID: "a-free", AddedBy: "u1", Display: "Free", Delivery: "all", Keywords: "[]", Outbound: true, Sign: true, Created: 16},
		{Room: "r1", Kind: "agent", MemberID: "a-never-a", AddedBy: "u1", Display: "Never", Delivery: "mentions", Keywords: "[]", Outbound: true, Sign: true, Created: 15},
		{Room: "r1", Kind: "agent", MemberID: "a-tie-a", AddedBy: "u1", Display: "Tie", Delivery: "all", Keywords: "[]", Outbound: true, Sign: true, Created: 14},
		{Room: "r2", Kind: "agent", MemberID: "a-2-new", AddedBy: "u1", Display: "Mine", Delivery: "all", Keywords: "[]", Outbound: true, Sign: true, Created: 12},
	}, e.members(), "latest connected session (ties by id, never-connected last); direct members keep their settings; empty projects drop out")

	require.Equal(t, 5, e.count(`SELECT seq FROM agent_cursors WHERE agent_id = 'a-2-new' AND room_id = 'r1'`), "the project's cursor was further")
	require.Equal(t, 2, e.count(`SELECT seq FROM agent_cursors WHERE agent_id = 'a-2-new' AND room_id = 'r2'`), "the agent's own cursor was further")
	require.Equal(t, 2, e.count(`SELECT COUNT(*) FROM agent_cursors`))

	require.Zero(t, e.count(`SELECT COUNT(*) FROM projects`))
	require.Empty(t, e.columnsOf("project_cursors"), "the project cursors moved to the agent, and 0016 dropped their table")
	require.Zero(t, e.count(`SELECT COUNT(*) FROM room_members WHERE kind = 'project'`))
	require.Zero(t, e.count(`SELECT COUNT(*) FROM grants WHERE resource_kind = 'project'`))
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM grants`), "other grants stay")
	require.Zero(t, e.count(`SELECT COUNT(*) FROM agents WHERE project_id IS NOT NULL`))
	require.Equal(t, 8, e.count(`SELECT COUNT(*) FROM agents`), "agents stay")
}
