package api_test

import (
	"encoding/json"
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

func movePreviewPath(org, iid string) string { return memberPath(org, iid) + "/move-preview" }

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

// toPosition is the PATCH body that moves a member to the position.
func toPosition(id string) dto.UpdateOrgMemberRequest {
	b, err := json.Marshal(id)
	if err != nil {
		panic(err)
	}
	return dto.UpdateOrgMemberRequest{PositionID: b}
}

// toNoPosition is the PATCH body that clears the position (null).
var toNoPosition = dto.UpdateOrgMemberRequest{PositionID: json.RawMessage("null")}

func toPermission(p dto.Permission) dto.UpdateOrgMemberRequest {
	return dto.UpdateOrgMemberRequest{Permission: &p}
}

func newPosition(t *testing.T, admin *testkit.Client, org, name, parent string) string {
	t.Helper()
	return testkit.Decode[dto.Position](admin.Post("/api/v1/orgs/"+org+"/positions",
		dto.CreatePositionRequest{Name: name, ParentID: parent}).RequireStatus(http.StatusCreated)).ID
}

// OMEM-03, CHART-02, the Members matrix: every member, a guest included, lists the members; whoever is
// not a member (an instance admin without membership too) does not find the organization.
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
			require.Len(t, list.Items, 8, "seven people and the organization's AI")
		})
	}
	r.Admin.Get(membersPath("no-such-org")).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	// {org} is an id or an address, in any case.
	slug := testkit.Decode[dto.List[dto.OrgMember]](r.Guest.Get(membersPath("ACME")).RequireStatus(http.StatusOK))
	require.Len(t, slug.Items, 8)
}

