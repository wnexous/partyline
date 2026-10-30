package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/containeragents"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/runners/spec"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// fakeRunner answers a runner's commands and records every one of them.
type fakeRunner struct {
	t    *testing.T
	cmds chan spec.Command

	mu    sync.Mutex
	reply func(spec.Command) *spec.Result
}

// fake answers the runner's commands with reply (OK when nil) and records every command.
func fake(t *testing.T, conn *testkit.RunnerConn, reply func(spec.Command) *spec.Result) *fakeRunner {
	t.Helper()
	f := &fakeRunner{t: t, cmds: make(chan spec.Command, 32), reply: reply}
	conn.Handle(func(cmd spec.Command) *spec.Result {
		f.cmds <- cmd
		f.mu.Lock()
		reply := f.reply
		f.mu.Unlock()
		if reply == nil {
			return &spec.Result{OK: true}
		}
		return reply(cmd)
	})
	return f
}

// setReply changes the answer of the next commands; nil means OK.
func (f *fakeRunner) setReply(reply func(spec.Command) *spec.Result) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reply = reply
}

// next returns the next command the runner received.
func (f *fakeRunner) next() spec.Command {
	f.t.Helper()
	select {
	case cmd := <-f.cmds:
		return cmd
	case <-time.After(5 * time.Second):
		f.t.Fatal("the runner received no command")
		return spec.Command{}
	}
}

// onlineRunner registers a runner of owner, connects it, and waits until the server sees it online.
// It returns the runner and the client carrying its runner token, for reconnecting it.
func onlineRunner(t *testing.T, owner *testkit.Client, machine string) (dto.Runner, *testkit.Client, *testkit.RunnerConn) {
	t.Helper()
	client := owner.RunnerToken(machine + " (runner)")
	conn, runner := client.DialRunner(machine, spec.Hello{DockerVersion: "27.5.1"})
	waitOnline(t, owner, runner.ID)
	return runner, client, conn
}

