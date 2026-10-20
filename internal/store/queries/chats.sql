-- name: CreateChat :exec
INSERT INTO chats (id, connector_id, external_id, title, kind, created_at) VALUES (?, ?, ?, ?, ?, ?);

-- name: GetChat :one
SELECT * FROM chats WHERE id = ?;

-- name: GetChatByExternalID :one
SELECT * FROM chats WHERE connector_id = ? AND external_id = ?;

-- name: UpsertChat :one
INSERT INTO chats (id, connector_id, external_id, title, kind, last_seen_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (connector_id, external_id) DO UPDATE SET
    title = CASE WHEN excluded.title <> '' THEN excluded.title ELSE chats.title END,
    kind = CASE WHEN excluded.kind <> '' THEN excluded.kind ELSE chats.kind END,
    last_seen_at = COALESCE(excluded.last_seen_at, chats.last_seen_at)
RETURNING *;

-- name: ListChatRoomIDs :many
SELECT room_id FROM room_members WHERE kind = 'chat' AND member_id = ? ORDER BY room_id;

-- name: ListConnectorChats :many
SELECT * FROM chats WHERE connector_id = ? ORDER BY title, external_id, id;

-- name: ListUsableChats :many
SELECT sqlc.embed(c), sqlc.embed(k)
FROM chats c JOIN connectors k ON k.id = c.connector_id
WHERE k.owner_id = sqlc.arg(user_id)
   OR k.id IN (SELECT g.resource_id FROM grants g
               WHERE g.resource_kind = 'connector' AND g.subject IN (sqlc.arg(user_id), '*'))
ORDER BY k.name, c.title, c.id;

-- name: FindChatMessageRef :one
SELECT m.id FROM messages m
WHERE m.room_id = sqlc.arg(room_id)
  AND ((m.author_kind = 'chat' AND m.author_id = sqlc.arg(chat_id) AND m.external_id = sqlc.arg(external_id))
       OR m.id IN (SELECT o.message_id FROM outbox o
                   WHERE o.chat_id = sqlc.arg(chat_id) AND o.external_id = sqlc.arg(external_id)))
ORDER BY m.seq
LIMIT 1;
