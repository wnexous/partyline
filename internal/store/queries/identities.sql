-- name: GetIdentity :one
SELECT * FROM identities WHERE id = ?;

-- name: CreatePairingCode :exec
INSERT INTO pairing_codes (code_hash, user_id, connector_id, expires_at) VALUES (?, ?, ?, ?);

-- name: DeleteExpiredPairingCodes :exec
DELETE FROM pairing_codes WHERE expires_at <= ?;

-- name: TakePairingCode :one
DELETE FROM pairing_codes WHERE code_hash = sqlc.arg(code_hash) AND connector_id = sqlc.arg(connector_id)
RETURNING user_id, expires_at;

-- name: DeleteIdentitiesForSender :exec
DELETE FROM identities
WHERE connector_id = sqlc.arg(connector_id)
  AND (external_id IN (sqlc.arg(sender_id), sqlc.arg(alt_id))
       OR (alt_external_id <> '' AND alt_external_id IN (sqlc.arg(sender_id), sqlc.arg(alt_id))));

-- name: CreateIdentity :exec
INSERT INTO identities (id, user_id, connector_id, connector_kind, external_id, alt_external_id, display, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ResolveIdentityUser :one
SELECT i.user_id FROM identities i JOIN users u ON u.id = i.user_id
WHERE i.connector_id = sqlc.arg(connector_id) AND u.disabled_at IS NULL
  AND (i.external_id IN (sqlc.arg(sender_id), sqlc.arg(alt_id))
       OR (i.alt_external_id <> '' AND i.alt_external_id IN (sqlc.arg(sender_id), sqlc.arg(alt_id))))
ORDER BY i.created_at DESC
LIMIT 1;

-- name: ListIdentities :many
SELECT * FROM identities WHERE user_id = ? ORDER BY connector_kind, created_at, id;

-- name: DeleteIdentity :execrows
DELETE FROM identities WHERE id = ? AND user_id = ?;

-- name: DeleteConnectorIdentities :execrows
DELETE FROM identities WHERE connector_id = ?;
