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

// ORG-04 (T37): GET /agent/orgs tells the user of an agent token where they stand in each of their organizations -
// the permission and the position - and the chart of positions of each, so `partyline orgs` can show --org and
// --position names. Every role of the matrix sees exactly its own organizations: the members of Acme, a guest
// included (CHART-02), see Acme's chart; an outsider and an instance admin who are not members see none of it.
func TestAgentOrgsTellsEveryRoleWhereItStands(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	acmeChart := []dto.AgentOrgPosition{
		{ID: r.AdminPosition, Name: "Admin"},
		{ID: r.ManagerPosition, Name: "Manager", ParentID: r.AdminPosition},
		{ID: r.WorkerPosition, Name: "Worker", ParentID: r.ManagerPosition},
	}
	cases := []struct {
		role       string
		c          *testkit.Client
		permission string
		position   string
		inAcme     bool
	}{
		{"admin", r.Admin, "admin", "Admin", true},
		{"manager", r.Manager, "member", "Manager", true},
		{"target", r.Target, "member", "Worker", true},
		{"peer", r.Peer, "member", "Worker", true},
		{"member", r.Member, "member", "", true},
		{"inviter", r.Inviter, "member", "", true},
		{"guest", r.Guest, "guest", "", true},
		{"outsider", r.Outsider, "", "", false},
		{"instance admin", r.InstanceAdmin, "", "", false},
	}
	for _, c := range cases {
		t.Run(c.role, func(t *testing.T) {
			list := agentOrgsOf(t, c.c)
			personal := c.c.PersonalOrg()
			if !c.inAcme {
				require.Equal(t, []string{personal.Slug}, slugsOf(list), "only their own organization")
				raw := c.c.Token(dto.TokenKindAgent, "other").Get("/api/v1/agent/orgs").Body
				require.NotContains(t, string(raw), r.WorkerPosition, "nothing of Acme's chart")
				require.NotContains(t, string(raw), r.Org.ID)
				return
			}
			require.Equal(t, []string{"acme", personal.Slug}, slugsOf(list), "by name: Acme, then their own organization")
			acme, own := list[0], list[1]
			require.Equal(t, []any{r.Org.ID, "acme", "acme", c.permission, c.position, false},
				[]any{acme.ID, acme.Name, acme.Slug, acme.Permission, acme.Position, acme.Default})
			require.Equal(t, acmeChart, acme.Positions, "Acme's chart, by name, each with the position above it")
			require.Equal(t, []any{personal.ID, "admin", "Admin", true},
				[]any{own.ID, own.Permission, own.Position, own.Default}, "their own organization is their default")
			require.Len(t, own.Positions, 1)
			require.Equal(t, dto.AgentOrgPosition{ID: own.Positions[0].ID, Name: "Admin"}, own.Positions[0], "a root position has no parent")
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

// Each organization lists its own positions and nobody else's; a user in two organizations sees both charts.
func TestAgentOrgsListsEachOrganizationsOwnChart(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	globex, err := env.App.Orgs.Create(context.Background(), r.Admin.User.ID, "Globex")
	require.NoError(t, err)
	treasurer := env.AddPosition(globex.ID, "Treasurer", globex.PositionID)

	list := agentOrgsOf(t, r.Admin)
	require.Equal(t, []string{"acme", r.Admin.PersonalOrg().Slug, "globex"}, slugsOf(list), "by name, ignoring case")
	names := func(o dto.AgentOrg) []string {
		out := make([]string, len(o.Positions))
		for i, p := range o.Positions {
			out[i] = p.Name
		}
		return out
	}
	require.Equal(t, []string{"Admin", "Manager", "Worker"}, names(list[0]))
	require.Equal(t, []string{"Admin"}, names(list[1]), "their own organization has the root position only")
	require.Equal(t, []string{"Admin", "Treasurer"}, names(list[2]))
	require.Equal(t, dto.AgentOrgPosition{ID: treasurer, Name: "Treasurer", ParentID: globex.PositionID}, list[2].Positions[1])

	// a member of Globex only sees Globex's chart
	env.AddMember(globex.ID, r.Outsider.User.ID, orgs.PermissionGuest, "")
	got := agentOrgsOf(t, r.Outsider)
	require.Equal(t, []string{r.Outsider.PersonalOrg().Slug, "globex"}, slugsOf(got))
	require.Equal(t, "guest", got[1].Permission)
	require.Equal(t, []string{"Admin", "Treasurer"}, names(got[1]))
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
	env.AddMember(r.Org.ID, user.User.ID, orgs.PermissionMember, r.WorkerPosition)
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
