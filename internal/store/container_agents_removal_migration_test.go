package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// AD-039: migration 0034 removes the container agents system: the three tables of the feature and
// the runner tokens (with their device requests), keeping every other token, request and row.
func TestMigration0034RemovesTheContainerAgents(t *testing.T) {
	ctx := context.Background()
	e := &schemaEnv{t: t, s: openMigratedBefore(t, "0034")}
	e.user("u1", "alice")
	e.must(`INSERT INTO agents (id, owner_id, name, created_at) VALUES ('a1', 'u1', 'atlas', 1)`)
	e.must(`INSERT INTO api_tokens (id, user_id, kind, name, prefix, token_hash, created_at) VALUES
		('t1', 'u1', 'agent', 'atlas', 'pl_atlas', 'hash-t1', 1),
		('t2', 'u1', 'user', 'script', 'pl_script', 'hash-t2', 1),
		('t3', 'u1', 'runner', 'devbox (runner)', 'pl_runner', 'hash-t3', 1)`)
	e.must(`INSERT INTO runners (id, owner_id, name, token_id, machine, created_at, updated_at)
		VALUES ('r1', 'u1', 'devbox', 't3', 'devbox', 1, 1)`)
	e.must(`INSERT INTO owner_secrets (id, owner_id, name, value_sealed, created_at, updated_at)
		VALUES ('c1', 'u1', 'OPENCODE_KEY', 'sealed-key', 1, 1)`)
	e.must(`INSERT INTO container_agents (agent_id, runner_id, harness, image, volume, container_name,
			env, cpus, memory_mb, desired, created_at, updated_at)
		VALUES ('a1', 'r1', 'claude', 'ghcr.io/wnexous/partyline-agent:1', 'vol-a1', 'pl-a1', '[]', 1, 1024, 'running', 1, 1)`)
	e.must(`INSERT INTO device_requests (id, code_hash, poll_secret_hash, machine, kind, status, token_id, created_at, expires_at) VALUES
		('d1', 'code-hash-1', 'poll-hash-1', 'laptop', 'agent', 'approved', 't1', 1, 600001),
		('d2', 'code-hash-2', 'poll-hash-2', 'runnerbox', 'runner', 'approved', 't3', 1, 600001),
		('d3', 'code-hash-3', 'poll-hash-3', 'otherbox', 'runner', 'pending', NULL, 1, 600001)`)

	require.NoError(t, e.s.applyMigration(ctx, "0034_remove_container_agents.sql"))
	require.Equal(t, "0034_remove_container_agents", versions(t, e.s)[len(versions(t, e.s))-1])

	// The three tables of the feature are gone, on an upgraded database and on a fresh one.
	fresh := newSchemaEnv(t)
	for _, table := range []string{"container_agents", "owner_secrets", "runners"} {
		require.Zero(t, e.count(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table),
			"%s is dropped", table)
		require.Zero(t, fresh.count(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table),
			"%s is absent from a fresh database", table)
	}

	// The runner token goes with its device requests; the agent and user tokens stay.
	require.Zero(t, e.count(`SELECT COUNT(*) FROM api_tokens WHERE kind = 'runner'`), "no runner token survives")
	require.Equal(t, "atlas", e.str(`SELECT name FROM api_tokens WHERE id = 't1'`), "the agent token is untouched")
	require.Equal(t, "script", e.str(`SELECT name FROM api_tokens WHERE id = 't2'`), "the user token is untouched")
	require.Equal(t, "d1", e.str(`SELECT id FROM device_requests`), "only the agent request stays")
	require.Zero(t, e.count(`SELECT COUNT(*) FROM pragma_foreign_key_check`), "no foreign key is left dangling")

	// The CHECKs were left as they were (the migration says so): the kind is unreachable because
	// nothing writes it any more, not because the schema refuses it.
	e.must(`INSERT INTO api_tokens (id, user_id, kind, name, prefix, token_hash, created_at)
		VALUES ('t4', 'u1', 'runner', 'old', 'pl_runner', 'hash-t4', 1)`)
}
