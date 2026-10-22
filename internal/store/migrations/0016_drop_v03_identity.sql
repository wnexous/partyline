-- Drop the v0.3 identity columns (intelligences feature, AD-010, AD-012).
-- Names live in intelligences since 0015; no code reads the columns below any more. Nothing
-- here rebuilds a table: with foreign_keys=ON a DROP TABLE of a referenced table deletes its
-- referencing rows (the implicit DELETE cascades), so columns go with ALTER TABLE ... DROP
-- COLUMN, which SQLite refuses while a view, trigger, index or constraint names the column.
-- None does but message_views (recreated below): the indexes of users and agents cover
-- other columns, the CHECK on agents.kind belongs to the column being dropped, and the
-- intelligence triggers use OLD.id only.
DROP VIEW message_views;

ALTER TABLE users DROP COLUMN display_name;            -- moved to intelligences
ALTER TABLE agents DROP COLUMN display_name;
ALTER TABLE agents DROP COLUMN auto_name;
ALTER TABLE agents DROP COLUMN kind;
ALTER TABLE agents DROP COLUMN session_no;
ALTER TABLE agents DROP COLUMN archived_at;
ALTER TABLE agents DROP COLUMN session_key;
-- Nothing references project_cursors, and 0015 emptied it with the projects.
DROP TABLE project_cursors;

-- Kept as documented dead schema (AD-012): agents.name (UNIQUE (owner_id, name); new agents
-- store their id), agents.project_id (foreign key to projects, indexed by agents_project),
-- the empty projects table, and the 'project' values the room_members and grants CHECKs
-- still allow (no row uses them; the API refuses them).

-- Authors are named by their intelligence, without the fallbacks of 0015: every message is
-- inserted with its author_iid. The per-room display name of an agent member still wins in
-- its room (GRP-04). A sender-less chat message takes the chat's title; a system one is
-- "Partyline".
CREATE VIEW message_views AS
SELECT m.id, m.room_id, m.seq, m.author_kind, m.author_id, m.author_user_id, m.author_iid,
       m.sender_id, m.sender_alt_id, m.sender_name, m.kind, m.text, m.reply_to,
       m.recipients_json, m.external_id, m.created_at,
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
