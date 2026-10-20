-- name: CreateAgent :exec
INSERT INTO agents (id, owner_id, name, description, created_at) VALUES (?, ?, ?, ?, ?);

-- name: GetAgent :one
SELECT * FROM agents WHERE id = ?;

-- name: InsertAgentIfMissing :exec
INSERT INTO agents (id, owner_id, name, description, created_at) VALUES (?, ?, ?, '', ?)
ON CONFLICT (owner_id, name) DO NOTHING;

-- name: GetAgentByName :one
SELECT * FROM agents WHERE owner_id = ? AND name = ?;

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
