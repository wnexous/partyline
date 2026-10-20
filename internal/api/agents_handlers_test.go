package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// registerAgent creates (or returns) the owner's agent as its first WebSocket connection would.
func registerAgent(t *testing.T, env *testkit.Env, owner *testkit.Client, name string) string {
	t.Helper()
	a, err := env.App.Agents.Upsert(context.Background(), owner.User.ID, name)
	require.NoError(t, err)
	return a.ID
}

func shareAgent(c *testkit.Client, agentID string, userIDs ...string) {
	c.T.Helper()
	users := make([]dto.SharingUser, len(userIDs))
	for i, id := range userIDs {
		users[i] = dto.SharingUser{UserID: id, Level: dto.LevelUse}
	}
	c.Put("/api/v1/agents/"+agentID+"/sharing", dto.Sharing{Mode: dto.VisibilityShared, Users: users}).RequireStatus(http.StatusOK)
}

// agentSetup creates an agent of the owner shared with the viewer (use grant).
func agentSetup(t *testing.T, m *matrix) matrixResource {
	owner := m.as(t, roleOwner)
	id := registerAgent(t, m.env, owner, "builder")
	shareAgent(owner, id, m.clients[roleViewer].User.ID)
	return matrixResource{"agent": id}
}

// Auth matrix for the agent management routes (ACL-05, ACL-07, ACL-08): viewer is the use grantee.
func TestAgentRoutesAuthMatrix(t *testing.T) {
	m := newMatrix(t)
	agent := func(res matrixResource, suffix string) string { return "/api/v1/agents/" + res["agent"] + suffix }
	desc := "Builds the project"
	routes := []matrixRoute{
		{"update agent", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Patch(agent(res, ""), dto.UpdateAgentRequest{Description: &desc})
		}, statuses(200, 403, 404, 404, 404)},
		{"delete agent", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Delete(agent(res, ""))
		}, statuses(204, 403, 404, 404, 404)},
		{"get sharing", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Get(agent(res, "/sharing"))
		}, statuses(200, 200, 404, 404, 404)},
		{"put sharing", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Put(agent(res, "/sharing"), dto.Sharing{Mode: dto.VisibilityPrivate})
		}, statuses(200, 403, 404, 404, 404)},
	}
	m.run(t, agentSetup, routes)

	// Listing: everyone gets 200, only owner and grantee see the agent, with their level.
	res := agentSetup(t, m)
	wantLevel := map[matrixRole]dto.Level{roleOwner: dto.LevelManage, roleViewer: dto.LevelUse}
	for _, role := range matrixRoles {
		list := testkit.Decode[dto.List[dto.Agent]](m.as(t, role).Get("/api/v1/agents").RequireStatus(http.StatusOK))
		var levels []dto.Level
		for _, a := range list.Items {
			if a.ID == res["agent"] {
				levels = append(levels, a.MyLevel)
			}
		}
		if want, ok := wantLevel[role]; ok {
			require.Equal(t, []dto.Level{want}, levels, string(role))
		} else {
			require.Empty(t, levels, string(role))
		}
	}
}

// GET /agents renders the agent; PATCH changes the description; DELETE removes it from rooms.
func TestAgentEndpoints(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	id := registerAgent(t, env, alice, "Billing API!")

	list := testkit.Decode[dto.List[dto.Agent]](alice.Get("/api/v1/agents").RequireStatus(http.StatusOK))
	require.Len(t, list.Items, 1)
	got := list.Items[0]
	require.Equal(t, id, got.ID)
	require.Equal(t, "billing-api", got.Name)
	require.Equal(t, dto.User{ID: alice.User.ID, Username: "alice", DisplayName: alice.User.DisplayName}, got.Owner)
	require.Equal(t, dto.LevelManage, got.MyLevel)
	require.False(t, got.Online)

	desc := "Answers billing questions"
	updated := testkit.Decode[dto.Agent](alice.Patch("/api/v1/agents/"+id, dto.UpdateAgentRequest{Description: &desc}).RequireStatus(http.StatusOK))
	require.Equal(t, desc, updated.Description)

	room := createRoom(alice, "Support")
	alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: id}).RequireStatus(http.StatusCreated)
	alice.Delete("/api/v1/agents/" + id).RequireStatus(http.StatusNoContent)
	members := testkit.Decode[dto.List[dto.RoomMember]](alice.Get("/api/v1/rooms/" + room.ID + "/members").RequireStatus(http.StatusOK))
	require.Empty(t, members.Items)
	require.Empty(t, testkit.Decode[dto.List[dto.Agent]](alice.Get("/api/v1/agents").RequireStatus(http.StatusOK)).Items)
}
