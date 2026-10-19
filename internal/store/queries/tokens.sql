-- name: CreateToken :exec
INSERT INTO api_tokens (id, user_id, kind, name, prefix, token_hash, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetTokenByHash :one
SELECT * FROM api_tokens WHERE token_hash = ?;

-- name: ListTokensByUser :many
SELECT * FROM api_tokens WHERE user_id = ? ORDER BY created_at DESC, id DESC;

-- name: RevokeToken :execrows
UPDATE api_tokens SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ? AND user_id = ?;

-- name: TouchToken :exec
UPDATE api_tokens SET last_used_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND (last_used_at IS NULL OR last_used_at <= sqlc.arg(stale_before));
