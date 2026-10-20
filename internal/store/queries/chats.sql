-- name: CreateChat :exec
INSERT INTO chats (id, connector_id, external_id, title, kind, created_at) VALUES (?, ?, ?, ?, ?, ?);

-- name: GetChat :one
SELECT * FROM chats WHERE id = ?;
