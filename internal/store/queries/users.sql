-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: CreateUser :exec
INSERT INTO users (id, username, password_hash, role, locale, created_at)
VALUES (?, ?, ?, ?, ?, ?);

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

-- name: SetUserLocale :exec
UPDATE users SET locale = ? WHERE id = ?;

-- name: UpdateUserPassword :exec
UPDATE users SET password_hash = ? WHERE id = ?;

-- name: CountActiveAdmins :one
SELECT COUNT(*) FROM users WHERE role = 'admin' AND disabled_at IS NULL;

-- name: SetUserNotifyChat :exec
UPDATE users SET notify_chat_id = ? WHERE id = ?;

-- name: ListContactUsers :many
-- The enabled users other than the given one, with their intelligence, by name (CONT-01).
SELECT sqlc.embed(i) FROM users u JOIN intelligences i ON i.id = u.id
WHERE u.disabled_at IS NULL AND u.id <> ?
ORDER BY lower(i.display_name), i.id;
