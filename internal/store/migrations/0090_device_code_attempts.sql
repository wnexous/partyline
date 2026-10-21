-- Wrong approval codes per user (harness-onboarding DEV-05): each code entry reserves a row
-- before the lookup; a code that exists gives its row back. More than 5 rows in the last
-- minute answer 429. Kept in the database so a restart does not reset the limit.
-- Numbered 0090 to stay clear of migrations other branches add; renumber at merge if needed.
CREATE TABLE device_code_attempts (
    id           INTEGER PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    attempted_at INTEGER NOT NULL
);
CREATE INDEX device_code_attempts_user ON device_code_attempts (user_id, attempted_at);
