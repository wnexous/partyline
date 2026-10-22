-- name: CreateInvite :exec
INSERT INTO invites (id, token_hash, role, created_by, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetInviteByTokenHash :one
SELECT * FROM invites WHERE token_hash = ?;

-- name: ListInvites :many
SELECT sqlc.embed(i),
       c.username AS creator_username, CAST(COALESCE(ci.display_name, '') AS TEXT) AS creator_display_name,
       COALESCE(u.username, '') AS used_by_username, CAST(COALESCE(ui.display_name, '') AS TEXT) AS used_by_display_name
FROM invites i
JOIN users c ON c.id = i.created_by
LEFT JOIN intelligences ci ON ci.id = c.id
LEFT JOIN users u ON u.id = i.used_by
LEFT JOIN intelligences ui ON ui.id = u.id
ORDER BY i.created_at DESC, i.id DESC;

-- name: MarkInviteUsed :execrows
UPDATE invites SET used_by = ?, used_at = ?
WHERE id = ? AND used_at IS NULL AND revoked_at IS NULL AND expires_at > ?;

-- name: RevokeInvite :execrows
UPDATE invites SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ?;