// waitOnline waits until the owner's runner with id is online.
func waitOnline(t *testing.T, owner *testkit.Client, id string) {
	t.Helper()
	require.Eventually(t, func() bool {
		for _, r := range runnerList(t, owner) {
			if r.ID == id {
				return r.Online
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "the runner is online")
}

// putSecret stores a value under name.
func putSecret(t *testing.T, owner *testkit.Client, name, value string) {
	t.Helper()
	owner.Put("/api/v1/secrets/"+name, dto.PutSecretRequest{Value: value}).RequireStatus(http.StatusNoContent)
}

// createContainer posts a create request and returns the container the API answered with.
func createContainer(t *testing.T, owner *testkit.Client, req dto.CreateContainerAgentRequest) dto.ContainerAgent {
	t.Helper()
	return testkit.Decode[dto.ContainerAgent](owner.Post("/api/v1/container-agents", req).
		RequireStatus(http.StatusCreated))
}

// CA-04, CA-05, CA-08: creating a container AI creates its identity and agent token, mounts a
// workspace volume, passes the Partyline variables and the mapped secrets to the runner's run
// command, and stores neither the token nor a secret value outside the sealed rows; the audit log
// records the create without them.
func TestCreateContainerAgentRunsTheImageWithSecrets(t *testing.T) {
	env := testkit.New(t, testkit.WithVersion("v1.2.3"))
	admin := env.SetupAdmin()
	alice := env.NewUser("alice")
	runner, _, conn := onlineRunner(t, alice, "devbox")
	f := fake(t, conn, nil)
	const secretValue = "sk-live-secret-123"
	putSecret(t, alice, "OPENCODE_KEY", secretValue)

	created := createContainer(t, alice, dto.CreateContainerAgentRequest{
		RunnerID: runner.ID, Harness: "opencode", DisplayName: "Atlas", Handle: "atlas",
		RepoURL: "https://github.com/example/repo", RepoRef: "dev",
		Env:      []dto.ContainerEnvVar{{Var: "OPENCODE_API_KEY", SecretName: "OPENCODE_KEY"}},
		CPUs:     1.5,
		MemoryMB: 512,
	})
	require.NotEmpty(t, created.Agent.ID)
	require.Equal(t, dto.IntelligenceKindAI, created.Agent.Kind)
	require.Equal(t, "Atlas", created.Agent.DisplayName)
	require.Equal(t, "atlas", created.Agent.Handle)
	require.Equal(t, runner.ID, created.RunnerID)
	require.Equal(t, "opencode", created.Harness)
	require.Equal(t, "ghcr.io/wnexous/partyline-agent:1.2.3", created.Image, "a release build runs the matching image")
	require.Regexp(t, `^pl-[0-9a-f]{12}$`, created.ContainerName, "the container name is the short agent id")
	require.Equal(t, created.ContainerName+"-ws", created.Volume, "the workspace is the volume (CA-05)")
	require.Equal(t, "https://github.com/example/repo", created.RepoURL)
	require.Equal(t, "dev", created.RepoRef)
	require.Equal(t, 1.5, created.CPUs)
	require.EqualValues(t, 512, created.MemoryMB)
	require.Equal(t, containeragents.DesiredRunning, created.Desired)
	require.Equal(t, string(spec.StateUnknown), created.State)
	require.Empty(t, created.LastError)
	require.Equal(t, []dto.ContainerEnvVar{{Var: "OPENCODE_API_KEY", SecretName: "OPENCODE_KEY"}}, created.Env)

	// What the runner was asked to run: the agent image, the volume, the limits, the label that
	// ties the container to the AI, and the environment.
	cmd := f.next()
	require.Equal(t, spec.OpRun, cmd.Op)
	require.NotNil(t, cmd.Run)
	run := cmd.Run
	require.Equal(t, created.ContainerName, run.Name)
	require.Equal(t, created.Image, run.Image)
	require.Equal(t, created.Volume, run.Volume)
	require.Equal(t, 1.5, run.CPUs)
	require.EqualValues(t, 512, run.MemoryMB)
	require.Equal(t, created.Agent.ID, run.Labels["partyline.agent"])
	require.Equal(t, env.URL, run.Env["PARTYLINE_URL"])
	require.Equal(t, "opencode", run.Env["PARTYLINE_HARNESS"])
	require.Equal(t, "https://github.com/example/repo", run.Env["PARTYLINE_REPO"])
	require.Equal(t, "dev", run.Env["PARTYLINE_REPO_REF"])
	require.Equal(t, secretValue, run.Env["OPENCODE_API_KEY"], "the mapped secret reaches the container")
	require.Regexp(t, `^pl_agent_[0-9A-Za-z]{43}$`, run.Env["PARTYLINE_TOKEN"])
	token := run.Env["PARTYLINE_TOKEN"]
	require.Len(t, run.Env, 6, "no other variable is passed")

	// The AI has the machine and folder of a container and the app of the harness (CA-04).
	ctx := context.Background()
	agentRow, err := env.App.Store.R().GetAgent(ctx, created.Agent.ID)
	require.NoError(t, err)
	require.Equal(t, agents.ContainerMachine(created.ContainerName), agentRow.Machine)
	require.Equal(t, "/workspace/repo", agentRow.Folder)
	require.Equal(t, "opencode", agentRow.Harness)
	require.True(t, agentRow.OrgID.Valid, "the AI joined the owner's default organization")

	// The row keeps the secret ids, never a value (CA-08).
	row := storedContainer(t, env, created.Agent.ID)
	require.NotContains(t, row.Env, secretValue)
	require.NotContains(t, row.Env, token)
	require.NotContains(t, row.Env, "OPENCODE_KEY", "the row stores the secret id, not its name")
	require.True(t, row.TokenID.Valid, "the AI's agent token is recorded")
	names, err := env.App.Store.R().ListOwnerSecretNames(ctx, alice.User.ID)
	require.NoError(t, err)
	require.Len(t, names, 1)
	require.Contains(t, row.Env, names[0].ID)

	// The token is an agent token of the owner, listed without its secret.
	var tokenFound bool
	for _, tok := range tokensOf(t, alice) {
		if tok.ID == row.TokenID.String {
			tokenFound = true
			require.Equal(t, dto.TokenKindAgent, tok.Kind)
			require.Equal(t, token[:12], tok.Prefix)
		}
	}
	require.True(t, tokenFound, "the AI's token is one of the owner's")

	// The audit log records the create and carries neither the secret value nor the token (CA-08).
	page := auditPage(admin, "")
	raw, err := json.Marshal(page)
	require.NoError(t, err)
	require.NotContains(t, string(raw), secretValue, "an audit entry never carries a secret value")
	require.NotContains(t, string(raw), token, "an audit entry never carries a token")
	var auditFound bool
	for _, entry := range page.Items {
		if entry.Action == containeragents.ActionCreate {
			auditFound = true
			require.Equal(t, created.Agent.ID, entry.TargetID)
			require.NotContains(t, entry.Detail, "env")
			require.NotContains(t, entry.Detail, "token")
		}
	}
	require.True(t, auditFound, "the create is audited")

	// The state the runner reports becomes the container's state (CA-06).
	conn.State(spec.Container{Name: created.ContainerName, State: spec.StateRunning})
	require.Eventually(t, func() bool {
		return testkit.Decode[dto.ContainerAgent](alice.Get("/api/v1/agents/"+created.Agent.ID+"/container").
			RequireStatus(http.StatusOK)).State == string(spec.StateRunning)
	}, 5*time.Second, 10*time.Millisecond)

	// A create without a repository uses the workspace folder and passes no repository.
	second := createContainer(t, alice, dto.CreateContainerAgentRequest{
		RunnerID: runner.ID, Harness: "pi", DisplayName: "Nova", Handle: "nova",
	})
	require.Empty(t, second.RepoURL)
	require.Empty(t, second.RepoRef)
	run2 := f.next()
	require.NotContains(t, run2.Run.Env, "PARTYLINE_REPO")
	require.NotContains(t, run2.Run.Env, "PARTYLINE_REPO_REF")
	withoutRepo, err := env.App.Store.R().GetAgent(ctx, second.Agent.ID)
	require.NoError(t, err)
	require.Equal(t, "/workspace", withoutRepo.Folder)
}

// CA-04, CA-07: a runner that is not connected is 409 runner_offline, another owner's runner is 404,
// and every invalid field is a 422 or 409 that names it; nothing is created in those cases.
func TestCreateContainerAgentRefusals(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	offline := registerRunner(t, alice.RunnerToken("offline"), "box-offline")
	valid := dto.CreateContainerAgentRequest{
		RunnerID: offline.ID, Harness: "claude", DisplayName: "Atlas", Handle: "nova", CPUs: 1, MemoryMB: 512,
	}
	alice.Post("/api/v1/container-agents", valid).RequireError(http.StatusConflict, dto.ErrorCodeRunnerOffline)
	bob.Post("/api/v1/container-agents", valid).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	runner, _, conn := onlineRunner(t, alice, "devbox")
	_ = fake(t, conn, nil)
	valid.RunnerID = runner.ID
	putSecret(t, alice, "OPENCODE_KEY", "sk-value")
	// An identity with the handle the "taken" case asks for.
	alice.NewAI("atlas")

	cases := []struct {
		name   string
		edit   func(*dto.CreateContainerAgentRequest)
		field  string
		code   dto.ErrorCode
		status int
	}{
		{"harness", func(r *dto.CreateContainerAgentRequest) { r.Harness = "codex" }, "harness", dto.ErrorCodeInvalid, http.StatusUnprocessableEntity},
		{"handle", func(r *dto.CreateContainerAgentRequest) { r.Handle = "Bad Handle" }, "handle", dto.ErrorCodeInvalid, http.StatusUnprocessableEntity},
		{"handle taken", func(r *dto.CreateContainerAgentRequest) { r.Handle = "atlas" }, "", dto.ErrorCodeHandleTaken, http.StatusConflict},
		{"display name", func(r *dto.CreateContainerAgentRequest) { r.DisplayName = "   " }, "display_name", dto.ErrorCodeInvalid, http.StatusUnprocessableEntity},
		{"repo url", func(r *dto.CreateContainerAgentRequest) { r.RepoURL = "http://example.com/repo" }, "repo_url", dto.ErrorCodeInvalid, http.StatusUnprocessableEntity},
		{"repo ref without repo", func(r *dto.CreateContainerAgentRequest) { r.RepoRef = "main" }, "repo_url", dto.ErrorCodeInvalid, http.StatusUnprocessableEntity},
		{"repo ref", func(r *dto.CreateContainerAgentRequest) {
			r.RepoURL = "https://example.com/repo"
			r.RepoRef = ".."
		}, "repo_ref", dto.ErrorCodeInvalid, http.StatusUnprocessableEntity},
		{"cpus", func(r *dto.CreateContainerAgentRequest) { r.CPUs = 100 }, "cpus", dto.ErrorCodeInvalid, http.StatusUnprocessableEntity},
		{"memory", func(r *dto.CreateContainerAgentRequest) { r.MemoryMB = 1 }, "memory_mb", dto.ErrorCodeInvalid, http.StatusUnprocessableEntity},
		{"env var", func(r *dto.CreateContainerAgentRequest) {
			r.Env = []dto.ContainerEnvVar{{Var: "lower", SecretName: "OPENCODE_KEY"}}
		}, "env[0].var", dto.ErrorCodeInvalid, http.StatusUnprocessableEntity},
		{"reserved env var", func(r *dto.CreateContainerAgentRequest) {
			r.Env = []dto.ContainerEnvVar{{Var: "PARTYLINE_TOKEN", SecretName: "OPENCODE_KEY"}}
		}, "env[0].var", dto.ErrorCodeInvalid, http.StatusUnprocessableEntity},
		{"duplicate env var", func(r *dto.CreateContainerAgentRequest) {
			r.Env = []dto.ContainerEnvVar{
				{Var: "OPENCODE_API_KEY", SecretName: "OPENCODE_KEY"},
				{Var: "OPENCODE_API_KEY", SecretName: "OPENCODE_KEY"},
			}
		}, "env[1].var", dto.ErrorCodeInvalid, http.StatusUnprocessableEntity},
		{"missing secret", func(r *dto.CreateContainerAgentRequest) {
			r.Env = []dto.ContainerEnvVar{{Var: "OPENCODE_API_KEY", SecretName: "NO_SUCH_KEY"}}
		}, "env[0].secret_name", dto.ErrorCodeInvalid, http.StatusUnprocessableEntity},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := valid
			c.edit(&req)
			resp := alice.Post("/api/v1/container-agents", req)
			e := resp.RequireError(c.status, c.code)
			if c.field != "" {
				require.Contains(t, e.Fields, c.field)
			}
			require.NotContains(t, string(resp.Body), "sk-value", "an error never carries a secret value")
		})
	}
	rows, err := env.App.Store.R().ListContainerAgentsByRunner(context.Background(), runner.ID)
	require.NoError(t, err)
	require.Empty(t, rows, "no refusal created a container")
}

// CA-06, CA-07: only whoever manages the AI sees and operates its container; start, stop, restart,
// logs and delete reach the runner with the container's name; logs are capped; delete revokes the
// AI's token and deletes the AI.
func TestContainerLifecycleAuthzAndDelete(t *testing.T) {
	env := testkit.New(t)
	admin := env.SetupAdmin()
	alice := env.NewUser("alice")
	roles := env.OrgRoles("acme")
	org := roles.Org
	// Alice runs a runner and administers the organization her container AI joins; the chart then
	// decides who else manages it.
	env.AddMember(org.ID, alice.User.ID, orgs.PermissionAdmin, roles.AdminPosition)
	require.NoError(t, env.App.Orgs.SetDefault(context.Background(), alice.User.ID, org.ID))
	runner, _, conn := onlineRunner(t, alice, "devbox")
	f := fake(t, conn, nil)
	created := createContainer(t, alice, dto.CreateContainerAgentRequest{
		RunnerID: runner.ID, Harness: "claude", DisplayName: "Atlas", Handle: "atlas",
	})
	f.next()
	// The container AI holds the worker position, so the manager above it manages it as the chart
	// says (POS-03, CA-07).
	require.NoError(t, env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
		return q.UpdateOrgMemberPosition(context.Background(), db.UpdateOrgMemberPositionParams{
			PositionID: sql.NullString{String: roles.WorkerPosition, Valid: true}, OrgID: org.ID, Iid: created.Agent.ID,
		})
	}))

	containerPath := "/api/v1/agents/" + created.Agent.ID + "/container"
	// A stranger gets 404 (CA-07): the AI is not visible to them.
	roles.Outsider.Get(containerPath).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	// A member of the AI's organization sees it but does not manage it: 403.
	roles.Member.Get(containerPath).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	roles.Member.Post(containerPath, dto.ContainerActionRequest{Action: containeragents.RequestStop}).
		RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	// Whoever manages it in the organization, here the manager above its position, may operate it.
	testkit.Decode[dto.ContainerAgent](roles.Manager.Get(containerPath).RequireStatus(http.StatusOK))

	// Start, restart and stop reach the runner and update the state and the desired state.
	started := testkit.Decode[dto.ContainerAgent](alice.Post(containerPath,
		dto.ContainerActionRequest{Action: containeragents.RequestStart}).RequireStatus(http.StatusOK))
	require.Equal(t, containeragents.DesiredRunning, started.Desired)
	require.Equal(t, string(spec.StateRunning), started.State)
	require.Equal(t, spec.OpStart, f.next().Op)

	restarted := testkit.Decode[dto.ContainerAgent](alice.Post(containerPath,
		dto.ContainerActionRequest{Action: containeragents.RequestRestart}).RequireStatus(http.StatusOK))
	require.Equal(t, string(spec.StateRunning), restarted.State)
	cmd := f.next()
	require.Equal(t, spec.OpRestart, cmd.Op)
	require.Equal(t, created.ContainerName, cmd.Name)

	stopped := testkit.Decode[dto.ContainerAgent](alice.Post(containerPath,
		dto.ContainerActionRequest{Action: containeragents.RequestStop}).RequireStatus(http.StatusOK))
	require.Equal(t, containeragents.DesiredStopped, stopped.Desired)
	require.Equal(t, string(spec.StateStopped), stopped.State)
	require.Equal(t, spec.OpStop, f.next().Op)

	// An action that is not start, stop or restart and a negative tail are invalid.
	alice.Post(containerPath, dto.ContainerActionRequest{Action: "boom"}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	alice.Get(containerPath+"/logs?tail=-1").
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)

	// Logs: the tail is capped at 500 and the answer carries the runner's lines; the read is audited.
	f.setReply(func(cmd spec.Command) *spec.Result {
		if cmd.Op == spec.OpLogs {
			return &spec.Result{OK: true, Logs: "line one\nline two\n"}
		}
		return &spec.Result{OK: true}
	})
	logs := testkit.Decode[dto.ContainerLogs](alice.Get(containerPath + "/logs?tail=5000").
		RequireStatus(http.StatusOK))
	require.Equal(t, "line one\nline two\n", logs.Logs)
	logCmd := f.next()
	require.Equal(t, spec.OpLogs, logCmd.Op)
	require.Equal(t, 500, logCmd.Tail)
	require.Equal(t, created.ContainerName, logCmd.Name)
	var sawLogs bool
	for _, entry := range auditPage(admin, "").Items {
		if entry.Action == containeragents.ActionLogs {
			sawLogs = true
		}
	}
	require.True(t, sawLogs, "reading the logs is audited")

	// Delete removes the container, revokes the AI's token and deletes the AI (the caller is an
	// admin of its home organization).
	row := storedContainer(t, env, created.Agent.ID)
	alice.Delete(containerPath + "?volume=true").RequireStatus(http.StatusNoContent)
	remove := f.next()
	require.Equal(t, spec.OpRemove, remove.Op)
	require.True(t, remove.RemoveVolume)
	require.Equal(t, created.ContainerName, remove.Name)
	alice.Get(containerPath).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	_, err := env.App.Store.R().GetContainerAgent(context.Background(), created.Agent.ID)
	require.ErrorIs(t, err, sql.ErrNoRows)
	_, err = env.App.Store.R().GetAgent(context.Background(), created.Agent.ID)
	require.ErrorIs(t, err, sql.ErrNoRows, "the AI went with its container")
	for _, tok := range tokensOf(t, alice) {
		if tok.ID == row.TokenID.String {
			require.NotNil(t, tok.RevokedAt, "the AI's token is revoked")
		}
	}
}

