-- name: CreateConnector :exec
INSERT INTO connectors (id, owner_id, kind, name, config_json, secrets_enc, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetConnector :one
SELECT * FROM connectors WHERE id = ?;

-- name: GetConnectorWithOwner :one
SELECT sqlc.embed(c), u.username AS owner_username,
       CAST(COALESCE(ui.display_name, '') AS TEXT) AS owner_display_name
FROM connectors c JOIN users u ON u.id = c.owner_id
LEFT JOIN intelligences ui ON ui.id = u.id
WHERE c.id = ?;

-- name: ListConnectorsForUser :many
SELECT sqlc.embed(c), u.username AS owner_username,
       CAST(COALESCE(ui.display_name, '') AS TEXT) AS owner_display_name
FROM connectors c JOIN users u ON u.id = c.owner_id
LEFT JOIN intelligences ui ON ui.id = u.id
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
SELECT k.id AS connector_id, k.auto_route_room_id, k.auto_route_scope
FROM chats c JOIN connectors k ON k.id = c.connector_id
WHERE c.id = ?;

-- name: SetConnectorOperator :exec
UPDATE connectors SET operator_iid = ? WHERE id = ?;

-- name: SetConnectorAccountLabel :exec
UPDATE connectors SET account_label = ? WHERE id = ?;

-- name: ListConnectorsOperatedBy :many
-- The connectors an AI operates (CID-01, ACC-07).
SELECT * FROM connectors WHERE operator_iid = ? ORDER BY name, id;

-- name: GetConnectorOperator :one
-- Who operates the connection right now (CID-05); NULL while nobody does (CID-02).
SELECT operator_iid FROM connectors WHERE id = ?;

-- name: StopCatchAllConnectors :many
-- ROUTE-03: the connectors whose catch-all room is going to be deleted stop auto-routing and say
-- so. The foreign key empties their room on deletion, and an empty room with a scope on reads as
-- per-chat conversations (ACC-02), so the scope goes off with it.
UPDATE connectors SET auto_route_scope = 'off', last_error = sqlc.arg(detail)
WHERE auto_route_room_id = sqlc.arg(room_id)
RETURNING id, status;

-- name: UpdateConnectorDelivery :exec
-- CID-12, CID-27: which chats of the connection are delivered, and whether a group message needs a
-- mention of the account or a reply to it.
UPDATE connectors SET deliver = ?, groups_mentions_only = ?, updated_at = ? WHERE id = ?;
