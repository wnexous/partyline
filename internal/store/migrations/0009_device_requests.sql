-- Device approval (harness-onboarding DEV-01..04): `partyline setup --browser` creates a
-- request; a logged-in user approves it in the web app and the polling CLI receives a new
-- agent token once. Only hashes of the code and of the poll secret are stored; the token is
-- sealed with the instance key until it is delivered, then cleared.
CREATE TABLE device_requests (
    id               TEXT PRIMARY KEY,
    code_hash        TEXT NOT NULL UNIQUE,
    poll_secret_hash TEXT NOT NULL UNIQUE,
    machine          TEXT NOT NULL,
    created_ip       TEXT NOT NULL DEFAULT '',
    -- ip_key groups addresses for the creation limit (IPv4 address or IPv6 /64).
    ip_key           TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'denied', 'expired')),
    user_id          TEXT REFERENCES users (id) ON DELETE CASCADE,
    token_id         TEXT REFERENCES api_tokens (id) ON DELETE SET NULL,
    token_once_enc   BLOB,
    created_at       INTEGER NOT NULL,
    expires_at       INTEGER NOT NULL,
    last_poll_at     INTEGER
);
CREATE INDEX device_requests_ip ON device_requests (ip_key, created_at);
CREATE INDEX device_requests_created ON device_requests (created_at);
