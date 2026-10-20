-- name: NextRoomSeq :one
UPDATE rooms SET next_seq = next_seq + 1, updated_at = ? WHERE id = ?
RETURNING *;

-- name: SetRoomLoopState :exec
UPDATE rooms SET agent_streak = ?, agents_paused = ? WHERE id = ?;

-- name: CreateMessage :exec
INSERT INTO messages (id, room_id, seq, author_kind, author_id, author_user_id, sender_id, sender_alt_id,
                      sender_name, kind, text, reply_to, recipients_json, external_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetMessageView :one
SELECT * FROM message_views WHERE id = ?;

-- name: GetMessageIDByExternalID :one
SELECT id FROM messages
WHERE room_id = ? AND author_kind = ? AND author_id = ? AND external_id = ?;

-- name: CountRoomMessage :one
SELECT COUNT(*) FROM messages WHERE id = ? AND room_id = ?;

-- name: ListMessagesBefore :many
SELECT * FROM message_views
WHERE room_id = sqlc.arg(room_id) AND seq < sqlc.arg(before_seq)
ORDER BY seq DESC
LIMIT sqlc.arg(limit);

-- name: ListMessagesAfter :many
SELECT * FROM message_views
WHERE room_id = sqlc.arg(room_id) AND seq > sqlc.arg(after_seq) AND seq < sqlc.arg(before_seq)
ORDER BY seq ASC
LIMIT sqlc.arg(limit);

-- name: CreateAttachment :exec
INSERT INTO attachments (id, message_id, sha256, mime, name, size, status, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListAttachmentsByMessages :many
SELECT * FROM attachments WHERE message_id IN (sqlc.slice(message_ids)) ORDER BY created_at, id;

-- name: CreateOutbox :exec
INSERT INTO outbox (id, message_id, chat_id, status, attempts, next_attempt_at, updated_at)
VALUES (?, ?, ?, 'pending', 0, ?, ?);

-- name: ListOutboxByMessages :many
SELECT * FROM outbox WHERE message_id IN (sqlc.slice(message_ids)) ORDER BY id;

-- name: ResumeRoom :exec
UPDATE rooms SET agent_streak = 0, agents_paused = 0, updated_at = ? WHERE id = ?;

-- name: GetAttachmentRoom :one
SELECT sqlc.embed(a), m.room_id
FROM attachments a JOIN messages m ON m.id = a.message_id
WHERE a.id = ?;
