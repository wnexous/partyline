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

-- name: MigrateChatExternalID :execrows
UPDATE chats SET external_id = sqlc.arg(new_external_id)
WHERE chats.connector_id = sqlc.arg(connector_id) AND chats.external_id = sqlc.arg(old_external_id)
  AND NOT EXISTS (SELECT 1 FROM chats o
                  WHERE o.connector_id = sqlc.arg(connector_id) AND o.external_id = sqlc.arg(new_external_id));

-- name: ListChatRefs :many
SELECT c.id, c.title, c.kind, k.kind AS platform
FROM chats c JOIN connectors k ON k.id = c.connector_id
WHERE c.id IN (sqlc.slice(chat_ids));

-- name: ListChatRooms :many
-- The rooms a chat is a member of with their kind, in one read: a platform DM chat belongs to a
-- DM (ACC-02).
SELECT r.id, r.kind FROM room_members m JOIN rooms r ON r.id = m.room_id
WHERE m.kind = 'chat' AND m.member_id = ?
ORDER BY r.id;
