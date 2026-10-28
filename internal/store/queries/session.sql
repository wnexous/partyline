-- name: CreateSessionDelivery :exec
-- Records that a message was pushed to the AI's label (SESS-24, SESS-37, SESS-38). owner_live is the live
-- id of the connection the message was pushed to, and '' for a row born fallen back (fell_back_at is the
-- dispatch time then: the message is the principal's from the start). A row is never deleted by an
-- acknowledgement, so a retried push is a no-op: the first row keeps the ownership it was given, and an
-- acknowledged message is never delivered again.
INSERT INTO agent_session_deliveries (message_id, agent_id, label, owner_live, created_at, fell_back_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (message_id, agent_id) DO NOTHING;

-- name: AckSessionDelivery :execrows
-- The acknowledgement of the connection that owns a pending row. The row stays as a tombstone, so the
-- principal's queue keeps excluding the message. Another connection of the AI, even one holding the same
-- label, and a row that already fell back or was acknowledged are left alone (SESS-24).
UPDATE agent_session_deliveries SET acked_at = sqlc.arg(acked_at)
WHERE message_id = sqlc.arg(message_id) AND agent_id = sqlc.arg(agent_id) AND owner_live = sqlc.arg(owner_live)
  AND fell_back_at IS NULL AND acked_at IS NULL;

-- name: AckFallbackDelivery :execrows
-- The principal's acknowledgement of a fallen-back row: it becomes a tombstone.
UPDATE agent_session_deliveries SET acked_at = sqlc.arg(acked_at)
WHERE message_id = sqlc.arg(message_id) AND agent_id = sqlc.arg(agent_id)
  AND fell_back_at IS NOT NULL AND acked_at IS NULL;

-- name: FallBackOwnedDeliveries :many
-- The end of one connection: its pending rows (and only its: owner_live is the connection's live id, so a
-- later connection that reused the label is untouched) become the principal's, and the rows that fell
-- back come back so the gateway can push each to a live principal with the note. A row whose message the
-- AI itself wrote is acknowledged instead of falling back, because the AI never receives its own message
-- (SESS-38): it keeps fell_back_at NULL, so the caller keeps only the rows with a fell_back_at.
UPDATE agent_session_deliveries
SET fell_back_at = CASE WHEN EXISTS (
        SELECT 1 FROM messages m
        WHERE m.id = agent_session_deliveries.message_id
          AND m.author_kind = 'agent' AND m.author_id = sqlc.arg(agent_id))
    THEN NULL ELSE sqlc.arg(now) END,
    acked_at = CASE WHEN EXISTS (
        SELECT 1 FROM messages m
        WHERE m.id = agent_session_deliveries.message_id
          AND m.author_kind = 'agent' AND m.author_id = sqlc.arg(agent_id))
    THEN sqlc.arg(now) ELSE NULL END
WHERE agent_id = sqlc.arg(agent_id) AND owner_live = sqlc.arg(owner_live)
  AND fell_back_at IS NULL AND acked_at IS NULL
RETURNING message_id, fell_back_at;

-- name: ListOwnedPending :many
-- What a connection owns and has not acknowledged, in sequence order: what a reconnecting session of the
-- same live id must receive again.
SELECT * FROM message_views
WHERE EXISTS (
      SELECT 1 FROM agent_session_deliveries d
      WHERE d.message_id = message_views.id AND d.agent_id = sqlc.arg(agent_id)
        AND d.owner_live = sqlc.arg(owner_live)
        AND d.fell_back_at IS NULL AND d.acked_at IS NULL)
ORDER BY seq ASC, id ASC;

-- name: GetSessionDelivery :one
-- The delivery row of a message for an AI (label, owner, fallback and acknowledgement), which routes an
-- edit or a delete; sql.ErrNoRows when the message was never pushed to a label.
SELECT * FROM agent_session_deliveries WHERE message_id = ? AND agent_id = ?;

-- Legacy queries below keep the callers of internal/agents compiling until the gateway is wired to the
-- owned rows; they are label-keyed and delete rows, so nothing new may use them.

-- name: DeleteSessionDelivery :exec
-- Deletes a fallen-back or unowned row: the principal's or a tools-only process's consumption.
DELETE FROM agent_session_deliveries WHERE message_id = ? AND agent_id = ?;

-- name: ReleaseSessionDelivery :execrows
-- A live labelled session's acknowledgement by deletion, keyed by the AI only.
DELETE FROM agent_session_deliveries
WHERE message_id = ? AND agent_id = ? AND fell_back_at IS NULL;

-- name: FallBackSessionDeliveries :many
-- The label-keyed end-of-session sweep.
UPDATE agent_session_deliveries SET fell_back_at = sqlc.arg(fell_back_at)
WHERE agent_id = sqlc.arg(agent_id) AND label = sqlc.arg(label) AND fell_back_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM messages m
      WHERE m.id = agent_session_deliveries.message_id
        AND m.author_kind = 'agent' AND m.author_id = sqlc.arg(agent_id))
RETURNING message_id;

-- name: DeleteSelfSessionDeliveries :execrows
-- The label-keyed drop of the AI's own pending rows.
DELETE FROM agent_session_deliveries
WHERE agent_id = sqlc.arg(agent_id) AND label = sqlc.arg(label) AND fell_back_at IS NULL
  AND EXISTS (
      SELECT 1 FROM messages m
      WHERE m.id = agent_session_deliveries.message_id
        AND m.author_kind = 'agent' AND m.author_id = sqlc.arg(agent_id));

-- name: ListSessionFallbacks :many
-- The principal's fallback queue of one room: the messages whose delivery to a label was not
-- acknowledged before that session ended, in the order they arrived. An acknowledged row is a tombstone
-- and is not listed, and neither is a message the AI wrote itself (SESS-38).
SELECT * FROM message_views
WHERE room_id = sqlc.arg(room_id)
  AND EXISTS (
      SELECT 1 FROM agent_session_deliveries d
      WHERE d.message_id = message_views.id AND d.agent_id = sqlc.arg(agent_id)
        AND d.fell_back_at IS NOT NULL AND d.acked_at IS NULL)
  AND NOT (author_kind = 'agent' AND author_id = sqlc.arg(agent_id))
ORDER BY seq ASC;

-- name: ListSessionFallbackLabels :many
-- The label each fallback row of one room belongs to, so the principal's backlog and pending reads can
-- render the note without reading the message text (SESS-22).
SELECT d.message_id, d.label
FROM agent_session_deliveries d
JOIN messages m ON m.id = d.message_id
WHERE d.agent_id = sqlc.arg(agent_id) AND m.room_id = sqlc.arg(room_id)
  AND d.fell_back_at IS NOT NULL AND d.acked_at IS NULL
  AND NOT (m.author_kind = 'agent' AND m.author_id = sqlc.arg(agent_id))
ORDER BY m.seq ASC;

-- name: ListSessionDeliveriesByLabel :many
-- The delivery rows of one label, in the order they were created (the end-of-session sweep reads the ids
-- of a label through FallBackSessionDeliveries' RETURNING; this lists them for tests and diagnostics).
SELECT * FROM agent_session_deliveries
WHERE agent_id = ? AND label = ?
ORDER BY created_at, message_id;
