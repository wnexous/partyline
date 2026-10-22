-- name: CreateAgent :exec
INSERT INTO agents (id, owner_id, name, description, created_at) VALUES (?, ?, ?, ?, ?);

-- name: GetAgent :one
SELECT * FROM agents WHERE id = ?;

-- name: CountAgentsByOwner :one
SELECT COUNT(*) FROM agents WHERE owner_id = ?;

-- name: GetAgentWithOwner :one
SELECT sqlc.embed(a), u.username AS owner_username, u.display_name AS owner_display_name,
       CAST(COALESCE(i.handle, '') AS TEXT) AS handle,
       CAST(COALESCE(i.display_name, '') AS TEXT) AS profile_name
FROM agents a JOIN users u ON u.id = a.owner_id
LEFT JOIN intelligences i ON i.id = a.id
WHERE a.id = ?;

-- name: ListAgentsForUser :many
SELECT sqlc.embed(a), u.username AS owner_username, u.display_name AS owner_display_name,
       CAST(COALESCE(i.handle, '') AS TEXT) AS handle,
       CAST(COALESCE(i.display_name, '') AS TEXT) AS profile_name
FROM agents a JOIN users u ON u.id = a.owner_id
LEFT JOIN intelligences i ON i.id = a.id
WHERE (a.owner_id = sqlc.arg(user_id)
       OR a.id IN (SELECT g.resource_id FROM grants g
                   WHERE g.resource_kind = 'agent' AND g.subject IN (sqlc.arg(user_id), '*')))
ORDER BY a.name, u.username, a.id;

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

-- name: SetAgentDisplayName :exec
UPDATE agents SET display_name = ? WHERE id = ?;

-- name: ListAgentRoomIDs :many
SELECT room_id FROM room_members WHERE kind = 'agent' AND member_id = ?;

-- name: GetAgentByFolder :one
SELECT * FROM agents WHERE owner_id = ? AND machine = ? AND folder = ? AND folder <> '';

-- name: InsertFolderAgent :exec
-- A new AI of a folder (AID-01): the row stores its id as its v0.3 name.
INSERT INTO agents (id, owner_id, name, description, created_at, harness, machine, folder)
VALUES (?, ?, ?, '', ?, ?, ?, ?);

-- name: SetAgentHarness :exec
UPDATE agents SET harness = ? WHERE id = ?;

-- name: GetUnlinkedAgentByHandle :one
-- The owner's agent without a folder (a v0.3 identity) whose intelligence has the handle
-- (MIG-02); handles compare case-insensitively.
SELECT a.* FROM agents a JOIN intelligences i ON i.id = a.id
WHERE a.owner_id = ? AND a.folder = '' AND i.handle = ?;

-- name: GetUnlinkedAgentByName :one
-- The owner's agent without a folder with the v0.3 name (MIG-02).
SELECT * FROM agents WHERE owner_id = ? AND folder = '' AND name = ?;

-- name: AdoptAgent :execrows
-- Links an unlinked agent to a machine and folder; the folder = '' guard makes the link happen
-- once (MIG-02).
UPDATE agents SET machine = ?, folder = ?, harness = ? WHERE id = ? AND folder = '';

-- name: ListAgentsByOwner :many
SELECT * FROM agents WHERE owner_id = ?;
