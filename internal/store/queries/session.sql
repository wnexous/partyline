-- name: CreateSessionDelivery :exec
-- Records that a message was pushed to the AI's label (SESS-24, SESS-37, SESS-38, SN-03). owner_live is the
-- live id of the connection the message was pushed to, and '' for a row born fallen back (fell_back_at is
-- the dispatch time then: the message is the principal's from the start). A row is never deleted by an
-- acknowledgement, so a retried push is a no-op: the first row keeps the ownership it was given, and an
-- acknowledged message is never delivered again. context marks a copy that never falls back (SN-03): the
-- context of a catch-up batch, or a notification the session follows the room for.
INSERT INTO agent_session_deliveries (message_id, agent_id, label, owner_live, created_at, fell_back_at, context)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (message_id, agent_id, label) DO NOTHING;

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
-- The end of one connection: its pending addressed rows (and only its: owner_live is the connection's live
-- id, so a later connection that reused the label is untouched) become the principal's, and the rows that
-- fell back come back so the gateway can push each to a live principal with the note. A row whose message
-- the AI itself wrote is acknowledged instead of falling back, because the AI never receives its own
-- message (SESS-38): it keeps fell_back_at NULL, so the caller keeps only the rows with a fell_back_at. A
-- copy (context = 1) is the context of a catch-up batch: the session was not the message's owner, so it is
-- left alone and never falls back (SN-03).
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
  AND context = 0 AND fell_back_at IS NULL AND acked_at IS NULL
RETURNING message_id, label, fell_back_at;

-- name: ListOwnedPending :many
-- What a connection owns and has not acknowledged, in sequence order: what a reconnecting session of the
-- same live id must receive again. Only the addressed deliveries (context = 0) are the session's own
-- backlog; a copy of a catch-up batch is re-read from the session's read position instead (SN-03).
SELECT * FROM message_views
WHERE EXISTS (
      SELECT 1 FROM agent_session_deliveries d
      WHERE d.message_id = message_views.id AND d.agent_id = sqlc.arg(agent_id)
        AND d.owner_live = sqlc.arg(owner_live)
        AND d.context = 0 AND d.fell_back_at IS NULL AND d.acked_at IS NULL)
ORDER BY seq ASC, id ASC;

-- name: GetSessionDelivery :one
-- The delivery row of a message for one session of an AI (label, owner, fallback and acknowledgement),
-- which routes an edit or a delete; sql.ErrNoRows when the message was never pushed to that session.
SELECT * FROM agent_session_deliveries WHERE message_id = ? AND agent_id = ? AND label = ?;

-- name: ListSessionDeliveries :many
-- Every delivery row of a message for an AI, one per session it reached: the addressed session and each
-- session whose catch-up batch carried it, which is every session an edit or a delete must follow (SN-09).
SELECT * FROM agent_session_deliveries WHERE message_id = ? AND agent_id = ? ORDER BY label;

-- name: ListSessionFallbacks :many
-- The principal's fallback queue of one room: the messages whose delivery to a label was not
-- acknowledged before that session ended, in the order they arrived. An acknowledged row is a tombstone
-- and is not listed, and neither is a message the AI wrote itself (SESS-38).
SELECT * FROM message_views
WHERE room_id = sqlc.arg(room_id)
  AND EXISTS (
      SELECT 1 FROM agent_session_deliveries d
      WHERE d.message_id = message_views.id AND d.agent_id = sqlc.arg(agent_id)
        AND d.context = 0 AND d.fell_back_at IS NOT NULL AND d.acked_at IS NULL)
  AND NOT (author_kind = 'agent' AND author_id = sqlc.arg(agent_id))
ORDER BY seq ASC;

-- name: ListSessionFallbackLabels :many
-- The label each fallback row of one room belongs to, so the principal's backlog and pending reads can
-- render the note without reading the message text (SESS-22).
SELECT d.message_id, d.label
FROM agent_session_deliveries d
JOIN messages m ON m.id = d.message_id
WHERE d.agent_id = sqlc.arg(agent_id) AND m.room_id = sqlc.arg(room_id)
  AND d.context = 0 AND d.fell_back_at IS NOT NULL AND d.acked_at IS NULL
  AND NOT (m.author_kind = 'agent' AND m.author_id = sqlc.arg(agent_id))
ORDER BY m.seq ASC;

-- name: ListSessionDeliveriesByLabel :many
-- The delivery rows of one label, in the order they were created (the end of a connection sweeps its own
-- rows through FallBackOwnedDeliveries; this lists a label's rows for tests and diagnostics).
SELECT * FROM agent_session_deliveries
WHERE agent_id = ? AND label = ?
ORDER BY created_at, message_id;
