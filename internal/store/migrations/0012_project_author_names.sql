-- Agent authors in a room they reach through their project (PROJ-07) are named by the
-- project member's display name in that room when they have no display name of their own
-- there: room member display name, else project member display name, else the owner's
-- rename, else the temporary agent's readable name, else the agent name (ID-04).
DROP VIEW message_views;

CREATE VIEW message_views AS
SELECT m.id, m.room_id, m.seq, m.author_kind, m.author_id, m.author_user_id, m.sender_id,
       m.sender_alt_id, m.sender_name, m.kind, m.text, m.reply_to, m.recipients_json,
       m.external_id, m.created_at,
       CAST(CASE m.author_kind
            WHEN 'user' THEN COALESCE(NULLIF(u.display_name, ''), u.username, '')
            WHEN 'agent' THEN COALESCE(NULLIF(rm.display_name, ''), NULLIF(pm.display_name, ''),
                                       NULLIF(a.display_name, ''), NULLIF(a.auto_name, ''), a.name, '')
            WHEN 'chat' THEN COALESCE(NULLIF(m.sender_name, ''), c.title, '')
            ELSE 'Partyline'
            END AS TEXT) AS author_name
FROM messages m
LEFT JOIN users u ON m.author_kind = 'user' AND u.id = m.author_id
LEFT JOIN agents a ON m.author_kind = 'agent' AND a.id = m.author_id
LEFT JOIN room_members rm ON m.author_kind = 'agent' AND rm.room_id = m.room_id
                         AND rm.kind = 'agent' AND rm.member_id = m.author_id
LEFT JOIN room_members pm ON m.author_kind = 'agent' AND pm.room_id = m.room_id
                         AND pm.kind = 'project' AND pm.member_id = a.project_id
LEFT JOIN chats c ON m.author_kind = 'chat' AND c.id = m.author_id;
