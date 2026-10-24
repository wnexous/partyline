package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// The room tests run on organizations (AD-016): a group belongs to one, is posted to by its room members
// and managed by its creator and the organization's admins; a DM is posted to by its participants and
// viewed by the managers of an AI participant. This file holds what they share: creating a group in a
// chosen organization, adding people to it, and the role matrix of the design ("Authorization matrix per
// route group") over testkit.OrgRoles.

// roomOrg is the organization the group belongs to (ORG-02).
func roomOrg(t *testing.T, env *testkit.Env, roomID string) string {
	t.Helper()
	r, err := env.App.Store.R().GetRoom(context.Background(), roomID)
	require.NoError(t, err)
	require.True(t, r.OrgID.Valid, "a group belongs to an organization")
	return r.OrgID.String
}

// groupIn creates a group named name as creator in the organization, which is creator's default while the
// group is made, and returns it. creator must belong to the organization.
func groupIn(t *testing.T, env *testkit.Env, creator *testkit.Client, orgID, name string) dto.Room {
	t.Helper()
	ctx := context.Background()
	before, err := env.App.Store.R().GetUser(ctx, creator.User.ID)
	require.NoError(t, err)
	require.NoError(t, env.App.Orgs.SetDefault(ctx, creator.User.ID, orgID))
	defer func() {
		require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
			return q.SetUserDefaultOrg(ctx, db.SetUserDefaultOrgParams{DefaultOrgID: before.DefaultOrgID, ID: creator.User.ID})
		}))
	}()
	return createRoom(creator, name)
}

// joinOrgOf makes the user a member of the organization the room belongs to with the permission: it adds
// them, or changes the permission of a member they already are.
func joinOrgOf(t *testing.T, env *testkit.Env, roomID string, u *testkit.Client, perm orgs.Permission) {
	t.Helper()
	ctx := context.Background()
	orgID := roomOrg(t, env, roomID)
	m, ok, err := env.App.Authz.Member(ctx, orgID, u.User.ID)
	require.NoError(t, err)
	switch {
	case !ok:
		env.AddMember(orgID, u.User.ID, perm, "")
	case m.Permission != perm:
		require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
			return q.UpdateOrgMemberPermission(ctx, db.UpdateOrgMemberPermissionParams{Permission: string(perm), OrgID: orgID, Iid: u.User.ID})
		}))
	}
}

// addToRoom adds the user to the group as a room member through the API, as manager, after making them a
// member of the group's organization when they are not one: they then post in it.
func addToRoom(t *testing.T, env *testkit.Env, manager *testkit.Client, roomID string, u *testkit.Client) dto.RoomMember {
	t.Helper()
	orgID := roomOrg(t, env, roomID)
	if _, ok, err := env.App.Authz.Member(context.Background(), orgID, u.User.ID); err != nil || !ok {
		require.NoError(t, err)
		env.AddMember(orgID, u.User.ID, orgs.PermissionMember, "")
	}
	return testkit.Decode[dto.RoomMember](manager.Post("/api/v1/rooms/"+roomID+"/members",
		dto.AddMemberRequest{Kind: dto.MemberKindUser, MemberID: u.User.ID}).RequireStatus(http.StatusCreated))
}

// roleRoute is one route of an organization-role matrix: call performs the request as c against res, and
// want maps every role name of testkit.OrgRoles.All to the expected status.
type roleRoute struct {
	name string
	call func(t *testing.T, c *testkit.Client, res map[string]string) *testkit.Response
	want map[string]int
}

// groupWant is the expected status of a route on a group by role: the creator (the role "member" of
// testkit.OrgRoles), an admin of the organization, the people in the room (the roles "target" and
// "guest") and everybody else (a manager above them, a peer, an inviter, an outsider, an instance admin).
func groupWant(creator, admin, inRoom, other int) map[string]int {
	return map[string]int{
		"member": creator, "admin": admin, "target": inRoom, "guest": inRoom,
		"manager": other, "peer": other, "inviter": other, "outsider": other, "instance_admin": other,
	}
}

// dmWant is the expected status of a route on the DM between "target" and the AI "manager" created, by
// role: the participant, the managers of the AI (its creator and the admin above it) and everybody else.
func dmWant(participant, viewer, other int) map[string]int {
	return map[string]int{
		"target": participant, "manager": viewer, "admin": viewer,
		"member": other, "peer": other, "inviter": other, "guest": other, "outsider": other, "instance_admin": other,
	}
}

// runRoleRoutes calls every route as every role, each against a fresh resource from setup, and checks the
// status; 403 and 404 must carry the forbidden and not_found error codes.
func runRoleRoutes(t *testing.T, roles *testkit.OrgRoles, setup func(t *testing.T) map[string]string, routes []roleRoute) {
	t.Helper()
	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			for _, role := range roles.All() {
				t.Run(role.Name, func(t *testing.T) {
					want, ok := rt.want[role.Name]
					require.True(t, ok, "no expectation for %s", role.Name)
					res := setup(t)
					c := *role.Client
					c.T = t
					resp := rt.call(t, &c, res)
					switch want {
					case http.StatusForbidden:
						resp.RequireError(want, dto.ErrorCodeForbidden)
					case http.StatusNotFound:
						resp.RequireError(want, dto.ErrorCodeNotFound)
					default:
						resp.RequireStatus(want)
					}
				})
			}
		})
	}
}
