package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/wnexous/partyline/internal/handles"
)

// personalAdminPosition is the name of the root position of a personal organization (ORG-01).
const personalAdminPosition = "Admin"

// backfillOrganizations is the Go hook of 0019_organizations (OUPG-01, AD-021): every user gets a
// personal organization where they are the admin holding the root position "Admin"; what they
// owned moves into it (AIs as members created by them, groups, connections); the users who held
// grants on it, and the members of its groups, join it as members; the grants are consumed.
// History (messages, cursors, attachments, people) is untouched. Raw SQL only: it runs against the
// 0019 schema. Set-based statements keep it fast on a large history; only the rows that need a
// generated id or a slug go through Go.
func backfillOrganizations(ctx context.Context, tx *sql.Tx) error {
	b := &orgBackfill{tx: tx, slugs: map[string]bool{}}
	steps := []func(context.Context) error{
		b.personalOrganizations, b.aiHomes, b.groups, b.connections, b.grantees, b.groupMembers, b.consumeGrants,
	}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return err
		}
	}
	return nil
}

type orgBackfill struct {
	tx    *sql.Tx
	slugs map[string]bool // slugs in use, lower-case
}

// personalOrganizations: for each user, oldest first (disabled users included), an organization
// named by the display name, else the username, with the first free slug derived from the
// username, the root position "Admin", the user as its admin holding it, and the user's default
// organization set to it.
func (b *orgBackfill) personalOrganizations(ctx context.Context) error {
	type account struct {
		id, username, display string
		created               int64
	}
	var accounts []account
	err := queryRows(ctx, b.tx, `SELECT u.id, u.username, COALESCE(i.display_name, ''), u.created_at
		FROM users u LEFT JOIN intelligences i ON i.id = u.id ORDER BY u.created_at, u.id`,
		func(rows *sql.Rows) error {
			var a account
			if err := rows.Scan(&a.id, &a.username, &a.display, &a.created); err != nil {
				return err
			}
			accounts = append(accounts, a)
			return nil
		})
	if err != nil {
		return err
	}
	if err := queryRows(ctx, b.tx, `SELECT slug FROM organizations`, func(rows *sql.Rows) error {
		var s string
		if err := rows.Scan(&s); err != nil {
			return err
		}
		b.slugs[strings.ToLower(s)] = true
		return nil
	}); err != nil {
		return err
	}
	for _, a := range accounts {
		slug := handles.AllocateSlug(func(s string) bool { return b.slugs[s] }, a.username)
		b.slugs[slug] = true
		orgID, positionID := NewID(), NewID()
		for _, stmt := range []struct {
			query string
			args  []any
		}{
			{`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
				[]any{orgID, handles.PersonalOrgName(a.display, a.username), slug, a.id, a.created, a.created}},
			{`INSERT INTO positions (id, org_id, name, created_at) VALUES (?, ?, ?, ?)`,
				[]any{positionID, orgID, personalAdminPosition, a.created}},
			{`INSERT INTO org_members (org_id, iid, permission, position_id, joined_at) VALUES (?, ?, 'admin', ?, ?)`,
				[]any{orgID, a.id, positionID, a.created}},
			{`UPDATE users SET default_org_id = ? WHERE id = ?`, []any{orgID, a.id}},
		} {
			if _, err := b.tx.ExecContext(ctx, stmt.query, stmt.args...); err != nil {
				return fmt.Errorf("personal organization of user %s: %w", a.id, err)
			}
		}
	}
	return nil
}

// aiHomes: every AI belongs to its owner's organization and was created by the owner, and is a
// member of it with no position: the owner manages it as its creator (POS-03).
func (b *orgBackfill) aiHomes(ctx context.Context) error {
	for _, stmt := range []struct{ what, query string }{
		{"AI homes", `UPDATE agents SET org_id = (SELECT u.default_org_id FROM users u WHERE u.id = agents.owner_id),
			creator_iid = owner_id`},
		{"AI memberships", `INSERT INTO org_members (org_id, iid, permission, joined_at)
			SELECT org_id, id, 'member', created_at FROM agents WHERE org_id IS NOT NULL`},
	} {
		if _, err := b.tx.ExecContext(ctx, stmt.query); err != nil {
			return fmt.Errorf("%s: %w", stmt.what, err)
		}
	}
	return nil
}

// groups: groups belong to their owner's organization and were created by the owner; a DM keeps
// no organization (ORG-02).
func (b *orgBackfill) groups(ctx context.Context) error {
	_, err := b.tx.ExecContext(ctx, `UPDATE rooms SET org_id = (SELECT u.default_org_id FROM users u WHERE u.id = rooms.owner_id),
		creator_iid = owner_id WHERE kind = 'group'`)
	if err != nil {
		return fmt.Errorf("groups: %w", err)
	}
	return nil
}

// connections: connections belong to their owner's organization. Their operator is an AI of the
// same owner (CID-01), so it belongs to the connection's organization (ORG-07).
func (b *orgBackfill) connections(ctx context.Context) error {
	_, err := b.tx.ExecContext(ctx, `UPDATE connectors SET org_id = (SELECT u.default_org_id FROM users u WHERE u.id = connectors.owner_id)`)
	if err != nil {
		return fmt.Errorf("connections: %w", err)
	}
	return nil
}

// grantees: a user granted a level on a group, an AI or a connection joins the organization of the
// resource's owner as a member with no position (the earliest grant gives the join time), and a
// user granted post or manage on a group is also made a user member of the group. Grants to
// everyone ('*'), grants on a DM, grants whose resource or user no longer exists and the owner's own
// grants give nothing: the owner is the creator.
func (b *orgBackfill) grantees(ctx context.Context) error {
	if _, err := b.tx.ExecContext(ctx, `INSERT OR IGNORE INTO org_members (org_id, iid, permission, joined_at)
		SELECT org_id, grantee, 'member', MIN(granted_at) FROM (
			SELECT o.default_org_id AS org_id, g.subject AS grantee, g.created_at AS granted_at
			FROM grants g JOIN rooms r ON g.resource_kind = 'room' AND r.id = g.resource_id AND r.kind = 'group'
			JOIN users o ON o.id = r.owner_id
			UNION ALL
			SELECT o.default_org_id, g.subject, g.created_at
			FROM grants g JOIN agents a ON g.resource_kind = 'agent' AND a.id = g.resource_id
			JOIN users o ON o.id = a.owner_id
			UNION ALL
			SELECT o.default_org_id, g.subject, g.created_at
			FROM grants g JOIN connectors c ON g.resource_kind = 'connector' AND c.id = g.resource_id
			JOIN users o ON o.id = c.owner_id
		) WHERE grantee <> '*' AND grantee IN (SELECT id FROM users)
		GROUP BY org_id, grantee`); err != nil {
		return fmt.Errorf("grantees: %w", err)
	}

	type grant struct {
		room, user, by string
		at             int64
	}
	var posters []grant
	err := queryRows(ctx, b.tx, `SELECT g.resource_id, g.subject, g.created_by, g.created_at
		FROM grants g JOIN rooms r ON r.id = g.resource_id AND r.kind = 'group'
		WHERE g.resource_kind = 'room' AND g.level IN ('post', 'manage') AND g.subject <> '*'
		  AND g.subject IN (SELECT id FROM users) AND g.subject <> r.owner_id
		ORDER BY g.created_at, g.resource_id, g.subject`,
		func(rows *sql.Rows) error {
			var g grant
			if err := rows.Scan(&g.room, &g.user, &g.by, &g.at); err != nil {
				return err
			}
			posters = append(posters, g)
			return nil
		})
	if err != nil {
		return err
	}
	for _, g := range posters {
		if _, err := b.tx.ExecContext(ctx, `INSERT OR IGNORE INTO room_members (id, room_id, kind, member_id, added_by, created_at)
			VALUES (?, ?, 'user', ?, ?, ?)`, NewID(), g.room, g.user, g.by, g.at); err != nil {
			return fmt.Errorf("room member of grant on %s: %w", g.room, err)
		}
	}
	return nil
}

// groupMembers: the users and AIs that are members of a group join the group's organization when
// they are not members of it yet, as members with no position, so that a group's members are
// members of its organization.
func (b *orgBackfill) groupMembers(ctx context.Context) error {
	_, err := b.tx.ExecContext(ctx, `INSERT OR IGNORE INTO org_members (org_id, iid, permission, joined_at)
		SELECT r.org_id, m.member_id, 'member', MIN(m.created_at)
		FROM room_members m JOIN rooms r ON r.id = m.room_id
		WHERE r.kind = 'group' AND r.org_id IS NOT NULL
		  AND ((m.kind = 'user' AND m.member_id IN (SELECT id FROM users))
		    OR (m.kind = 'agent' AND m.member_id IN (SELECT id FROM agents)))
		GROUP BY r.org_id, m.member_id`)
	if err != nil {
		return fmt.Errorf("group members: %w", err)
	}
	return nil
}

// consumeGrants: sharing is organization membership from now on (AD-016). Every grant has become a
// membership by now; the table is emptied here and 0020, which runs right after this hook in the same
// transaction, drops it.
func (b *orgBackfill) consumeGrants(ctx context.Context) error {
	_, err := b.tx.ExecContext(ctx, `DELETE FROM grants`)
	return err
}
