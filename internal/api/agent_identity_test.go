package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// listedAgent returns the agent with id from GET /agents of c.
func listedAgent(t *testing.T, c *testkit.Client, id string) dto.Agent {
	t.Helper()
	list := testkit.Decode[dto.List[dto.Agent]](c.Get("/api/v1/agents").RequireStatus(http.StatusOK))
	for _, a := range list.Items {
		if a.ID == id {
			return a
		}
	}
	require.Failf(t, "agent not listed", "%s", id)
	return dto.Agent{}
}

// AID-01, ID-04: every AI is listed with the name people read (its profile's), its app and
// machine; the legacy fields are constant: fixed, not archived, no project (until a v0.3
// project is put on it).
func TestAgentListCarriesIdentity(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	id := env.NewAI(alice, "builder")

	a := listedAgent(t, alice, id)
	require.Nil(t, a.Project)
	require.Equal(t, []any{"builder", "claude", testkit.Machine, dto.AgentKindFixed, false},
		[]any{a.DisplayName, a.Harness, a.Machine, a.Kind, a.Archived})

	projectID := createProject(t, env, alice, "proj", "Proj")
	putInProject(t, env, id, projectID)
	a = listedAgent(t, alice, id)
	require.Equal(t, &dto.ProjectRef{ID: projectID, Slug: "proj", Name: "Proj"}, a.Project)
}

// AID-02: the harness and machine a session sends are cleaned: harness aliases map to their id
// and unknown harnesses to "agent"; the machine loses control characters and is cut to 64
// characters.
func TestAgentWSIdentityHeadersAreCleaned(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	dial := func(f testkit.Folder) dto.AgentInfo {
		t.Helper()
		conn, resp := tok.DialFolder(f)
		require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
		return conn.Welcome(wsWait).Agent
	}

	alias := dial(testkit.Folder{Machine: "  my\tlaptop  ", Path: "/work/alias", Harness: "Claude-Code"})
	a := listedAgent(t, alice, alias.ID)
	require.Equal(t, []string{"claude", "mylaptop"}, []string{a.Harness, a.Machine})

	unknown := dial(testkit.Folder{Machine: strings.Repeat("x", 70), Path: "/work/unknown", Harness: "vim"})
	a = listedAgent(t, alice, unknown.ID)
	require.Equal(t, []string{"agent", strings.Repeat("x", 64)}, []string{a.Harness, a.Machine})
}

// AID-02: the WebSocket stamps the AI's connect time; POST /agent/register resolves the same AI
// by folder and leaves it offline without a connect time.
func TestAgentConnectTimeAndRegisterIdentity(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	ctx := context.Background()

	reg := testkit.Decode[dto.AgentInfo](tok.AsAgent("tools").Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK))
	r, err := env.App.Store.R().GetAgent(ctx, reg.ID)
	require.NoError(t, err)
	require.False(t, r.ConnectedAt.Valid, "registering does not connect")
	require.False(t, listedAgent(t, alice, reg.ID).Online)

	conn, resp := tok.DialAgent("tools")
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	require.Equal(t, reg.ID, conn.Welcome(wsWait).Agent.ID, "the same folder is the same AI")
	a, err := env.App.Store.R().GetAgent(ctx, reg.ID)
	require.NoError(t, err)
	require.True(t, a.ConnectedAt.Valid, "connect time stamped")
}
