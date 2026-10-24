-- name: CreateConnector :exec
-- owner_id is the creator and a dead anchor since AD-019; org_id is the owning organization (ORG-02).
INSERT INTO connectors (id, owner_id, org_id, kind, name, config_json, secrets_enc, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetConnector :one
SELECT * FROM connectors WHERE id = ?;

-- name: GetConnectorWithOwner :one
SELECT sqlc.embed(c), u.username AS owner_username,
       CAST(COALESCE(ui.display_name, '') AS TEXT) AS owner_display_name
FROM connectors c JOIN users u ON u.id = c.owner_id
LEFT JOIN intelligences ui ON ui.id = u.id
WHERE c.id = ?;

-- name: ListConnectorsOfMember :many
-- The connections of the organizations an identity belongs to, whatever its permission, with that
-- permission (AD-016): admins manage them, everybody else uses them.
SELECT sqlc.embed(c), u.username AS owner_username,
       CAST(COALESCE(ui.display_name, '') AS TEXT) AS owner_display_name,
       m.permission
FROM connectors c
JOIN org_members m ON m.org_id = c.org_id AND m.iid = sqlc.arg(iid)
JOIN users u ON u.id = c.owner_id
LEFT JOIN intelligences ui ON ui.id = u.id
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

-- name: SetConnectorLastError :exec
UPDATE connectors SET last_error = ? WHERE id = ?;

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

-- name: UpdateConnectorDelivery :exec
-- CID-12, CID-27: which chats of the connection are delivered, and whether a group message needs a
-- mention of the account or a reply to it.
UPDATE connectors SET deliver = ?, groups_mentions_only = ?, updated_at = ? WHERE id = ?;
