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

func manages(t *testing.T, env *testkit.Env, actor, target string) bool {
	t.Helper()
	ok, err := env.App.Authz.Manages(context.Background(), actor, target)
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
	c, err := env.App.Authz.Roster(ctx, personal.ID)
	require.NoError(t, err)
	require.Equal(t, orgs.PermissionAdmin, c.Members[alice.User.ID].Permission)

	// The default can point elsewhere; the personal organization stays what it is.
	team, err := env.App.Orgs.Create(ctx, alice.User.ID, "Team")
	require.NoError(t, err)
	require.NoError(t, env.App.Orgs.SetDefault(ctx, alice.User.ID, team.ID))
	require.Equal(t, personal.ID, alice.PersonalOrg().ID)
	require.NotEqual(t, env.NewUser("bob").PersonalOrg().ID, personal.ID, "each account has its own")
	require.Equal(t, "root", env.NewAdmin("root").PersonalOrg().Slug, "an admin of the instance too")
}

// T11: the store-level helpers set an organization up without going through the services' rules.
func TestOrgHelpersSetMembersAndInstructors(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	org := alice.PersonalOrg().ID

	env.AddMember(org, bob.User.ID, orgs.PermissionMember)
	requireMember(t, env, org, bob.User.ID, orgs.Member{IID: bob.User.ID, Permission: orgs.PermissionMember})

	// The host manages its AI; AddInstructor makes a user manage it too (SR2-13), SetCreator gives nothing.
	ai := env.NewAI(alice, "notes")
	require.True(t, manages(t, env, alice.User.ID, ai))
	env.SetCreator(ai, bob.User.ID)
	require.False(t, manages(t, env, bob.User.ID, ai), "a creator is history only")
	env.AddInstructor(ai, bob.User.ID)
	require.True(t, manages(t, env, bob.User.ID, ai), "a user instructor manages it")
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
	requireMember(t, env, team.ID, inTeam, orgs.Member{IID: inTeam, Permission: orgs.PermissionMember})
	b, err := env.App.Store.R().GetAgent(ctx, atHome)
	require.NoError(t, err)
	require.Equal(t, personal, b.OrgID.String, "NewAI still goes to the default")
	u, err := env.App.Store.R().GetUser(ctx, alice.User.ID)
	require.NoError(t, err)
	require.Equal(t, personal, u.DefaultOrgID.String, "and the default is as it was")
}

// T11: OrgRoles builds one organization with an identity in every role of the authorization matrices: the
// stated permissions hold, and the host and the user instructor manage TargetAI where nobody else does.
func TestOrgRolesYieldsEveryRoleOfTheMatrices(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	r := env.OrgRoles("acme")

	require.Equal(t, "acme", r.Org.Name, "named as asked")
	require.Equal(t, "acme", r.Org.Slug)
	c, err := env.App.Authz.Roster(ctx, r.Org.ID)
	require.NoError(t, err)
	require.Len(t, c.Members, 7, "six people and an AI")
	require.Equal(t, orgs.PermissionAdmin, c.Members[r.Admin.User.ID].Permission)
	for _, m := range []*testkit.Client{r.Manager, r.Instructor, r.Target, r.Peer, r.Member} {
		require.Equal(t, orgs.PermissionMember, c.Members[m.User.ID].Permission, m.User.Username)
	}
	for _, outside := range []*testkit.Client{r.Outsider, r.InstanceAdmin} {
		_, ok, err := env.App.Authz.Member(ctx, r.Org.ID, outside.User.ID)
		require.NoError(t, err)
		require.False(t, ok, "%s is not a member", outside.User.Username)
	}
	require.EqualValues(t, "admin", r.InstanceAdmin.User.Role, "the instance admin is an admin of the instance, and of no organization")
	require.EqualValues(t, "member", r.Outsider.User.Role)

	requireMember(t, env, r.Org.ID, r.TargetAI, orgs.Member{IID: r.TargetAI, Permission: orgs.PermissionMember})
	a, err := env.App.Store.R().GetAgent(ctx, r.TargetAI)
	require.NoError(t, err)
	require.Equal(t, r.Org.ID, a.OrgID.String, "its home")
	require.Equal(t, r.Manager.User.ID, a.OwnerID, "the manager hosts it")

	for name, tc := range map[string]struct {
		actor *testkit.Client
		want  bool
	}{
		"the host manages the AI":        {r.Manager, true},
		"the user instructor manages it": {r.Instructor, true},
		"the admin does not":             {r.Admin, false},
		"a member does not":              {r.Member, false},
		"the target does not":            {r.Target, false},
		"an outsider does not":           {r.Outsider, false},
	} {
		require.Equal(t, tc.want, manages(t, env, tc.actor.User.ID, r.TargetAI), name)
	}
	require.False(t, manages(t, env, r.Admin.User.ID, r.Target.User.ID), "nobody manages a user")
	del, err := env.App.Authz.CanDeleteAI(ctx, r.Admin.User.ID, r.TargetAI)
	require.NoError(t, err)
	require.True(t, del, "an admin of its home deletes the AI")

	// Every client is logged in as its own account.
	all := r.All()
	require.Len(t, all, 8)
	seen := map[string]bool{}
	for _, rc := range all {
		require.False(t, seen[rc.Name], "role %s once", rc.Name)
		seen[rc.Name] = true
		var me dto.Me
		rc.Client.Get("/api/v1/me").RequireStatus(http.StatusOK).Decode(&me)
		require.Equal(t, rc.Client.User.ID, me.ID, rc.Name)
	}
	require.True(t, seen["admin"] && seen["manager"] && seen["instructor"] && seen["target"] && seen["peer"] && seen["member"] && seen["outsider"] && seen["instance_admin"])

	// Two organizations can live in one environment.
	other := env.OrgRoles("globex")
	require.NotEqual(t, r.Org.ID, other.Org.ID)
	require.Equal(t, "globex", other.Org.Slug)
}
