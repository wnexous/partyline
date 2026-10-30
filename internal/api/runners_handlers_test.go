package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/runners"
	"github.com/wnexous/partyline/internal/runners/spec"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

var runnerT0 = time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)

func registerRunner(t *testing.T, c *testkit.Client, machine string) dto.Runner {
	t.Helper()
	return testkit.Decode[dto.Runner](c.Post("/api/v1/runner/register", dto.RegisterRunnerRequest{Machine: machine}).
		RequireStatus(http.StatusCreated))
}

func runnerList(t *testing.T, c *testkit.Client) []dto.Runner {
	t.Helper()
	return testkit.Decode[dto.List[dto.Runner]](c.Get("/api/v1/runners").RequireStatus(http.StatusOK)).Items
}

// seedContainer inserts the container_agents row of a new AI called name on the runner, as the
// container service will (CA-04); it returns the AI's id.
func seedContainer(t *testing.T, env *testkit.Env, owner *testkit.Client, runnerID, name string) string {
	t.Helper()
	agentID := owner.NewAI(name)
	ctx := context.Background()
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.CreateContainerAgent(ctx, db.CreateContainerAgentParams{
			AgentID: agentID, RunnerID: runnerID, Harness: "claude",
			Image: spec.DefaultImageRepo + ":1", Volume: "pl-" + name + "-workspace",
			ContainerName: "pl-" + name, Env: "[]", Cpus: 1, MemoryMb: 1024, Desired: "running",
			CreatedAt: runnerT0.UnixMilli(), UpdatedAt: runnerT0.UnixMilli(),
		})
	}))
	return agentID
}

func storedContainer(t *testing.T, env *testkit.Env, agentID string) db.ContainerAgent {
	t.Helper()
	row, err := env.App.Store.R().GetContainerAgent(context.Background(), agentID)
	require.NoError(t, err)
	return row
}

// CA-01, TOK-03: a runner token reaches only the runner routes; an agent token cannot register a
// runner and a user credential cannot open the runner WebSocket.
func TestRunnerTokenScoping(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	runnerClient := alice.RunnerToken("devbox (runner)")

	// The runner kind is not offered by POST /tokens: only the device approval creates it.
	alice.Post("/api/v1/tokens", dto.CreateTokenRequest{Kind: dto.TokenKindRunner, Name: "sneaky"}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)

	runnerClient.Get("/api/v1/me").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	runnerClient.Get("/api/v1/runners").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	runnerClient.Post("/api/v1/tokens", dto.CreateTokenRequest{Kind: dto.TokenKindUser, Name: "x"}).
		RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)

	agent := alice.Token(dto.TokenKindAgent, "machine")
	agent.Post("/api/v1/runner/register", dto.RegisterRunnerRequest{Machine: "devbox"}).
		RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)

	runnerClient.Get("/api/v1/agent/ws").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	alice.Get("/api/v1/runner/ws").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	alice.Post("/api/v1/runner/register", dto.RegisterRunnerRequest{Machine: "devbox"}).
		RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	alice.Token(dto.TokenKindUser, "script").Get("/api/v1/runner/ws").
		RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	env.Anon().Get("/api/v1/runner/ws").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)

	// A runner token that registered nothing has no connection yet.
	runnerClient.Get("/api/v1/runner/ws").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)

	registered := registerRunner(t, runnerClient, "devbox")
	require.Equal(t, "devbox", registered.Name)
	require.Equal(t, "devbox", registered.Machine)
	require.False(t, registered.Online)
	// The token is accepted on the route; a plain GET is not a WebSocket handshake.
	runnerClient.Get("/api/v1/runner/ws").RequireStatus(http.StatusUpgradeRequired)
}

