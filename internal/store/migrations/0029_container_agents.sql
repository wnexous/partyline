-- Container agents (runner model): an AI that lives in a container on a server that logs in to
-- Partyline, CA-01, CA-03, CA-04, CA-05. Additive: nothing is rebuilt.

-- A server that joined by running `partyline runner` (CA-01, CA-02): it holds an outbound
-- WebSocket, so Partyline never dials it. token_id is the runner token the registration created;
-- deleting the account removes the runner and its token, and the token is never purged while the
-- runner exists (no action). machine is the hostname the runner reported, name the label the owner
-- chooses (unique per owner, defaults to the hostname). docker_version and docker_error are what
-- the last hello said about Docker (one of them is NULL: usable or not), last_seen_at the time of
-- that hello and NULL until the first connection.
CREATE TABLE runners (
    id             TEXT    PRIMARY KEY,
    owner_id       TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name           TEXT    NOT NULL,
    token_id       TEXT    NOT NULL REFERENCES api_tokens (id),
    machine        TEXT    NOT NULL,
    docker_version TEXT,
    docker_error   TEXT,
    last_seen_at   INTEGER,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    UNIQUE (owner_id, name)
);
-- The register/login path looks a runner up by its token (idempotent per token) on every connect.
CREATE INDEX runners_token ON runners (token_id);

-- A named credential the owner stores for their containers (CA-03): value_sealed is sealed with
-- the instance key and no query returns it. The name becomes an environment variable, so it is
-- restricted to the pattern the shell and the harnesses accept; UNIQUE (owner_id, name) serves the
-- upsert by name and the list.
CREATE TABLE owner_secrets (
    id           TEXT    PRIMARY KEY,
    owner_id     TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 64
                                        AND substr(name, 1, 1) GLOB '[A-Za-z]'
                                        AND name NOT GLOB '*[^A-Za-z0-9_-]*'),
    value_sealed BLOB    NOT NULL,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL,
    UNIQUE (owner_id, name)
);

-- The container of one AI (CA-04, CA-05, CA-06). The row is the AI's row: it goes with the AI
-- (ON DELETE CASCADE) and is never created without one. A runner in use cannot be deleted
-- (RESTRICT) while one of its containers exists: the container would be left on the host.
-- env is the JSON list of the environment variables to expose, [{var, secret_id}] by secret id so
-- a renamed secret keeps working and no value is stored here. desired is what the owner wants, the
-- state the runner last reported (unknown before the first hello, CA-02). last_error is the last
-- failure shown in the web app, NULL when none. token_id is the AI's agent token; a token purge
-- drops it from the row instead of blocking (CA-08: the row does not hold the token itself).
CREATE TABLE container_agents (
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
                           CHECK (state IN ('running', 'stopped', 'error', 'unknown')),
    last_error     TEXT,
    token_id       TEXT    REFERENCES api_tokens (id) ON DELETE SET NULL,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
-- The containers of one runner (list by runner, and the delete of a runner checks it).
CREATE INDEX container_agents_runner ON container_agents (runner_id);
