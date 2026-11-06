-- name: CreateTranscription :exec
INSERT INTO transcriptions (attachment_id, message_id, room_id, state, text, attempts, created_at, updated_at)
VALUES (?, ?, ?, ?, '', 0, ?, ?);

-- name: ListTranscriptionsByMessages :many
SELECT attachment_id, message_id, state, text FROM transcriptions
WHERE message_id IN (sqlc.slice(message_ids));

-- name: CountPendingTranscriptions :one
-- Whether a message still waits for a transcript (TR-03).
SELECT COUNT(*) FROM transcriptions WHERE message_id = ? AND state = 'pending';

-- name: NextPendingTranscription :one
-- The oldest pending job with the blob it reads (TR-08).
SELECT t.attachment_id, t.message_id, t.room_id, t.attempts, a.sha256, a.mime, a.name
FROM transcriptions t JOIN attachments a ON a.id = t.attachment_id
WHERE t.state = 'pending'
ORDER BY t.created_at, t.attachment_id
LIMIT 1;

-- name: SetTranscriptionAttempts :exec
UPDATE transcriptions SET attempts = ?, updated_at = ? WHERE attachment_id = ? AND state = 'pending';

-- name: FinishTranscription :execrows
UPDATE transcriptions SET state = ?, text = ?, attempts = ?, updated_at = ?
WHERE attachment_id = ? AND state = 'pending';

-- name: FailPendingTranscriptions :execrows
-- With no service configured any more, nothing will finish the pending rows (TR-06).
UPDATE transcriptions SET state = 'failed', updated_at = ? WHERE state = 'pending';
