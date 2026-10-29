package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
)

// tx runs a write of the test in the store's write transaction.
func (e *schemaEnv) tx(fn func(*db.Queries) error) {
	e.t.Helper()
	require.NoError(e.t, e.s.WithTx(context.Background(), fn))
}

// CA-01, CA-07 (queries): a runner is created with its token and machine, read by its owner (never by
// somebody else's id) and by its token, listed per owner, renamed by its owner only, updated with what
// the runner reports (last seen, Docker version or error) and deleted by its owner only.
func TestRunnerQueriesAreScopedByOwner(t *testing.T) {
	ctx := context.Background()
	e := newSchemaEnv(t)
	containerAccounts(e)
	e.must(`INSERT INTO api_tokens (id, user_id, kind, name, prefix, token_hash, created_at) VALUES
		('t3', 'u1', 'agent', 'second', 'pl_second', 'hash-t3', 1)`)

	create := func(id, owner, name, token, machine string) {
		e.tx(func(q *db.Queries) error {
			return q.CreateRunner(ctx, db.CreateRunnerParams{
				ID: id, OwnerID: owner, Name: name, TokenID: token, Machine: machine, CreatedAt: 10, UpdatedAt: 10,
			})
		})
	}
	create("r1", "u1", "devbox", "t1", "devbox")
	create("r2", "u1", "apt", "t3", "apt")
	create("r3", "u2", "bob-box", "t2", "bob-box")

	_, err := e.s.R().GetRunner(ctx, db.GetRunnerParams{ID: "r1", OwnerID: "u2"})
	require.ErrorIs(t, err, sql.ErrNoRows, "a runner is not readable by another owner (CA-07)")
	r, err := e.s.R().GetRunner(ctx, db.GetRunnerParams{ID: "r1", OwnerID: "u1"})
	require.NoError(t, err)
	require.Equal(t, "devbox", r.Machine)
	require.False(t, r.DockerVersion.Valid, "nothing reported yet")

	byToken, err := e.s.R().GetRunnerByToken(ctx, "t1")
	require.NoError(t, err)
	require.Equal(t, "r1", byToken.ID, "register is idempotent per token")

	list, err := e.s.R().ListRunners(ctx, "u1")
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, []string{"apt", "devbox"}, []string{list[0].Name, list[1].Name}, "ordered by name")

	var n int64
	e.tx(func(q *db.Queries) error {
		var err error
		n, err = q.UpdateRunnerName(ctx, db.UpdateRunnerNameParams{Name: "stolen", UpdatedAt: 11, ID: "r1", OwnerID: "u2"})
		return err
	})
	require.Zero(t, n, "another owner cannot rename it")
	e.tx(func(q *db.Queries) error {
		var err error
		n, err = q.UpdateRunnerName(ctx, db.UpdateRunnerNameParams{Name: "workstation", UpdatedAt: 12, ID: "r1", OwnerID: "u1"})
		return err
	})
	require.Equal(t, int64(1), n)
	r, err = e.s.R().GetRunner(ctx, db.GetRunnerParams{ID: "r1", OwnerID: "u1"})
	require.NoError(t, err)
	require.Equal(t, "workstation", r.Name)
	require.Equal(t, int64(12), r.UpdatedAt)

	e.tx(func(q *db.Queries) error {
		return q.SetRunnerSeen(ctx, db.SetRunnerSeenParams{LastSeenAt: sql.NullInt64{Int64: 99, Valid: true}, UpdatedAt: 99, ID: "r1"})
	})
	r, err = e.s.R().GetRunner(ctx, db.GetRunnerParams{ID: "r1", OwnerID: "u1"})
	require.NoError(t, err)
	require.Equal(t, sql.NullInt64{Int64: 99, Valid: true}, r.LastSeenAt)

	e.tx(func(q *db.Queries) error {
		return q.SetRunnerDocker(ctx, db.SetRunnerDockerParams{
			DockerVersion: sql.NullString{String: "27.5.1", Valid: true}, UpdatedAt: 100, ID: "r1",
		})
	})
	r, err = e.s.R().GetRunner(ctx, db.GetRunnerParams{ID: "r1", OwnerID: "u1"})
	require.NoError(t, err)
	require.Equal(t, "27.5.1", r.DockerVersion.String, "hello reports the Docker version")
	require.False(t, r.DockerError.Valid)

	e.tx(func(q *db.Queries) error {
		return q.SetRunnerDocker(ctx, db.SetRunnerDockerParams{
			DockerError: sql.NullString{String: "cannot connect to the docker daemon", Valid: true}, UpdatedAt: 101, ID: "r1",
		})
	})
	r, err = e.s.R().GetRunner(ctx, db.GetRunnerParams{ID: "r1", OwnerID: "u1"})
	require.NoError(t, err)
	require.False(t, r.DockerVersion.Valid, "a runner without Docker reports why instead (CA-02)")
	require.Equal(t, "cannot connect to the docker daemon", r.DockerError.String)

	e.tx(func(q *db.Queries) error {
		var err error
		n, err = q.DeleteRunner(ctx, db.DeleteRunnerParams{ID: "r1", OwnerID: "u2"})
		return err
	})
	require.Zero(t, n, "another owner cannot delete it")
	e.tx(func(q *db.Queries) error {
		var err error
		n, err = q.DeleteRunner(ctx, db.DeleteRunnerParams{ID: "r1", OwnerID: "u1"})
		return err
	})
	require.Equal(t, int64(1), n)
	_, err = e.s.R().GetRunner(ctx, db.GetRunnerParams{ID: "r1", OwnerID: "u1"})
	require.ErrorIs(t, err, sql.ErrNoRows)
}

