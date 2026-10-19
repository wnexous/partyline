-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: CreateUser :exec
INSERT INTO users (id, username, display_name, password_hash, role, locale, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetUser :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = ?;

-- name: ListUsers :many
SELECT * FROM users ORDER BY username;

-- name: UpdateUserRole :exec
UPDATE users SET role = ? WHERE id = ?;

-- name: SetUserDisabledAt :exec
UPDATE users SET disabled_at = ? WHERE id = ?;

-- name: UpdateUserProfile :exec
UPDATE users SET display_name = ?, locale = ? WHERE id = ?;

-- name: UpdateUserPassword :exec
UPDATE users SET password_hash = ? WHERE id = ?;
