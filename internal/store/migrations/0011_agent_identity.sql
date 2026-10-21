-- Agent identity (agent-identity feature).
-- A project groups an owner's agents (PROJ-01): one per (owner, slug); the slug is stable and
-- the name is what the owner reads and may rename (PROJ-05).
CREATE TABLE projects (
    id         TEXT PRIMARY KEY,
    owner_id   TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    slug       TEXT NOT NULL,
    name       TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE (owner_id, slug)
);

-- Agent identity columns (ID-01, ID-02, PROJ-01, PROJ-02):
-- harness: the launcher's harness id (claude, opencode, pi, omp, codex, gemini) or 'agent';
-- machine: the session's hostname; kind: fixed (named with --name, never archived) or
-- temporary (named after project and harness); display_name: the owner's rename ('' = none);
-- auto_name: the server's readable name of a temporary agent, "<Project> · <Harness>[ #N]";
-- session_no: the N of a temporary agent; archived_at: set by the daily job after 24 h
-- offline; connected_at: the last connect, which picks a project's active session.
ALTER TABLE agents ADD COLUMN project_id TEXT REFERENCES projects (id) ON DELETE SET NULL;
ALTER TABLE agents ADD COLUMN harness TEXT NOT NULL DEFAULT 'agent';
ALTER TABLE agents ADD COLUMN machine TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN kind TEXT NOT NULL DEFAULT 'fixed' CHECK (kind IN ('fixed', 'temporary'));
ALTER TABLE agents ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN auto_name TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN session_no INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agents ADD COLUMN archived_at INTEGER;
ALTER TABLE agents ADD COLUMN connected_at INTEGER;
CREATE INDEX agents_project ON agents (project_id);

-- Existing agents stay fixed and join the project their name derives from: the name without
-- a trailing "-N" suffix of the old unique mode (proj-2 → proj). The default name "agent"
-- derives nothing (no project). Project names start with an upper-case letter.
INSERT INTO projects (id, owner_id, slug, name, created_at)
SELECT lower(hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' || substr(hex(randomblob(2)), 2) || '-' ||
             hex(randomblob(2)) || '-' || hex(randomblob(6))),
       owner_id, slug, upper(substr(slug, 1, 1)) || substr(slug, 2), MIN(created_at)
FROM (SELECT owner_id, created_at,
             CASE
                 WHEN name GLOB '?*-[2-9]' THEN substr(name, 1, length(name) - 2)
                 WHEN name GLOB '?*-[1-9][0-9]' THEN substr(name, 1, length(name) - 3)
                 ELSE name
                 END AS slug
      FROM agents
      WHERE name <> 'agent')
GROUP BY owner_id, slug;

UPDATE agents
SET project_id = (SELECT p.id FROM projects p
                  WHERE p.owner_id = agents.owner_id
                    AND p.slug = CASE
                                     WHEN agents.name GLOB '?*-[2-9]' THEN substr(agents.name, 1, length(agents.name) - 2)
                                     WHEN agents.name GLOB '?*-[1-9][0-9]' THEN substr(agents.name, 1, length(agents.name) - 3)
                                     ELSE agents.name
                                     END)
WHERE name <> 'agent';

-- Messages a project member has not received yet (PROJ-08): one cursor per (project, room).
CREATE TABLE project_cursors (
    project_id TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    room_id    TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    seq        INTEGER NOT NULL,
    PRIMARY KEY (project_id, room_id)
);

-- room_members.kind and grants.resource_kind gain 'project' (PROJ-06). SQLite cannot change
-- a CHECK, so both tables are rebuilt with their data, keys and indexes. message_views reads
-- room_members, so it is dropped first and recreated at the end.
DROP VIEW message_views;

CREATE TABLE room_members_new (
    id            TEXT PRIMARY KEY,
    room_id       TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN ('agent', 'chat', 'user', 'project')),
    member_id     TEXT NOT NULL,
    outbound      BOOLEAN NOT NULL DEFAULT 1,
    added_by      TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    display_name  TEXT NOT NULL DEFAULT '',
    sign_outbound BOOLEAN NOT NULL DEFAULT 1,
    delivery      TEXT NOT NULL DEFAULT 'all' CHECK (delivery IN ('all', 'mentions', 'trusted')),
    keywords_json TEXT NOT NULL DEFAULT '[]',
    UNIQUE (room_id, kind, member_id)
);
INSERT INTO room_members_new (id, room_id, kind, member_id, outbound, added_by, created_at,
                              display_name, sign_outbound, delivery, keywords_json)
SELECT id, room_id, kind, member_id, outbound, added_by, created_at,
       display_name, sign_outbound, delivery, keywords_json
FROM room_members;
DROP TABLE room_members;
ALTER TABLE room_members_new RENAME TO room_members;
CREATE INDEX room_members_member ON room_members (kind, member_id);

CREATE TABLE grants_new (
    resource_kind TEXT NOT NULL CHECK (resource_kind IN ('room', 'agent', 'connector', 'project')),
    resource_id   TEXT NOT NULL,
    subject       TEXT NOT NULL,
    level         TEXT NOT NULL CHECK (level IN ('view', 'post', 'manage', 'use')),
    created_by    TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    PRIMARY KEY (resource_kind, resource_id, subject)
);
INSERT INTO grants_new (resource_kind, resource_id, subject, level, created_by, created_at)
SELECT resource_kind, resource_id, subject, level, created_by, created_at FROM grants;
DROP TABLE grants;
ALTER TABLE grants_new RENAME TO grants;
CREATE INDEX grants_subject ON grants (subject);

-- Agent authors are named by their display name in the room when set, else the owner's
-- rename, else the temporary agent's readable name, else the agent name (ID-04).
CREATE VIEW message_views AS
SELECT m.id, m.room_id, m.seq, m.author_kind, m.author_id, m.author_user_id, m.sender_id,
       m.sender_alt_id, m.sender_name, m.kind, m.text, m.reply_to, m.recipients_json,
       m.external_id, m.created_at,
       CAST(CASE m.author_kind
            WHEN 'user' THEN COALESCE(NULLIF(u.display_name, ''), u.username, '')
            WHEN 'agent' THEN COALESCE(NULLIF(rm.display_name, ''), NULLIF(a.display_name, ''),
                                       NULLIF(a.auto_name, ''), a.name, '')
            WHEN 'chat' THEN COALESCE(NULLIF(m.sender_name, ''), c.title, '')
            ELSE 'Partyline'
            END AS TEXT) AS author_name
FROM messages m
LEFT JOIN users u ON m.author_kind = 'user' AND u.id = m.author_id
LEFT JOIN agents a ON m.author_kind = 'agent' AND a.id = m.author_id
LEFT JOIN room_members rm ON m.author_kind = 'agent' AND rm.room_id = m.room_id
                         AND rm.kind = 'agent' AND rm.member_id = m.author_id
LEFT JOIN chats c ON m.author_kind = 'chat' AND c.id = m.author_id;
