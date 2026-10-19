-- name: CreateSession :exec
INSERT INTO sessions (id, token_hash, user_id, created_at, expires_at, last_seen_at, user_agent, ip)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetSessionByTokenHash :one
SELECT * FROM sessions WHERE token_hash = ?;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id = ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = ?;

-- name: DeleteUserSession :execrows
DELETE FROM sessions WHERE id = ? AND user_id = ?;

-- name: DeleteSessionsByUser :exec
DELETE FROM sessions WHERE user_id = ?;

-- name: ListSessionsByUser :many
SELECT * FROM sessions WHERE user_id = ? AND expires_at > ? ORDER BY last_seen_at DESC, id DESC;
