package api_test

import (
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/testkit"
)

func membersPath(org string) string { return "/api/v1/orgs/" + org + "/members" }

// listMembers is what c sees at GET /orgs/{org}/members, by identity id.
func listMembers(t *testing.T, c *testkit.Client, org string) map[string]dto.OrgMember {
	t.Helper()
	list := testkit.Decode[dto.List[dto.OrgMember]](c.Get(membersPath(org)).RequireStatus(http.StatusOK))
	out := make(map[string]dto.OrgMember, len(list.Items))
	for _, m := range list.Items {
		out[m.Intelligence.ID] = m
	}
	require.Len(t, out, len(list.Items), "an identity is listed once")
	return out
}

func toPermission(p dto.Permission) dto.UpdateOrgMemberRequest {
	return dto.UpdateOrgMemberRequest{Permission: &p}
}

// OMEM-03, the Members matrix: every member lists the members; whoever is not a member (an instance admin
// without membership too) does not find the organization.
func TestListOrgMembersMatrix(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	for _, role := range r.All() {
		t.Run(role.Name, func(t *testing.T) {
			resp := role.Client.Get(membersPath(r.Org.ID))
			if role.Name == "outsider" || role.Name == "instance_admin" {
				resp.RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
				return
			}
			list := testkit.Decode[dto.List[dto.OrgMember]](resp.RequireStatus(http.StatusOK))
			require.Len(t, list.Items, 7, "six people and the organization's AI")
		})
	}
	r.Admin.Get(membersPath("no-such-org")).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	// {org} is an id or an address, in any case.
	slug := testkit.Decode[dto.List[dto.OrgMember]](r.Member.Get(membersPath("ACME")).RequireStatus(http.StatusOK))
	require.Len(t, slug.Items, 7)
}

// OMEM-03, SR2-01, SR2-04: the list names each identity with its kind and permission - admin or member, no
// guest, no position, no inviter - and what the viewer may do to it (SR2-12). Everybody sees the same rows.
func TestListOrgMembersShowsKindAndPermission(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")

	type row struct {
		who        *testkit.Client
		kind       dto.IntelligenceKind
		permission dto.Permission
		name       string
	}
	want := []row{
		{r.Admin, dto.IntelligenceKindHuman, dto.PermissionAdmin, "admin"},
		{r.Manager, dto.IntelligenceKindHuman, dto.PermissionMember, "manager"},
		{r.Instructor, dto.IntelligenceKindHuman, dto.PermissionMember, "instructor"},
		{r.Target, dto.IntelligenceKindHuman, dto.PermissionMember, "target"},
		{r.Peer, dto.IntelligenceKindHuman, dto.PermissionMember, "peer"},
		{r.Member, dto.IntelligenceKindHuman, dto.PermissionMember, "member"},
		{nil, dto.IntelligenceKindAI, dto.PermissionMember, "ai"},
	}
	for _, viewer := range []*testkit.Client{r.Admin, r.Member} {
		resp := viewer.Get(membersPath(r.Org.ID)).RequireStatus(http.StatusOK)
		for _, gone := range []string{`"position_id"`, `"position_name"`, `"invited_by"`, `"can_move"`, `"can_share_access"`} {
			require.NotContains(t, string(resp.Body), gone)
		}
		got := listMembers(t, viewer, r.Org.ID)
		require.Len(t, got, len(want))
		for _, w := range want {
			id := r.TargetAI
			if w.who != nil {
				id = w.who.User.ID
			}
			m, ok := got[id]
			require.True(t, ok, "%s is listed", w.name)
			require.Equal(t, w.kind, m.Intelligence.Kind, w.name)
			require.Equal(t, w.permission, m.Permission, w.name)
			if w.kind == dto.IntelligenceKindAI {
				require.Equal(t, dto.IntelligenceRoleAI, m.Intelligence.Role)
				require.Equal(t, dto.DeleteEffectDelete, m.DeleteEffect, "its home: deleted for good")
			} else {
				require.Equal(t, dto.IntelligenceRoleUser, m.Intelligence.Role, w.name)
				require.Equal(t, "acme-"+w.name, m.Intelligence.Handle, w.name)
				require.Equal(t, dto.DeleteEffectRemove, m.DeleteEffect, w.name)
			}
			require.False(t, m.JoinedAt.IsZero(), w.name)
		}
	}
}

