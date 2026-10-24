-- name: CreateAgent :exec
INSERT INTO agents (id, owner_id, name, description, created_at) VALUES (?, ?, ?, ?, ?);

-- name: GetAgent :one
SELECT * FROM agents WHERE id = ?;

-- name: CountAgentsByOwner :one
SELECT COUNT(*) FROM agents WHERE owner_id = ?;

-- name: GetAgentWithOwner :one
-- An AI with its host, profile, home organization and the home folder of the host's machine it lives on.
SELECT sqlc.embed(a), u.username AS owner_username,
       CAST(COALESCE(ui.display_name, '') AS TEXT) AS owner_display_name,
       CAST(COALESCE(i.handle, '') AS TEXT) AS handle,
       CAST(COALESCE(i.display_name, '') AS TEXT) AS profile_name,
       CAST(COALESCE(i.avatar_sha256, '') AS TEXT) AS profile_avatar,
       CAST(COALESCE(o.name, '') AS TEXT) AS org_name,
       CAST(COALESCE(o.slug, '') AS TEXT) AS org_slug,
       CAST(COALESCE(mc.home, '') AS TEXT) AS machine_home
FROM agents a JOIN users u ON u.id = a.owner_id
LEFT JOIN intelligences i ON i.id = a.id
LEFT JOIN intelligences ui ON ui.id = u.id
LEFT JOIN organizations o ON o.id = a.org_id
LEFT JOIN machines mc ON mc.owner_id = a.owner_id AND mc.hostname = a.machine
WHERE a.id = ?;

-- name: ListAgentsForUser :many
SELECT sqlc.embed(a), u.username AS owner_username,
       CAST(COALESCE(ui.display_name, '') AS TEXT) AS owner_display_name,
       CAST(COALESCE(i.handle, '') AS TEXT) AS handle,
       CAST(COALESCE(i.display_name, '') AS TEXT) AS profile_name,
       CAST(COALESCE(i.avatar_sha256, '') AS TEXT) AS profile_avatar
FROM agents a JOIN users u ON u.id = a.owner_id
LEFT JOIN intelligences i ON i.id = a.id
LEFT JOIN intelligences ui ON ui.id = u.id
WHERE (a.owner_id = sqlc.arg(user_id)
       OR a.id IN (SELECT g.resource_id FROM grants g
                   WHERE g.resource_kind = 'agent' AND g.subject IN (sqlc.arg(user_id), '*')))
ORDER BY a.name, u.username, a.id;

-- name: ListVisibleAgents :many
-- The AIs a user sees (AD-016): those hosted on their computers and those of the organizations they
-- belong to, whatever their permission there.
SELECT sqlc.embed(a), u.username AS owner_username,
       CAST(COALESCE(ui.display_name, '') AS TEXT) AS owner_display_name,
       CAST(COALESCE(i.handle, '') AS TEXT) AS handle,
       CAST(COALESCE(i.display_name, '') AS TEXT) AS profile_name,
       CAST(COALESCE(i.avatar_sha256, '') AS TEXT) AS profile_avatar,
       CAST(COALESCE(o.name, '') AS TEXT) AS org_name,
       CAST(COALESCE(o.slug, '') AS TEXT) AS org_slug,
       CAST(COALESCE(mc.home, '') AS TEXT) AS machine_home
FROM agents a JOIN users u ON u.id = a.owner_id
LEFT JOIN intelligences i ON i.id = a.id
LEFT JOIN intelligences ui ON ui.id = u.id
LEFT JOIN organizations o ON o.id = a.org_id
LEFT JOIN machines mc ON mc.owner_id = a.owner_id AND mc.hostname = a.machine
WHERE a.owner_id = sqlc.arg(user_id)
   OR a.id IN (SELECT y.iid FROM org_members x JOIN org_members y ON y.org_id = x.org_id
               WHERE x.iid = sqlc.arg(user_id))
ORDER BY a.name, u.username, a.id;

-- name: ListOrgAIs :many
-- The AIs that are members of the organizations an identity belongs to, each with the organization it is
-- listed under (AD-016): an AI of two of them comes twice.
SELECT m.org_id AS org_id, sqlc.embed(a)
FROM org_members me
JOIN org_members m ON m.org_id = me.org_id
JOIN agents a ON a.id = m.iid
WHERE me.iid = ?
ORDER BY m.org_id, a.id;

-- name: UpdateAgentDescription :exec
UPDATE agents SET description = ? WHERE id = ?;

-- name: TouchAgent :exec
UPDATE agents SET last_seen_at = ? WHERE id = ?;

-- name: DeleteAgentMemberships :many
DELETE FROM room_members WHERE kind = 'agent' AND member_id = ?
RETURNING room_id;

-- name: DeleteAgent :exec
DELETE FROM agents WHERE id = ?;

