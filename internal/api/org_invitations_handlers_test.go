package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/testkit"
)

func invitationsPath(org string) string { return "/api/v1/orgs/" + org + "/invitations" }

// sendInvitation posts an invitation of the handle to the organization as c.
func sendInvitation(c *testkit.Client, org, handle string, perm dto.Permission, position string) *testkit.Response {
	return c.Post(invitationsPath(org), dto.CreateInvitationRequest{Handle: handle, Permission: perm, PositionID: position})
}

// invited sends an invitation that must succeed (201) and returns it.
func invited(t *testing.T, c *testkit.Client, org, handle string, perm dto.Permission, position string) dto.Invitation {
	t.Helper()
	return testkit.Decode[dto.Invitation](sendInvitation(bound(t, c), org, handle, perm, position).RequireStatus(http.StatusCreated))
}

// answerInvitation accepts or declines (verb "accept" or "decline") as c.
func answerInvitation(c *testkit.Client, id, verb string) *testkit.Response {
	return c.Post("/api/v1/invitations/"+id+"/"+verb, nil)
}

// myInvitations is what GET /invitations shows c, newest first.
func myInvitations(t *testing.T, c *testkit.Client) []dto.Invitation {
	t.Helper()
	return testkit.Decode[dto.List[dto.Invitation]](c.Get("/api/v1/invitations").RequireStatus(http.StatusOK)).Items
}

// orgInvitations is what GET /orgs/{org}/invitations shows an admin.
func orgInvitations(t *testing.T, c *testkit.Client, org string) []dto.Invitation {
	t.Helper()
	return testkit.Decode[dto.List[dto.Invitation]](c.Get(invitationsPath(org)).RequireStatus(http.StatusOK)).Items
}

func invitationIDs(list []dto.Invitation) []string {
	ids := make([]string, len(list))
	for i, inv := range list {
		ids[i] = inv.ID
	}
	return ids
}

// handleOfID is the @handle of an identity.
func handleOfID(t *testing.T, env *testkit.Env, iid string) string {
	t.Helper()
	i, err := env.App.Store.R().GetIntelligence(context.Background(), iid)
	require.NoError(t, err)
	return i.Handle
}

// roleClient finds the client of a role of testkit.OrgRoles.
func roleClient(t *testing.T, r *testkit.OrgRoles, name string) *testkit.Client {
	t.Helper()
	for _, rc := range r.All() {
		if rc.Name == name {
			return rc.Client
		}
	}
	require.FailNow(t, "no such role", name)
	return nil
}

// OMEM-01, OMEM-04, the Invitations matrix: an admin invites with any permission and a position; every other
// member and guest invites guests with no position - a member who asks for more is refused - and whoever is
// not a member does not find the organization. Each refusal is the only thing wrong with its request.
func TestInviteMatrix(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	type send struct {
		key      string // names the invitee
		perm     dto.Permission
		position string
	}
	sends := []send{
		{"guest", dto.PermissionGuest, ""},                // a guest without a position
		{"member", dto.PermissionMember, ""},              // a member
		{"placed", dto.PermissionGuest, r.WorkerPosition}, // a guest in a position
	}
	for _, tc := range []struct {
		role string
		want [3]int // for each of sends
	}{
		{"admin", [3]int{201, 201, 201}},
		{"manager", [3]int{201, 403, 403}},
		{"target", [3]int{201, 403, 403}},
		{"peer", [3]int{201, 403, 403}},
		{"member", [3]int{201, 403, 403}},
		{"inviter", [3]int{201, 403, 403}},
		{"guest", [3]int{201, 403, 403}},
		{"outsider", [3]int{404, 404, 404}},
		{"instance_admin", [3]int{404, 404, 404}},
	} {
		invitees := make([]*testkit.Client, len(sends))
		for i, send := range sends {
			invitees[i] = env.NewUser(strings.ReplaceAll(tc.role, "_", "") + "-" + send.key) // a new invitee for each request
		}
		t.Run(tc.role, func(t *testing.T) {
			c := bound(t, roleClient(t, r, tc.role))
			for i, send := range sends {
				resp := sendInvitation(c, r.Org.ID, "@"+invitees[i].User.Username, send.perm, send.position)
				switch tc.want[i] {
				case http.StatusForbidden:
					resp.RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
				case http.StatusNotFound:
					resp.RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
				default:
					inv := testkit.Decode[dto.Invitation](resp.RequireStatus(http.StatusCreated))
					require.Equal(t, send.perm, inv.Permission, send.key)
					require.Equal(t, send.position, inv.PositionID, send.key)
					require.Equal(t, invitees[i].User.ID, inv.Invitee.ID, send.key)
					require.Equal(t, c.User.ID, inv.InvitedBy.ID, "the inviter is recorded: %s", send.key)
					require.Equal(t, r.Org.ID, inv.OrgID)
					require.Equal(t, "acme", inv.OrgSlug)
					require.Equal(t, 14*24*time.Hour, inv.ExpiresAt.Sub(inv.CreatedAt), "OMEM-07: 14 days")
					if send.position != "" {
						require.Equal(t, "Worker", inv.PositionName)
					}
				}
			}
		})
	}
}