// CA-06: deleting without the volume leaves it; a runner that is offline keeps the row, so the
// container is never forgotten while it may still run.
func TestContainerDeleteVolumeAndOffline(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	runner, client, conn := onlineRunner(t, alice, "devbox")
	f := fake(t, conn, nil)
	created := createContainer(t, alice, dto.CreateContainerAgentRequest{
		RunnerID: runner.ID, Harness: "omp", DisplayName: "Nova", Handle: "nova",
	})
	f.next()

	// Offline: the delete is refused and the row stays, so the container is not left unmanaged.
	conn.Close()
	require.Eventually(t, func() bool {
		list := runnerList(t, alice)
		return len(list) == 1 && !list[0].Online
	}, 5*time.Second, 10*time.Millisecond)
	alice.Delete("/api/v1/agents/"+created.Agent.ID+"/container?volume=true").
		RequireError(http.StatusConflict, dto.ErrorCodeRunnerOffline)
	alice.Get("/api/v1/agents/" + created.Agent.ID + "/container").RequireStatus(http.StatusOK)

	// Back online, delete without the volume: the command says so and the row goes.
	conn = client.DialRunnerConn(spec.Hello{DockerVersion: "27.5.1"})
	waitOnline(t, alice, runner.ID)
	f = fake(t, conn, nil)
	alice.Delete("/api/v1/agents/" + created.Agent.ID + "/container?volume=false").RequireStatus(http.StatusNoContent)
	remove := f.next()
	require.Equal(t, spec.OpRemove, remove.Op)
	require.False(t, remove.RemoveVolume)
	_, err := env.App.Store.R().GetContainerAgent(context.Background(), created.Agent.ID)
	require.ErrorIs(t, err, sql.ErrNoRows)
}

