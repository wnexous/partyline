-- Organizations (organizations feature, AD-016, AD-017, AD-021, AD-012). Organizations own AIs,
-- connections and groups, have members (users and AIs) with a permission and an org chart of
-- positions, and replace the per-resource grants. Additive: no table that other tables reference
-- is rebuilt (AD-012), so every new reference to an existing table is an ADD COLUMN with a NULL
-- default. The Go hook of this migration (store.hooks["0019_organizations"]) moves what exists into
-- personal organizations in the same transaction; 0020 drops the grants table.

CREATE TABLE organizations (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,                    -- 1-100 characters (ORG-01)
    slug          TEXT NOT NULL COLLATE NOCASE,     -- a-z0-9-, 1-40; what --org takes
    avatar_sha256 TEXT NOT NULL DEFAULT '',         -- blob of the 256x256 PNG, '' = initials
    created_by    TEXT NOT NULL DEFAULT '',         -- intelligence id; history only
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE UNIQUE INDEX organizations_slug ON organizations (slug);

-- The org chart: a tree of positions per organization (POS-01).
CREATE TABLE positions (
    id         TEXT PRIMARY KEY,
    org_id     TEXT NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name       TEXT NOT NULL COLLATE NOCASE,        -- 1-60 characters (POS-01)
    parent_id  TEXT REFERENCES positions (id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX positions_name ON positions (org_id, name);   -- --position <name> is unambiguous
CREATE INDEX positions_parent ON positions (parent_id);

-- Membership of an identity (a user or an AI) in an organization: a permission and at most one
-- position (OMEM-03). The primary key is the one membership per (organization, identity).
CREATE TABLE org_members (
    org_id      TEXT NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    iid         TEXT NOT NULL REFERENCES intelligences (id) ON DELETE CASCADE,
    permission  TEXT NOT NULL CHECK (permission IN ('admin', 'member', 'guest')),
    position_id TEXT REFERENCES positions (id) ON DELETE SET NULL,
    inviter_iid TEXT REFERENCES intelligences (id) ON DELETE SET NULL, -- a guest's inviter (OMEM-01); NULL otherwise
    joined_at   INTEGER NOT NULL,
    PRIMARY KEY (org_id, iid)
);
CREATE INDEX org_members_iid ON org_members (iid);
CREATE INDEX org_members_inviter ON org_members (org_id, inviter_iid) WHERE inviter_iid IS NOT NULL;
CREATE INDEX org_members_position ON org_members (position_id);

-- Pending invitations (OMEM-01, OMEM-07): one row per (organization, invitee), updated when the
-- invitee is invited again; answered and expired rows are deleted.
CREATE TABLE org_invitations (
    id          TEXT PRIMARY KEY,
    org_id      TEXT NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    invitee_iid TEXT NOT NULL REFERENCES intelligences (id) ON DELETE CASCADE,
    permission  TEXT NOT NULL CHECK (permission IN ('admin', 'member', 'guest')),
    position_id TEXT REFERENCES positions (id) ON DELETE SET NULL,
    invited_by  TEXT NOT NULL,                      -- intelligence id of the inviter; the guest's inviter on accept
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,                   -- created_at + 14 days (OMEM-07)
    UNIQUE (org_id, invitee_iid)
);
CREATE INDEX org_invitations_invitee ON org_invitations (invitee_iid);

-- Ownership (ORG-02). org_id is NULL only for DMs (rooms) and for rows written before the Go step
-- runs; creator_iid is the identity that created the AI or the group (POS-03 follows it).
-- agents.org_id, rooms.org_id and connectors.org_id have no ON DELETE action: deleting an
-- organization that still owns anything fails in the database as well as in the service (ORG-06).
-- agents.owner_id stays the host user (AD-019); rooms.owner_id and connectors.owner_id become dead
-- foreign-key anchors that cannot be dropped (AD-012).
ALTER TABLE agents     ADD COLUMN org_id TEXT REFERENCES organizations (id);
ALTER TABLE agents     ADD COLUMN creator_iid TEXT REFERENCES intelligences (id) ON DELETE SET NULL;
ALTER TABLE rooms      ADD COLUMN org_id TEXT REFERENCES organizations (id);
ALTER TABLE rooms      ADD COLUMN creator_iid TEXT REFERENCES intelligences (id) ON DELETE SET NULL;
ALTER TABLE connectors ADD COLUMN org_id TEXT REFERENCES organizations (id);
-- The organization a new AI of the user joins when --org is not given (ORG-04, AD-021).
ALTER TABLE users      ADD COLUMN default_org_id TEXT REFERENCES organizations (id) ON DELETE SET NULL;
CREATE INDEX agents_org ON agents (org_id);
CREATE INDEX rooms_org ON rooms (org_id) WHERE org_id IS NOT NULL;
CREATE INDEX connectors_org ON connectors (org_id);