// SR2-12: can_delete says who may delete whom: an admin anyone but itself, the host of an AI whose home is the
// organization that AI, and nobody else anything - an instructor manages the AI but does not delete it.
func TestSR212TheMembersListSaysWhoMayDeleteWhom(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	deletable := func(c *testkit.Client) []string {
		var out []string
		for id, m := range listMembers(t, c, r.Org.ID) {
			if m.CanDelete {
				out = append(out, id)
			}
		}
		sort.Strings(out)
		return out
	}
	all := []string{r.Manager.User.ID, r.Instructor.User.ID, r.Target.User.ID, r.Peer.User.ID, r.Member.User.ID, r.TargetAI}
	sort.Strings(all)
	require.Equal(t, all, deletable(r.Admin), "an admin: everyone but itself")
	require.Equal(t, []string{r.TargetAI}, deletable(r.Manager), "the host: its AI")
	require.Empty(t, deletable(r.Instructor), "an instructor: nobody")
	require.Empty(t, deletable(r.Member))
}

// The list is ordered by display name, ignoring case, so every client shows it the same way.
func TestListOrgMembersIsOrderedByName(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	list := testkit.Decode[dto.List[dto.OrgMember]](r.Member.Get(membersPath(r.Org.ID)).RequireStatus(http.StatusOK))
	names := make([]string, len(list.Items))
	for i, m := range list.Items {
		names[i] = m.Intelligence.DisplayName
	}
	require.True(t, sort.SliceIsSorted(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) }),
		"display names: %v", names)
}

// OMEM-03: one identity belongs to several organizations with its own permission in each; each organization's
// list shows that organization's membership.
func TestOneIdentityHasAMembershipPerOrganization(t *testing.T) {
	env := testkit.New(t)
	acme := env.OrgRoles("acme")
	globex := env.OrgRoles("globex")
	env.AddMember(globex.Org.ID, acme.Member.User.ID, orgs.PermissionAdmin)

	require.Equal(t, dto.PermissionMember, listMembers(t, acme.Admin, acme.Org.ID)[acme.Member.User.ID].Permission)
	require.Equal(t, dto.PermissionAdmin, listMembers(t, globex.Admin, globex.Org.ID)[acme.Member.User.ID].Permission)

	// Changing the membership in one organization leaves the other alone.
	globex.Admin.Patch(memberPath(globex.Org.ID, acme.Member.User.ID), toPermission(dto.PermissionMember)).RequireStatus(http.StatusOK)
	require.Equal(t, dto.PermissionMember, listMembers(t, globex.Admin, globex.Org.ID)[acme.Member.User.ID].Permission)
	acme.Admin.Patch(memberPath(acme.Org.ID, acme.Member.User.ID), toPermission(dto.PermissionAdmin)).RequireStatus(http.StatusOK)
	require.Equal(t, dto.PermissionMember, listMembers(t, globex.Admin, globex.Org.ID)[acme.Member.User.ID].Permission)
	require.Len(t, listMembers(t, acme.Admin, acme.Org.ID), 7)
}

