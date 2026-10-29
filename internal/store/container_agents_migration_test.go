package store

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// containerAccounts inserts two owners, each with an AI and an agent token: alice owns a1/t1, bob a2/t2.
func containerAccounts(e *schemaEnv) {
	e.t.Helper()
	e.user("u1", "alice")
	e.user("u2", "bob")
	e.must(`INSERT INTO agents (id, owner_id, name, created_at) VALUES ('a1', 'u1', 'atlas', 1), ('a2', 'u2', 'borg', 1)`)
	e.must(`INSERT INTO api_tokens (id, user_id, kind, name, prefix, token_hash, created_at) VALUES
		('t1', 'u1', 'agent', 'atlas', 'pl_atlas', 'hash-t1', 1),
		('t2', 'u2', 'agent', 'borg', 'pl_borg', 'hash-t2', 1)`)
}

// runner inserts a runner of owner: its token, its machine, and nothing reported yet (no Docker
// version, no error, never seen).
func (e *schemaEnv) runner(id, owner, name, tokenID, machine string) error {
	return e.exec(`INSERT INTO runners (id, owner_id, name, token_id, machine, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 1, 1)`, id, owner, name, tokenID, machine)
}

// ownerSecret inserts a secret of owner with its name and a stand-in sealed value.
func (e *schemaEnv) ownerSecret(id, owner, name, sealed string) error {
	return e.exec(`INSERT INTO owner_secrets (id, owner_id, name, value_sealed, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, 1)`, id, owner, name, sealed)
}

// containerAgent inserts the container row of agent: built by runner, wanting desired, with the AI's
// token and the given limits.
func (e *schemaEnv) containerAgent(agentID, runnerID, tokenID, desired string, cpus float64, memoryMB int64) error {
	return e.exec(`INSERT INTO container_agents (agent_id, runner_id, harness, image, repo_url, repo_ref,
			volume, container_name, env, cpus, memory_mb, desired, last_error, token_id, created_at, updated_at)
		VALUES (?, ?, 'claude', 'ghcr.io/wnexous/partyline-agent:1', NULL, NULL, ?, ?, '[]', ?, ?, ?, NULL, ?, 1, 1)`,
		agentID, runnerID, "vol-"+agentID, "partyline-"+agentID, cpus, memoryMB, desired, tokenID)
}

// containerAgentsRows fills alice's new tables: a runner, a secret and an AI with its container, so
// every key of 0029 has a row.
func containerAgentsRows(e *schemaEnv) {
	e.t.Helper()
	require.NoError(e.t, e.runner("r1", "u1", "devbox", "t1", "devbox"))
	require.NoError(e.t, e.ownerSecret("c1", "u1", "OPENCODE_KEY", "sealed-key"))
	require.NoError(e.t, e.containerAgent("a1", "r1", "t1", "running", 1.5, 2048))
}

// containerAgentsFixture is the accounts above plus the rows of the new tables.
func containerAgentsFixture(e *schemaEnv) {
	e.t.Helper()
	containerAccounts(e)
	containerAgentsRows(e)
}

// CA-01, CA-03, CA-04 (data): migration 0029 creates runners, owner_secrets and container_agents
// with the design's columns on a database that existed before it, keeps every row of the schema it
// upgrades, and leaves no foreign key dangling. A fresh database has the same three tables.
func TestMigration0029CreatesTheContainerAgentTables(t *testing.T) {
	ctx := context.Background()
	e := &schemaEnv{t: t, s: openMigratedBefore(t, "0029")}
	require.Empty(t, e.columnsOf("runners"), "fixture sanity: no runners table before 0029")
	containerAccounts(e)
	agentsBefore := e.rowsOf("agents", e.columnsOf("agents"))
	usersBefore := e.rowsOf("users", e.columnsOf("users"))

	require.NoError(t, e.s.applyMigration(ctx, "0029_container_agents.sql"))
	require.Equal(t, "0029_container_agents", versions(t, e.s)[len(versions(t, e.s))-1])
	require.Equal(t, agentsBefore, e.rowsOf("agents", e.columnsOf("agents")), "no AI changed")
	require.Equal(t, usersBefore, e.rowsOf("users", e.columnsOf("users")), "no account changed")

	want := map[string][]string{
		"runners":       {"id", "owner_id", "name", "token_id", "machine", "docker_version", "docker_error", "last_seen_at", "created_at", "updated_at"},
		"owner_secrets": {"id", "owner_id", "name", "value_sealed", "created_at", "updated_at"},
		"container_agents": {"agent_id", "runner_id", "harness", "image", "repo_url", "repo_ref", "volume",
			"container_name", "env", "cpus", "memory_mb", "desired", "state", "last_error", "token_id", "created_at", "updated_at"},
	}
	fresh := newSchemaEnv(t)
	for table, cols := range want {
		require.Equal(t, cols, e.columnsOf(table), "columns of %s on an upgraded database", table)
		require.Equal(t, cols, fresh.columnsOf(table), "columns of %s on a fresh database", table)
	}

	containerAgentsRows(e)
	require.Zero(t, e.count(`SELECT COUNT(*) FROM pragma_foreign_key_check`), "no foreign key is left dangling")
}

