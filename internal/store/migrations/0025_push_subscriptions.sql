-- Web push notifications (web-push: PUSH-03, PUSH-06, PUSH-07, PUSH-09).
-- One row per browser push subscription. endpoint is the browser's capability URL and is
-- unique: a second POST for it updates the row (the browser moved to another account, the
-- keys rotated, or the user enabled it again after logging in). session_id ties the row to
-- the session that registered it, so logout / a revoked device / a password change removes
-- it (ON DELETE CASCADE); an account delete removes every row of the user. last_ok_at is
-- written only after a 2xx from the push service.
CREATE TABLE push_subscriptions (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    session_id TEXT REFERENCES sessions (id) ON DELETE CASCADE,
    endpoint   TEXT NOT NULL,
    p256dh     TEXT NOT NULL,
    auth       TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    last_ok_at INTEGER
);
CREATE UNIQUE INDEX push_subscriptions_endpoint ON push_subscriptions (endpoint);
CREATE INDEX push_subscriptions_user ON push_subscriptions (user_id);
CREATE INDEX push_subscriptions_session ON push_subscriptions (session_id) WHERE session_id IS NOT NULL;

-- How group messages reach an account (PUSH-06). DMs ignore it. Existing accounts start
-- with mentions, as the brief requires.
ALTER TABLE users ADD COLUMN push_groups TEXT NOT NULL DEFAULT 'mentions'
    CHECK (push_groups IN ('all', 'mentions', 'off'));