-- name: ListAgentRoomStates :many
-- The rooms an agent is a member of. A DM is named by the display name of its other participant
-- (with_id and with_handle name that intelligence), a group by its name.
SELECT r.id, CAST(CASE WHEN r.kind = 'dm' THEN COALESCE(w.display_name, '') ELSE r.name END AS TEXT) AS name,
       r.kind, r.agents_paused, CAST(COALESCE(c.seq, 0) AS INTEGER) AS cursor,
       m.display_name, m.delivery, m.keywords_json, r.instructions, r.instructions_by,
       CAST(COALESCE(w.id, '') AS TEXT) AS with_id, CAST(COALESCE(w.handle, '') AS TEXT) AS with_handle
FROM room_members m
JOIN rooms r ON r.id = m.room_id
LEFT JOIN intelligences w ON r.kind = 'dm' AND w.id = CASE WHEN r.dm_a = m.member_id THEN r.dm_b ELSE r.dm_a END
LEFT JOIN agent_cursors c ON c.agent_id = m.member_id AND c.room_id = m.room_id
WHERE m.kind = 'agent' AND m.member_id = ?
ORDER BY name, r.id;

-- name: CountAgentBacklog :one
SELECT COUNT(*) FROM messages
WHERE room_id = sqlc.arg(room_id) AND seq > sqlc.arg(after_seq)
  AND NOT (author_kind = 'agent' AND author_id = sqlc.arg(agent_id));

-- name: ListAgentBacklog :many
SELECT * FROM message_views
WHERE room_id = sqlc.arg(room_id) AND seq > sqlc.arg(after_seq)
  AND NOT (author_kind = 'agent' AND author_id = sqlc.arg(agent_id))
ORDER BY seq DESC
LIMIT sqlc.arg(limit);

-- name: ListAgentPending :many
SELECT * FROM message_views
WHERE room_id = sqlc.arg(room_id) AND seq > sqlc.arg(after_seq)
  AND NOT (author_kind = 'agent' AND author_id = sqlc.arg(agent_id))
ORDER BY seq ASC
LIMIT sqlc.arg(limit);

-- name: GetMessageRoomSeq :one
SELECT room_id, seq FROM messages WHERE id = ?;

-- name: AdvanceAgentCursor :exec
INSERT INTO agent_cursors (agent_id, room_id, seq) VALUES (?, ?, ?)
ON CONFLICT (agent_id, room_id) DO UPDATE SET seq = MAX(agent_cursors.seq, excluded.seq);

-- name: GetAgentCursor :one
SELECT seq FROM agent_cursors WHERE agent_id = ? AND room_id = ?;

-- name: MarkAgentConnected :exec
UPDATE agents SET connected_at = ? WHERE id = ?;

-- name: ListAgentRoomIDs :many
SELECT room_id FROM room_members WHERE kind = 'agent' AND member_id = ?;

-- name: GetAgentByFolder :one
SELECT * FROM agents WHERE owner_id = ? AND machine = ? AND folder = ? AND folder <> '';

-- name: InsertFolderAgent :exec
-- A new AI of a folder (AID-01): the row stores its id as its v0.3 name. org_id is its home
-- organization and creator_iid the identity that created it (ORG-02, ORG-04).
INSERT INTO agents (id, owner_id, name, description, created_at, harness, machine, folder, org_id, creator_iid)
VALUES (?, ?, ?, '', ?, ?, ?, ?, ?, ?);

-- name: SetAgentHarness :exec
UPDATE agents SET harness = ? WHERE id = ?;

-- name: GetUnlinkedAgentByHandle :one
-- The owner's agent without a folder (a v0.3 identity) whose intelligence has the handle
-- (MIG-02); handles compare case-insensitively. Only an identity the owner created is the owner's
-- to adopt: the upgrade made every v0.3 owner the creator of their agents, while the AIs left
-- behind by a deleted account (owned by its heir, created by someone else) are not.
SELECT a.* FROM agents a JOIN intelligences i ON i.id = a.id
WHERE a.owner_id = ? AND a.folder = '' AND a.creator_iid = a.owner_id AND i.handle = ?;

-- name: GetUnlinkedAgentByName :one
-- The owner's agent without a folder with the v0.3 name (MIG-02), under the same rule.
SELECT * FROM agents WHERE owner_id = ? AND folder = '' AND creator_iid = owner_id AND name = ?;

-- name: AdoptAgent :execrows
-- Links an unlinked agent to a machine and folder; the folder = '' guard makes the link happen
-- once (MIG-02).
UPDATE agents SET machine = ?, folder = ?, harness = ? WHERE id = ? AND folder = '';

-- name: ListAgentsByOwner :many
SELECT * FROM agents WHERE owner_id = ?;