// CA-06: a start on a container the runner no longer has creates it again with a fresh token; the
// old token is revoked.
func TestContainerStartRecreatesAMissingContainer(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	runner, _, conn := onlineRunner(t, alice, "devbox")
	f := fake(t, conn, nil)
	created := createContainer(t, alice, dto.CreateContainerAgentRequest{
		RunnerID: runner.ID, Harness: "opencode", DisplayName: "Atlas", Handle: "atlas",
	})
	f.next()
	before := storedContainer(t, env, created.Agent.ID)

	f.setReply(func(cmd spec.Command) *spec.Result {
		if cmd.Op == spec.OpStart {
			return &spec.Result{OK: false, Error: "No such container: " + created.ContainerName}
		}
		return &spec.Result{OK: true}
	})
	started := testkit.Decode[dto.ContainerAgent](alice.Post("/api/v1/agents/"+created.Agent.ID+"/container",
		dto.ContainerActionRequest{Action: containeragents.RequestStart}).RequireStatus(http.StatusOK))
	require.Equal(t, string(spec.StateRunning), started.State)
	require.Empty(t, started.LastError)
	require.Equal(t, spec.OpStart, f.next().Op)
	runCmd := f.next()
	require.Equal(t, spec.OpRun, runCmd.Op)
	require.NotNil(t, runCmd.Run)
	require.Regexp(t, `^pl_agent_[0-9A-Za-z]{43}$`, runCmd.Run.Env["PARTYLINE_TOKEN"])

	after := storedContainer(t, env, created.Agent.ID)
	require.NotEqual(t, before.TokenID.String, after.TokenID.String, "the recreated container got a fresh token")
	require.Equal(t, runCmd.Run.Env["PARTYLINE_TOKEN"][:12], tokenPrefix(t, alice, after.TokenID.String))
	for _, tok := range tokensOf(t, alice) {
		if tok.ID == before.TokenID.String {
			require.NotNil(t, tok.RevokedAt, "the token of the old container is revoked")
		}
	}
}

