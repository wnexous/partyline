-- name: CreateAgent :exec
INSERT INTO agents (id, owner_id, name, description, created_at) VALUES (?, ?, ?, ?, ?);

-- name: GetAgent :one
SELECT * FROM agents WHERE id = ?;

-- name: InsertAgentIfMissing :exec
INSERT INTO agents (id, owner_id, name, description, created_at) VALUES (?, ?, ?, '', ?)
ON CONFLICT (owner_id, name) DO NOTHING;

-- name: GetAgentByName :one
SELECT * FROM agents WHERE owner_id = ? AND name = ?;

-- name: CountAgentsByOwner :one
SELECT COUNT(*) FROM agents WHERE owner_id = ?;

-- name: GetAgentWithOwner :one
SELECT sqlc.embed(a), u.username AS owner_username, u.display_name AS owner_display_name
FROM agents a JOIN users u ON u.id = a.owner_id
WHERE a.id = ?;

-- name: ListAgentsForUser :many
SELECT sqlc.embed(a), u.username AS owner_username, u.display_name AS owner_display_name
FROM agents a JOIN users u ON u.id = a.owner_id
WHERE a.owner_id = sqlc.arg(user_id)
   OR a.id IN (SELECT g.resource_id FROM grants g
               WHERE g.resource_kind = 'agent' AND g.subject IN (sqlc.arg(user_id), '*'))
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
SELECT r.id, r.name, r.agents_paused, CAST(COALESCE(c.seq, 0) AS INTEGER) AS cursor
FROM room_members m
JOIN rooms r ON r.id = m.room_id
LEFT JOIN agent_cursors c ON c.agent_id = m.member_id AND c.room_id = m.room_id
WHERE m.kind = 'agent' AND m.member_id = ?
ORDER BY r.name, r.id;

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

-- name: GetMessageRoomSeq :one
SELECT room_id, seq FROM messages WHERE id = ?;

-- name: AdvanceAgentCursor :exec
INSERT INTO agent_cursors (agent_id, room_id, seq) VALUES (?, ?, ?)
ON CONFLICT (agent_id, room_id) DO UPDATE SET seq = MAX(agent_cursors.seq, excluded.seq);

-- name: GetAgentCursor :one
SELECT seq FROM agent_cursors WHERE agent_id = ? AND room_id = ?;
