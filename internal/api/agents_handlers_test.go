package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// registerAgent creates (or returns) the owner's AI of the folder /work/<name>, named name, as
// its first session would.
func registerAgent(t *testing.T, env *testkit.Env, owner *testkit.Client, name string) string {
	t.Helper()
	return env.NewAI(owner, name)
}

func shareAgent(c *testkit.Client, agentID string, userIDs ...string) {
	c.T.Helper()
	users := make([]dto.SharingUser, len(userIDs))
	for i, id := range userIDs {
		users[i] = dto.SharingUser{UserID: id, Level: dto.LevelUse}
	}
	c.Put("/api/v1/agents/"+agentID+"/sharing", dto.Sharing{Mode: dto.VisibilityShared, Users: users}).RequireStatus(http.StatusOK)
}

// agentWant is the expected status of a route on the AI that the role "manager" created and hosts, by role:
// whoever manages it (its creator and the admin whose position is above the creator's), whoever only sees
// it (the other people of the organization, guests included) and whoever cannot see it (an outsider and an
// admin of the instance).
func agentWant(manages, sees, hidden int) map[string]int {
	return map[string]int{
		"admin": manages, "manager": manages,
		"target": sees, "peer": sees, "member": sees, "inviter": sees, "guest": sees,
		"outsider": hidden, "instance_admin": hidden,
	}
}

// Auth matrix for the agent management routes (ACL-05, ACL-07, ACL-08, POS-04, POS-05) over the
// organization roles (AD-016): an AI is seen by the people of the organizations it belongs to and managed
// by the identities above it in the chart; the others get 404. (Deleting an AI is covered with the
// deletion rule, in TestDeletingAnAIOfAnOrganization, merging with the merge tests.)
func TestAgentRoutesAuthMatrix(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	agent := func(res map[string]string, suffix string) string { return "/api/v1/agents/" + res["agent"] + suffix }
	desc := "Builds the project"
	setup := func(*testing.T) map[string]string { return map[string]string{"agent": roles.TargetAI} }
	runRoleRoutes(t, roles, setup, []roleRoute{
		{"get agent", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Get(agent(res, ""))
		}, agentWant(200, 200, 404)},
		{"update agent", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Patch(agent(res, ""), dto.UpdateAgentRequest{Description: &desc})
		}, agentWant(200, 403, 404)},
		{"get sharing", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Get(agent(res, "/sharing"))
		}, agentWant(200, 200, 404)},
		{"put sharing", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Put(agent(res, "/sharing"), dto.Sharing{Mode: dto.VisibilityPrivate})
		}, agentWant(200, 403, 404)},
	})

	// Listing: everyone gets 200, only the people of the organization see the AI, and the level says
	// whether they manage it.
	for _, role := range roles.All() {
		list := testkit.Decode[dto.List[dto.Agent]](role.Client.Get("/api/v1/agents").RequireStatus(http.StatusOK))
		var levels []dto.Level
		for _, a := range list.Items {
			if a.ID == roles.TargetAI {
				levels = append(levels, a.MyLevel)
			}
		}
		switch role.Name {
		case "admin", "manager":
			require.Equal(t, []dto.Level{dto.LevelManage}, levels, role.Name)
		case "outsider", "instance_admin":
			require.Empty(t, levels, role.Name)
		default:
			require.Equal(t, []dto.Level{dto.LevelUse}, levels, role.Name)
		}
	}

	// The tools of an AI are agent calls, not a way to manage AIs (TOK-03); anonymous callers are 401.
	ai := roles.Manager.Token(dto.TokenKindAgent, "tools").AsAgent("acme-ai")
	ai.Get("/api/v1/agents").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	ai.Patch("/api/v1/agents/"+roles.TargetAI, dto.UpdateAgentRequest{Description: &desc}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	env.Anon().Get("/api/v1/agents").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
}

// POS-04: the AIs a user hosts stay in their list whatever the chart says, but hosting is no management:
// a host who left the organization of the AI lists it with the level of someone who only sees it, and
// cannot change it. Seeing an AI is hosting it or sharing an organization with it.
func TestAgentListAndLevelFollowTheChartNotTheHost(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	host := roles.Manager // hosts and created roles.TargetAI
	levelOf := func(c *testkit.Client, id string) (dto.Level, bool) {
		list := testkit.Decode[dto.List[dto.Agent]](c.Get("/api/v1/agents").RequireStatus(http.StatusOK))
		for _, a := range list.Items {
			if a.ID == id {
				return a.MyLevel, true
			}
		}
		return "", false
	}
	level, listed := levelOf(host, roles.TargetAI)
	require.True(t, listed)
	require.Equal(t, dto.LevelManage, level, "the creator manages the AI it started")

	require.NoError(t, env.App.Orgs.Remove(ctx, roles.Admin.User.ID, roles.Org.ID, host.User.ID), "the host leaves the organization")
	level, listed = levelOf(host, roles.TargetAI)
	require.True(t, listed, "the AIs a user hosts stay in their list")
	require.Equal(t, dto.LevelUse, level, "but hosting is no management")
	desc := "no longer mine"
	host.Patch("/api/v1/agents/"+roles.TargetAI, dto.UpdateAgentRequest{Description: &desc}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	host.Get("/api/v1/agents/" + roles.TargetAI).RequireStatus(http.StatusOK)
	_, listed = levelOf(roles.Peer, roles.TargetAI)
	require.True(t, listed, "and the AI stays in the organization")
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
	require.Equal(t, "billing-api", got.Intelligence.Handle)
	require.Equal(t, dto.User{ID: alice.User.ID, Username: "alice", DisplayName: "alice"}, got.Owner, "an account that chose no name goes by its username")
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
