-- Simple identity, release 2 (SR2-01, SR2-04, SR2-06, SR2-08, SR2-15). Guests, positions and the org
-- chart go; one explicit link replaces the chains of management: an AI is instructed by its host and by
-- the identities listed in agent_instructors. It runs, like every migration, after the automatic
-- pre-migration backup (OPS-07) and inside the one transaction of Migrate: a failure leaves nothing.

-- SR2-06: who instructs an AI besides its host. created_by is the intelligence id that added the link
-- (history only, no foreign key).
CREATE TABLE agent_instructors (
    agent_iid      TEXT NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    instructor_iid TEXT NOT NULL REFERENCES intelligences (id) ON DELETE CASCADE,
    created_by     TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    PRIMARY KEY (agent_iid, instructor_iid)
);
CREATE INDEX agent_instructors_instructor ON agent_instructors (instructor_iid);

-- SR2-08: nobody loses a manager it has today through creation. Before, an identity managed an AI when it
-- created the AI or created (transitively) the AI's creator, as long as each creator on the way was a member
-- of the AI's home organization. Every such creator other than the AI's host (who is trusted anyway) and the
-- AI itself becomes an instructor; the host is the one who added the link.
WITH RECURSIVE chain (agent_id, org_id, creator, depth) AS (
    SELECT a.id, a.org_id, a.creator_iid, 1
    FROM agents a
    JOIN org_members m ON m.org_id = a.org_id AND m.iid = a.creator_iid
    WHERE a.creator_iid IS NOT NULL AND a.org_id IS NOT NULL
    UNION
    SELECT c.agent_id, c.org_id, p.creator_iid, c.depth + 1
    FROM chain c
    JOIN agents p ON p.id = c.creator
    JOIN org_members m ON m.org_id = c.org_id AND m.iid = p.creator_iid
    WHERE p.creator_iid IS NOT NULL AND c.depth < 64
)
INSERT OR IGNORE INTO agent_instructors (agent_iid, instructor_iid, created_by, created_at)
SELECT DISTINCT c.agent_id, c.creator, a.owner_id, a.created_at
FROM chain c JOIN agents a ON a.id = c.agent_id
WHERE c.creator <> a.owner_id AND c.creator <> a.id;

-- SR2-01: every guest becomes a member, and so does every pending guest invitation. SR2-03: the inviter
-- link granted management of guests only; with no guests it grants nothing and goes with the rebuild.
-- SR2-04: positions go: org_members.position_id, org_invitations.position_id and the positions table.
-- SQLite drops neither a column with a foreign key nor a value from a CHECK, so both tables are rebuilt.
-- Nothing references them, so the rebuild touches no other table.
CREATE TABLE org_members_new (
    org_id     TEXT NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    iid        TEXT NOT NULL REFERENCES intelligences (id) ON DELETE CASCADE,
    permission TEXT NOT NULL CHECK (permission IN ('admin', 'member')),
    joined_at  INTEGER NOT NULL,
    PRIMARY KEY (org_id, iid)
);
INSERT INTO org_members_new (org_id, iid, permission, joined_at)
SELECT org_id, iid, CASE permission WHEN 'admin' THEN 'admin' ELSE 'member' END, joined_at FROM org_members;
DROP TABLE org_members;
ALTER TABLE org_members_new RENAME TO org_members;
CREATE INDEX org_members_iid ON org_members (iid);

CREATE TABLE org_invitations_new (
    id          TEXT PRIMARY KEY,
    org_id      TEXT NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    invitee_iid TEXT NOT NULL REFERENCES intelligences (id) ON DELETE CASCADE,
    permission  TEXT NOT NULL CHECK (permission IN ('admin', 'member')),
    invited_by  TEXT NOT NULL,                      -- intelligence id of the inviter
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,                   -- created_at + 14 days (OMEM-07)
    UNIQUE (org_id, invitee_iid)
);
INSERT INTO org_invitations_new (id, org_id, invitee_iid, permission, invited_by, created_at, expires_at)
SELECT id, org_id, invitee_iid, CASE permission WHEN 'admin' THEN 'admin' ELSE 'member' END, invited_by,
       created_at, expires_at
FROM org_invitations;
DROP TABLE org_invitations;
ALTER TABLE org_invitations_new RENAME TO org_invitations;
CREATE INDEX org_invitations_invitee ON org_invitations (invitee_iid);

DROP TABLE positions;

-- agents.creator_iid stays: it is history, and the welcome still says who created an AI and which AIs it
-- created (SI-10). It grants nothing any more.
