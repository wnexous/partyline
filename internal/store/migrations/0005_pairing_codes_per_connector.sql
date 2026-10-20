-- A pairing code is bound to the connector it was requested for (IDN-01, IDN-02, AD-008):
-- "!link CODE" links only when it arrives through that connector, so a code typed in a chat
-- where someone else's connector listens links nothing there. Codes live 10 minutes; the
-- ones issued before this migration have no connector and are dropped.
DROP TABLE pairing_codes;

CREATE TABLE pairing_codes (
    code_hash    TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    connector_id TEXT NOT NULL REFERENCES connectors (id) ON DELETE CASCADE,
    expires_at   INTEGER NOT NULL
);
