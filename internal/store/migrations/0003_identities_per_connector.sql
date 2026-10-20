-- Verified identities belong to the connector they were linked through (IDN-02, AD-006).
-- A sender id is only as trustworthy as the connector that reports it: a webhook caller or
-- the owner of a connector with a custom API URL chooses sender ids freely, so an identity
-- linked through one connector must not match messages of another connector, even of the
-- same kind. Identities linked before this migration have no connector and are dropped;
-- their users link again with a new pairing code.
DROP TABLE identities;

CREATE TABLE identities (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    connector_id    TEXT NOT NULL REFERENCES connectors (id) ON DELETE CASCADE,
    connector_kind  TEXT NOT NULL,
    external_id     TEXT NOT NULL,
    alt_external_id TEXT NOT NULL DEFAULT '',
    display         TEXT NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    UNIQUE (connector_id, external_id)
);
CREATE INDEX identities_user ON identities (user_id);
