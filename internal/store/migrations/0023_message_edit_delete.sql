-- Edit and delete messages (messenger-polish, EDT/DLT, AD-026).
-- edited_at is the time (unix milliseconds) of the last edit of a message's text and deleted_at the time the
-- message was deleted. Both are NULL for a message that was never edited or deleted, which is every message
-- stored before this migration. A deleted message keeps its row - id, sequence number, author, time and
-- reply_to - with an empty text and no attachment rows, so that replies and the order stay intact; its
-- edited_at is cleared.
--
-- The columns are added with ADD COLUMN, so no table is rebuilt (AD-012). message_views lists the messages
-- column by column, so it is recreated to expose them; it is dropped first, as the migrations that changed
-- the table under it did.
DROP VIEW message_views;

ALTER TABLE messages ADD COLUMN edited_at INTEGER;
ALTER TABLE messages ADD COLUMN deleted_at INTEGER;

CREATE VIEW message_views AS
SELECT m.id, m.room_id, m.seq, m.author_kind, m.author_id, m.author_user_id, m.author_iid,
       m.sender_id, m.sender_alt_id, m.sender_name, m.kind, m.text, m.reply_to,
       m.recipients_json, m.external_id, m.created_at, m.edited_at, m.deleted_at,
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