// CA-06, CA-08: a run the runner refuses is stored as last_error, keeps the row and the AI so the
// owner can retry start or delete, and the error carries no secret.
func TestContainerCreateRunFailureIsKept(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	runner, _, conn := onlineRunner(t, alice, "devbox")
	f := fake(t, conn, func(spec.Command) *spec.Result {
		return &spec.Result{OK: false, Error: "docker: no such image"}
	})
	putSecret(t, alice, "OPENCODE_KEY", "sk-value")
	resp := alice.Post("/api/v1/container-agents", dto.CreateContainerAgentRequest{
		RunnerID: runner.ID, Harness: "opencode", DisplayName: "Atlas", Handle: "atlas",
		Env: []dto.ContainerEnvVar{{Var: "OPENCODE_API_KEY", SecretName: "OPENCODE_KEY"}},
	})
	resp.RequireError(http.StatusBadGateway, dto.ErrorCodeUnavailable)
	require.NotContains(t, string(resp.Body), "sk-value")
	f.next()

	// The row and the AI stay, with the failure in last_error.
	rows, err := env.App.Store.R().ListContainerAgentsByRunner(context.Background(), runner.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "the row is kept for a retry")
	require.True(t, rows[0].LastError.Valid)
	require.Contains(t, rows[0].LastError.String, "no such image")
	require.NotContains(t, rows[0].LastError.String, "sk-value")

	// A retry with start creates it: the fake now succeeds.
	f.setReply(nil)
	started := testkit.Decode[dto.ContainerAgent](alice.Post("/api/v1/agents/"+rows[0].AgentID+"/container",
		dto.ContainerActionRequest{Action: containeragents.RequestStart}).RequireStatus(http.StatusOK))
	require.Empty(t, started.LastError)
	require.Equal(t, string(spec.StateRunning), started.State)
}

