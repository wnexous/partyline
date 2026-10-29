-- Runners (CA-01, CA-02, CA-07). A runner is the server that logged in and runs Docker; Partyline
-- only talks to it through its live connection.

-- name: CreateRunner :exec
INSERT INTO runners (id, owner_id, name, token_id, machine, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetRunner :one
-- A runner is only ever read by its owner (CA-07): an id of somebody else is not found.
SELECT * FROM runners WHERE id = ? AND owner_id = ?;

-- name: GetRunnerByToken :one
-- Who a runner token belongs to: register is idempotent per token and a connection finds its runner.
SELECT * FROM runners WHERE token_id = ?;

-- name: ListRunners :many
-- The owner's runners, for the web list: online state comes from the hub, not from the row.
SELECT * FROM runners WHERE owner_id = ? ORDER BY name, id;

-- name: UpdateRunnerName :execrows
-- Rename only: the token, the machine and what the runner reported stay.
UPDATE runners SET name = ?, updated_at = ? WHERE id = ? AND owner_id = ?;

-- name: SetRunnerSeen :exec
-- The hello of a live connection: NULL last_seen_at is a runner that never connected (CA-01).
UPDATE runners SET last_seen_at = ?, updated_at = ? WHERE id = ?;

-- name: SetRunnerDocker :exec
-- What the hello reported about Docker, CA-02: the version when usable, why not when it is not
-- (exactly one of the two is set; the other is cleared).
UPDATE runners SET docker_version = ?, docker_error = ?, updated_at = ? WHERE id = ?;

-- name: DeleteRunner :execrows
-- Removes the runner; its token is revoked by the service. A container_agents row still using the
-- runner (ON DELETE RESTRICT) makes this fail instead of leaving the container behind.
DELETE FROM runners WHERE id = ? AND owner_id = ?;

-- Owner secrets (CA-03). The value is sealed by the service; no query returns it outside
-- GetOwnerSecrets, which only the container service calls.

-- name: UpsertOwnerSecret :exec
-- Replacing a secret keeps its id (and so the env mapping of every container that uses it) and its
-- creation time; only the sealed value and updated_at change.
INSERT INTO owner_secrets (id, owner_id, name, value_sealed, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (owner_id, name) DO UPDATE SET
    value_sealed = excluded.value_sealed,
    updated_at = excluded.updated_at;

-- name: ListOwnerSecretNames :many
-- The owner's secret names only: the API never returns a value (CA-03, CA-08).
SELECT name FROM owner_secrets WHERE owner_id = ? ORDER BY name;

-- name: GetOwnerSecrets :many
-- The sealed rows of the chosen secrets of one owner (the container service opens them to write the
-- container's env file); an id of another owner is not returned (CA-07).
SELECT * FROM owner_secrets WHERE owner_id = ? AND id IN (sqlc.slice('ids')) ORDER BY name, id;

-- name: DeleteOwnerSecret :execrows
DELETE FROM owner_secrets WHERE owner_id = ? AND name = ?;

-- The container of one AI (CA-04, CA-05, CA-06).

-- name: CreateContainerAgent :exec
-- state is left at its default (unknown) until the runner reports the container.
INSERT INTO container_agents (agent_id, runner_id, harness, image, repo_url, repo_ref, volume,
                              container_name, env, cpus, memory_mb, desired, last_error, token_id,
                              created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetContainerAgent :one
SELECT * FROM container_agents WHERE agent_id = ?;

-- name: ListContainerAgentsByRunner :many
-- The containers a runner reports and operates, by container name.
SELECT * FROM container_agents WHERE runner_id = ? ORDER BY container_name, agent_id;

-- name: SetContainerAgentDesired :exec
-- What the owner wants (start, stop, restart, or the intent a create writes).
UPDATE container_agents SET desired = ?, updated_at = ? WHERE agent_id = ?;

-- name: SetContainerAgentState :exec
-- The last state the runner reported (running, stopped, error, unknown), CA-06.
UPDATE container_agents SET state = ?, updated_at = ? WHERE agent_id = ?;

-- name: SetContainerAgentLastError :exec
-- The last failure (NULL clears it), CA-06.
UPDATE container_agents SET last_error = ?, updated_at = ? WHERE agent_id = ?;

-- name: DeleteContainerAgent :exec
-- Removes the row; the container and its volume on the runner are removed by the service before it.
DELETE FROM container_agents WHERE agent_id = ?;
