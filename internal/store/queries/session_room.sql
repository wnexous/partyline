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
-- (CreateSessionRoomState).
SELECT m.room_id, COUNT(*) AS unread
FROM messages m
JOIN room_members rm ON rm.room_id = m.room_id AND rm.kind = 'agent' AND rm.member_id = sqlc.arg(agent_id)
LEFT JOIN session_room_state st ON st.room_id = m.room_id AND st.agent_id = sqlc.arg(agent_id)
                               AND st.session_key = sqlc.arg(session_key)
WHERE m.seq > COALESCE(st.read_seq, 0)
  AND m.deleted_at IS NULL
  AND NOT (m.author_kind = 'agent' AND m.author_id = sqlc.arg(agent_id) AND m.author_label = sqlc.arg(author_label))
GROUP BY m.room_id;

-- name: ListSessionUnread :many
-- The messages of one room a session has not read, at most limit of them, oldest first: the catch-up
-- batch that goes with a message that notifies the session (SN-03). The cap keeps the newest ones (a
-- limit of 50 reads the last 50 and the caller notes how many earlier ones it skipped) and the batch is
-- ordered oldest first. The session's own messages are not part of it (SN-06).
SELECT * FROM (
    SELECT * FROM message_views
    WHERE room_id = sqlc.arg(room_id) AND seq > sqlc.arg(after_seq)
      AND deleted_at IS NULL
      AND NOT (author_kind = 'agent' AND author_id = sqlc.arg(agent_id) AND author_label = sqlc.arg(author_label))
    ORDER BY seq DESC, id DESC
    LIMIT sqlc.arg(limit)
)
ORDER BY seq ASC, id ASC;

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
