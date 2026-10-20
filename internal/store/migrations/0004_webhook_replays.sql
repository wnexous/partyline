-- Accepted webhook signatures, kept until their timestamp can no longer pass the ±5 minute
-- check (HOOK-05), so a captured request cannot be replayed after a connector restart, a
-- config change or a server restart. Only a hash of the computed signature is stored.
CREATE TABLE webhook_replays (
    connector_id TEXT NOT NULL REFERENCES connectors (id) ON DELETE CASCADE,
    sig_hash     TEXT NOT NULL,
    expires_at   INTEGER NOT NULL,
    UNIQUE (connector_id, sig_hash)
);
CREATE INDEX webhook_replays_expires ON webhook_replays (expires_at);