// OMEM-01: the list of an organization's invitations and revoking one are for admins; revoking an
// invitation withdraws it, so the invitee can no longer answer it.
func TestListAndRevokeInvitationsMatrix(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	guest := env.NewUser("lena")
	admin := env.NewUser("adam")
	first := invited(t, r.Inviter, r.Org.ID, "lena", dto.PermissionGuest, "")
	second := invited(t, r.Admin, r.Org.ID, "adam", dto.PermissionAdmin, r.AdminPosition)

	for _, role := range r.All() {
		t.Run(role.Name, func(t *testing.T) {
			c := bound(t, role.Client)
			list := c.Get(invitationsPath(r.Org.ID))
			if role.Name == "admin" { // the admin's revoking comes below
				items := testkit.Decode[dto.List[dto.Invitation]](list.RequireStatus(http.StatusOK)).Items
				require.Equal(t, []string{second.ID, first.ID}, invitationIDs(items), "newest first")
				require.Equal(t, dto.PermissionAdmin, items[0].Permission)
				require.Equal(t, "Admin", items[0].PositionName)
				require.Equal(t, r.Inviter.User.ID, items[1].InvitedBy.ID)
				return
			}
			revoke := c.Delete(invitationsPath(r.Org.ID) + "/" + second.ID)
			if role.Name == "outsider" || role.Name == "instance_admin" {
				list.RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
				revoke.RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
				return
			}
			list.RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
			revoke.RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		})
	}
	// Every refusal left the invitations as they were; now the admin revokes one.
	require.Len(t, orgInvitations(t, r.Admin, r.Org.ID), 2)
	r.Admin.Delete(invitationsPath(r.Org.ID) + "/" + second.ID).RequireStatus(http.StatusNoContent)
	require.Equal(t, []string{first.ID}, invitationIDs(orgInvitations(t, r.Admin, r.Org.ID)))
	require.Empty(t, myInvitations(t, admin), "the invitee no longer sees it")
	answerInvitation(admin, second.ID, "accept").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	r.Admin.Delete(invitationsPath(r.Org.ID)+"/"+second.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	// An invitation of another organization is not found through this one, even by its admin (the only guard).
	other := env.OrgRoles("globex")
	foreign := invited(t, other.Admin, other.Org.ID, "lena", dto.PermissionGuest, "")
	r.Admin.Delete(invitationsPath(r.Org.ID)+"/"+foreign.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Equal(t, []string{foreign.ID}, invitationIDs(orgInvitations(t, other.Admin, other.Org.ID)))
	require.Equal(t, []string{foreign.ID, first.ID}, invitationIDs(myInvitations(t, guest)), "the invitee has both, newest first")
}

// The spec's independent test (P1 Members and permissions): Alice, an admin, invites Bob as a member. Bob
// accepts, creates a group in the organization, and is refused when he invites someone as a member.
func TestAnInvitedMemberWorksInTheOrganizationButCannotInvite(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	bob := env.NewUser("bob")
	env.NewUser("carol")
	acme := testkit.Decode[dto.Organization](alice.Post("/api/v1/orgs", dto.CreateOrganizationRequest{Name: "acme"}).RequireStatus(http.StatusCreated))

	inv := invited(t, alice, acme.ID, "bob", dto.PermissionMember, "")
	require.Equal(t, "alice", inv.InvitedBy.Handle)
	require.Equal(t, "bob", inv.Invitee.Handle)
	require.Equal(t, acme.Name, inv.OrgName)
	require.Equal(t, []string{inv.ID}, invitationIDs(myInvitations(t, bob)))
	require.Empty(t, myInvitations(t, alice), "the inviter answers nothing")
	bob.Get("/api/v1/orgs/"+acme.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	org := testkit.Decode[dto.Organization](answerInvitation(bob, inv.ID, "accept").RequireStatus(http.StatusOK))
	require.Equal(t, acme.ID, org.ID)
	require.Equal(t, dto.PermissionMember, org.Permission)
	require.Empty(t, myInvitations(t, bob), "an accepted invitation is gone")
	require.Empty(t, orgInvitations(t, alice, acme.ID))
	bob.Get("/api/v1/orgs/" + acme.ID).RequireStatus(http.StatusOK)

	room := testkit.Decode[dto.Room](bob.Post("/api/v1/groups", dto.CreateGroupRequest{Name: "Launch", OrgID: acme.ID}).RequireStatus(http.StatusCreated))
	require.Equal(t, acme.ID, room.OrgID)

	sendInvitation(bob, acme.ID, "carol", dto.PermissionMember, "").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	sendInvitation(bob, acme.ID, "carol", dto.PermissionGuest, "").RequireStatus(http.StatusCreated)
	require.Equal(t, dto.PermissionMember, listMembers(t, alice, acme.ID)[bob.User.ID].Permission)
}

// OMEM-02: accepting makes the invitee a member with the invitation's permission and position, and a guest
// carries the identity that invited it.
func TestAcceptingGivesThePermissionAndPositionOfTheInvitation(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	adam := env.NewUser("adam")
	gina := env.NewUser("gina")

	byAdmin := invited(t, r.Admin, r.Org.ID, "adam", dto.PermissionAdmin, r.ManagerPosition)
	org := testkit.Decode[dto.Organization](answerInvitation(adam, byAdmin.ID, "accept").RequireStatus(http.StatusOK))
	require.Equal(t, dto.PermissionAdmin, org.Permission)
	require.Equal(t, r.ManagerPosition, org.PositionID)
	member := listMembers(t, r.Admin, r.Org.ID)[adam.User.ID]
	require.Equal(t, dto.PermissionAdmin, member.Permission)
	require.Equal(t, "Manager", member.PositionName)
	require.Nil(t, member.InvitedBy, "only a guest has an inviter")

	byMember := invited(t, r.Member, r.Org.ID, "gina", dto.PermissionGuest, "")
	org = testkit.Decode[dto.Organization](answerInvitation(gina, byMember.ID, "accept").RequireStatus(http.StatusOK))
	require.Equal(t, dto.PermissionGuest, org.Permission)
	guest := listMembers(t, r.Admin, r.Org.ID)[gina.User.ID]
	require.NotNil(t, guest.InvitedBy)
	require.Equal(t, r.Member.User.ID, guest.InvitedBy.ID, "the guest is its inviter's responsibility")
	// The new admin looks after the organization at once.
	adam.Patch(memberPath(r.Org.ID, r.Peer.User.ID), toPermission(dto.PermissionGuest)).RequireStatus(http.StatusOK)
}

// OMEM-01: only the invitee answers; the inviter, an admin of the organization, another member, an outsider
// and the instance admin are told 404, and the invitation stays for the invitee. Declining deletes it.
func TestOnlyTheInviteeAnswersAnInvitation(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	bob := env.NewUser("bob")
	inv := invited(t, r.Admin, r.Org.ID, "bob", dto.PermissionMember, "")

	for _, role := range r.All() {
		for _, verb := range []string{"accept", "decline"} {
			answerInvitation(bound(t, role.Client), inv.ID, verb).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		}
	}
	answerInvitation(bob, "no-such-invitation", "accept").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Equal(t, []string{inv.ID}, invitationIDs(myInvitations(t, bob)), "still waiting for its invitee")
	require.Empty(t, myInvitations(t, r.Admin))

	answerInvitation(bob, inv.ID, "decline").RequireStatus(http.StatusNoContent)
	require.Empty(t, myInvitations(t, bob))
	require.Empty(t, orgInvitations(t, r.Admin, r.Org.ID))
	answerInvitation(bob, inv.ID, "decline").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	answerInvitation(bob, inv.ID, "accept").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	bob.Get("/api/v1/orgs/"+r.Org.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	// Declining is not the end: the admin can invite again.
	again := invited(t, r.Admin, r.Org.ID, "bob", dto.PermissionMember, "")
	answerInvitation(bob, again.ID, "accept").RequireStatus(http.StatusOK)
}

// OMEM-01: an invited AI is answered by the identities that manage it in its home organization - its
// creator and whoever is above - and by nobody else; the AI joins the inviting organization as invited.
func TestAnInvitedAIIsAnsweredByItsManagers(t *testing.T) {
	env := testkit.New(t)
	acme := env.OrgRoles("acme")
	globex := env.OrgRoles("globex")
	ai := globex.TargetAI // hosted and created by globex's Manager, held by no position

	inv := invited(t, acme.Inviter, acme.Org.ID, handleOfID(t, env, ai), dto.PermissionGuest, "")
	require.Equal(t, ai, inv.Invitee.ID)
	require.Equal(t, dto.IntelligenceKindAI, inv.Invitee.Kind)
	require.Equal(t, dto.IntelligenceRoleAI, inv.Invitee.Role)

	for _, role := range globex.All() {
		got := invitationIDs(myInvitations(t, bound(t, role.Client)))
		switch role.Name {
		case "manager", "admin": // its creator, and the root position above the creator
			require.Equal(t, []string{inv.ID}, got, role.Name)
		default:
			require.Empty(t, got, role.Name)
		}
	}
	require.Empty(t, myInvitations(t, acme.Inviter), "the inviter does not answer it")
	require.Empty(t, myInvitations(t, acme.Admin))
	require.Equal(t, []string{inv.ID}, invitationIDs(orgInvitations(t, acme.Admin, acme.Org.ID)), "the admins of the inviting organization list it")

	for _, name := range []string{"target", "peer", "member", "inviter", "guest", "outsider", "instance_admin"} {
		for _, verb := range []string{"accept", "decline"} {
			answerInvitation(bound(t, roleClient(t, globex, name)), inv.ID, verb).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		}
	}
	// The AI is not a member of acme yet, and answering the invitation of acme's own people changes nothing.
	require.NotContains(t, listMembers(t, acme.Admin, acme.Org.ID), ai)
	answerInvitation(acme.Admin, inv.ID, "accept").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	org := testkit.Decode[dto.Organization](answerInvitation(globex.Manager, inv.ID, "accept").RequireStatus(http.StatusOK))
	require.Equal(t, acme.Org.ID, org.ID)
	require.Equal(t, dto.PermissionGuest, org.Permission, "the AI's membership: a guest")
	joined := listMembers(t, acme.Admin, acme.Org.ID)[ai]
	require.Equal(t, dto.IntelligenceKindAI, joined.Intelligence.Kind)
	require.Equal(t, dto.PermissionGuest, joined.Permission)
	require.NotNil(t, joined.InvitedBy)
	require.Equal(t, acme.Inviter.User.ID, joined.InvitedBy.ID)
	require.Empty(t, myInvitations(t, globex.Manager))
	require.Empty(t, myInvitations(t, globex.Admin))

	// Its admin may decline for it as well.
	other := env.OrgRoles("initech")
	declined := invited(t, other.Admin, other.Org.ID, handleOfID(t, env, ai), dto.PermissionMember, "")
	answerInvitation(globex.Admin, declined.ID, "decline").RequireStatus(http.StatusNoContent)
	require.Empty(t, myInvitations(t, globex.Manager))
	require.NotContains(t, listMembers(t, other.Admin, other.Org.ID), ai)
}

// OMEM-01: inviting again updates the pending invitation instead of adding another - the permission, the
// position and the inviter change, the id stays - and whoever may invite only guests cannot replace an
// invitation for more.
func TestInvitingAgainUpdatesThePendingInvitation(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	bob := env.NewUser("bob")

	first := invited(t, r.Admin, r.Org.ID, "bob", dto.PermissionGuest, "")
	second := invited(t, r.Admin, r.Org.ID, "bob", dto.PermissionAdmin, r.ManagerPosition)
	require.Equal(t, first.ID, second.ID, "the same invitation")
	require.Equal(t, dto.PermissionAdmin, second.Permission)
	require.Equal(t, "Manager", second.PositionName)
	items := orgInvitations(t, r.Admin, r.Org.ID)
	require.Len(t, items, 1, "one pending invitation per invitee")
	require.Equal(t, dto.PermissionAdmin, items[0].Permission)
	mine := myInvitations(t, bob)
	require.Len(t, mine, 1)
	require.Equal(t, dto.PermissionAdmin, mine[0].Permission)

	// A member cannot replace an invitation for an admin with one for a guest.
	sendInvitation(r.Member, r.Org.ID, "bob", dto.PermissionGuest, "").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	require.Equal(t, dto.PermissionAdmin, orgInvitations(t, r.Admin, r.Org.ID)[0].Permission)

	org := testkit.Decode[dto.Organization](answerInvitation(bob, first.ID, "accept").RequireStatus(http.StatusOK))
	require.Equal(t, dto.PermissionAdmin, org.Permission, "the updated terms")
	require.Equal(t, r.ManagerPosition, org.PositionID)
}

// OMEM-01: a guest invitation can be sent again by someone else, and then the latest inviter is the guest's.
// A member and a guest each invite a guest, and the chain of guests the design describes is recorded: every
// guest answers to the one who invited it, and the first inviter manages the whole chain.
func TestGuestsInviteGuestsInAChain(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	g1 := env.NewUser("gabe")
	g2 := env.NewUser("gail")
	g3 := env.NewUser("gus")

	for _, step := range []struct {
		by, guest *testkit.Client
	}{{r.Inviter, g1}, {g1, g2}, {g2, g3}} {
		inv := invited(t, step.by, r.Org.ID, step.guest.User.Username, dto.PermissionGuest, "")
		require.Equal(t, step.by.User.ID, inv.InvitedBy.ID)
		org := testkit.Decode[dto.Organization](answerInvitation(step.guest, inv.ID, "accept").RequireStatus(http.StatusOK))
		require.Equal(t, dto.PermissionGuest, org.Permission)
	}
	members := listMembers(t, r.Member, r.Org.ID)
	require.Equal(t, r.Inviter.User.ID, members[g1.User.ID].InvitedBy.ID)
	require.Equal(t, g1.User.ID, members[g2.User.ID].InvitedBy.ID)
	require.Equal(t, g2.User.ID, members[g3.User.ID].InvitedBy.ID)
	for _, manager := range []*testkit.Client{r.Inviter, g1, g2} {
		ok, err := env.App.Authz.Manages(context.Background(), manager.User.ID, g3.User.ID, r.Org.ID)
		require.NoError(t, err)
		require.True(t, ok, "%s is up g3's inviter chain", manager.User.Username)
	}
	ok, err := env.App.Authz.Manages(context.Background(), r.Peer.User.ID, g3.User.ID, r.Org.ID)
	require.NoError(t, err)
	require.False(t, ok)

	// Re-sending a pending guest invitation makes the new sender the guest's inviter.
	late := env.NewUser("lara")
	invited(t, r.Inviter, r.Org.ID, "lara", dto.PermissionGuest, "")
	again := invited(t, r.Member, r.Org.ID, "lara", dto.PermissionGuest, "")
	answerInvitation(late, again.ID, "accept").RequireStatus(http.StatusOK)
	require.Equal(t, r.Member.User.ID, listMembers(t, r.Admin, r.Org.ID)[late.User.ID].InvitedBy.ID)
}

// OMEM-01 and its refusals, each observed at its status (L-017): an identity that already is a member (409
// already_member, whoever invites), an unknown handle (404 contact_not_found), a permission or position
// that does not exist (422), a body that is not JSON (400) and an unknown organization (404). The
// authorization comes before the validation, and a refused request invites nobody.
func TestInviteRefusals(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	other := env.OrgRoles("globex")
	env.NewUser("sam")

	// Already a member, with any permission: a person in a position, a guest, an admin, the inviter, an AI.
	members := []string{
		r.Peer.User.Username, r.Guest.User.Username, r.Admin.User.Username, r.Member.User.Username, handleOfID(t, env, r.TargetAI),
	}
	for _, member := range members {
		sendInvitation(r.Admin, r.Org.ID, member, dto.PermissionMember, "").RequireError(http.StatusConflict, dto.ErrorCodeAlreadyMember)
		sendInvitation(r.Member, r.Org.ID, "@"+strings.ToUpper(member), dto.PermissionGuest, "").RequireError(http.StatusConflict, dto.ErrorCodeAlreadyMember)
	}
	// A member of another organization is not a member of this one.
	invited(t, r.Admin, r.Org.ID, other.Peer.User.Username, dto.PermissionMember, "")

	for _, handle := range []string{"nobody-by-that-name", "", "  ", "@"} {
		sendInvitation(r.Admin, r.Org.ID, handle, dto.PermissionMember, "").RequireError(http.StatusNotFound, dto.ErrorCodeContactNotFound)
		sendInvitation(r.Member, r.Org.ID, handle, dto.PermissionGuest, "").RequireError(http.StatusNotFound, dto.ErrorCodeContactNotFound)
	}

	for name, tc := range map[string]struct {
		perm     dto.Permission
		position string
		field    string
	}{
		"a permission that does not exist": {dto.Permission("owner"), "", "permission"},
		"no permission":                    {"", "", "permission"},
		"a position that does not exist":   {dto.PermissionMember, "no-such-position", "position_id"},
		"a position of another org":        {dto.PermissionMember, other.WorkerPosition, "position_id"},
	} {
		body := sendInvitation(r.Admin, r.Org.ID, "sam", tc.perm, tc.position).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, body.Fields, tc.field, name)
		// A member is told 403 before anything is validated.
		sendInvitation(r.Member, r.Org.ID, "sam", tc.perm, tc.position).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	}

	r.Admin.DoRaw(http.MethodPost, invitationsPath(r.Org.ID), "application/json", strings.NewReader("{nope")).
		RequireError(http.StatusBadRequest, dto.ErrorCodeInvalid)
	sendInvitation(r.Admin, "no-such-org", "sam", dto.PermissionGuest, "").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	sendInvitation(r.Admin, "ACME", "sam", dto.PermissionGuest, "").RequireStatus(http.StatusCreated) // {org} is an id or an address

	require.Len(t, orgInvitations(t, r.Admin, r.Org.ID), 2, "only the two valid invitations were sent")
}

// OMEM-01, sweep (auth boundaries and rate limits): an organization sends at most 30 invitations per hour,
// whoever sends them - the 31st is 429 rate_limited - and the limit is the organization's own.
func TestInvitationsAreRateLimitedPerOrganization(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	other := env.OrgRoles("globex")
	env.NewUser("sam")

	for range 30 {
		sendInvitation(r.Admin, r.Org.ID, "sam", dto.PermissionGuest, "").RequireStatus(http.StatusCreated) // a new send each time
	}
	sendInvitation(r.Admin, r.Org.ID, "sam", dto.PermissionGuest, "").RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	sendInvitation(r.Member, r.Org.ID, "sam", dto.PermissionGuest, "").RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	invited(t, other.Admin, other.Org.ID, "sam", dto.PermissionGuest, "")
	require.Len(t, orgInvitations(t, r.Admin, r.Org.ID), 1, "the same invitee, one pending invitation")
}

// OMEM-07: an invitation waits 14 days. At 14 days minus a millisecond it can still be accepted; at 14 days it
// is gone (410): neither answer works, no list shows it, and inviting again starts the 14 days over.
func TestInvitationsExpireAfterFourteenDays(t *testing.T) {
	clock := testkit.NewClock(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))
	env := testkit.New(t, testkit.WithClock(clock.Now))
	alice := env.NewUser("alice")
	bob := env.NewUser("bob")
	carol := env.NewUser("carol")
	org := alice.PersonalOrg()

	last := invited(t, alice, org.ID, "bob", dto.PermissionMember, "")
	expiring := invited(t, alice, org.ID, "carol", dto.PermissionMember, "")
	require.Equal(t, clock.Now().Add(14*24*time.Hour), expiring.ExpiresAt)

	clock.Advance(14*24*time.Hour - time.Millisecond)
	require.Len(t, myInvitations(t, bob), 1)
	answerInvitation(bob, last.ID, "accept").RequireStatus(http.StatusOK)

	clock.Advance(time.Millisecond)
	require.Empty(t, myInvitations(t, carol), "expired invitations are not listed")
	require.Empty(t, orgInvitations(t, alice, org.ID))
	answerInvitation(carol, expiring.ID, "accept").RequireError(http.StatusGone, dto.ErrorCodeGone)
	answerInvitation(carol, expiring.ID, "decline").RequireError(http.StatusGone, dto.ErrorCodeGone)
	carol.Get("/api/v1/orgs/"+org.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	// Inviting again starts over, with the same invitation.
	again := invited(t, alice, org.ID, "carol", dto.PermissionMember, "")
	require.Equal(t, expiring.ID, again.ID)
	require.Equal(t, clock.Now().Add(14*24*time.Hour), again.ExpiresAt)
	clock.Advance(10 * 24 * time.Hour)
	resent := invited(t, alice, org.ID, "carol", dto.PermissionMember, "")
	require.Equal(t, clock.Now().Add(14*24*time.Hour), resent.ExpiresAt, "sending it again restarts the 14 days")
	answerInvitation(carol, resent.ID, "accept").RequireStatus(http.StatusOK)
}

// GET /invitations lists the invitations of every organization, newest first.
func TestMyInvitationsAreNewestFirst(t *testing.T) {
	clock := testkit.NewClock(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))
	env := testkit.New(t, testkit.WithClock(clock.Now))
	alice := env.NewUser("alice")
	dave := env.NewUser("dave")
	bob := env.NewUser("bob")

	older := invited(t, alice, alice.PersonalOrg().ID, "bob", dto.PermissionMember, "")
	clock.Advance(time.Hour)
	newer := invited(t, dave, dave.PersonalOrg().ID, "bob", dto.PermissionGuest, "")
	require.Equal(t, []string{newer.ID, older.ID}, invitationIDs(myInvitations(t, bob)))
	require.Equal(t, "dave", myInvitations(t, bob)[0].OrgSlug)
	require.Equal(t, "alice", myInvitations(t, bob)[1].OrgSlug)
}

// OMEM-02 and the lifetime of an invitation: an invitee that became a member another way is told 409
// already_member and the stale invitation goes; an invitation for an admin whose inviter is no longer an admin
// is gone (410).
func TestAnInvitationOnlyHoldsWhileItsTermsDo(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	bob := env.NewUser("bob")
	zoe := env.NewUser("zoe")

	inv := invited(t, r.Admin, r.Org.ID, "bob", dto.PermissionMember, "")
	env.AddMember(r.Org.ID, bob.User.ID, orgs.PermissionGuest, "")
	answerInvitation(bob, inv.ID, "accept").RequireError(http.StatusConflict, dto.ErrorCodeAlreadyMember)
	require.Empty(t, myInvitations(t, bob), "the stale invitation went")
	require.Equal(t, dto.PermissionGuest, listMembers(t, r.Admin, r.Org.ID)[bob.User.ID].Permission, "nothing else changed")

	// Another admin demotes the inviter before the invitee answers.
	lapsing := invited(t, r.Admin, r.Org.ID, "zoe", dto.PermissionAdmin, "")
	r.Manager.Patch(memberPath(r.Org.ID, r.Admin.User.ID), toPermission(dto.PermissionMember)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	r.Admin.Patch(memberPath(r.Org.ID, r.Manager.User.ID), toPermission(dto.PermissionAdmin)).RequireStatus(http.StatusOK)
	r.Manager.Patch(memberPath(r.Org.ID, r.Admin.User.ID), toPermission(dto.PermissionMember)).RequireStatus(http.StatusOK)
	answerInvitation(zoe, lapsing.ID, "accept").RequireError(http.StatusGone, dto.ErrorCodeGone)
	zoe.Get("/api/v1/orgs/"+r.Org.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Empty(t, myInvitations(t, zoe))
}

// OMEM-01 (live): the invitee hears of an invitation, and for an invited AI so do the identities that
// manage it; nobody else does. Inviting again sends it again, with the new terms.
func TestInvitationReceivedReachesTheInviteeAndTheManagersOfAnInvitedAI(t *testing.T) {
	env := testkit.New(t)
	acme := env.OrgRoles("acme")
	globex := env.OrgRoles("globex")
	bob := env.NewUser("bob")
	carol := env.NewUser("carol")
	dial := func(c *testkit.Client) *testkit.LiveConn {
		t.Helper()
		l, _ := bound(t, c).DialLive("")
		require.NotNil(t, l)
		return l
	}
	bobLive, carolLive := dial(bob), dial(carol)
	managerLive, adminLive, memberLive := dial(globex.Manager), dial(globex.Admin), dial(globex.Member)
	noInvitation := func(name string, l *testkit.LiveConn) {
		t.Helper()
		for _, f := range l.Drain(quiet) {
			require.NotEqual(t, dto.LiveEventTypeInvitationReceived, f.Type, "%s heard of an invitation: %s", name, f.Data)
		}
	}

	var got dto.Invitation
	inv := invited(t, acme.Admin, acme.Org.ID, "bob", dto.PermissionMember, "")
	bobLive.Expect(dto.LiveEventTypeInvitationReceived, &got, liveWait)
	require.Equal(t, inv, got)
	for name, l := range map[string]*testkit.LiveConn{"carol": carolLive, "manager": managerLive, "admin": adminLive, "member": memberLive} {
		noInvitation(name, l)
	}

	ai := globex.TargetAI
	forAI := invited(t, acme.Admin, acme.Org.ID, handleOfID(t, env, ai), dto.PermissionGuest, "")
	managerLive.Expect(dto.LiveEventTypeInvitationReceived, &got, liveWait)
	require.Equal(t, forAI, got)
	got = dto.Invitation{}
	adminLive.Expect(dto.LiveEventTypeInvitationReceived, &got, liveWait)
	require.Equal(t, forAI, got, "whoever manages the AI hears of it")
	noInvitation("member", memberLive)
	noInvitation("carol", carolLive)
	noInvitation("bob", bobLive)

	again := invited(t, acme.Admin, acme.Org.ID, "bob", dto.PermissionAdmin, "")
	got = dto.Invitation{}
	bobLive.Expect(dto.LiveEventTypeInvitationReceived, &got, liveWait)
	require.Equal(t, again, got)
	require.Equal(t, dto.PermissionAdmin, got.Permission, "the new terms")
	require.Equal(t, inv.ID, got.ID)
}

// The invitation routes are for people: an agent token never reaches them, and nobody anonymous does.
func TestAgentTokenCannotReachInvitationRoutes(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	env.NewUser("sam")
	tok := r.Admin.Token(dto.TokenKindAgent, "t")
	tok.Get("/api/v1/invitations").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	tok.Get(invitationsPath(r.Org.ID)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	sendInvitation(tok, r.Org.ID, "sam", dto.PermissionGuest, "").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	answerInvitation(tok, "any", "accept").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	require.Empty(t, orgInvitations(t, r.Admin, r.Org.ID))

	anon := env.Anon()
	anon.Get("/api/v1/invitations").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	answerInvitation(anon, "any", "accept").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	sendInvitation(anon, r.Org.ID, "sam", dto.PermissionGuest, "").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
}