// CA-01 (data): a runner belongs to one owner, its name is unique per owner, its token and machine
// are set, and what the runner reports (Docker version or error, last seen) starts empty.
func TestRunnersAreOwnedAndValidated(t *testing.T) {
	e := newSchemaEnv(t)
	containerAccounts(e)

	require.NoError(t, e.runner("r1", "u1", "devbox", "t1", "devbox"))
	require.NoError(t, e.runner("r2", "u2", "devbox", "t2", "bob-box"), "another owner may use the same name")
	require.Error(t, e.runner("r3", "u1", "devbox", "t1", "devbox"), "the name is unique per owner")
	require.Error(t, e.runner("r4", "u1", "ghost", "missing", "devbox"), "the token must exist")
	require.Error(t, e.runner("r5", "missing", "orphan", "t1", "devbox"), "the owner must exist")
	require.Error(t, e.exec(`INSERT INTO runners (id, owner_id, name, machine, created_at, updated_at)
		VALUES ('r6', 'u1', 'notoken', 'devbox', 1, 1)`), "the runner token is required")
	require.Error(t, e.exec(`INSERT INTO runners (id, owner_id, name, token_id, created_at, updated_at)
		VALUES ('r7', 'u1', 'nomachine', 't1', 1, 1)`), "the machine is required")

	var dockerVersion, dockerError sql.NullString
	var lastSeen sql.NullInt64
	require.NoError(t, e.s.write.QueryRow(`SELECT docker_version, docker_error, last_seen_at FROM runners WHERE id = 'r1'`).
		Scan(&dockerVersion, &dockerError, &lastSeen))
	require.False(t, dockerVersion.Valid, "no Docker version before the first hello")
	require.False(t, dockerError.Valid, "no Docker error before the first hello")
	require.False(t, lastSeen.Valid, "a new runner was never seen")
}

// CA-03 (data): a secret's name is the environment variable it becomes, so only the pattern the
// design names is accepted; the name is unique per owner and its sealed value has no default.
func TestOwnerSecretsHaveCheckedNamesPerOwner(t *testing.T) {
	e := newSchemaEnv(t)
	containerAccounts(e)

	for _, name := range []string{"A", "OPENCODE_KEY", "k-1", "Z9_", strings.Repeat("a", 64)} {
		require.NoError(t, e.ownerSecret("c-"+name, "u1", name, "sealed-"+name), "name %q is accepted", name)
	}
	for _, name := range []string{"", "1KEY", "_KEY", "KEY=1", "KEY VALUE", "KEY.X", strings.Repeat("a", 65), "KÉY"} {
		require.Error(t, e.ownerSecret("bad-"+name, "u1", name, "sealed"), "name %q is refused", name)
	}
	require.Error(t, e.ownerSecret("c1", "u1", "OPENCODE_KEY", "sealed"), "the name is unique per owner")
	require.NoError(t, e.ownerSecret("c2", "u2", "OPENCODE_KEY", "sealed"), "another owner may reuse the name")
	require.Error(t, e.exec(`INSERT INTO owner_secrets (id, owner_id, name, created_at, updated_at)
		VALUES ('c3', 'u1', 'NOVALUE', 1, 1)`), "the sealed value is required")
	require.Error(t, e.ownerSecret("c4", "missing", "GHOST", "sealed"), "an owner must exist")
}

