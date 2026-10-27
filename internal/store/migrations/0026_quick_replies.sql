-- Quick replies and question wizards (QR-01..QR-09).
-- interactive_json is the question a message asks, empty for every other message: at most 4 quick
-- replies or a wizard of at most 8 questions, one versioned JSON document whose ids the server
-- assigns. answers_json is the structured answer of a reply, empty for every other message. Both are
-- only written when the message is created, and both are cleared when the message is deleted
-- (DLT-01, QR-04), so a deleted message keeps nothing of what it asked or answered.
ALTER TABLE messages ADD COLUMN interactive_json TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN answers_json TEXT NOT NULL DEFAULT '';

-- message_views lists the messages column by column, so it is recreated to expose the two new
-- columns; it is dropped first, as 0023 did. The author columns are unchanged.
DROP VIEW message_views;
CREATE VIEW message_views AS
SELECT m.id, m.room_id, m.seq, m.author_kind, m.author_id, m.author_user_id, m.author_iid,
       m.sender_id, m.sender_alt_id, m.sender_name, m.kind, m.text, m.reply_to,
       m.recipients_json, m.external_id, m.created_at, m.edited_at, m.deleted_at,
       m.interactive_json, m.answers_json,
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

-- One structured answer per author per question (QR-08): a member answers once; a deleted answer
-- clears answers_json, so deleting and answering again works. Plain replies carry an empty
-- answers_json and are not affected.
CREATE UNIQUE INDEX messages_answer_once ON messages (reply_to, author_iid)
    WHERE answers_json <> '';