// CA-03 (queries): the upsert replaces the sealed value in place (the id the containers map keeps),
// the list returns the names only, the open-by-ids read returns the owner's rows only, and the delete
// is by owner and name.
func TestOwnerSecretQueriesUpsertInPlaceAndNeverListValues(t *testing.T) {
	ctx := context.Background()
	e := newSchemaEnv(t)
	containerAccounts(e)

	e.tx(func(q *db.Queries) error {
		return q.UpsertOwnerSecret(ctx, db.UpsertOwnerSecretParams{
			ID: "c1", OwnerID: "u1", Name: "OPENCODE_KEY", ValueSealed: []byte("first"), CreatedAt: 10, UpdatedAt: 10,
		})
	})
	e.tx(func(q *db.Queries) error {
		return q.UpsertOwnerSecret(ctx, db.UpsertOwnerSecretParams{
			ID: "c-other", OwnerID: "u1", Name: "CLAUDE_TOKEN", ValueSealed: []byte("claude"), CreatedAt: 11, UpdatedAt: 11,
		})
	})
	e.tx(func(q *db.Queries) error {
		return q.UpsertOwnerSecret(ctx, db.UpsertOwnerSecretParams{
			ID: "c-bob", OwnerID: "u2", Name: "OPENCODE_KEY", ValueSealed: []byte("bob"), CreatedAt: 12, UpdatedAt: 12,
		})
	})
	// Replacing the value keeps the id and the creation time, and bumps updated_at.
	e.tx(func(q *db.Queries) error {
		return q.UpsertOwnerSecret(ctx, db.UpsertOwnerSecretParams{
			ID: "c-replacement", OwnerID: "u1", Name: "OPENCODE_KEY", ValueSealed: []byte("second"), CreatedAt: 13, UpdatedAt: 14,
		})
	})

	list, err := e.s.R().ListOwnerSecretNames(ctx, "u1")
	require.NoError(t, err)
	require.Len(t, list, 2, "the list is the names and dates only, no value")
	require.Equal(t, []string{"CLAUDE_TOKEN", "OPENCODE_KEY"}, []string{list[0].Name, list[1].Name}, "sorted by name")
	require.Equal(t, int64(11), list[0].CreatedAt)
	require.Equal(t, int64(10), list[1].CreatedAt)
	require.Equal(t, int64(14), list[1].UpdatedAt, "the upsert kept the creation time and bumped the update")

	rows, err := e.s.R().GetOwnerSecrets(ctx, db.GetOwnerSecretsParams{OwnerID: "u1", Ids: []string{"c1", "c-bob"}})
	require.NoError(t, err)
	require.Len(t, rows, 1, "a secret of another owner is not opened (CA-07)")
	require.Equal(t, "c1", rows[0].ID, "the upsert kept the id the containers map")
	require.Equal(t, []byte("second"), rows[0].ValueSealed, "the value was replaced")
	require.Equal(t, int64(10), rows[0].CreatedAt)
	require.Equal(t, int64(14), rows[0].UpdatedAt)

	var n int64
	e.tx(func(q *db.Queries) error {
		var err error
		n, err = q.DeleteOwnerSecret(ctx, db.DeleteOwnerSecretParams{OwnerID: "u2", Name: "CLAUDE_TOKEN"})
		return err
	})
	require.Zero(t, n, "another owner cannot delete it")
	e.tx(func(q *db.Queries) error {
		var err error
		n, err = q.DeleteOwnerSecret(ctx, db.DeleteOwnerSecretParams{OwnerID: "u1", Name: "OPENCODE_KEY"})
		return err
	})
	require.Equal(t, int64(1), n)
	left, err := e.s.R().ListOwnerSecretNames(ctx, "u1")
	require.NoError(t, err)
	require.Len(t, left, 1)
	require.Equal(t, "CLAUDE_TOKEN", left[0].Name)
}

