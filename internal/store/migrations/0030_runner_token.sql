-- Runner tokens (CA-01): `partyline runner` asks the device approval flow for a token of its own
-- kind, so the CLI never needs an agent token (which the AI inside a container holds, CA-08) and a
-- runner token reaches only the runner routes. The approving user chooses what the request creates;
-- an old request keeps the default it had when it was created.
ALTER TABLE device_requests ADD COLUMN kind TEXT NOT NULL DEFAULT 'agent'
    CHECK (kind IN ('agent', 'runner'));

-- api_tokens.kind gains the runner kind. SQLite cannot alter a CHECK constraint, so the table is
-- rebuilt: the rows that reference a token are copied aside and emptied first, or the enforced
-- foreign keys of the old table (NO ACTION on runners, ON DELETE SET NULL on the others) would
-- fire on the rebuild. The copies are restored once the new table holds every id again.
CREATE TEMP TABLE api_tokens_backup AS SELECT * FROM api_tokens;
CREATE TEMP TABLE runners_backup AS SELECT * FROM runners;
CREATE TEMP TABLE container_agents_backup AS SELECT * FROM container_agents;
CREATE TEMP TABLE device_requests_backup AS SELECT * FROM device_requests;

DELETE FROM device_requests;
DELETE FROM container_agents;
DELETE FROM runners;
DELETE FROM api_tokens;

CREATE TABLE api_tokens_new (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind         TEXT NOT NULL CHECK (kind IN ('agent', 'user', 'runner')),
    name         TEXT NOT NULL,
    prefix       TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER,
    expires_at   INTEGER,
    revoked_at   INTEGER
);
INSERT INTO api_tokens_new (id, user_id, kind, name, prefix, token_hash, created_at, last_used_at, expires_at, revoked_at)
SELECT id, user_id, kind, name, prefix, token_hash, created_at, last_used_at, expires_at, revoked_at
FROM api_tokens_backup;
DROP TABLE api_tokens;
ALTER TABLE api_tokens_new RENAME TO api_tokens;
CREATE INDEX api_tokens_user ON api_tokens (user_id);

INSERT INTO runners (id, owner_id, name, token_id, machine, docker_version, docker_error, last_seen_at, created_at, updated_at)
SELECT id, owner_id, name, token_id, machine, docker_version, docker_error, last_seen_at, created_at, updated_at
FROM runners_backup;
INSERT INTO container_agents (agent_id, runner_id, harness, image, repo_url, repo_ref, volume, container_name,
                              env, cpus, memory_mb, desired, state, last_error, token_id, created_at, updated_at)
SELECT agent_id, runner_id, harness, image, repo_url, repo_ref, volume, container_name,
       env, cpus, memory_mb, desired, state, last_error, token_id, created_at, updated_at
FROM container_agents_backup;
INSERT INTO device_requests (id, code_hash, poll_secret_hash, machine, created_ip, ip_key, status, user_id,
                             token_id, token_once_enc, created_at, expires_at, last_poll_at, kind)
SELECT id, code_hash, poll_secret_hash, machine, created_ip, ip_key, status, user_id,
       token_id, token_once_enc, created_at, expires_at, last_poll_at, kind
FROM device_requests_backup;

DROP TABLE api_tokens_backup;
DROP TABLE runners_backup;
DROP TABLE container_agents_backup;
DROP TABLE device_requests_backup;

-- A token backs at most one runner: registering is idempotent per token even when two calls race
-- (the second insert fails and the service returns the existing row).
DROP INDEX runners_token;
CREATE UNIQUE INDEX runners_token ON runners (token_id);
