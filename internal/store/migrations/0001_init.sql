-- Initial schema. IDs are UUIDv7 strings; times are unix milliseconds.

CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
    display_name  TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    locale        TEXT NOT NULL DEFAULT '',
    disabled_at   INTEGER,
    created_at    INTEGER NOT NULL
);

CREATE TABLE sessions (
    id           TEXT PRIMARY KEY,
    token_hash   TEXT NOT NULL UNIQUE,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    user_agent   TEXT NOT NULL DEFAULT '',
    ip           TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user ON sessions (user_id);

CREATE TABLE invites (
    id         TEXT PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE,
    role       TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    created_by TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_by    TEXT REFERENCES users (id) ON DELETE SET NULL,
    used_at    INTEGER,
    revoked_at INTEGER
);

CREATE TABLE api_tokens (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind         TEXT NOT NULL CHECK (kind IN ('agent', 'user')),
    name         TEXT NOT NULL,
    prefix       TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER,
    expires_at   INTEGER,
    revoked_at   INTEGER
);
CREATE INDEX api_tokens_user ON api_tokens (user_id);

CREATE TABLE agents (
    id           TEXT PRIMARY KEY,
    owner_id     TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER,
    UNIQUE (owner_id, name)
);

CREATE TABLE connectors (
    id          TEXT PRIMARY KEY,
    owner_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,
    name        TEXT NOT NULL,
    config_json TEXT NOT NULL DEFAULT '{}',
    secrets_enc BLOB,
    enabled     BOOLEAN NOT NULL DEFAULT 1,
    status      TEXT NOT NULL DEFAULT 'stopped',
    last_error  TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
CREATE INDEX connectors_owner ON connectors (owner_id);

CREATE TABLE chats (
    id           TEXT PRIMARY KEY,
    connector_id TEXT NOT NULL REFERENCES connectors (id) ON DELETE CASCADE,
    external_id  TEXT NOT NULL,
    title        TEXT NOT NULL DEFAULT '',
    kind         TEXT NOT NULL DEFAULT '',
    last_seen_at INTEGER,
    created_at   INTEGER NOT NULL,
    UNIQUE (connector_id, external_id)
);

CREATE TABLE rooms (
    id             TEXT PRIMARY KEY,
    owner_id       TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    bridge_chats   BOOLEAN NOT NULL DEFAULT 0,
    retention_days INTEGER NOT NULL DEFAULT 90,
    next_seq       INTEGER NOT NULL DEFAULT 1,
    agent_streak   INTEGER NOT NULL DEFAULT 0,
    agents_paused  BOOLEAN NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
CREATE INDEX rooms_owner ON rooms (owner_id);

CREATE TABLE room_members (
    id         TEXT PRIMARY KEY,
    room_id    TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('agent', 'chat', 'user')),
    member_id  TEXT NOT NULL,
    outbound   BOOLEAN NOT NULL DEFAULT 1,
    added_by   TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    UNIQUE (room_id, kind, member_id)
);
CREATE INDEX room_members_member ON room_members (kind, member_id);

CREATE TABLE grants (
    resource_kind TEXT NOT NULL CHECK (resource_kind IN ('room', 'agent', 'connector')),
    resource_id   TEXT NOT NULL,
    subject       TEXT NOT NULL,
    level         TEXT NOT NULL CHECK (level IN ('view', 'post', 'manage', 'use')),
    created_by    TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    PRIMARY KEY (resource_kind, resource_id, subject)
);
CREATE INDEX grants_subject ON grants (subject);

CREATE TABLE messages (
    id              TEXT PRIMARY KEY,
    room_id         TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    seq             INTEGER NOT NULL,
    author_kind     TEXT NOT NULL CHECK (author_kind IN ('agent', 'chat', 'user', 'system')),
    author_id       TEXT NOT NULL DEFAULT '',
    author_user_id  TEXT NOT NULL DEFAULT '',
    sender_id       TEXT NOT NULL DEFAULT '',
    sender_alt_id   TEXT NOT NULL DEFAULT '',
    sender_name     TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL DEFAULT 'text',
    text            TEXT NOT NULL DEFAULT '',
    reply_to        TEXT NOT NULL DEFAULT '',
    recipients_json TEXT NOT NULL DEFAULT '[]',
    external_id     TEXT,
    created_at      INTEGER NOT NULL,
    UNIQUE (room_id, seq)
);
CREATE UNIQUE INDEX messages_dedupe ON messages (room_id, author_kind, author_id, external_id)
    WHERE external_id IS NOT NULL;
CREATE INDEX messages_room_created ON messages (room_id, created_at);

CREATE TABLE attachments (
    id         TEXT PRIMARY KEY,
    message_id TEXT NOT NULL REFERENCES messages (id) ON DELETE CASCADE,
    sha256     TEXT NOT NULL,
    mime       TEXT NOT NULL,
    name       TEXT NOT NULL,
    size       INTEGER NOT NULL,
    status     TEXT NOT NULL DEFAULT 'stored',
    created_at INTEGER NOT NULL
);
CREATE INDEX attachments_message ON attachments (message_id);
CREATE INDEX attachments_sha256 ON attachments (sha256);

CREATE TABLE agent_cursors (
    agent_id TEXT NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    room_id  TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    seq      INTEGER NOT NULL,
    PRIMARY KEY (agent_id, room_id)
);

CREATE TABLE outbox (
    id              TEXT PRIMARY KEY,
    message_id      TEXT NOT NULL REFERENCES messages (id) ON DELETE CASCADE,
    chat_id         TEXT NOT NULL REFERENCES chats (id) ON DELETE CASCADE,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sending', 'sent', 'failed')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER NOT NULL,
    last_error      TEXT NOT NULL DEFAULT '',
    external_id     TEXT NOT NULL DEFAULT '',
    updated_at      INTEGER NOT NULL
);
CREATE INDEX outbox_due ON outbox (status, next_attempt_at);

CREATE TABLE identities (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    connector_kind  TEXT NOT NULL,
    external_id     TEXT NOT NULL,
    alt_external_id TEXT NOT NULL DEFAULT '',
    display         TEXT NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    UNIQUE (connector_kind, external_id)
);

CREATE TABLE pairing_codes (
    code_hash  TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL
);

CREATE TABLE audit_log (
    id          TEXT PRIMARY KEY,
    actor_id    TEXT NOT NULL DEFAULT '',
    action      TEXT NOT NULL,
    target_kind TEXT NOT NULL DEFAULT '',
    target_id   TEXT NOT NULL DEFAULT '',
    detail_json TEXT NOT NULL DEFAULT '{}',
    ip          TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);
CREATE INDEX audit_log_created ON audit_log (created_at);