// CA-01: registering is idempotent per token, the name defaults to the machine and is made unique
// per owner, renaming refuses another runner's name and only the owner sees the runner.
func TestRunnerRegisterIdempotentAndNames(t *testing.T) {
	env := testkit.New(t)
	admin := env.SetupAdmin()
	alice, bob := env.NewUser("alice"), env.NewUser("bob")

	t1 := alice.RunnerToken("one")
	first := registerRunner(t, t1, "devbox")
	again := testkit.Decode[dto.Runner](t1.Post("/api/v1/runner/register", dto.RegisterRunnerRequest{Name: "other", Machine: "devbox"}).
		RequireStatus(http.StatusOK))
	require.Equal(t, first.ID, again.ID, "idempotent per token")
	require.Equal(t, "devbox", again.Name, "a second register changes nothing")

	t2 := alice.RunnerToken("two")
	second := registerRunner(t, t2, "devbox")
	require.Equal(t, "devbox-2", second.Name, "the name is made unique per owner")

	t3 := alice.RunnerToken("three")
	named := testkit.Decode[dto.Runner](t3.Post("/api/v1/runner/register",
		dto.RegisterRunnerRequest{Name: "  desk  ", Machine: "laptop"}).RequireStatus(http.StatusCreated))
	require.Equal(t, "desk", named.Name, "the name is trimmed")

	// Invalid values name their field.
	e := t3.Post("/api/v1/runner/register", dto.RegisterRunnerRequest{Machine: ""}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "machine")
	e = t3.Post("/api/v1/runner/register", dto.RegisterRunnerRequest{Name: "bad/name", Machine: "box"}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "name")

	list := runnerList(t, alice)
	require.Equal(t, []string{"desk", "devbox", "devbox-2"}, []string{list[0].Name, list[1].Name, list[2].Name})
	for _, r := range list {
		require.False(t, r.Online)
		require.Zero(t, r.ContainerCount)
	}
	require.Empty(t, runnerList(t, bob), "a runner is only ever its owner's (CA-07)")

	// Rename: only the owner, only a free name.
	renamed := testkit.Decode[dto.Runner](alice.Patch("/api/v1/runners/"+named.ID, dto.RenameRunnerRequest{Name: "runner-a"}).
		RequireStatus(http.StatusOK))
	require.Equal(t, "runner-a", renamed.Name)
	alice.Patch("/api/v1/runners/"+named.ID, dto.RenameRunnerRequest{Name: "devbox"}).
		RequireError(http.StatusConflict, dto.ErrorCodeConflict)
	alice.Patch("/api/v1/runners/"+named.ID, dto.RenameRunnerRequest{Name: ""}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	bob.Patch("/api/v1/runners/"+first.ID, dto.RenameRunnerRequest{Name: "mine"}).
		RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	// CA-07: register, rename and delete are audited.
	entries := testkit.Decode[dto.Page[dto.AuditEntry]](admin.Get("/api/v1/admin/audit").RequireStatus(http.StatusOK)).Items
	actions := map[string]int{}
	for _, entry := range entries {
		actions[entry.Action]++
	}
	require.Equal(t, 3, actions[runners.ActionRegister])
	require.Equal(t, 1, actions[runners.ActionRename])
}

// CA-02, CA-06, CA-09: a live runner reports Docker and its containers, answers commands through
// the hub and pushes state events into container_agents; an offline runner fails fast.
func TestRunnerHubRoundTrip(t *testing.T) {
	clock := testkit.NewClock(runnerT0)
	env := testkit.New(t, testkit.WithClock(clock.Now))
	alice := env.NewUser("alice")
	runnerClient := alice.RunnerToken("devbox")
	runner := registerRunner(t, runnerClient, "devbox")

	conn := runnerClient.DialRunnerConn(spec.Hello{DockerVersion: "27.3.1"})
	require.Eventually(t, func() bool {
		list := runnerList(t, alice)
		return len(list) == 1 && list[0].Online
	}, 5*time.Second, 10*time.Millisecond)
	list := runnerList(t, alice)
	require.Equal(t, "27.3.1", list[0].DockerVersion)
	require.Empty(t, list[0].DockerError)
	require.NotNil(t, list[0].LastSeenAt)
	require.True(t, runnerT0.Equal(*list[0].LastSeenAt), "last_seen_at is the hello time: %s", list[0].LastSeenAt)

	conn.Handle(func(cmd spec.Command) *spec.Result {
		if cmd.Op == spec.OpLogs {
			return &spec.Result{OK: true, Logs: "ready\n"}
		}
		return &spec.Result{OK: true}
	})
	ctx := context.Background()
	res, err := env.App.Runners.Hub().Do(ctx, runner.ID, spec.Command{Op: spec.OpStart, Name: "pl-atlas"})
	require.NoError(t, err)
	require.True(t, res.OK)
	logs, err := env.App.Runners.Hub().Do(ctx, runner.ID, spec.Command{Op: spec.OpLogs, Name: "pl-atlas", Tail: 10})
	require.NoError(t, err)
	require.Equal(t, "ready\n", logs.Logs)

	// The container states the runner reports land in container_agents (CA-06).
	agentID := seedContainer(t, env, alice, runner.ID, "atlas")
	conn.State(spec.Container{Name: "pl-atlas", State: spec.StateRunning})
	require.Eventually(t, func() bool { return storedContainer(t, env, agentID).State == "running" },
		5*time.Second, 10*time.Millisecond)
	conn.State(spec.Container{Name: "pl-atlas", State: spec.StateError, Error: "exit code 2"})
	require.Eventually(t, func() bool {
		row := storedContainer(t, env, agentID)
		return row.State == "error" && row.LastError.String == "exit code 2"
	}, 5*time.Second, 10*time.Millisecond)
	conn.State(spec.Container{Name: "pl-atlas", State: spec.StateRunning})
	require.Eventually(t, func() bool {
		row := storedContainer(t, env, agentID)
		return row.State == "running" && !row.LastError.Valid
	}, 5*time.Second, 10*time.Millisecond)

	// A restart loop is its own state, not running, and the reason is the container's last log line
	// the runner read and redacted, not Docker's exit code (CA-06).
	conn.State(spec.Container{
		Name: "pl-atlas", State: spec.StateRestarting,
		LastLog: "partyline-agent: error: opencode: set OPENCODE_API_KEY in the environment",
	})
	require.Eventually(t, func() bool {
		row := storedContainer(t, env, agentID)
		return row.State == "restarting" &&
			row.LastError.String == "partyline-agent: error: opencode: set OPENCODE_API_KEY in the environment"
	}, 5*time.Second, 10*time.Millisecond)
	conn.State(spec.Container{Name: "pl-atlas", State: spec.StateError, Error: "the container exited with code 2"})
	require.Eventually(t, func() bool {
		row := storedContainer(t, env, agentID)
		return row.State == "error" && row.LastError.String == "the container exited with code 2"
	}, 5*time.Second, 10*time.Millisecond)

	// A runner that is gone fails a command at once instead of queueing it (no queue in v1).
	conn.Close()
	require.Eventually(t, func() bool {
		list := runnerList(t, alice)
		return len(list) == 1 && !list[0].Online
	}, 5*time.Second, 10*time.Millisecond)
	_, err = env.App.Runners.Hub().Do(ctx, runner.ID, spec.Command{Op: spec.OpStart, Name: "pl-atlas"})
	require.ErrorIs(t, err, runners.ErrOffline)

	// The next hello replaces what the last one said about Docker (CA-02).
	runnerClient.DialRunnerConn(spec.Hello{DockerError: "permission denied on /var/run/docker.sock"})
	require.Eventually(t, func() bool {
		list := runnerList(t, alice)
		return len(list) == 1 && list[0].Online && list[0].DockerError != ""
	}, 5*time.Second, 10*time.Millisecond)
	list = runnerList(t, alice)
	require.Empty(t, list[0].DockerVersion)
	require.Equal(t, "permission denied on /var/run/docker.sock", list[0].DockerError)
}

// CA-02: a first frame that is no valid hello closes the connection and changes nothing.
func TestRunnerInvalidHelloCloses(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	runnerClient := alice.RunnerToken("devbox")
	registerRunner(t, runnerClient, "devbox")

	conn := runnerClient.DialRunnerConn(spec.Hello{DockerVersion: "27.3.1", DockerError: "conflicting"})
	_, err := conn.TryCommand(5 * time.Second)
	require.Error(t, err, "the server closes a connection whose hello is invalid")
	require.Eventually(t, func() bool { return !runnerList(t, alice)[0].Online }, 5*time.Second, 10*time.Millisecond)
	list := runnerList(t, alice)
	require.Empty(t, list[0].DockerVersion)
	require.Empty(t, list[0].DockerError)
}

// CA-01: a new connection of a runner replaces the old one, so a restarted `partyline runner`
// takes over at once.
func TestRunnerConnectionReplacesTheOldOne(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	runnerClient := alice.RunnerToken("devbox")
	runner := registerRunner(t, runnerClient, "devbox")

	first := runnerClient.DialRunnerConn(spec.Hello{DockerVersion: "27.3.1"})
	require.Eventually(t, func() bool { return runnerList(t, alice)[0].Online }, 5*time.Second, 10*time.Millisecond)
	second := runnerClient.DialRunnerConn(spec.Hello{DockerVersion: "27.3.2"})
	second.Handle(func(spec.Command) *spec.Result { return &spec.Result{OK: true} })

	// The old connection is closed by the server…
	_, err := first.TryCommand(5 * time.Second)
	require.Error(t, err)
	// …and the commands go to the new one.
	_, err = env.App.Runners.Hub().Do(context.Background(), runner.ID, spec.Command{Op: spec.OpRestart, Name: "pl-atlas"})
	require.NoError(t, err)
	require.Equal(t, "27.3.2", runnerList(t, alice)[0].DockerVersion)
}

// CA-09: an operation has its own timeout; a runner that reads but never answers is an error, not
// a hang, and the next command still works.
func TestRunnerCommandTimeout(t *testing.T) {
	env := testkit.New(t, testkit.WithRunnerTimeouts(map[spec.Op]time.Duration{
		spec.OpState: 100 * time.Millisecond,
	}))
	alice := env.NewUser("alice")
	runnerClient := alice.RunnerToken("devbox")
	runner := registerRunner(t, runnerClient, "devbox")
	conn := runnerClient.DialRunnerConn(spec.Hello{DockerVersion: "27.3.1"})
	require.Eventually(t, func() bool { return runnerList(t, alice)[0].Online }, 5*time.Second, 10*time.Millisecond)
	conn.Handle(func(cmd spec.Command) *spec.Result {
		if cmd.Op == spec.OpState {
			return nil // a hung runner: the command is read and never answered
		}
		return &spec.Result{OK: true}
	})
	ctx := context.Background()
	_, err := env.App.Runners.Hub().Do(ctx, runner.ID, spec.Command{Op: spec.OpState})
	require.ErrorIs(t, err, runners.ErrTimeout)
	_, err = env.App.Runners.Hub().Do(ctx, runner.ID, spec.Command{Op: spec.OpStart, Name: "pl-atlas"})
	require.NoError(t, err)
}

// CA-07: deleting a runner is its owner's, revokes the token and refuses while a container of the
// runner exists (409 runner_in_use); the live connection closes.
func TestRunnerDeleteRefusalsAndRevocation(t *testing.T) {
	env := testkit.New(t)
	admin := env.SetupAdmin()
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	runnerClient := alice.RunnerToken("devbox")
	runner := registerRunner(t, runnerClient, "devbox")
	conn := runnerClient.DialRunnerConn(spec.Hello{DockerVersion: "27.3.1"})

	agentID := seedContainer(t, env, alice, runner.ID, "atlas")
	alice.Delete("/api/v1/runners/"+runner.ID).RequireError(http.StatusConflict, dto.ErrorCodeRunnerInUse)
	bob.Delete("/api/v1/runners/"+runner.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	ctx := context.Background()
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.DeleteContainerAgent(ctx, agentID)
	}))
	alice.Delete("/api/v1/runners/" + runner.ID).RequireStatus(http.StatusNoContent)
	require.Empty(t, runnerList(t, alice))

	// The live connection is closed and the token is revoked: the runner cannot re-register.
	_, err := conn.TryCommand(5 * time.Second)
	require.Error(t, err)
	runnerClient.Post("/api/v1/runner/register", dto.RegisterRunnerRequest{Machine: "devbox"}).
		RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)

	entries := testkit.Decode[dto.Page[dto.AuditEntry]](admin.Get("/api/v1/admin/audit").RequireStatus(http.StatusOK)).Items
	found := false
	for _, entry := range entries {
		if entry.Action == runners.ActionDelete {
			found = true
			require.Equal(t, runner.ID, entry.TargetID)
			require.Equal(t, runners.TargetRunner, entry.TargetKind)
		}
	}
	require.True(t, found, "the delete is audited")
}
