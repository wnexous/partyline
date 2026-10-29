package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// CA-01: migration 0030 gives api_tokens the runner kind (rebuilding the table without losing a
// token, its row or the foreign keys that name it), gives device_requests the kind the approval
// creates, and makes a token back at most one runner.
func TestMigration0030AddsTheRunnerTokenKind(t *testing.T) {
	ctx := context.Background()
	e := &schemaEnv{t: t, s: openMigratedBefore(t, "0030")}
	containerAccounts(e)
	e.must(`INSERT INTO device_requests (id, code_hash, poll_secret_hash, machine, created_ip, ip_key, status, created_at, expires_at)
		VALUES ('d1', 'code-hash', 'poll-hash', 'devbox', '203.0.113.10', '203.0.113.10', 'pending', 1, 600001)`)
	require.NoError(t, e.runner("r1", "u1", "devbox", "t1", "devbox"))
	e.must(`INSERT INTO container_agents (agent_id, runner_id, harness, image, volume, container_name, cpus, memory_mb, desired, token_id, created_at, updated_at)
		VALUES ('a1', 'r1', 'claude', 'ghcr.io/wnexous/partyline-agent:1', 'vol-a1', 'pl-a1', 1.5, 2048, 'running', 't1', 1, 1)`)

	require.NoError(t, e.s.applyMigration(ctx, "0030_runner_token.sql"))
	require.Equal(t, "0030_runner_token", versions(t, e.s)[len(versions(t, e.s))-1])

	// The rebuilt table keeps every token and the rows that name one.
	require.Equal(t, 2, e.count(`SELECT COUNT(*) FROM api_tokens`))
	require.Equal(t, "t1", e.str(`SELECT token_id FROM runners WHERE id = 'r1'`))
	require.Equal(t, "t1", e.str(`SELECT token_id FROM container_agents WHERE agent_id = 'a1'`))
	require.Equal(t, 0, e.count(`SELECT COUNT(*) FROM pragma_foreign_key_check`))

	// An old approval keeps the default; the runner kind is what the new approval path needs.
	require.Equal(t, "agent", e.str(`SELECT kind FROM device_requests WHERE id = 'd1'`))
	e.must(`INSERT INTO device_requests (id, code_hash, poll_secret_hash, machine, kind, created_ip, ip_key, status, created_at, expires_at)
		VALUES ('d2', 'code-hash-2', 'poll-hash-2', 'runnerbox', 'runner', '', '', 'pending', 1, 600001)`)
	require.Equal(t, "runner", e.str(`SELECT kind FROM device_requests WHERE id = 'd2'`))

	// api_tokens accepts the runner kind (the CHECK of 0001 did not) and refuses unknown kinds.
	e.must(`INSERT INTO api_tokens (id, user_id, kind, name, prefix, token_hash, created_at)
		VALUES ('t3', 'u1', 'runner', 'devbox (runner)', 'pl_runner_x', 'hash-t3', 1)`)
	require.Error(t, e.exec(`INSERT INTO api_tokens (id, user_id, kind, name, prefix, token_hash, created_at)
		VALUES ('t4', 'u1', 'root', 'bad', 'pl_root_x', 'hash-t4', 1)`))

	// One runner per token: the second registration with the same token fails instead of inserting.
	require.Error(t, e.runner("r2", "u1", "other", "t1", "other"))

	// The index the rebuild recreated and the unique one of the runners table.
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'api_tokens_user'`))
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'runners_token'`))
}
