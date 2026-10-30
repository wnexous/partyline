-- Session notifications (SN-03, SN-04, SN-09): a message can now reach more than one session of one AI -
-- the live session whose label the text addresses and every live session whose notification setting is
-- 'on', each of them together with the context it had not read - so the delivery row of a message is keyed
-- by the session it reached as well as by the message and the AI. `context` marks a copy: the message was
-- carried to that session as context or because the session follows the room, so the row is a routing
-- record (the acknowledgement moves the session's read position, and edits and deletes follow it) and it
-- never falls back to the principal. A row with context = 0 is the addressed delivery of SESS-22/SESS-24
-- and keeps exactly the ownership, the fallback and the tombstones it had. label is the session's label,
-- "" for the principal (which gets copies of the messages that address its siblings, SN-04): the old
-- CHECK that a row always had a label goes away with the rebuild SQLite needs to change the primary key.
CREATE TABLE agent_session_deliveries_new (
    message_id   TEXT NOT NULL REFERENCES messages (id) ON DELETE CASCADE,
    agent_id     TEXT NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    label        TEXT NOT NULL,
    session_key  TEXT NOT NULL DEFAULT '',
    owner_live   TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    fell_back_at INTEGER,
    acked_at     INTEGER,
    context      INTEGER NOT NULL DEFAULT 0 CHECK (context IN (0, 1)),
    PRIMARY KEY (message_id, agent_id, label)
);
INSERT INTO agent_session_deliveries_new (message_id, agent_id, label, session_key, owner_live, created_at,
                                          fell_back_at, acked_at, context)
SELECT message_id, agent_id, label,
       CASE WHEN label = '' THEN 'principal' ELSE 'label:' || label END,
       owner_live, created_at, fell_back_at, acked_at, 0
FROM agent_session_deliveries;
DROP TABLE agent_session_deliveries;
ALTER TABLE agent_session_deliveries_new RENAME TO agent_session_deliveries;
CREATE INDEX agent_session_deliveries_pending
    ON agent_session_deliveries (agent_id, owner_live) WHERE fell_back_at IS NULL AND acked_at IS NULL;
CREATE INDEX agent_session_deliveries_fallback
    ON agent_session_deliveries (agent_id) WHERE fell_back_at IS NOT NULL AND acked_at IS NULL;

-- SN-03 (decision): the messages an AI's own sessions wrote while the loop guard had paused the room (DLV-08)
-- are not context for the sessions that read the room later. The pause is a loop boundary: the delivery rules
-- suppressed every dispatch while it lasted, so replaying that burst as "what you missed" would feed the loop
-- back. The flag is written with the message, so it survives any number of pauses, resumes and restarts, and
-- the view is recreated to expose it (0023's rule: every column one by one, in order).
ALTER TABLE messages ADD COLUMN during_pause INTEGER NOT NULL DEFAULT 0;
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
       CAST(COALESCE(i.avatar_sha256, '') AS TEXT) AS author_avatar,
       m.during_pause
FROM messages m
LEFT JOIN intelligences i ON i.id = NULLIF(m.author_iid, '')
LEFT JOIN room_members rm ON m.author_kind = 'agent' AND rm.room_id = m.room_id
                         AND rm.kind = 'agent' AND rm.member_id = m.author_id
LEFT JOIN chats c ON m.author_kind = 'chat' AND c.id = m.author_id;
