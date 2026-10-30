-- A container in Docker's restart loop is its own state (CA-06): the app inside is not serving
-- anything, so showing it as running hides the failure. container_agents.state gains 'restarting';
-- SQLite cannot alter a CHECK constraint, so the table is rebuilt with its rows copied. The table is
-- referenced by nobody (it references agents, runners and api_tokens), so dropping the old one
-- cascades nothing; its index is created again afterwards.
CREATE TABLE container_agents_new (
    agent_id       TEXT    PRIMARY KEY REFERENCES agents (id) ON DELETE CASCADE,
    runner_id      TEXT    NOT NULL REFERENCES runners (id) ON DELETE RESTRICT,
    harness        TEXT    NOT NULL,
    image          TEXT    NOT NULL,
    repo_url       TEXT,
    repo_ref       TEXT,
    volume         TEXT    NOT NULL,
    container_name TEXT    NOT NULL,
    env            TEXT    NOT NULL DEFAULT '[]',
    cpus           REAL    NOT NULL CHECK (cpus > 0),
    memory_mb      INTEGER NOT NULL CHECK (memory_mb >= 64),
    desired        TEXT    NOT NULL CHECK (desired IN ('running', 'stopped')),
    state          TEXT    NOT NULL DEFAULT 'unknown'
                           CHECK (state IN ('running', 'restarting', 'stopped', 'error', 'unknown')),
    last_error     TEXT,
    token_id       TEXT    REFERENCES api_tokens (id) ON DELETE SET NULL,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
INSERT INTO container_agents_new (agent_id, runner_id, harness, image, repo_url, repo_ref, volume,
                                  container_name, env, cpus, memory_mb, desired, state, last_error,
                                  token_id, created_at, updated_at)
SELECT agent_id, runner_id, harness, image, repo_url, repo_ref, volume, container_name, env, cpus,
       memory_mb, desired, state, last_error, token_id, created_at, updated_at
FROM container_agents;

DROP TABLE container_agents;
ALTER TABLE container_agents_new RENAME TO container_agents;
CREATE INDEX container_agents_runner ON container_agents (runner_id);
