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
SELECT t.attachment_id, t.message_id, t.room_id, t.attempts, t.created_at, a.sha256, a.mime, a.name
FROM transcriptions t JOIN attachments a ON a.id = t.attachment_id
WHERE t.state = 'pending'
ORDER BY t.created_at, t.attachment_id
LIMIT 1;

-- name: SetTranscriptionAttempts :execrows
UPDATE transcriptions SET attempts = ?, updated_at = ? WHERE attachment_id = ? AND state = 'pending';

-- name: FinishTranscription :execrows
UPDATE transcriptions SET state = ?, text = ?, attempts = ?, updated_at = ?
WHERE attachment_id = ? AND state = 'pending';

-- name: FailPendingTranscriptions :execrows
-- With no service configured any more, nothing will finish the pending rows (TR-06).
UPDATE transcriptions SET state = 'failed', updated_at = ? WHERE state = 'pending';

-- name: CreatePlatformTranscription :exec
INSERT INTO platform_transcriptions (attachment_id, item_id, state, text, attempts, created_at, updated_at)
VALUES (?, ?, ?, '', 0, ?, ?);

-- name: ListPlatformTranscriptionsByItems :many
SELECT attachment_id, item_id, state, text, created_at FROM platform_transcriptions
WHERE item_id IN (sqlc.slice(item_ids));

-- name: NextPendingPlatformTranscription :one
-- The oldest pending job of a platform file with the blob it reads (TR-04, TR-08).
SELECT t.attachment_id, t.item_id, t.attempts, t.created_at, a.sha256, a.mime, a.name
FROM platform_transcriptions t JOIN platform_attachments a ON a.id = t.attachment_id
WHERE t.state = 'pending'
ORDER BY t.created_at, t.attachment_id
LIMIT 1;

-- name: SetPlatformTranscriptionAttempts :execrows
UPDATE platform_transcriptions SET attempts = ?, updated_at = ? WHERE attachment_id = ? AND state = 'pending';

-- name: FinishPlatformTranscription :one
-- The item's AI is the one the feed to wake; no row when the file went away or the item was
-- acknowledged meanwhile.
UPDATE platform_transcriptions SET state = ?, text = ?, attempts = ?, updated_at = ?
WHERE attachment_id = ? AND state = 'pending'
RETURNING (SELECT agent_id FROM platform_items WHERE id = platform_transcriptions.item_id) AS agent_id;

-- name: DeletePlatformTranscriptionsOfItem :exec
-- CID-09: the transcript is platform text and goes with the ack; a pending job of the item is
-- dropped, since the item is never delivered again.
DELETE FROM platform_transcriptions WHERE item_id = ?;

-- name: FailPendingPlatformTranscriptions :execrows
-- With no service configured any more, nothing will finish the pending rows (TR-06).
UPDATE platform_transcriptions SET state = 'failed', updated_at = ? WHERE state = 'pending';
