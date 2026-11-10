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

-- name: ListExpirableAttachments :many
-- Stored room attachments written before the cutoff, oldest first (RET-03).
SELECT id FROM attachments
WHERE status = 'stored' AND created_at < sqlc.arg(cutoff)
ORDER BY created_at, id
LIMIT sqlc.arg(limit);

-- name: ExpireAttachments :execrows
-- Marks the attachments expired and drops their hash, so no reference to the blob remains (RET-03, RET-04).
UPDATE attachments SET status = 'expired', sha256 = ''
WHERE status = 'stored' AND id IN (sqlc.slice(ids));

-- name: DeleteTranscriptionsByAttachments :exec
-- The server transcript of a voice file goes with its file (RET-05).
DELETE FROM transcriptions WHERE attachment_id IN (sqlc.slice(ids));
