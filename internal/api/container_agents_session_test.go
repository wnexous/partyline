package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// agentList is every AI alice hosts, as the agents list shows them.
func agentList(t *testing.T, owner *testkit.Client) []dto.Agent {
	t.Helper()
	return testkit.Decode[dto.List[dto.Agent]](owner.Get("/api/v1/agents").RequireStatus(http.StatusOK)).Items
}

// agentByID waits until alice's agent list holds id and returns it.
func agentByID(t *testing.T, owner *testkit.Client, id string) dto.Agent {
	t.Helper()
	for _, a := range agentList(t, owner) {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("AI %s is not listed", id)
	return dto.Agent{}
}

// CA-04: the session inside a container IS the AI that was created for it. The runner hands the
// container the AI's own machine and folder (PARTYLINE_MACHINE, PARTYLINE_FOLDER); the CLI in the
// container sends exactly those as its identity headers, so the session resolves to the created AI
// instead of one named after the container's hostname (the prod bug), no second AI is created, and
// the AI shows online while that session is connected.
func TestContainerSessionResolvesToTheCreatedAI(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	runner, _, conn := onlineRunner(t, alice, "devbox")
	f := fake(t, conn, nil)
	putSecret(t, alice, "OPENCODE_KEY", "sk-value")

	created := createContainer(t, alice, dto.CreateContainerAgentRequest{
		RunnerID: runner.ID, Harness: "opencode", DisplayName: "Atlas", Handle: "atlas",
		RepoURL: "https://github.com/example/repo", RepoRef: "main",
		Env: []dto.ContainerEnvVar{{Var: "OPENCODE_API_KEY", SecretName: "OPENCODE_KEY"}},
	})
	cmd := f.next()
	require.Equal(t, created.Agent.ID, cmd.Run.Labels["partyline.agent"])
	machine, folder := cmd.Run.Env["PARTYLINE_MACHINE"], cmd.Run.Env["PARTYLINE_FOLDER"]
	token := cmd.Run.Env["PARTYLINE_TOKEN"]
	require.Equal(t, agents.ContainerMachine(created.ContainerName), machine)
	require.Equal(t, agents.ContainerRepoFolder, folder)

	before := len(agentList(t, alice))

	// What the CLI in the container sends with PARTYLINE_MACHINE and PARTYLINE_FOLDER set: the two
	// values the runner passed, with the AI's own token.
	session := alice.WithBearer(token)
	agentConn, resp := session.DialFolder(testkit.Folder{Machine: machine, Path: folder, Harness: "opencode"})
	require.NotNil(t, agentConn, "the session was refused: %d %s", resp.Status, resp.Body)
	defer agentConn.Close()
	welcome := agentConn.Welcome(5 * time.Second)
	require.Equal(t, created.Agent.ID, welcome.Agent.ID, "the container session is the created AI")

	require.Eventually(t, func() bool {
		return agentByID(t, alice, created.Agent.ID).Online
	}, 5*time.Second, 10*time.Millisecond, "the AI is online while its container session is connected")

	require.Len(t, agentList(t, alice), before, "the session created no second AI")
	for _, a := range agentList(t, alice) {
		require.NotEqual(t, "f38b3aaa1710", a.Machine, "no AI is named after the container's hostname")
	}
	// The AI keeps the identity the container was created with, and the container keeps its row.
	row := agentByID(t, alice, created.Agent.ID)
	require.Equal(t, machine, row.Machine)
	require.Equal(t, folder, row.Folder)
	require.Equal(t, "opencode", row.Harness)

	// Once the session ends the AI is offline again: the presence is the container session's.
	agentConn.Close()
	require.Eventually(t, func() bool {
		return !agentByID(t, alice, created.Agent.ID).Online
	}, 5*time.Second, 10*time.Millisecond, "the AI is offline when the container session ends")
}
