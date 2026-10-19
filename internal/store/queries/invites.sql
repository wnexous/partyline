-- name: CreateInvite :exec
INSERT INTO invites (id, token_hash, role, created_by, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetInviteByTokenHash :one
SELECT * FROM invites WHERE token_hash = ?;

-- name: ListInvites :many
SELECT sqlc.embed(i),
       c.username AS creator_username, c.display_name AS creator_display_name,
       COALESCE(u.username, '') AS used_by_username, COALESCE(u.display_name, '') AS used_by_display_name
FROM invites i
JOIN users c ON c.id = i.created_by
LEFT JOIN users u ON u.id = i.used_by
ORDER BY i.created_at DESC, i.id DESC;

-- name: MarkInviteUsed :execrows
UPDATE invites SET used_by = ?, used_at = ?
WHERE id = ? AND used_at IS NULL AND revoked_at IS NULL AND expires_at > ?;

-- name: RevokeInvite :execrows
UPDATE invites SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ?;
