-- Chat history of connections (chat-history feature, CH-01..CH-08). Additive (AD-012): every
-- existing connection keeps history off, and nothing is kept until its owner turns it on.
--
-- A platform item loses its text when the AI acknowledges it and a send when it is over (CID-09,
-- CID-16): delivery stays as it is (CH-04). The history is a store of its own, per chat of a
-- connection, kept only while the connection's retention is on and, unless it is forever, only for that
-- many days (CH-03).

-- How long the chat history of the connection is kept (CH-01): 0 is off (the default), 1 to 365 is that
-- many days, -1 is forever (no age limit: nothing is deleted for its age). Turning it off deletes the
-- history of the connection at once.
ALTER TABLE connectors ADD COLUMN history_days INTEGER NOT NULL DEFAULT 0 CHECK (history_days = -1 OR history_days BETWEEN 0 AND 365);

-- One message of a chat of a connection (CH-02): what arrived on it, what the AI sent through it and
-- what the account typed on the platform (from_self). id is the id the AI knows the message by: the
-- inbound item's id, the send's id, or an id of its own for a message that was never delivered (a
-- message the connection's filters kept from the AI, one fetched from the platform's past). at is when
-- the platform says the message was written (or sent, for a send): the order of the history and its
-- age for the retention. seq breaks ties and is never reused.
CREATE TABLE chat_history (
    seq              INTEGER PRIMARY KEY AUTOINCREMENT,
    id               TEXT NOT NULL UNIQUE,
    connector_id     TEXT NOT NULL REFERENCES connectors (id) ON DELETE CASCADE,
    chat_id          TEXT NOT NULL REFERENCES chats (id) ON DELETE CASCADE,
    external_id      TEXT NOT NULL DEFAULT '',                                   -- platform message id
    sender_id        TEXT NOT NULL DEFAULT '',
    sender_name      TEXT NOT NULL DEFAULT '',
    from_self        BOOLEAN NOT NULL DEFAULT 0 CHECK (from_self IN (0, 1)),
    trusted          BOOLEAN NOT NULL DEFAULT 0 CHECK (trusted IN (0, 1)),       -- as when it was received (CH-05)
    kind             TEXT NOT NULL DEFAULT 'text',
    text             TEXT NOT NULL DEFAULT '',                                   -- dropped by a delete
    reply_to         TEXT NOT NULL DEFAULT '',                                   -- the quoted message (QC-01)
    quoted           TEXT NOT NULL DEFAULT '',                                   -- its preview (QC-02)
    quoted_from_self BOOLEAN NOT NULL DEFAULT 0 CHECK (quoted_from_self IN (0, 1)),
    edited           BOOLEAN NOT NULL DEFAULT 0 CHECK (edited IN (0, 1)),
    deleted          BOOLEAN NOT NULL DEFAULT 0 CHECK (deleted IN (0, 1)),
    at               INTEGER NOT NULL
);
-- A platform message of one chat is kept once, however it came (live, a repeat, the platform's past:
-- CH-08); a message without a platform id never conflicts.
CREATE UNIQUE INDEX chat_history_dedupe ON chat_history (connector_id, chat_id, external_id) WHERE external_id <> '';
CREATE INDEX chat_history_chat ON chat_history (chat_id, at, seq);
CREATE INDEX chat_history_connector ON chat_history (connector_id, at);

-- The files of a kept message: name, type and size, and the storage state when it was received. id is
-- the id of the queued item's file when the message was delivered with one, so the file stays
-- downloadable while the queue keeps it (24 hours after the ack); the history keeps no file of its own.
CREATE TABLE chat_history_attachments (
    id         TEXT PRIMARY KEY,
    message_id TEXT NOT NULL REFERENCES chat_history (id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    mime       TEXT NOT NULL,
    size       INTEGER NOT NULL DEFAULT 0,
    status     TEXT NOT NULL
);
CREATE INDEX chat_history_attachments_message ON chat_history_attachments (message_id);

-- The reactions to a kept message, one per sender (CH-02): a new reaction of the sender replaces the
-- old one and an empty one removes it. sender_id is '' for the account itself.
CREATE TABLE chat_history_reactions (
    message_id  TEXT NOT NULL REFERENCES chat_history (id) ON DELETE CASCADE,
    sender_id   TEXT NOT NULL,
    sender_name TEXT NOT NULL DEFAULT '',
    from_self   BOOLEAN NOT NULL DEFAULT 0 CHECK (from_self IN (0, 1)),
    emoji       TEXT NOT NULL,
    at          INTEGER NOT NULL,
    PRIMARY KEY (message_id, sender_id)
);