// The Members matrix, PATCH permission: only an admin changes a permission - not the host of an AI, not its
// instructor, not the member itself - and whoever cannot see the organization does not find it. The request is
// valid for every role (the admin's is a no-op), so the role alone decides.
func TestPatchOrgMemberPermissionMatrix(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	want := map[string]int{
		"admin": http.StatusOK, "manager": http.StatusForbidden, "instructor": http.StatusForbidden,
		"target": http.StatusForbidden, "peer": http.StatusForbidden, "member": http.StatusForbidden,
		"outsider": http.StatusNotFound, "instance_admin": http.StatusNotFound,
	}
	for _, role := range r.All() {
		t.Run(role.Name, func(t *testing.T) {
			resp := role.Client.Patch(memberPath(r.Org.ID, r.Peer.User.ID), toPermission(dto.PermissionMember))
			if want[role.Name] == http.StatusOK {
				got := testkit.Decode[dto.OrgMember](resp.RequireStatus(http.StatusOK))
				require.Equal(t, r.Peer.User.ID, got.Intelligence.ID)
				require.Equal(t, dto.PermissionMember, got.Permission)
				return
			}
			requireRefusal(t, resp, want[role.Name])
		})
	}
	// Hosting or instructing an AI gives no right over its permission, and a member does not promote itself.
	r.Manager.Patch(memberPath(r.Org.ID, r.TargetAI), toPermission(dto.PermissionAdmin)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	r.Instructor.Patch(memberPath(r.Org.ID, r.TargetAI), toPermission(dto.PermissionAdmin)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	r.Target.Patch(memberPath(r.Org.ID, r.Target.User.ID), toPermission(dto.PermissionAdmin)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	members := listMembers(t, r.Admin, r.Org.ID)
	require.Equal(t, dto.PermissionMember, members[r.Target.User.ID].Permission)
	require.Equal(t, dto.PermissionMember, members[r.TargetAI].Permission)
}

// OMEM-04, OMEM-03: an admin promotes a member to admin and back, and does the same for an AI; the list shows
// each result.
func TestAdminChangesAMembersPermission(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	list := func() map[string]dto.OrgMember { return listMembers(t, r.Member, r.Org.ID) }

	got := testkit.Decode[dto.OrgMember](r.Admin.Patch(memberPath(r.Org.ID, r.Peer.User.ID), toPermission(dto.PermissionAdmin)).RequireStatus(http.StatusOK))
	require.Equal(t, dto.PermissionAdmin, got.Permission)
	require.Equal(t, r.Peer.User.ID, got.Intelligence.ID)
	require.Equal(t, dto.PermissionAdmin, list()[r.Peer.User.ID].Permission, "the list shows the admin")
	// The new admin may now change permissions.
	r.Peer.Patch(memberPath(r.Org.ID, r.Member.User.ID), toPermission(dto.PermissionAdmin)).RequireStatus(http.StatusOK)
	r.Peer.Patch(memberPath(r.Org.ID, r.Member.User.ID), toPermission(dto.PermissionMember)).RequireStatus(http.StatusOK)
	require.Equal(t, dto.PermissionMember, list()[r.Member.User.ID].Permission)

	got = testkit.Decode[dto.OrgMember](r.Admin.Patch(memberPath(r.Org.ID, r.TargetAI), toPermission(dto.PermissionAdmin)).RequireStatus(http.StatusOK))
	require.Equal(t, dto.IntelligenceKindAI, got.Intelligence.Kind)
	require.Equal(t, dto.PermissionAdmin, got.Permission)
	require.Equal(t, dto.PermissionAdmin, list()[r.TargetAI].Permission)
}

// OMEM-05: the only admin cannot be demoted - 409 last_admin - and the same request works once a second
// admin exists, so the count of admins is the only thing that differs.
func TestDemotingTheLastAdminIsConflict(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")

	r.Admin.Patch(memberPath(r.Org.ID, r.Admin.User.ID), toPermission(dto.PermissionMember)).
		RequireError(http.StatusConflict, dto.ErrorCodeLastAdmin)
	require.Equal(t, dto.PermissionAdmin, listMembers(t, r.Admin, r.Org.ID)[r.Admin.User.ID].Permission)

	r.Admin.Patch(memberPath(r.Org.ID, r.Manager.User.ID), toPermission(dto.PermissionAdmin)).RequireStatus(http.StatusOK)
	got := testkit.Decode[dto.OrgMember](r.Admin.Patch(memberPath(r.Org.ID, r.Admin.User.ID), toPermission(dto.PermissionMember)).RequireStatus(http.StatusOK))
	require.Equal(t, dto.PermissionMember, got.Permission)
	// The one admin left is the manager, and it cannot step down either.
	r.Manager.Patch(memberPath(r.Org.ID, r.Manager.User.ID), toPermission(dto.PermissionMember)).
		RequireError(http.StatusConflict, dto.ErrorCodeLastAdmin)
	// The former admin is a member now: it can no longer change permissions.
	r.Admin.Patch(memberPath(r.Org.ID, r.Manager.User.ID), toPermission(dto.PermissionMember)).
		RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
}

// SR2-01: a permission that is not admin or member - guest included - is 422 on permission for an admin; the
// authorization comes first, so a member that sends it is told 403. A position_id an older client still sends
// is ignored (SR2-05).
func TestSR201PatchOrgMemberValidates(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	for _, bad := range []dto.Permission{"owner", "guest"} {
		body := r.Admin.Patch(memberPath(r.Org.ID, r.Peer.User.ID), toPermission(bad)).
			RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, body.Fields, "permission")
		r.Member.Patch(memberPath(r.Org.ID, r.Peer.User.ID), toPermission(bad)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	}
	got := testkit.Decode[dto.OrgMember](r.Admin.DoRaw(http.MethodPatch, memberPath(r.Org.ID, r.Peer.User.ID), "application/json",
		strings.NewReader(`{"position_id":"anything"}`)).RequireStatus(http.StatusOK))
	require.Equal(t, dto.PermissionMember, got.Permission, "an old field changes nothing")
	// Not JSON at all is a bad request.
	r.Admin.DoRaw(http.MethodPatch, memberPath(r.Org.ID, r.Peer.User.ID), "application/json", strings.NewReader("{nope")).
		RequireError(http.StatusBadRequest, dto.ErrorCodeInvalid)
	require.Equal(t, dto.PermissionMember, listMembers(t, r.Admin, r.Org.ID)[r.Peer.User.ID].Permission, "nothing changed")
}

// The target must be a member of the organization: an identity of another organization, an outsider and
// an unknown id are 404, and so is an unknown organization.
func TestPatchOrgMemberNeedsAMemberTarget(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	for _, target := range []string{r.Outsider.User.ID, r.InstanceAdmin.User.ID, "no-such-identity"} {
		r.Admin.Patch(memberPath(r.Org.ID, target), toPermission(dto.PermissionMember)).
			RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	}
	r.Admin.Patch(memberPath("no-such-org", r.Peer.User.ID), toPermission(dto.PermissionMember)).
		RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// A request that asks for nothing changes nothing: a member gets the row back, an outsider is 404.
func TestPatchOrgMemberWithNothingToChange(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	got := testkit.Decode[dto.OrgMember](r.Member.Patch(memberPath(r.Org.ID, r.Peer.User.ID), dto.UpdateOrgMemberRequest{}).RequireStatus(http.StatusOK))
	require.Equal(t, r.Peer.User.ID, got.Intelligence.ID)
	r.Outsider.Patch(memberPath(r.Org.ID, r.Peer.User.ID), dto.UpdateOrgMemberRequest{}).
		RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// The routes are for people: an agent token never reaches an organization route.
func TestAgentTokenCannotReachMemberRoutes(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	tok := r.Admin.Token(dto.TokenKindAgent, "t")
	tok.Get(membersPath(r.Org.ID)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	tok.Patch(memberPath(r.Org.ID, r.Peer.User.ID), toPermission(dto.PermissionAdmin)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	require.Equal(t, dto.PermissionMember, listMembers(t, r.Admin, r.Org.ID)[r.Peer.User.ID].Permission)
}