// OMEM-01, OMEM-03: the list names each identity with its kind, permission and position, and a guest
// with the identity that invited it. Everybody sees the same list.
func TestListOrgMembersShowsPermissionPositionAndInviter(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")

	type row struct {
		who        *testkit.Client
		kind       dto.IntelligenceKind
		permission dto.Permission
		position   string // position id, "" for none
		name       string
		invitedBy  *testkit.Client
	}
	want := map[string]row{
		"admin":   {r.Admin, dto.IntelligenceKindHuman, dto.PermissionAdmin, r.AdminPosition, "Admin", nil},
		"manager": {r.Manager, dto.IntelligenceKindHuman, dto.PermissionMember, r.ManagerPosition, "Manager", nil},
		"target":  {r.Target, dto.IntelligenceKindHuman, dto.PermissionMember, r.WorkerPosition, "Worker", nil},
		"peer":    {r.Peer, dto.IntelligenceKindHuman, dto.PermissionMember, r.WorkerPosition, "Worker", nil},
		"member":  {r.Member, dto.IntelligenceKindHuman, dto.PermissionMember, "", "", nil},
		"inviter": {r.Inviter, dto.IntelligenceKindHuman, dto.PermissionMember, "", "", nil},
		"guest":   {r.Guest, dto.IntelligenceKindHuman, dto.PermissionGuest, "", "", r.Inviter},
		"theAI":   {nil, dto.IntelligenceKindAI, dto.PermissionMember, "", "", nil},
	}

	for _, viewer := range []*testkit.Client{r.Admin, r.Member, r.Guest} {
		got := listMembers(t, viewer, r.Org.ID)
		require.Len(t, got, len(want))
		for name, w := range want {
			id := r.TargetAI
			if w.who != nil {
				id = w.who.User.ID
			}
			m, ok := got[id]
			require.True(t, ok, "%s is listed", name)
			require.Equal(t, w.kind, m.Intelligence.Kind, name)
			require.Equal(t, w.permission, m.Permission, name)
			require.Equal(t, w.position, m.PositionID, name)
			require.Equal(t, w.name, m.PositionName, name)
			if w.kind == dto.IntelligenceKindAI {
				require.Equal(t, dto.IntelligenceRoleAI, m.Intelligence.Role)
			} else {
				require.Equal(t, dto.IntelligenceRoleUser, m.Intelligence.Role, name)
				require.Equal(t, "acme-"+name, m.Intelligence.Handle, name)
			}
			require.False(t, m.JoinedAt.IsZero(), name)
			if w.invitedBy == nil {
				require.Nil(t, m.InvitedBy, "only a guest has an inviter: %s", name)
				continue
			}
			require.NotNil(t, m.InvitedBy, name)
			require.Equal(t, w.invitedBy.User.ID, m.InvitedBy.ID)
			require.Equal(t, "acme-inviter", m.InvitedBy.Handle)
		}
	}
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

// OMEM-03: one identity belongs to several organizations with its own permission and position in each;
// each organization's list shows that organization's membership.
func TestOneIdentityHasAMembershipPerOrganization(t *testing.T) {
	env := testkit.New(t)
	acme := env.OrgRoles("acme")
	globex := env.OrgRoles("globex")
	env.AddMember(globex.Org.ID, acme.Member.User.ID, orgs.PermissionAdmin, globex.ManagerPosition)

	inAcme := listMembers(t, acme.Admin, acme.Org.ID)[acme.Member.User.ID]
	require.Equal(t, dto.PermissionMember, inAcme.Permission)
	require.Empty(t, inAcme.PositionID)

	inGlobex := listMembers(t, globex.Admin, globex.Org.ID)[acme.Member.User.ID]
	require.Equal(t, dto.PermissionAdmin, inGlobex.Permission)
	require.Equal(t, globex.ManagerPosition, inGlobex.PositionID)
	require.Equal(t, "Manager", inGlobex.PositionName)

	// Changing the membership in one organization leaves the other alone.
	globex.Admin.Patch(memberPath(globex.Org.ID, acme.Member.User.ID), toPermission(dto.PermissionGuest)).RequireStatus(http.StatusOK)
	require.Equal(t, dto.PermissionGuest, listMembers(t, globex.Admin, globex.Org.ID)[acme.Member.User.ID].Permission)
	require.Equal(t, dto.PermissionMember, listMembers(t, acme.Admin, acme.Org.ID)[acme.Member.User.ID].Permission)
	// The acme member is nobody's member of globex as far as acme's list goes: it lists acme only.
	require.Len(t, listMembers(t, acme.Admin, acme.Org.ID), 8)
}

// The Members matrix, PATCH permission: only an admin changes a permission - not a manager, not the
// inviter of a guest, not the member itself - and whoever cannot see the organization does not find
// it. The request is valid for every role (the admin's is a no-op), so the role alone decides.
func TestPatchOrgMemberPermissionMatrix(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	want := map[string]int{
		"admin": http.StatusOK, "manager": http.StatusForbidden, "target": http.StatusForbidden,
		"peer": http.StatusForbidden, "member": http.StatusForbidden, "inviter": http.StatusForbidden,
		"guest": http.StatusForbidden, "outsider": http.StatusNotFound, "instance_admin": http.StatusNotFound,
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
	// Managing the target gives no right over its permission (each guard on its own): the manager of the
	// target, the inviter of a guest and a member about itself are refused too.
	r.Manager.Patch(memberPath(r.Org.ID, r.Target.User.ID), toPermission(dto.PermissionGuest)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	r.Inviter.Patch(memberPath(r.Org.ID, r.Guest.User.ID), toPermission(dto.PermissionMember)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	r.Target.Patch(memberPath(r.Org.ID, r.Target.User.ID), toPermission(dto.PermissionAdmin)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	members := listMembers(t, r.Admin, r.Org.ID)
	require.Equal(t, dto.PermissionMember, members[r.Target.User.ID].Permission)
	require.Equal(t, dto.PermissionGuest, members[r.Guest.User.ID].Permission)
}

// OMEM-04, OMEM-03: an admin turns a member into a guest and back, promotes to admin and does the same
// for an AI; the list shows each result, and a guest that becomes a member is nobody's guest any more.
func TestAdminChangesAMembersPermission(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	list := func() map[string]dto.OrgMember { return listMembers(t, r.Member, r.Org.ID) }

	got := testkit.Decode[dto.OrgMember](r.Admin.Patch(memberPath(r.Org.ID, r.Member.User.ID), toPermission(dto.PermissionGuest)).RequireStatus(http.StatusOK))
	require.Equal(t, dto.PermissionGuest, got.Permission)
	require.Equal(t, r.Member.User.ID, got.Intelligence.ID)
	require.Equal(t, dto.PermissionGuest, list()[r.Member.User.ID].Permission, "the list shows the guest")

	// The inviter of Guest is its manager; promoting Guest ends that.
	require.NotNil(t, list()[r.Guest.User.ID].InvitedBy)
	got = testkit.Decode[dto.OrgMember](r.Admin.Patch(memberPath(r.Org.ID, r.Guest.User.ID), toPermission(dto.PermissionMember)).RequireStatus(http.StatusOK))
	require.Equal(t, dto.PermissionMember, got.Permission)
	require.Nil(t, got.InvitedBy)
	require.Nil(t, list()[r.Guest.User.ID].InvitedBy)

	got = testkit.Decode[dto.OrgMember](r.Admin.Patch(memberPath(r.Org.ID, r.Peer.User.ID), toPermission(dto.PermissionAdmin)).RequireStatus(http.StatusOK))
	require.Equal(t, dto.PermissionAdmin, got.Permission)
	require.Equal(t, r.WorkerPosition, got.PositionID, "the position stays")
	// The new admin may now change permissions.
	r.Peer.Patch(memberPath(r.Org.ID, r.Inviter.User.ID), toPermission(dto.PermissionGuest)).RequireStatus(http.StatusOK)

	got = testkit.Decode[dto.OrgMember](r.Admin.Patch(memberPath(r.Org.ID, r.TargetAI), toPermission(dto.PermissionGuest)).RequireStatus(http.StatusOK))
	require.Equal(t, dto.IntelligenceKindAI, got.Intelligence.Kind)
	require.Equal(t, dto.PermissionGuest, got.Permission)
	require.Equal(t, dto.PermissionGuest, list()[r.TargetAI].Permission)
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
	r.Manager.Patch(memberPath(r.Org.ID, r.Manager.User.ID), toPermission(dto.PermissionGuest)).
		RequireError(http.StatusConflict, dto.ErrorCodeLastAdmin)
	// The former admin is a member now: it can no longer change permissions.
	r.Admin.Patch(memberPath(r.Org.ID, r.Manager.User.ID), toPermission(dto.PermissionMember)).
		RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
}

// A permission that is not admin, member or guest is 422 on permission for an admin; the authorization
// comes first, so a member that sends it is told 403.
func TestPatchOrgMemberValidates(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	bad := dto.Permission("owner")

	body := r.Admin.Patch(memberPath(r.Org.ID, r.Peer.User.ID), toPermission(bad)).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, body.Fields, "permission")
	r.Member.Patch(memberPath(r.Org.ID, r.Peer.User.ID), toPermission(bad)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)

	// A position that is not a string is 422 on position_id.
	for _, junk := range []string{`5`, `true`, `{}`, `[]`} {
		body = r.Admin.Patch(memberPath(r.Org.ID, r.Peer.User.ID), dto.UpdateOrgMemberRequest{PositionID: json.RawMessage(junk)}).
			RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, body.Fields, "position_id", junk)
	}
	// Not JSON at all is a bad request.
	r.Admin.DoRaw(http.MethodPatch, memberPath(r.Org.ID, r.Peer.User.ID), "application/json", strings.NewReader("{nope")).
		RequireError(http.StatusBadRequest, dto.ErrorCodeInvalid)
	require.Equal(t, r.WorkerPosition, listMembers(t, r.Admin, r.Org.ID)[r.Peer.User.ID].PositionID, "nothing changed")
}

// The target must be a member of the organization: an identity of another organization, an outsider and
// an unknown id are 404 whoever asks, and so is an unknown organization.
func TestPatchOrgMemberNeedsAMemberTarget(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	for _, target := range []string{r.Outsider.User.ID, r.InstanceAdmin.User.ID, "no-such-identity"} {
		for _, c := range []*testkit.Client{r.Admin, r.Manager} {
			c.Patch(memberPath(r.Org.ID, target), toPosition(r.WorkerPosition)).
				RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		}
		r.Admin.Patch(memberPath(r.Org.ID, target), toPermission(dto.PermissionGuest)).
			RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	}
	r.Admin.Patch(memberPath("no-such-org", r.Peer.User.ID), toPermission(dto.PermissionGuest)).
		RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// A request that asks for nothing changes nothing: a member gets the row back, an outsider is 404.
func TestPatchOrgMemberWithNothingToChange(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	got := testkit.Decode[dto.OrgMember](r.Member.Patch(memberPath(r.Org.ID, r.Peer.User.ID), dto.UpdateOrgMemberRequest{}).RequireStatus(http.StatusOK))
	require.Equal(t, r.Peer.User.ID, got.Intelligence.ID)
	require.Equal(t, r.WorkerPosition, got.PositionID)
	r.Outsider.Patch(memberPath(r.Org.ID, r.Peer.User.ID), dto.UpdateOrgMemberRequest{}).
		RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// POS-04, the Members matrix, PATCH position: an admin gives anyone any position or none; a manager moves
// a subordinate to a position strictly below its own and nowhere else; the rest are refused. Each
// refusal below is the only reason the request fails (L-018).
func TestMovingAMemberBetweenPositions(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	intern := newPosition(t, r.Admin, r.Org.ID, "Intern", r.WorkerPosition)  // below Manager and Worker
	auditor := newPosition(t, r.Admin, r.Org.ID, "Auditor", r.AdminPosition) // beside Manager, not below it
	deputy := newPosition(t, r.Admin, r.Org.ID, "Deputy", r.ManagerPosition) // below Manager, beside Worker
	held := func(who string) string { return listMembers(t, r.Admin, r.Org.ID)[who].PositionID }
	restore := func(who, position string) {
		t.Helper()
		r.Admin.Patch(memberPath(r.Org.ID, who), toPosition(position)).RequireStatus(http.StatusOK)
	}

	t.Run("a manager moves a subordinate below its own position", func(t *testing.T) {
		for _, below := range []string{intern, deputy, r.WorkerPosition} {
			got := testkit.Decode[dto.OrgMember](r.Manager.Patch(memberPath(r.Org.ID, r.Target.User.ID), toPosition(below)).RequireStatus(http.StatusOK))
			require.Equal(t, below, got.PositionID)
			require.Equal(t, below, held(r.Target.User.ID), "the list shows it")
		}
		// An AI the manager created, with no position, is a subordinate too.
		got := testkit.Decode[dto.OrgMember](r.Manager.Patch(memberPath(r.Org.ID, r.TargetAI), toPosition(intern)).RequireStatus(http.StatusOK))
		require.Equal(t, intern, got.PositionID)
		require.Equal(t, dto.IntelligenceKindAI, got.Intelligence.Kind)
		restore(r.TargetAI, "")
	})

	t.Run("a manager cannot move to its own level, above it, beside it or to none", func(t *testing.T) {
		for name, position := range map[string]string{"its own level": r.ManagerPosition, "above": r.AdminPosition, "another branch": auditor} {
			r.Manager.Patch(memberPath(r.Org.ID, r.Target.User.ID), toPosition(position)).
				RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
			require.Equal(t, r.WorkerPosition, held(r.Target.User.ID), name)
		}
		r.Manager.Patch(memberPath(r.Org.ID, r.Target.User.ID), toNoPosition).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		r.Manager.Patch(memberPath(r.Org.ID, r.Target.User.ID), toPosition("")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		require.Equal(t, r.WorkerPosition, held(r.Target.User.ID), "clearing is for admins")
		// A position that does not exist is not below anything.
		r.Manager.Patch(memberPath(r.Org.ID, r.Target.User.ID), toPosition("no-such-position")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	})

	t.Run("a manager moves only the members it manages", func(t *testing.T) {
		// Intern is below the manager's position in every case, so only the target decides.
		for name, target := range map[string]string{
			"a superior": r.Admin.User.ID, "itself": r.Manager.User.ID, "a member nobody created": r.Member.User.ID,
			"a guest of someone else": r.Guest.User.ID,
		} {
			r.Manager.Patch(memberPath(r.Org.ID, target), toPosition(intern)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
			require.NotEqual(t, intern, held(target), name)
		}
	})

	t.Run("a peer cannot move its peer", func(t *testing.T) {
		// Intern is strictly below Worker, the peer's own position: the missing management is the only guard.
		r.Peer.Patch(memberPath(r.Org.ID, r.Target.User.ID), toPosition(intern)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		r.Target.Patch(memberPath(r.Org.ID, r.Target.User.ID), toPosition(intern)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		require.Equal(t, r.WorkerPosition, held(r.Target.User.ID))
	})

	t.Run("an inviter without a position positions nobody", func(t *testing.T) {
		// Inviter manages Guest, but holds no position, so no position is below its own.
		r.Inviter.Patch(memberPath(r.Org.ID, r.Guest.User.ID), toPosition(intern)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		require.Empty(t, held(r.Guest.User.ID))
	})

	t.Run("a member and a guest move nobody", func(t *testing.T) {
		for _, c := range []*testkit.Client{r.Member, r.Guest} {
			c = bound(t, c)
			c.Patch(memberPath(r.Org.ID, r.Peer.User.ID), toPosition(intern)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
			c.Patch(memberPath(r.Org.ID, c.User.ID), toPosition(intern)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		}
	})

	t.Run("whoever is not a member does not find the organization", func(t *testing.T) {
		for _, c := range []*testkit.Client{r.Outsider, r.InstanceAdmin} {
			bound(t, c).Patch(memberPath(r.Org.ID, r.Target.User.ID), toPosition(intern)).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		}
	})

	t.Run("an admin gives anyone any position or none", func(t *testing.T) {
		for name, position := range map[string]string{"its own level": r.AdminPosition, "beside": auditor, "below": intern} {
			got := testkit.Decode[dto.OrgMember](r.Admin.Patch(memberPath(r.Org.ID, r.Manager.User.ID), toPosition(position)).RequireStatus(http.StatusOK))
			require.Equal(t, position, got.PositionID, name)
		}
		restore(r.Manager.User.ID, r.ManagerPosition)
		// An admin may move a member above and beside the others, and clear a position (null or "").
		got := testkit.Decode[dto.OrgMember](r.Admin.Patch(memberPath(r.Org.ID, r.Target.User.ID), toNoPosition).RequireStatus(http.StatusOK))
		require.Empty(t, got.PositionID)
		require.Empty(t, got.PositionName)
		require.Empty(t, held(r.Target.User.ID))
		restore(r.Target.User.ID, r.WorkerPosition)
		r.Admin.Patch(memberPath(r.Org.ID, r.Target.User.ID), toPosition("")).RequireStatus(http.StatusOK)
		require.Empty(t, held(r.Target.User.ID))
		restore(r.Target.User.ID, r.WorkerPosition)
		// An admin may position itself, and give a position to a guest or an AI.
		r.Admin.Patch(memberPath(r.Org.ID, r.Admin.User.ID), toPosition(auditor)).RequireStatus(http.StatusOK)
		require.Equal(t, auditor, held(r.Admin.User.ID))
		restore(r.Admin.User.ID, r.AdminPosition)
		r.Admin.Patch(memberPath(r.Org.ID, r.Guest.User.ID), toPosition(deputy)).RequireStatus(http.StatusOK)
		r.Admin.Patch(memberPath(r.Org.ID, r.TargetAI), toPosition(deputy)).RequireStatus(http.StatusOK)
		require.Equal(t, deputy, held(r.Guest.User.ID))
		require.Equal(t, deputy, held(r.TargetAI))
	})

	t.Run("a position of the organization", func(t *testing.T) {
		other := env.OrgRoles("globex")
		foreign := newPosition(t, other.Admin, other.Org.ID, "Foreign", "")
		for name, position := range map[string]string{"unknown": "no-such-position", "another organization's": foreign} {
			body := r.Admin.Patch(memberPath(r.Org.ID, r.Peer.User.ID), toPosition(position)).
				RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
			require.Contains(t, body.Fields, "position_id", name)
		}
	})
}

// POS-04, OMEM-04: a request may change the permission and the position together, and then both happen or
// neither does.
func TestPatchOrgMemberAppliesPermissionAndPositionTogether(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	intern := newPosition(t, r.Admin, r.Org.ID, "Intern", r.WorkerPosition)
	both := func(p dto.Permission, position string) dto.UpdateOrgMemberRequest {
		req := toPosition(position)
		req.Permission = &p
		return req
	}
	state := func(who string) (dto.Permission, string) {
		m := listMembers(t, r.Admin, r.Org.ID)[who]
		return m.Permission, m.PositionID
	}

	got := testkit.Decode[dto.OrgMember](r.Admin.Patch(memberPath(r.Org.ID, r.Member.User.ID), both(dto.PermissionGuest, intern)).RequireStatus(http.StatusOK))
	require.Equal(t, dto.PermissionGuest, got.Permission)
	require.Equal(t, intern, got.PositionID)

	// The last admin steps down and moves in one request: the demotion is refused, so the move is not made.
	r.Admin.Patch(memberPath(r.Org.ID, r.Admin.User.ID), both(dto.PermissionMember, r.WorkerPosition)).
		RequireError(http.StatusConflict, dto.ErrorCodeLastAdmin)
	permission, position := state(r.Admin.User.ID)
	require.Equal(t, dto.PermissionAdmin, permission)
	require.Equal(t, r.AdminPosition, position, "the move was not made")

	// A bad position stops a valid permission change.
	r.Admin.Patch(memberPath(r.Org.ID, r.Peer.User.ID), both(dto.PermissionGuest, "no-such-position")).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	permission, position = state(r.Peer.User.ID)
	require.Equal(t, dto.PermissionMember, permission, "the permission was not changed")
	require.Equal(t, r.WorkerPosition, position)

	// A manager may move a subordinate but not change its permission: the whole request is refused.
	r.Manager.Patch(memberPath(r.Org.ID, r.Target.User.ID), both(dto.PermissionGuest, intern)).
		RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	permission, position = state(r.Target.User.ID)
	require.Equal(t, dto.PermissionMember, permission)
	require.Equal(t, r.WorkerPosition, position, "the move was not made")

	// An admin that steps down while moving is judged by what it was when it asked.
	r.Admin.Patch(memberPath(r.Org.ID, r.Manager.User.ID), toPermission(dto.PermissionAdmin)).RequireStatus(http.StatusOK)
	got = testkit.Decode[dto.OrgMember](r.Admin.Patch(memberPath(r.Org.ID, r.Admin.User.ID), both(dto.PermissionMember, intern)).RequireStatus(http.StatusOK))
	require.Equal(t, dto.PermissionMember, got.Permission)
	require.Equal(t, intern, got.PositionID)
}

// POS-04: the move preview names, before the move, the managers that would stop managing the member;
// it answers the move's own refusals and changes nothing.
func TestMovePreviewShowsWhoStopsManagingTheMember(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	ai := env.NewAIIn(r.Admin, r.Org.ID, "acme-own-ai")
	before := listMembers(t, r.Admin, r.Org.ID)

	got := testkit.Decode[dto.MovePreview](r.Admin.Get(movePreviewPath(r.Org.ID, ai) + "?position_id=" + r.AdminPosition).RequireStatus(http.StatusOK))
	require.Len(t, got.LoseManagement, 1)
	require.Equal(t, before[r.Admin.User.ID].Intelligence, got.LoseManagement[0], "the incident: its creator stops managing it")
	require.Equal(t, r.Admin.User.ID, got.LoseManagement[0].ID)
	require.Equal(t, before[r.Admin.User.ID].Intelligence.Handle, got.LoseManagement[0].Handle)

	// Below its creator's position, nobody loses the management.
	got = testkit.Decode[dto.MovePreview](r.Admin.Get(movePreviewPath(r.Org.ID, ai) + "?position_id=" + r.WorkerPosition).RequireStatus(http.StatusOK))
	require.NotNil(t, got.LoseManagement)
	require.Empty(t, got.LoseManagement)

	// The move's refusals are the preview's refusals.
	r.Manager.Get(movePreviewPath(r.Org.ID, r.TargetAI)+"?position_id="+r.ManagerPosition).
		RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)

	// Neither preview changed the members table.
	require.Equal(t, before, listMembers(t, r.Admin, r.Org.ID))

	// An organization the caller cannot see is not found.
	r.Outsider.Get(movePreviewPath(r.Org.ID, ai)+"?position_id="+r.AdminPosition).
		RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	r.Admin.Get(movePreviewPath("no-such-org", ai)+"?position_id="+r.AdminPosition).
		RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// The routes are for people: an agent token never reaches an organization route.
func TestAgentTokenCannotReachMemberRoutes(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	tok := r.Admin.Token(dto.TokenKindAgent, "t")
	tok.Get(membersPath(r.Org.ID)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	tok.Patch(memberPath(r.Org.ID, r.Peer.User.ID), toPermission(dto.PermissionGuest)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	require.Equal(t, dto.PermissionMember, listMembers(t, r.Admin, r.Org.ID)[r.Peer.User.ID].Permission)
}