// CA-06: deleting the account removes the containers of the user's runners first (the runner rows
// would otherwise refuse the deletion), best effort when the runner is offline.
func TestAccountDeletionRemovesContainers(t *testing.T) {
	env := testkit.New(t)
	env.SetupAdmin()
	alice := env.NewUser("alice")
	runner, _, conn := onlineRunner(t, alice, "devbox")
	f := fake(t, conn, nil)
	createContainer(t, alice, dto.CreateContainerAgentRequest{
		RunnerID: runner.ID, Harness: "pi", DisplayName: "Atlas", Handle: "atlas",
	})
	f.next()

	alice.Do(http.MethodDelete, "/api/v1/me", dto.DeleteAccountRequest{Password: testkit.Password}).
		RequireStatus(http.StatusNoContent)
	remove := f.next()
	require.Equal(t, spec.OpRemove, remove.Op)
	require.True(t, remove.RemoveVolume, "the workspace of a deleted account is removed too")

	rows, err := env.App.Store.R().ListContainerAgentsByOwner(context.Background(), alice.User.ID)
	require.NoError(t, err)
	require.Empty(t, rows)
	runners, err := env.App.Store.R().ListRunners(context.Background(), alice.User.ID)
	require.NoError(t, err)
	require.Empty(t, runners)

	// An offline runner does not stop the deletion: the row goes, the container cannot be removed
	// and is logged, and the account is still deleted.
	bob := env.NewUser("bob")
	offline := registerRunner(t, bob.RunnerToken("box"), "box")
	seedContainer(t, env, bob, offline.ID, "atlas2")
	bob.Do(http.MethodDelete, "/api/v1/me", dto.DeleteAccountRequest{Password: testkit.Password}).
		RequireStatus(http.StatusNoContent)
	rows, err = env.App.Store.R().ListContainerAgentsByOwner(context.Background(), bob.User.ID)
	require.NoError(t, err)
	require.Empty(t, rows)
}

