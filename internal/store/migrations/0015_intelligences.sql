-- Intelligences (intelligences feature, AD-010, AD-011, AD-012).
-- Every participant is an intelligence: a user, an AI (agents) or a person seen by a
-- connector (people) shares its id with its intelligences row. No table that other tables
-- reference is rebuilt (AD-012): columns are added with ADD COLUMN only. The Go hook of this
-- migration (store.hooks["0015_intelligences"]) fills intelligences, people and message
-- authors in the same transaction.
DROP VIEW message_views;

CREATE TABLE intelligences (
    id            TEXT PRIMARY KEY,                 -- = users.id | agents.id | people.id
    kind          TEXT NOT NULL CHECK (kind IN ('human', 'ai')),
    display_name  TEXT NOT NULL,
    handle        TEXT NOT NULL COLLATE NOCASE,     -- stored lower-case
    avatar_sha256 TEXT NOT NULL DEFAULT '',         -- blob of the 256x256 PNG, '' = initials
    bio           TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE UNIQUE INDEX intelligences_handle ON intelligences (handle);
CREATE INDEX intelligences_avatar ON intelligences (avatar_sha256) WHERE avatar_sha256 <> '';

-- AI identity: (owner, machine, folder). folder '' = unlinked (legacy, TREE-06).
ALTER TABLE agents ADD COLUMN folder TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX agents_folder ON agents (owner_id, machine, folder) WHERE folder <> '';

CREATE TABLE machines (
    owner_id     TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    hostname     TEXT NOT NULL,
    label        TEXT NOT NULL DEFAULT '',          -- TREE-04, '' = show the hostname
    home         TEXT NOT NULL DEFAULT '',          -- canonical home folder sent by the CLI (TREE-02)
    last_seen_at INTEGER,
    PRIMARY KEY (owner_id, hostname)
);

-- External accounts seen by a connector (AD-008: per connector). id = intelligences.id.
CREATE TABLE people (
    id              TEXT PRIMARY KEY,
    connector_id    TEXT NOT NULL REFERENCES connectors (id) ON DELETE CASCADE,
    external_id     TEXT NOT NULL,
    alt_external_id TEXT NOT NULL DEFAULT '',
    platform_name   TEXT NOT NULL DEFAULT '',        -- last name reported by the platform
    name_locked     BOOLEAN NOT NULL DEFAULT 0,      -- a user renamed the contact (PEOP-05, PROF-11)
    last_seen_at    INTEGER,
    created_at      INTEGER NOT NULL,
    UNIQUE (connector_id, external_id)
);
CREATE INDEX people_alt ON people (connector_id, alt_external_id) WHERE alt_external_id <> '';

-- People seen in each chat (GRP-02).
CREATE TABLE chat_people (
    chat_id      TEXT NOT NULL REFERENCES chats (id) ON DELETE CASCADE,
    person_id    TEXT NOT NULL REFERENCES people (id) ON DELETE CASCADE,
    last_seen_at INTEGER NOT NULL,
    PRIMARY KEY (chat_id, person_id)
);
CREATE INDEX chat_people_person ON chat_people (person_id);
-- The counterpart of a DM chat.
ALTER TABLE chats ADD COLUMN person_id TEXT REFERENCES people (id) ON DELETE SET NULL;

-- Conversations (AD-002 holds: a DM is a room of kind dm keyed by its sorted participants).
ALTER TABLE rooms ADD COLUMN kind TEXT NOT NULL DEFAULT 'group' CHECK (kind IN ('group', 'dm'));
ALTER TABLE rooms ADD COLUMN dm_a TEXT REFERENCES intelligences (id) ON DELETE CASCADE;
ALTER TABLE rooms ADD COLUMN dm_b TEXT REFERENCES intelligences (id) ON DELETE CASCADE;
ALTER TABLE rooms ADD COLUMN read_baseline INTEGER NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX rooms_dm_pair ON rooms (dm_a, dm_b) WHERE kind = 'dm';
-- Upgrade: existing rooms are groups (MIG-01) and their history counts as read.
UPDATE rooms SET read_baseline = next_seq - 1;

-- Per user, per conversation (DM-06, DM-07).
CREATE TABLE read_cursors (
    user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    room_id TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    seq     INTEGER NOT NULL,
    PRIMARY KEY (user_id, room_id)
);

-- Author intelligence of a message ('' = system or a chat message without a sender).
ALTER TABLE messages ADD COLUMN author_iid TEXT NOT NULL DEFAULT '';
CREATE INDEX messages_author_iid ON messages (author_iid) WHERE author_iid <> '';

-- NULL operator = the connector's owner (ACC-01); account label such as "@bot" (ACC-07).
ALTER TABLE connectors ADD COLUMN operator_iid TEXT REFERENCES intelligences (id) ON DELETE SET NULL;
ALTER TABLE connectors ADD COLUMN account_label TEXT NOT NULL DEFAULT '';
-- The chat that receives copies of AI DMs written to the user (NOTI-01).
ALTER TABLE users ADD COLUMN notify_chat_id TEXT REFERENCES chats (id) ON DELETE SET NULL;
ALTER TABLE outbox ADD COLUMN purpose TEXT NOT NULL DEFAULT 'room' CHECK (purpose IN ('room', 'notify'));

-- Deleting a user, an AI or a person deletes its intelligence; its DMs follow (ON DELETE
-- CASCADE on rooms.dm_a/dm_b). The triggers also fire for rows deleted by a cascade.
CREATE TRIGGER users_intelligence_delete AFTER DELETE ON users
BEGIN DELETE FROM intelligences WHERE id = OLD.id; END;
CREATE TRIGGER agents_intelligence_delete AFTER DELETE ON agents
BEGIN DELETE FROM intelligences WHERE id = OLD.id; END;
CREATE TRIGGER people_intelligence_delete AFTER DELETE ON people
BEGIN DELETE FROM intelligences WHERE id = OLD.id; END;

-- Authors are named by their intelligence. The fallbacks after it are transitional (removed
-- in 0016): rows written before author_iid is set on insert, agents and users created before
-- their intelligence exists, and project members until project code is removed keep their
-- v0.3 names. The per-room agent display name still wins in its room (GRP-04).
CREATE VIEW message_views AS
SELECT m.id, m.room_id, m.seq, m.author_kind, m.author_id, m.author_user_id, m.author_iid,
       m.sender_id, m.sender_alt_id, m.sender_name, m.kind, m.text, m.reply_to,
       m.recipients_json, m.external_id, m.created_at,
       CAST(CASE
            WHEN m.author_kind = 'system' THEN 'Partyline'
            WHEN m.author_kind = 'agent' THEN COALESCE(NULLIF(rm.display_name, ''), NULLIF(pm.display_name, ''),
                                                       i.display_name, ia.display_name,
                                                       NULLIF(a.display_name, ''), NULLIF(a.auto_name, ''), a.name, '')
            WHEN m.author_kind = 'user' THEN COALESCE(i.display_name, iu.display_name,
                                                      NULLIF(u.display_name, ''), u.username, '')
            ELSE COALESCE(i.display_name, NULLIF(m.sender_name, ''), c.title, '')
            END AS TEXT) AS author_name,
       CAST(COALESCE(i.handle, ia.handle, iu.handle, '') AS TEXT) AS author_handle,
       CAST(COALESCE(i.kind, ia.kind, iu.kind, '') AS TEXT) AS author_ikind,
       CAST(COALESCE(i.avatar_sha256, ia.avatar_sha256, iu.avatar_sha256, '') AS TEXT) AS author_avatar
FROM messages m
LEFT JOIN intelligences i ON i.id = NULLIF(m.author_iid, '')
LEFT JOIN intelligences ia ON m.author_kind = 'agent' AND ia.id = m.author_id
LEFT JOIN intelligences iu ON m.author_kind = 'user' AND iu.id = m.author_id
LEFT JOIN agents a ON m.author_kind = 'agent' AND a.id = m.author_id
LEFT JOIN users u ON m.author_kind = 'user' AND u.id = m.author_id
LEFT JOIN room_members rm ON m.author_kind = 'agent' AND rm.room_id = m.room_id
                         AND rm.kind = 'agent' AND rm.member_id = m.author_id
LEFT JOIN room_members pm ON m.author_kind = 'agent' AND pm.room_id = m.room_id
                         AND pm.kind = 'project' AND pm.member_id = a.project_id
LEFT JOIN chats c ON m.author_kind = 'chat' AND c.id = m.author_id;
