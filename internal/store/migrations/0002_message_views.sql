-- message_views adds the display name of each message's author: the user's display name
-- (or username), the agent's name, the chat sender's name (or the chat title), or
-- "Partyline" for system messages.
CREATE VIEW message_views AS
SELECT m.id, m.room_id, m.seq, m.author_kind, m.author_id, m.author_user_id, m.sender_id,
       m.sender_alt_id, m.sender_name, m.kind, m.text, m.reply_to, m.recipients_json,
       m.external_id, m.created_at,
       CAST(CASE m.author_kind
            WHEN 'user' THEN COALESCE(NULLIF(u.display_name, ''), u.username, '')
            WHEN 'agent' THEN COALESCE(a.name, '')
            WHEN 'chat' THEN COALESCE(NULLIF(m.sender_name, ''), c.title, '')
            ELSE 'Partyline'
            END AS TEXT) AS author_name
FROM messages m
LEFT JOIN users u ON m.author_kind = 'user' AND u.id = m.author_id
LEFT JOIN agents a ON m.author_kind = 'agent' AND a.id = m.author_id
LEFT JOIN chats c ON m.author_kind = 'chat' AND c.id = m.author_id;