// tokensOf returns the owner's API tokens.
func tokensOf(t *testing.T, owner *testkit.Client) []dto.Token {
	t.Helper()
	return testkit.Decode[dto.List[dto.Token]](owner.Get("/api/v1/tokens").RequireStatus(http.StatusOK)).Items
}

// tokenPrefix returns the display prefix of the owner's token id.
func tokenPrefix(t *testing.T, owner *testkit.Client, id string) string {
	t.Helper()
	for _, tok := range tokensOf(t, owner) {
		if tok.ID == id {
			return tok.Prefix
		}
	}
	t.Fatalf("token %s not found", id)
	return ""
}

// CA-04: two container AIs on one runner without a repository are two AIs with their own identity:
// a shared machine (the runner) and folder (the workspace) would make the second create collide
// with the first and answer the generic 500 instead of creating it.
func TestCreateTwoContainerAgentsWithoutRepo(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	runner, _, conn := onlineRunner(t, alice, "devbox")
	f := fake(t, conn, nil)
	putSecret(t, alice, "OPENCODE_KEY", "sk-value")
	request := func(name, handle string) dto.CreateContainerAgentRequest {
		return dto.CreateContainerAgentRequest{
			RunnerID: runner.ID, Harness: "opencode", DisplayName: name, Handle: handle,
			Env: []dto.ContainerEnvVar{{Var: "OPENCODE_API_KEY", SecretName: "OPENCODE_KEY"}},
		}
	}
	first := createContainer(t, alice, request("Atlas", "atlas"))
	f.next()
	second := createContainer(t, alice, request("Nova", "nova"))
	f.next()
	require.NotEqual(t, first.Agent.ID, second.Agent.ID)

	ctx := context.Background()
	one, err := env.App.Store.R().GetAgent(ctx, first.Agent.ID)
	require.NoError(t, err)
	two, err := env.App.Store.R().GetAgent(ctx, second.Agent.ID)
	require.NoError(t, err)
	require.Equal(t, agents.ContainerRootFolder, one.Folder, "a container without a repository works in the root workspace")
	require.Equal(t, one.Folder, two.Folder)
	require.NotEqual(t, one.Machine, two.Machine, "each container AI has its own machine")
	require.Equal(t, agents.ContainerMachine(first.ContainerName), one.Machine, "the machine is the container's own name")
}
