-- name: DeleteExpiredMessages :execrows
DELETE FROM messages WHERE id IN (
    SELECT m.id FROM messages m JOIN rooms r ON r.id = m.room_id
    WHERE r.retention_days > 0 AND m.created_at < sqlc.arg(now) - r.retention_days * 86400000
    LIMIT sqlc.arg(limit)
);

-- name: CountAttachmentsBySHA :one
SELECT COUNT(*) FROM attachments WHERE sha256 = ?;

-- name: ListRoomAttachmentSHAs :many
SELECT DISTINCT a.sha256 FROM attachments a JOIN messages m ON m.id = a.message_id
WHERE m.room_id = ?;
