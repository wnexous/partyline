-- name: CreateConnector :exec
INSERT INTO connectors (id, owner_id, kind, name, config_json, secrets_enc, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetConnector :one
SELECT * FROM connectors WHERE id = ?;

-- name: GetConnectorWithOwner :one
SELECT sqlc.embed(c), u.username AS owner_username, u.display_name AS owner_display_name
FROM connectors c JOIN users u ON u.id = c.owner_id
WHERE c.id = ?;

-- name: ListConnectorsForUser :many
SELECT sqlc.embed(c), u.username AS owner_username, u.display_name AS owner_display_name
FROM connectors c JOIN users u ON u.id = c.owner_id
WHERE c.owner_id = sqlc.arg(user_id)
   OR c.id IN (SELECT g.resource_id FROM grants g
               WHERE g.resource_kind = 'connector' AND g.subject IN (sqlc.arg(user_id), '*'))
ORDER BY c.name, u.username, c.id;

-- name: UpdateConnector :exec
UPDATE connectors SET name = ?, config_json = ?, secrets_enc = ?, enabled = ?, updated_at = ?
WHERE id = ?;

-- name: DeleteConnectorChatMemberships :many
DELETE FROM room_members
WHERE kind = 'chat' AND member_id IN (SELECT id FROM chats WHERE connector_id = ?)
RETURNING room_id;

-- name: DeleteConnector :exec
DELETE FROM connectors WHERE id = ?;

-- name: ListEnabledConnectorIDs :many
SELECT id FROM connectors WHERE enabled = 1 ORDER BY id;

-- name: UpdateConnectorStatus :exec
UPDATE connectors SET status = ?, last_error = ? WHERE id = ?;

-- name: UpdateConnectorAutoRoute :exec
UPDATE connectors SET auto_route_scope = ?, auto_route_room_id = ?, updated_at = ? WHERE id = ?;

-- name: SetConnectorLastError :exec
UPDATE connectors SET last_error = ? WHERE id = ?;

-- name: GetAutoRouteSkip :one
SELECT until FROM auto_route_skips WHERE connector_id = ? AND chat_id = ?;

-- name: UpsertAutoRouteSkip :exec
INSERT INTO auto_route_skips (connector_id, chat_id, until) VALUES (?, ?, ?)
ON CONFLICT (connector_id, chat_id) DO UPDATE SET until = excluded.until;

-- name: GetChatAutoRoute :one
SELECT k.id AS connector_id, k.auto_route_room_id
FROM chats c JOIN connectors k ON k.id = c.connector_id
WHERE c.id = ?;
