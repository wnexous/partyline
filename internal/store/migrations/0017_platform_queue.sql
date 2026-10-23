-- Platform queue (connection-identity feature, AD-015, AD-012). Additive: nothing is rebuilt.
-- What arrives on a connection is queued for the AI that operates it instead of being stored as a
-- conversation message; the AI's answers go through platform_sends. 0018 (the upgrade) retires
-- the room features that carried platforms.

-- The queue, per AI. seq is the delivery order (CID-08) and, with AUTOINCREMENT, never reused,
-- so a cursor on seq cannot skip an item queued after a deleted one. An item moves between AIs
-- with its connection (CID-05), so it is acknowledged item by item (acked_at), not by a cursor.
-- text is cleared when the session acknowledges the item (CID-09); the row stays 24 hours so
-- that reply keeps working, and an item nobody acknowledged is deleted after 7 days (CID-10).
CREATE TABLE platform_items (
    seq          INTEGER PRIMARY KEY AUTOINCREMENT,
    id           TEXT NOT NULL UNIQUE,
    agent_id     TEXT NOT NULL REFERENCES agents (id) ON DELETE CASCADE,        -- CID-24
    connector_id TEXT NOT NULL REFERENCES connectors (id) ON DELETE CASCADE,
    chat_id      TEXT NOT NULL REFERENCES chats (id) ON DELETE CASCADE,
    external_id  TEXT NOT NULL DEFAULT '',                                      -- platform message id
    sender_id    TEXT NOT NULL DEFAULT '',
    sender_name  TEXT NOT NULL DEFAULT '',
    trusted      BOOLEAN NOT NULL DEFAULT 0,
    from_self    BOOLEAN NOT NULL DEFAULT 0,
    kind         TEXT NOT NULL DEFAULT 'text',
    text         TEXT NOT NULL DEFAULT '',                                      -- cleared on ack (CID-09)
    received_at  INTEGER NOT NULL,
    acked_at     INTEGER
);
-- A platform message of one chat of one connection queues once, whoever operates it (CID-26).
CREATE UNIQUE INDEX platform_items_dedupe ON platform_items (connector_id, chat_id, external_id)
    WHERE external_id <> '';
CREATE INDEX platform_items_agent ON platform_items (agent_id, acked_at, seq);
CREATE INDEX platform_items_connector ON platform_items (connector_id, acked_at);
CREATE INDEX platform_items_chat ON platform_items (chat_id);

-- The files of an item: blobs of the attachment store, deleted 24 hours after the ack (CID-09).
CREATE TABLE platform_attachments (
    id      TEXT PRIMARY KEY,
    item_id TEXT NOT NULL REFERENCES platform_items (id) ON DELETE CASCADE,
    sha256  TEXT NOT NULL DEFAULT '',                                           -- '' = no blob
    name    TEXT NOT NULL,
    mime    TEXT NOT NULL,
    size    INTEGER NOT NULL,
    status  TEXT NOT NULL
);
CREATE INDEX platform_attachments_item ON platform_attachments (item_id);
CREATE INDEX platform_attachments_sha ON platform_attachments (sha256) WHERE sha256 <> '';

-- What an AI sends through its connection (CID-15, CID-16, CID-18): the outbox states and
-- backoff. text is cleared when the send reaches sent or failed, so no platform text outlives
-- its delivery; external_id (the platform's id of the sent message) tells the echo of a send
-- from something typed on the account (CID-14).
CREATE TABLE platform_sends (
    id                   TEXT PRIMARY KEY,
    connector_id         TEXT NOT NULL REFERENCES connectors (id) ON DELETE CASCADE,
    chat_id              TEXT NOT NULL REFERENCES chats (id) ON DELETE CASCADE,
    agent_id             TEXT NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    text                 TEXT NOT NULL DEFAULT '',
    reply_to_external_id TEXT NOT NULL DEFAULT '',
    status               TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sending', 'sent', 'failed')),
    attempts             INTEGER NOT NULL DEFAULT 0,
    next_attempt_at      INTEGER NOT NULL,
    last_error           TEXT NOT NULL DEFAULT '',
    external_id          TEXT NOT NULL DEFAULT '',
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL
);
CREATE INDEX platform_sends_due ON platform_sends (status, next_attempt_at);
CREATE INDEX platform_sends_chat ON platform_sends (chat_id, external_id) WHERE external_id <> '';
CREATE INDEX platform_sends_agent ON platform_sends (agent_id);
CREATE INDEX platform_sends_connector ON platform_sends (connector_id);

-- Items an AI lost to the 5,000 cap or the 7-day expiry since its last connection (CID-10,
-- CID-11); the next platform-capable session is told, and the counter resets.
ALTER TABLE agents ADD COLUMN platform_dropped INTEGER NOT NULL DEFAULT 0;

-- Which chats a connection delivers and whether a group message needs a mention (CID-12). Every
-- connection, new or upgraded, starts with the same defaults whatever its old catch-all scope
-- (CID-27): DMs plus the group messages that mention or reply to the account.
ALTER TABLE connectors ADD COLUMN deliver TEXT NOT NULL DEFAULT 'all' CHECK (deliver IN ('dms', 'groups', 'all'));
ALTER TABLE connectors ADD COLUMN groups_mentions_only BOOLEAN NOT NULL DEFAULT 1;
