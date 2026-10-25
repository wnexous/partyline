-- name: InsertPlatformSend :exec
-- CID-15, CID-16: an AI's answer to a chat of its connection. The row is created already claimed
-- (status sending) by the synchronous attempt that follows, so the retry worker never sends it a
-- second time; next_attempt_at is when it is due if the process dies in between.
INSERT INTO platform_sends (id, connector_id, chat_id, agent_id, text, reply_to_external_id, once, status,
                            attempts, next_attempt_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, 'sending', 0, ?, ?, ?);

-- name: GetPlatformSend :one
SELECT * FROM platform_sends WHERE id = ?;

-- name: GetPlatformSendAttempt :one
-- Everything one attempt needs besides the send: the platform's id of its chat, the connection's
-- operator now (CID-03), the AI's name (for platforms that carry the author as data) and the
-- sender of the message it quotes, when that message is still in the queue (CID-15).
SELECT sqlc.embed(s),
       c.external_id AS chat_external_id,
       k.operator_iid AS operator_iid,
       CAST(COALESCE(i.display_name, '') AS TEXT) AS agent_name,
       CAST(COALESCE((SELECT p.sender_id FROM platform_items p
                      WHERE p.chat_id = s.chat_id AND p.external_id <> '' AND p.external_id = s.reply_to_external_id), '') AS TEXT) AS reply_to_sender_id
FROM platform_sends s
JOIN chats c ON c.id = s.chat_id
JOIN connectors k ON k.id = s.connector_id
LEFT JOIN intelligences i ON i.id = s.agent_id
WHERE s.id = ?;

-- name: ListDuePlatformSends :many
SELECT * FROM platform_sends
WHERE status = 'pending' AND next_attempt_at <= sqlc.arg(now)
ORDER BY next_attempt_at, id
LIMIT sqlc.arg(limit);

-- name: ClaimPlatformSend :execrows
UPDATE platform_sends SET status = 'sending', updated_at = ? WHERE id = ? AND status = 'pending';

-- name: MarkPlatformSendSent :exec
-- CID-18: the text is deleted when the send is over; external_id (the platform's id of the first
-- message it created) stays, so that the pipeline can tell the echo of the send (CID-14).
UPDATE platform_sends
SET status = 'sent', attempts = attempts + 1, external_id = ?, last_error = '', text = '', updated_at = ?
WHERE id = ?;

-- name: MarkPlatformSendRetry :exec
-- A failed attempt: the send waits for the next one with its text.
UPDATE platform_sends
SET status = 'pending', attempts = ?, next_attempt_at = ?, last_error = ?, updated_at = ?
WHERE id = ?;

-- name: MarkPlatformSendFailed :exec
-- CID-18: a send that will not be tried again loses its text.
UPDATE platform_sends
SET status = 'failed', attempts = ?, last_error = ?, text = '', updated_at = ?
WHERE id = ?;

-- name: RequeueSendingPlatformSends :many
-- Sends a previous process left in flight are due again, except those that are attempted once (a
-- file is not kept, CID-32): those are failed by FailSendingOncePlatformSends.
UPDATE platform_sends SET status = 'pending', updated_at = ? WHERE status = 'sending' AND once = 0
RETURNING id;

-- name: FailSendingOncePlatformSends :many
-- A send of a file that a previous process left in flight cannot be repeated: its file is gone.
UPDATE platform_sends
SET status = 'failed', attempts = 1, last_error = ?, text = '', updated_at = ?
WHERE status = 'sending' AND once = 1
RETURNING id;

-- name: CountSendingPlatformSendsForChat :one
-- Sends to the chat that are in flight: an echo of one of them can reach the pipeline before the
-- row is marked sent (CID-14).
SELECT COUNT(*) FROM platform_sends WHERE chat_id = ? AND status = 'sending';

-- name: CountChatPlatformSendExternalID :one
-- Messages an AI sent through the connection to the chat that the platform knows by this id
-- (CID-14, CID-12). A send that is not over has no id yet.
SELECT COUNT(*) FROM platform_sends WHERE chat_id = ? AND external_id <> '' AND external_id = ?;

-- name: DeleteFinishedPlatformSends :execrows
-- A finished send keeps its row (without text) for the retention period, then goes: at most limit
-- rows are deleted at a time, oldest first.
DELETE FROM platform_sends WHERE id IN (
    SELECT d.id FROM platform_sends d
    WHERE d.status IN ('sent', 'failed') AND d.updated_at <= sqlc.arg(cutoff)
    ORDER BY d.updated_at
    LIMIT sqlc.arg(limit)
);