// CA-04, CA-06 (queries): the container row of an AI is created from the new tables, read by agent,
// listed by runner, and the lifecycle updates the wanted state, the reported state and the last error
// (which can be cleared), before the AI's delete removes it.
func TestContainerAgentQueries(t *testing.T) {
	ctx := context.Background()
	e := newSchemaEnv(t)
	containerAccounts(e)
	e.must(`INSERT INTO api_tokens (id, user_id, kind, name, prefix, token_hash, created_at) VALUES
		('t3', 'u1', 'agent', 'second', 'pl_second', 'hash-t3', 1)`)
	require.NoError(t, e.runner("r1", "u1", "devbox", "t1", "devbox"))

	e.tx(func(q *db.Queries) error {
		return q.CreateContainerAgent(ctx, db.CreateContainerAgentParams{
			AgentID: "a1", RunnerID: "r1", Harness: "claude", Image: "ghcr.io/wnexous/partyline-agent:1",
			Volume: "vol-a1", ContainerName: "partyline-a1", Env: `[{"var":"OPENCODE_KEY","secret_id":"c1"}]`,
			Cpus: 1.5, MemoryMb: 2048, Desired: "running", TokenID: sql.NullString{String: "t1", Valid: true},
			CreatedAt: 10, UpdatedAt: 10,
		})
	})
	e.tx(func(q *db.Queries) error {
		return q.CreateContainerAgent(ctx, db.CreateContainerAgentParams{
			AgentID: "a2", RunnerID: "r1", Harness: "pi", Image: "ghcr.io/wnexous/partyline-agent:1",
			RepoUrl: sql.NullString{String: "https://example.test/alice/repo.git", Valid: true},
			RepoRef: sql.NullString{String: "main", Valid: true},
			Volume:  "vol-a2", ContainerName: "partyline-a2", Env: "[]",
			Cpus: 1, MemoryMb: 1024, Desired: "stopped",
			CreatedAt: 11, UpdatedAt: 11,
		})
	})

	// A second owner's runner and container, for the by-owner list.
	require.NoError(t, e.runner("r2", "u2", "bob-box", "t2", "bob-box"))
	e.must(`INSERT INTO agents (id, owner_id, name, created_at) VALUES ('a3', 'u2', 'cache', 1)`)
	e.tx(func(q *db.Queries) error {
		return q.CreateContainerAgent(ctx, db.CreateContainerAgentParams{
			AgentID: "a3", RunnerID: "r2", Harness: "omp", Image: "ghcr.io/wnexous/partyline-agent:1",
			Volume: "vol-a3", ContainerName: "partyline-a3", Env: "[]",
			Cpus: 1, MemoryMb: 1024, Desired: "stopped", CreatedAt: 12, UpdatedAt: 12,
		})
	})

	ca, err := e.s.R().GetContainerAgent(ctx, "a1")
	require.NoError(t, err)
	require.Equal(t, "r1", ca.RunnerID)
	require.Equal(t, "unknown", ca.State, "the state is unknown until the runner reports")
	require.False(t, ca.LastError.Valid)
	require.Equal(t, `[{"var":"OPENCODE_KEY","secret_id":"c1"}]`, ca.Env)

	list, err := e.s.R().ListContainerAgentsByRunner(ctx, "r1")
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, []string{"a1", "a2"}, []string{list[0].AgentID, list[1].AgentID}, "ordered by container name")

	// The account deletion reads every container on the owner's runners, and only those (CA-06).
	owned, err := e.s.R().ListContainerAgentsByOwner(ctx, "u1")
	require.NoError(t, err)
	require.Equal(t, []string{"a1", "a2"}, []string{owned[0].AgentID, owned[1].AgentID})
	other, err := e.s.R().ListContainerAgentsByOwner(ctx, "u2")
	require.NoError(t, err)
	require.Len(t, other, 1)
	require.Equal(t, "a3", other[0].AgentID)

	e.tx(func(q *db.Queries) error {
		return q.SetContainerAgentDesired(ctx, db.SetContainerAgentDesiredParams{Desired: "stopped", UpdatedAt: 20, AgentID: "a1"})
	})
	e.tx(func(q *db.Queries) error {
		return q.SetContainerAgentState(ctx, db.SetContainerAgentStateParams{State: "error", UpdatedAt: 21, AgentID: "a1"})
	})
	e.tx(func(q *db.Queries) error {
		return q.SetContainerAgentLastError(ctx, db.SetContainerAgentLastErrorParams{
			LastError: sql.NullString{String: "container exited with code 1", Valid: true}, UpdatedAt: 21, AgentID: "a1",
		})
	})
	ca, err = e.s.R().GetContainerAgent(ctx, "a1")
	require.NoError(t, err)
	require.Equal(t, "stopped", ca.Desired)
	require.Equal(t, "error", ca.State)
	require.Equal(t, "container exited with code 1", ca.LastError.String)
	require.Equal(t, int64(21), ca.UpdatedAt)

	e.tx(func(q *db.Queries) error {
		return q.SetContainerAgentLastError(ctx, db.SetContainerAgentLastErrorParams{UpdatedAt: 22, AgentID: "a1"})
	})
	ca, err = e.s.R().GetContainerAgent(ctx, "a1")
	require.NoError(t, err)
	require.False(t, ca.LastError.Valid, "a later success clears the error")

	// A container created again gets a fresh token (CA-08).
	e.tx(func(q *db.Queries) error {
		return q.SetContainerAgentToken(ctx, db.SetContainerAgentTokenParams{
			TokenID: sql.NullString{String: "t3", Valid: true}, UpdatedAt: 23, AgentID: "a1",
		})
	})
	ca, err = e.s.R().GetContainerAgent(ctx, "a1")
	require.NoError(t, err)
	require.Equal(t, "t3", ca.TokenID.String)

	e.tx(func(q *db.Queries) error { return q.DeleteContainerAgent(ctx, "a1") })
	_, err = e.s.R().GetContainerAgent(ctx, "a1")
	require.ErrorIs(t, err, sql.ErrNoRows)
	list, err = e.s.R().ListContainerAgentsByRunner(ctx, "r1")
	require.NoError(t, err)
	require.Len(t, list, 1)
}
