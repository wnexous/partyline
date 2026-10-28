-- One AI, several sessions (ai-sessions, SESS-01..SESS-39).
-- messages.author_label is the session that wrote an agent message ("" = the principal, and every
-- message stored before this migration). It is authorship, and the only way the dispatcher knows which
-- session wrote a message (D11); the session a message is addressed to is parsed from the text at
-- delivery time.
ALTER TABLE messages ADD COLUMN author_label TEXT NOT NULL DEFAULT '';

-- The delivery state of a message that was pushed to a labelled session, and the fallback queue of a
-- session that ended before it acknowledged. A row exists once the message belongs to a label and is
-- never deleted by an acknowledgement: it stays as a tombstone, so the principal's queue keeps
-- excluding the message (it excludes any message that has a row for the AI). owner_live is the live id
-- of the connection the message was pushed to ('' for a row born fallen back, which no connection
-- owns): only that connection can acknowledge the row or have it swept when it ends, so a new
-- connection that reused the label never inherits the rows of a dead one. fell_back_at is set when the
-- owner ended before acknowledging (the principal owns the row from then on), acked_at when the owner,
-- or the principal for a fallen-back row, acknowledged it. A pending row of a message the same AI wrote
-- is acknowledged instead of falling back, because the AI never receives its own message (SESS-38).
-- The primary key makes a retried push idempotent.
CREATE TABLE agent_session_deliveries (
    message_id   TEXT NOT NULL REFERENCES messages (id) ON DELETE CASCADE,
    agent_id     TEXT NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    label        TEXT NOT NULL CHECK (label <> ''),
    owner_live   TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    fell_back_at INTEGER,
    acked_at     INTEGER,
    PRIMARY KEY (message_id, agent_id)
);
CREATE INDEX agent_session_deliveries_pending
    ON agent_session_deliveries (agent_id, owner_live) WHERE fell_back_at IS NULL AND acked_at IS NULL;
CREATE INDEX agent_session_deliveries_fallback
    ON agent_session_deliveries (agent_id) WHERE fell_back_at IS NOT NULL AND acked_at IS NULL;

-- The view lists its columns one by one, so it is recreated to expose author_label (0023's rule): it
-- keeps every column it had, including the question and answer columns of 0026, in their order, and adds
-- the author's session before the author's name.
DROP VIEW message_views;
CREATE VIEW message_views AS
SELECT m.id, m.room_id, m.seq, m.author_kind, m.author_id, m.author_user_id, m.author_iid,
       m.sender_id, m.sender_alt_id, m.sender_name, m.kind, m.text, m.reply_to,
       m.recipients_json, m.external_id, m.created_at, m.edited_at, m.deleted_at,
       m.interactive_json, m.answers_json,
       m.author_label,
       CAST(CASE
            WHEN m.author_kind = 'system' THEN 'Partyline'
            WHEN m.author_kind = 'agent' THEN COALESCE(NULLIF(rm.display_name, ''), i.display_name, '')
            WHEN m.author_kind = 'user' THEN COALESCE(i.display_name, '')
            ELSE COALESCE(i.display_name, NULLIF(m.sender_name, ''), c.title, '')
            END AS TEXT) AS author_name,
       CAST(COALESCE(i.handle, '') AS TEXT) AS author_handle,
       CAST(COALESCE(i.kind, '') AS TEXT) AS author_ikind,
       CAST(COALESCE(i.avatar_sha256, '') AS TEXT) AS author_avatar
FROM messages m
LEFT JOIN intelligences i ON i.id = NULLIF(m.author_iid, '')
LEFT JOIN room_members rm ON m.author_kind = 'agent' AND rm.room_id = m.room_id
                         AND rm.kind = 'agent' AND rm.member_id = m.author_id
LEFT JOIN chats c ON m.author_kind = 'chat' AND c.id = m.author_id;
