-- name: ListDueOutbox :many
SELECT o.id, o.message_id, o.chat_id, o.attempts, m.room_id
FROM outbox o JOIN messages m ON m.id = o.message_id
WHERE o.status = 'pending' AND o.next_attempt_at <= sqlc.arg(now)
ORDER BY o.next_attempt_at, o.id
LIMIT sqlc.arg(limit);

-- name: MarkOutboxSending :execrows
UPDATE outbox SET status = 'sending', updated_at = ? WHERE id = ? AND status = 'pending';

-- name: MarkOutboxSent :exec
UPDATE outbox SET status = 'sent', attempts = attempts + 1, external_id = ?, last_error = '', updated_at = ?
WHERE id = ?;

-- name: MarkOutboxAttemptFailed :exec
UPDATE outbox SET status = ?, attempts = ?, next_attempt_at = ?, last_error = ?, updated_at = ?
WHERE id = ?;

-- name: RequeueSendingOutbox :many
UPDATE outbox SET status = 'pending', updated_at = ? WHERE status = 'sending'
RETURNING id;
