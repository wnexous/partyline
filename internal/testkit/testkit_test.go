package testkit_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/testkit"
)

// The kit boots a ready server and logs accounts in with real sessions.
func TestKitBootsAndLogsIn(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)

	env.Anon().Get("/healthz").RequireStatus(http.StatusOK)
	env.Anon().Get("/readyz").RequireStatus(http.StatusOK)

	admin := env.SetupAdmin()
	require.Same(t, admin, env.SetupAdmin())
	p, err := env.App.Users.ValidateSession(ctx, admin.Session)
	require.NoError(t, err)
	require.Equal(t, authn.KindUser, p.Kind)
	require.Equal(t, admin.User.ID, p.UserID)
	require.Equal(t, "admin", p.Role)

	bob := env.NewUser("bob")
	p, err = env.App.Users.ValidateSession(ctx, bob.Session)
	require.NoError(t, err)
	require.Equal(t, bob.User.ID, p.UserID)
	require.Equal(t, "member", p.Role)
}

// requireMember asserts the membership of iid in the organization.
func requireMember(t *testing.T, env *testkit.Env, org, iid string, want orgs.Member) {
	t.Helper()
	got, ok, err := env.App.Authz.Member(context.Background(), org, iid)
	require.NoError(t, err)
	require.True(t, ok, "%s is a member", iid)
	require.Equal(t, want, got)
}

func manages(t *testing.T, env *testkit.Env, org, actor, target string) bool {
	t.Helper()
	ok, err := env.App.Authz.Manages(context.Background(), actor, target, org)
	require.NoError(t, err)
	return ok
}

// T11: every account has a personal organization, and PersonalOrg finds it - the one the account created
// with itself as admin, not the account's default, which can change.
func TestPersonalOrgIsTheOrganizationOfTheAccount(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	alice := env.NewUser("alice")

	personal := alice.PersonalOrg()

	require.Equal(t, "alice", personal.Slug)
	require.Equal(t, alice.User.ID, personal.CreatedBy)
	c, err := env.App.Authz.Chart(ctx, personal.ID)
	require.NoError(t, err)
	require.Equal(t, orgs.PermissionAdmin, c.Members[alice.User.ID].Permission)
	require.Equal(t, "Admin", c.Positions[c.Members[alice.User.ID].PositionID].Name)

	// The default can point elsewhere; the personal organization stays what it is.
	team, err := env.App.Orgs.Create(ctx, alice.User.ID, "Team")
	require.NoError(t, err)
	require.NoError(t, env.App.Orgs.SetDefault(ctx, alice.User.ID, team.ID))
	require.Equal(t, personal.ID, alice.PersonalOrg().ID)
	require.NotEqual(t, env.NewUser("bob").PersonalOrg().ID, personal.ID, "each account has its own")
	require.Equal(t, "root", env.NewAdmin("root").PersonalOrg().Slug, "an admin of the instance too")
}

// T11: the store-level helpers set an organization up without going through the services' rules.
func TestOrgHelpersBuildAChart(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	org := alice.PersonalOrg().ID

	boss := env.AddPosition(org, "Boss", "")
	crew := env.AddPosition(org, "Crew", boss)
	env.AddMember(org, bob.User.ID, orgs.PermissionMember, crew)
	requireMember(t, env, org, bob.User.ID, orgs.Member{IID: bob.User.ID, Permission: orgs.PermissionMember, PositionID: crew})
	c, err := env.App.Authz.Chart(ctx, org)
	require.NoError(t, err)
	require.Equal(t, orgs.Position{ID: crew, Name: "Crew", ParentID: boss}, c.Positions[crew])
	require.Equal(t, orgs.Position{ID: boss, Name: "Boss"}, c.Positions[boss])

	// A guest has an inviter.
	carol := env.NewUser("carol")
	env.AddGuest(org, carol.User.ID, alice.User.ID, "")
	requireMember(t, env, org, carol.User.ID, orgs.Member{IID: carol.User.ID, Permission: orgs.PermissionGuest, InviterIID: alice.User.ID})
	require.True(t, manages(t, env, org, alice.User.ID, carol.User.ID), "the inviter manages its guest")

	// SetCreator re-points the creator of an AI: the new creator manages it.
	ai := env.NewAI(alice, "notes")
	require.True(t, manages(t, env, org, alice.User.ID, ai))
	env.SetCreator(ai, bob.User.ID)
	require.True(t, manages(t, env, org, bob.User.ID, ai), "bob is the creator now")
	require.False(t, manages(t, env, org, alice.User.ID, ai), "alice created nothing")
}

// T11: NewAIIn creates an AI whose home is the given organization and leaves the owner's default alone.
func TestNewAIInPlacesTheAIWhereAsked(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	alice := env.NewUser("alice")
	team, err := env.App.Orgs.Create(ctx, alice.User.ID, "Team")
	require.NoError(t, err)
	personal := alice.PersonalOrg().ID

	inTeam := env.NewAIIn(alice, team.ID, "worker")
	atHome := env.NewAI(alice, "notes")

	a, err := env.App.Store.R().GetAgent(ctx, inTeam)
	require.NoError(t, err)
	require.Equal(t, team.ID, a.OrgID.String)
	require.Equal(t, alice.User.ID, a.CreatorIid.String)
	requireMember(t, env, team.ID, inTeam, orgs.Member{IID: inTeam, Permission: orgs.PermissionMember, CreatorIID: alice.User.ID})
	b, err := env.App.Store.R().GetAgent(ctx, atHome)
	require.NoError(t, err)
	require.Equal(t, personal, b.OrgID.String, "NewAI still goes to the default")
	u, err := env.App.Store.R().GetUser(ctx, alice.User.ID)
	require.NoError(t, err)
	require.Equal(t, personal, u.DefaultOrgID.String, "and the default is as it was")
}