// CA-04, CA-05, CA-06 (data): one container row per AI; desired is running or stopped, the reported
// state is one of the four the design names (unknown until the runner reports), the limits are
// positive and at least 64 MB, the AI takes the row with it, and a runner in use cannot be deleted.
func TestContainerAgentsConstraintsAndCascades(t *testing.T) {
	e := newSchemaEnv(t)
	containerAgentsFixture(e)

	require.NoError(t, e.containerAgent("a2", "r1", "t2", "stopped", 0.5, 64), "the limits' minimums are accepted")
	require.Error(t, e.containerAgent("a1", "r1", "t1", "paused", 1, 1024), "desired is running or stopped")
	require.Error(t, e.containerAgent("a1", "r1", "t1", "running", 0, 1024), "cpus must be positive")
	require.Error(t, e.containerAgent("a1", "r1", "t1", "running", -1, 1024), "cpus must be positive")
	require.Error(t, e.containerAgent("a1", "r1", "t1", "running", 1, 63), "memory must be at least 64 MB")
	require.Error(t, e.containerAgent("a1", "missing", "t1", "running", 1, 1024), "the runner must exist")
	require.Error(t, e.containerAgent("missing", "r1", "t1", "running", 1, 1024), "the AI must exist")
	require.Error(t, e.containerAgent("a1", "r1", "missing", "running", 1, 1024), "the token must exist")

	var state string
	require.NoError(t, e.s.write.QueryRow(`SELECT state FROM container_agents WHERE agent_id = 'a1'`).Scan(&state))
	require.Equal(t, "unknown", state, "nothing is reported before the first hello")
	for _, ok := range []string{"running", "stopped", "error", "unknown"} {
		require.NoError(t, e.exec(`UPDATE container_agents SET state = ? WHERE agent_id = 'a1'`, ok), "state %q is accepted", ok)
	}
	require.Error(t, e.exec(`UPDATE container_agents SET state = 'starting' WHERE agent_id = 'a1'`), "an unreported state is refused")

	// A runner in use is restricted; deleting an AI takes its container row with it.
	require.Error(t, e.exec(`DELETE FROM runners WHERE id = 'r1'`), "a runner in use is restricted")
	require.NoError(t, e.exec(`DELETE FROM agents WHERE id = 'a2'`), "the AI delete cascades its container")
	require.Zero(t, e.count(`SELECT COUNT(*) FROM container_agents WHERE agent_id = 'a2'`))
	require.Error(t, e.exec(`DELETE FROM runners WHERE id = 'r1'`), "a1 still uses the runner")
	require.NoError(t, e.exec(`DELETE FROM agents WHERE id = 'a1'`), "the AI delete cascades its container")
	require.NoError(t, e.exec(`DELETE FROM runners WHERE id = 'r1'`), "the runner is free once no container uses it")

	// A dangling token is dropped from the row instead of blocking the token purge.
	e.must(`INSERT INTO agents (id, owner_id, name, created_at) VALUES ('a3', 'u1', 'comet', 1)`)
	e.must(`INSERT INTO api_tokens (id, user_id, kind, name, prefix, token_hash, created_at) VALUES
		('t3', 'u1', 'agent', 'comet', 'pl_comet', 'hash-t3', 1), ('t4', 'u1', 'agent', 'runner', 'pl_runner', 'hash-t4', 1)`)
	require.NoError(t, e.runner("r2", "u1", "devbox-2", "t4", "devbox-2"))
	require.NoError(t, e.containerAgent("a3", "r2", "t3", "running", 1, 1024))
	require.NoError(t, e.exec(`DELETE FROM api_tokens WHERE id = 't3'`))
	var tokenID sql.NullString
	require.NoError(t, e.s.write.QueryRow(`SELECT token_id FROM container_agents WHERE agent_id = 'a3'`).Scan(&tokenID))
	require.False(t, tokenID.Valid, "the container row survives the purge of its token")
}

// CA-01, CA-03, CA-07 (data): deleting an account takes its runners and its secrets with it and
// leaves another owner's rows alone (a runner a container still uses blocks the delete first, because
// runner_id is RESTRICT: the container row has to go, as the container service does).
func TestDeletingTheOwnerTakesTheRunnersAndSecrets(t *testing.T) {
	e := newSchemaEnv(t)
	containerAgentsFixture(e)
	require.NoError(t, e.runner("r2", "u2", "bob-box", "t2", "bob-box"), "bob's runner")

	require.NoError(t, e.exec(`DELETE FROM container_agents WHERE agent_id = 'a1'`))
	e.must(`DELETE FROM users WHERE id = 'u1'`)
	require.Zero(t, e.count(`SELECT COUNT(*) FROM runners WHERE owner_id = 'u1'`), "alice's runners are gone")
	require.Zero(t, e.count(`SELECT COUNT(*) FROM owner_secrets`), "alice's secrets are gone")
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM runners WHERE owner_id = 'u2'`), "bob's runner stays")
	require.Zero(t, e.count(`SELECT COUNT(*) FROM pragma_foreign_key_check`), "no foreign key is left dangling")
}
