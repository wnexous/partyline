-- name: CreateSessionDelivery :exec
-- Records that a message was pushed to the AI's label (SESS-24, SESS-37, SESS-38). A row exists only
-- while the message belongs to that label: the session's acknowledgement deletes it, or the end of the
-- session marks fell_back_at and the principal consumes it. fell_back_at is NULL for a live label and
-- the dispatch time when the label has no session (the message is the principal's from the start). A
-- retried push is a no-op: the first row keeps the ownership it was given.
INSERT INTO agent_session_deliveries (message_id, agent_id, label, created_at, fell_back_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (message_id, agent_id) DO NOTHING;

-- name: DeleteSessionDelivery :exec
-- Deletes a fallen-back or unowned row: the principal's or a tools-only process's consumption.
DELETE FROM agent_session_deliveries WHERE message_id = ? AND agent_id = ?;

-- name: AckSessionDelivery :execrows
-- A live labelled session's acknowledgement. A row that already fell back belongs to the principal, so it
-- is left alone: a late acknowledgement never consumes a message twice (SESS-24).
DELETE FROM agent_session_deliveries
WHERE message_id = ? AND agent_id = ? AND fell_back_at IS NULL;

-- name: FallBackSessionDeliveries :many
-- The end of a labelled session: the pending rows of that label become the principal's, and the ids come
-- back so the gateway can push each to a live principal with the note. A message the AI itself wrote is
-- never handed back to it (SESS-38).
UPDATE agent_session_deliveries SET fell_back_at = sqlc.arg(fell_back_at)
WHERE agent_id = sqlc.arg(agent_id) AND label = sqlc.arg(label) AND fell_back_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM messages m
      WHERE m.id = agent_session_deliveries.message_id
        AND m.author_kind = 'agent' AND m.author_id = sqlc.arg(agent_id))
RETURNING message_id;

-- name: DeleteSelfSessionDeliveries :execrows
-- The end of a labelled session drops the pending rows whose message the AI itself wrote instead of
-- falling them back: the AI must not receive its own text (SESS-38).
DELETE FROM agent_session_deliveries
WHERE agent_id = sqlc.arg(agent_id) AND label = sqlc.arg(label) AND fell_back_at IS NULL
  AND EXISTS (
      SELECT 1 FROM messages m
      WHERE m.id = agent_session_deliveries.message_id
        AND m.author_kind = 'agent' AND m.author_id = sqlc.arg(agent_id));

-- name: ListSessionFallbacks :many
-- The principal's fallback queue of one room: the messages whose delivery to a label was not
-- acknowledged before that session ended, in the order they arrived.
SELECT * FROM message_views
WHERE room_id = sqlc.arg(room_id)
  AND EXISTS (
      SELECT 1 FROM agent_session_deliveries d
      WHERE d.message_id = message_views.id AND d.agent_id = sqlc.arg(agent_id)
        AND d.fell_back_at IS NOT NULL)
ORDER BY seq ASC;

-- name: ListSessionFallbackLabels :many
-- The label each fallback row of one room belongs to, so the principal's backlog and pending reads can
-- render the note without reading the message text (SESS-22).
SELECT d.message_id, d.label
FROM agent_session_deliveries d
JOIN messages m ON m.id = d.message_id
WHERE d.agent_id = sqlc.arg(agent_id) AND m.room_id = sqlc.arg(room_id)
  AND d.fell_back_at IS NOT NULL
ORDER BY m.seq ASC;

-- name: ListSessionDeliveriesByLabel :many
-- The delivery rows of one label, in the order they were created (the end-of-session sweep reads the ids
-- of a label through FallBackSessionDeliveries' RETURNING; this lists them for tests and diagnostics).
SELECT * FROM agent_session_deliveries
WHERE agent_id = ? AND label = ?
ORDER BY created_at, message_id;