// T11: OrgRoles builds one organization with an identity in every role of the authorization matrices.
// The stated permissions and positions hold, and Manages(manager, target) is true where
// Manages(peer, target) is not.
func TestOrgRolesYieldsEveryRoleOfTheMatrices(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	r := env.OrgRoles("acme")

	require.Equal(t, "acme", r.Org.Name, "named as asked")
	require.Equal(t, "acme", r.Org.Slug)
	c, err := env.App.Authz.Chart(ctx, r.Org.ID)
	require.NoError(t, err)
	require.Len(t, c.Positions, 3, "Admin above Manager above Worker")
	require.Len(t, c.Members, 8, "seven people and an AI")
	of := func(cl *testkit.Client) orgs.Member { return c.Members[cl.User.ID] }
	posName := func(m orgs.Member) string { return c.Positions[m.PositionID].Name }

	require.Equal(t, orgs.PermissionAdmin, of(r.Admin).Permission)
	require.Equal(t, "Admin", posName(of(r.Admin)), "the admin holds the root position")
	require.Equal(t, orgs.PermissionMember, of(r.Manager).Permission)
	require.Equal(t, "Manager", posName(of(r.Manager)))
	require.Equal(t, orgs.PermissionMember, of(r.Target).Permission)
	require.Equal(t, "Worker", posName(of(r.Target)))
	require.Equal(t, orgs.PermissionMember, of(r.Peer).Permission)
	require.Equal(t, of(r.Target).PositionID, of(r.Peer).PositionID, "the peer holds the target's own position")
	require.Equal(t, orgs.Member{IID: r.Member.User.ID, Permission: orgs.PermissionMember}, of(r.Member), "a member with no position and no relation")
	require.Equal(t, orgs.Member{IID: r.Inviter.User.ID, Permission: orgs.PermissionMember}, of(r.Inviter))
	require.Equal(t, orgs.Member{IID: r.Guest.User.ID, Permission: orgs.PermissionGuest, InviterIID: r.Inviter.User.ID}, of(r.Guest), "the guest of the inviter")
	for _, outside := range []*testkit.Client{r.Outsider, r.InstanceAdmin} {
		_, ok, err := env.App.Authz.Member(ctx, r.Org.ID, outside.User.ID)
		require.NoError(t, err)
		require.False(t, ok, "%s is not a member", outside.User.Username)
	}
	require.EqualValues(t, "admin", r.InstanceAdmin.User.Role, "the instance admin is an admin of the instance, and of no organization")
	require.EqualValues(t, "member", r.Outsider.User.Role)

	// The AI of the organization: created by the manager, no position, member.
	requireMember(t, env, r.Org.ID, r.TargetAI, orgs.Member{IID: r.TargetAI, Permission: orgs.PermissionMember, CreatorIID: r.Manager.User.ID})
	a, err := env.App.Store.R().GetAgent(ctx, r.TargetAI)
	require.NoError(t, err)
	require.Equal(t, r.Org.ID, a.OrgID.String, "its home")

	// Who manages whom: the point of the roles.
	id := func(cl *testkit.Client) string { return cl.User.ID }
	for name, tc := range map[string]struct {
		actor  *testkit.Client
		target string
		want   bool
	}{
		"the manager manages the target":                    {r.Manager, id(r.Target), true},
		"the peer does not":                                 {r.Peer, id(r.Target), false},
		"the admin does, through the root":                  {r.Admin, id(r.Target), true},
		"a member of no relation does not":                  {r.Member, id(r.Target), false},
		"the target does not manage upward":                 {r.Target, id(r.Manager), false},
		"the peer does not manage the manager":              {r.Peer, id(r.Manager), false},
		"the inviter manages its guest":                     {r.Inviter, id(r.Guest), true},
		"a member of no relation does not manage the guest": {r.Member, id(r.Guest), false},
		"the manager does not manage the guest":             {r.Manager, id(r.Guest), false},
		"the manager manages the AI it created":             {r.Manager, r.TargetAI, true},
		"the admin manages it through the creator":          {r.Admin, r.TargetAI, true},
		"the peer does not manage the AI":                   {r.Peer, r.TargetAI, false},
		"the target does not manage the AI":                 {r.Target, r.TargetAI, false},
		"nobody manages the admin":                          {r.Manager, id(r.Admin), false},
	} {
		require.Equal(t, tc.want, manages(t, env, r.Org.ID, id(tc.actor), tc.target), name)
	}
	del, err := env.App.Authz.CanDelete(ctx, id(r.Admin), id(r.Guest), r.Org.ID)
	require.NoError(t, err)
	require.True(t, del, "an admin deletes anyone")

	// Every client is logged in as its own account.
	all := r.All()
	require.Len(t, all, 9)
	seen := map[string]bool{}
	for _, rc := range all {
		require.False(t, seen[rc.Name], "role %s once", rc.Name)
		seen[rc.Name] = true
		var me dto.Me
		rc.Client.Get("/api/v1/me").RequireStatus(http.StatusOK).Decode(&me)
		require.Equal(t, rc.Client.User.ID, me.ID, rc.Name)
	}
	require.True(t, seen["admin"] && seen["manager"] && seen["target"] && seen["peer"] && seen["member"] && seen["inviter"] && seen["guest"] && seen["outsider"] && seen["instance_admin"])

	// Two organizations can live in one environment.
	other := env.OrgRoles("globex")
	require.NotEqual(t, r.Org.ID, other.Org.ID)
	require.Equal(t, "globex", other.Org.Slug)
}
