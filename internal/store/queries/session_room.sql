-- name: GetRoomHighSeq :one
-- The highest sequence number the room has ever assigned: rooms.next_seq is the next one (MSG-01), so a
-- session that joins now is caught up at this point (SN-01). It is counted from next_seq, not from
-- MAX(messages.seq), because retention may have deleted the newest messages and the join point must not
-- move back.
SELECT CAST(next_seq - 1 AS INTEGER) AS seq FROM rooms WHERE id = ?;

-- name: CreateSessionRoomState :exec
-- The state of a session in a room on first sight (SN-01, SN-02): read up to the join point and set to
-- the session's default. An existing row is left alone, so a session that connects again keeps what it
-- had read and its setting.
INSERT INTO session_room_state (agent_id, session_key, room_id, read_seq, notify, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (agent_id, session_key, room_id) DO NOTHING;

-- name: GetSessionRoomState :one
-- The state of one session in one room (SN-01, SN-02); sql.ErrNoRows for a session that has not joined it.
SELECT * FROM session_room_state WHERE agent_id = ? AND session_key = ? AND room_id = ?;

-- name: HasSessionDelivery :one
-- Whether a message already reached one session of the AI (as the addressed delivery or as a copy of a
-- catch-up batch), so a retried push is not written again (SN-03).
SELECT EXISTS (
    SELECT 1 FROM agent_session_deliveries d
    WHERE d.message_id = sqlc.arg(message_id) AND d.agent_id = sqlc.arg(agent_id) AND d.label = sqlc.arg(label)
) AS delivered;

-- name: AdvanceSessionReadSeq :exec
-- Moves a read position forward, never back: an acknowledgement that arrives late, or a catch-up older
-- than a live push that already moved it, cannot make a session unread what it has read. notify is the
-- session's default and only applies to a row created here; an existing setting is kept.
INSERT INTO session_room_state (agent_id, session_key, room_id, read_seq, notify, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (agent_id, session_key, room_id) DO UPDATE SET
    read_seq = MAX(session_room_state.read_seq, excluded.read_seq),
    updated_at = excluded.updated_at;

-- name: SetSessionNotify :execrows
-- The session's notification setting in one room (SN-02). 0 rows when the session has no state row there
-- yet: the caller creates it at its join point first.
UPDATE session_room_state SET notify = sqlc.arg(notify), updated_at = sqlc.arg(updated_at)
WHERE agent_id = sqlc.arg(agent_id) AND session_key = sqlc.arg(session_key) AND room_id = sqlc.arg(room_id);

-- name: ListSessionRoomStates :many
-- Every room where the session has a state row, by room (SN-01, SN-02).
SELECT * FROM session_room_state WHERE agent_id = ? AND session_key = ? ORDER BY room_id;

-- name: CountSessionUnreadByRoom :many
-- Per room of the AI, the messages after the session's read position that it did not write itself
-- (SN-05, SN-06): a message a sibling session wrote is unread for it, its own never is. A deleted
-- message keeps its place in the stream but does not count, and a room with nothing unread has no row. A
-- missing state row counts from 0, so a caller that wants the join-point default creates it first
-- (CreateSessionRoomState). What the AI missed while it was not a member of the room does not count
-- (ROOM-01): nothing at or below the sequence the room had when the membership row was created is.
SELECT m.room_id, COUNT(*) AS unread
FROM messages m
JOIN room_members rm ON rm.room_id = m.room_id AND rm.kind = 'agent' AND rm.member_id = sqlc.arg(agent_id)
LEFT JOIN session_room_state st ON st.room_id = m.room_id AND st.agent_id = sqlc.arg(agent_id)
                               AND st.session_key = sqlc.arg(session_key)
WHERE m.seq > COALESCE(st.read_seq, 0)
  AND m.seq > COALESCE((SELECT MAX(b.seq) FROM messages b
                        WHERE b.room_id = m.room_id AND b.created_at < rm.created_at), 0)
  AND m.deleted_at IS NULL
  AND NOT (m.author_kind = 'agent' AND m.author_id = sqlc.arg(agent_id) AND m.author_label = sqlc.arg(author_label))
GROUP BY m.room_id;

-- name: ListSessionUnread :many
-- The messages of one room a session has not read, at most limit of them, oldest first: the catch-up
-- batch that goes with a message that notifies the session (SN-03). The cap keeps the newest ones (a
-- limit of 50 reads the last 50 and the caller notes how many earlier ones it skipped) and the batch is
-- ordered oldest first. The session's own messages are not part of it (SN-06), the message that is being
-- pushed now is not either (before_seq), and neither is one that session acknowledged. A message it was
-- given but never acknowledged is part of it again: a batch lost with its connection comes back (SN-03).
-- Nothing from before the AI's current membership of the room is part of it (ROOM-01), and neither is an
-- agent message written while the loop guard had paused the room (SN-03, DLV-08).
SELECT * FROM (
    SELECT mv.* FROM message_views mv
    JOIN room_members rm ON rm.room_id = mv.room_id AND rm.kind = 'agent' AND rm.member_id = sqlc.arg(agent_id)
    WHERE mv.room_id = sqlc.arg(room_id) AND mv.seq > sqlc.arg(after_seq) AND mv.seq < sqlc.arg(before_seq)
      AND mv.seq > COALESCE((SELECT MAX(b.seq) FROM messages b
                             WHERE b.room_id = mv.room_id AND b.created_at < rm.created_at), 0)
      AND mv.deleted_at IS NULL
      AND mv.during_pause = 0
      AND NOT (mv.author_kind = 'agent' AND mv.author_id = sqlc.arg(agent_id) AND mv.author_label = sqlc.arg(author_label))
      AND NOT EXISTS (
          SELECT 1 FROM agent_session_deliveries d
          WHERE d.message_id = mv.id AND d.agent_id = sqlc.arg(agent_id) AND d.label = sqlc.arg(label)
            AND d.acked_at IS NOT NULL)
    ORDER BY mv.seq DESC, mv.id DESC
    LIMIT sqlc.arg(limit)
)
ORDER BY seq ASC, id ASC;

-- name: CountSessionUnreadInRoom :one
-- How many messages of one room a session has not read and that ListSessionUnread would list (SN-03): the
-- difference between this count and the number listed is the "N earlier messages skipped" note of the
-- catch-up batch. Only an acknowledged message is left out, so an unacknowledged batch counts again
-- (SN-03). The messages of the room from before the AI's current membership are not part of it (ROOM-01),
-- and neither are the agent messages written while the loop guard had paused the room (SN-03, DLV-08).
SELECT COUNT(*) FROM messages m
JOIN room_members rm ON rm.room_id = m.room_id AND rm.kind = 'agent' AND rm.member_id = sqlc.arg(agent_id)
WHERE m.room_id = sqlc.arg(room_id) AND m.seq > sqlc.arg(after_seq) AND m.seq < sqlc.arg(before_seq)
  AND m.seq > COALESCE((SELECT MAX(b.seq) FROM messages b
                        WHERE b.room_id = m.room_id AND b.created_at < rm.created_at), 0)
  AND m.deleted_at IS NULL
  AND m.during_pause = 0
  AND NOT (m.author_kind = 'agent' AND m.author_id = sqlc.arg(agent_id) AND m.author_label = sqlc.arg(author_label))
  AND NOT EXISTS (
      SELECT 1 FROM agent_session_deliveries d
      WHERE d.message_id = m.id AND d.agent_id = sqlc.arg(agent_id) AND d.label = sqlc.arg(label)
        AND d.acked_at IS NOT NULL);

-- name: ListSessionRoomStatesInRoom :many
-- Every state row of the AI in one room, whatever its session: the notification setting and the read
-- position of each session a message may notify (SN-02, SN-03).
SELECT * FROM session_room_state WHERE agent_id = sqlc.arg(agent_id) AND room_id = sqlc.arg(room_id);

-- name: DeleteSessionRoomStatesInRoom :exec
-- The read positions of an AI in one room, dropped when it is taken out of it (ROOM-01): what it missed
-- while it was away never comes back as context or as unread after it is added again.
DELETE FROM session_room_state WHERE agent_id = sqlc.arg(agent_id) AND room_id = sqlc.arg(room_id);

-- name: DeleteAgentCursor :exec
-- The principal's queue position in one room, dropped when the AI is taken out of it (ROOM-01).
DELETE FROM agent_cursors WHERE agent_id = sqlc.arg(agent_id) AND room_id = sqlc.arg(room_id);

-- name: AdvancePrincipalReadSeq :exec
-- Mirrors the agent's delivery cursor into the read position of its principal session, in the transaction
-- that moves the cursor (SN-01): agent_cursors stays the source of truth of the principal's backlog, so every
-- place that moves it writes both. The principal's key is its pinned harness session id, or "principal" when
-- nothing is pinned (internal/agents.SessionKey), which is what the SELECT computes; the row is created when
-- the principal has none yet, notified of everything (SN-02).
INSERT INTO session_room_state (agent_id, session_key, room_id, read_seq, notify, updated_at)
SELECT a.id, CASE WHEN a.principal_session <> '' THEN a.principal_session ELSE 'principal' END,
       sqlc.arg(room_id), sqlc.arg(read_seq), 'on', sqlc.arg(updated_at)
FROM agents a
WHERE a.id = sqlc.arg(agent_id)
ON CONFLICT (agent_id, session_key, room_id) DO UPDATE SET
    read_seq = MAX(session_room_state.read_seq, excluded.read_seq),
    updated_at = excluded.updated_at;

-- name: GetSessionJoinSeq :one
-- The join point of a session that has no state row in a room: the highest sequence the room had assigned
-- when the AI became a member of it (SN-01). A session with a live connection that joins a room after it
-- attached - a DM created for the AI, a group it was added to - starts caught up at that moment, not at 0,
-- so it is not flooded with the room's history, and the messages of the room that arrive afterwards are
-- unread for it. The AI's membership row is created with the room (a DM) or when it is added (a group),
-- and messages carry the same creation clock.
SELECT CAST(COALESCE(MAX(m.seq), 0) AS INTEGER) AS seq
FROM room_members rm
LEFT JOIN messages m ON m.room_id = rm.room_id AND m.created_at <= rm.created_at
WHERE rm.room_id = sqlc.arg(room_id) AND rm.kind = 'agent' AND rm.member_id = sqlc.arg(agent_id);
