package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/testkit"
)

// agentOrgsOf reads GET /agent/orgs with an agent token of c.
func agentOrgsOf(t *testing.T, c *testkit.Client) []dto.AgentOrg {
	t.Helper()
	return testkit.Decode[dto.List[dto.AgentOrg]](c.Token(dto.TokenKindAgent, "laptop").Get("/api/v1/agent/orgs").RequireStatus(http.StatusOK)).Items
}

func slugsOf(list []dto.AgentOrg) []string {
	out := make([]string, len(list))
	for i, o := range list {
		out[i] = o.Slug
	}
	return out
}

// ORG-04 (T37), SR2-05: GET /agent/orgs tells the user of an agent token their permission in each of their
// organizations, so `partyline orgs` can show --org names; organizations have no chart of positions any more.
// Every role of the matrix sees exactly its own organizations: an outsider and an instance admin who are not
// members see none of Acme.
func TestAgentOrgsTellsEveryRoleWhereItStands(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	cases := []struct {
		role       string
		c          *testkit.Client
		permission string
		inAcme     bool
	}{
		{"admin", r.Admin, "admin", true},
		{"manager", r.Manager, "member", true},
		{"instructor", r.Instructor, "member", true},
		{"target", r.Target, "member", true},
		{"member", r.Member, "member", true},
		{"outsider", r.Outsider, "", false},
		{"instance admin", r.InstanceAdmin, "", false},
	}
	for _, c := range cases {
		t.Run(c.role, func(t *testing.T) {
			resp := c.c.Token(dto.TokenKindAgent, "laptop").Get("/api/v1/agent/orgs").RequireStatus(http.StatusOK)
			list := testkit.Decode[dto.List[dto.AgentOrg]](resp).Items
			require.NotContains(t, string(resp.Body), `"position`, "SR2-05: no position nor chart")
			personal := c.c.PersonalOrg()
			if !c.inAcme {
				require.Equal(t, []string{personal.Slug}, slugsOf(list), "only their own organization")
				require.NotContains(t, string(resp.Body), r.Org.ID)
				return
			}
			require.Equal(t, []string{"acme", personal.Slug}, slugsOf(list), "by name: Acme, then their own organization")
			acme, own := list[0], list[1]
			require.Equal(t, dto.AgentOrg{ID: r.Org.ID, Name: "acme", Slug: "acme", Permission: c.permission}, acme)
			require.Equal(t, dto.AgentOrg{ID: personal.ID, Name: personal.Name, Slug: personal.Slug, Permission: "admin", Default: true}, own,
				"their own organization is their default")
		})
	}
}

// ORG-04: exactly one organization is the default, the one a new AI joins when --org is not given: the user's
// choice while they still belong to it, otherwise where they have administered the longest.
func TestAgentOrgsMarksTheDefault(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	marked := func(list []dto.AgentOrg) []string {
		var out []string
		for _, o := range list {
			if o.Default {
				out = append(out, o.Slug)
			}
		}
		return out
	}

	member := r.Member
	require.Equal(t, []string{member.PersonalOrg().Slug}, marked(agentOrgsOf(t, member)), "the personal organization until they choose")

	require.NoError(t, env.App.Orgs.SetDefault(ctx, member.User.ID, r.Org.ID))
	require.Equal(t, []string{"acme"}, marked(agentOrgsOf(t, member)), "their choice")

	require.NoError(t, env.App.Orgs.Leave(ctx, member.User.ID, r.Org.ID))
	list := agentOrgsOf(t, member)
	require.Equal(t, []string{member.PersonalOrg().Slug}, slugsOf(list), "no longer a member: Acme is gone from the list")
	require.Equal(t, []string{member.PersonalOrg().Slug}, marked(list), "and the default is the organization they still have")
}

// The list is read when asked, never cached: joining and leaving show on the next request.
func TestAgentOrgsFollowMembershipChanges(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	user := r.Outsider
	tok := user.Token(dto.TokenKindAgent, "laptop")
	slugs := func() []string {
		return slugsOf(testkit.Decode[dto.List[dto.AgentOrg]](tok.Get("/api/v1/agent/orgs").RequireStatus(http.StatusOK)).Items)
	}
	require.Equal(t, []string{user.PersonalOrg().Slug}, slugs())
	env.AddMember(r.Org.ID, user.User.ID, orgs.PermissionMember)
	require.Equal(t, []string{"acme", user.PersonalOrg().Slug}, slugs(), "the same token sees the new organization")
	require.NoError(t, env.App.Orgs.Leave(ctx, user.User.ID, r.Org.ID))
	require.Equal(t, []string{user.PersonalOrg().Slug}, slugs())
}

// A user in no organization gets an empty list, not null, and nothing is marked.
func TestAgentOrgsOfAUserInNoOrganization(t *testing.T) {
	env := testkit.New(t)
	loner := env.NewUser("loner")
	require.NoError(t, env.App.Orgs.Delete(context.Background(), loner.User.ID, loner.PersonalOrg().ID))
	resp := loner.Token(dto.TokenKindAgent, "laptop").Get("/api/v1/agent/orgs").RequireStatus(http.StatusOK)
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(resp.Body, &raw))
	require.JSONEq(t, `[]`, string(raw["items"]))
}

// It is the token's user who is listed, whatever AI the request acts as: an agent token with folder headers
// answers the same list as one without.
func TestAgentOrgsAreTheTokenUsersWhateverTheFolder(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	tok := r.Manager.Token(dto.TokenKindAgent, "laptop")
	plain := testkit.Decode[dto.List[dto.AgentOrg]](tok.Get("/api/v1/agent/orgs").RequireStatus(http.StatusOK))
	asAI := testkit.Decode[dto.List[dto.AgentOrg]](tok.AsAgent("worker").Get("/api/v1/agent/orgs").RequireStatus(http.StatusOK))
	require.Equal(t, plain, asAI)
	require.Len(t, plain.Items, 2)
}

// The route is for agent tokens only: a user's session or token is 403, no credentials and a token the server
// does not know are 401, and the token of a disabled user stops working.
func TestAgentOrgsNeedAnAgentToken(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	r.Admin.Get("/api/v1/agent/orgs").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	r.Admin.Token(dto.TokenKindUser, "cli").Get("/api/v1/agent/orgs").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	env.Anon().Get("/api/v1/agent/orgs").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	env.Anon().WithBearer("pl_agent_not-a-token").Get("/api/v1/agent/orgs").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)

	tok := r.Member.Token(dto.TokenKindAgent, "laptop")
	tok.Get("/api/v1/agent/orgs").RequireStatus(http.StatusOK)
	disabled := true
	r.InstanceAdmin.Patch("/api/v1/admin/users/"+r.Member.User.ID, dto.UpdateUserRequest{Disabled: &disabled}).RequireStatus(http.StatusOK)
	tok.Get("/api/v1/agent/orgs").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
}
