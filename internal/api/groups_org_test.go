package api_test

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// orgGroups counts the groups that belong to the organization.
func orgGroups(t *testing.T, env *testkit.Env, orgID string) int64 {
	t.Helper()
	n, err := env.App.Store.R().CountOrgGroups(context.Background(), sql.NullString{String: orgID, Valid: true})
	require.NoError(t, err)
	return n
}

// ORG-03, OMEM-04: a group is made in an organization the creator is a member or an admin of, and belongs
// to it; a guest, someone who is not a member, an admin of the instance who is not one and an unknown
// organization are all refused with 403 forbidden, and nothing is created. Each refusal is the only thing
// wrong with an otherwise valid request.
func TestCreateGroupInAnOrganization(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	created := 0
	for _, tc := range []struct {
		role string
		c    *testkit.Client
		want int
	}{
		{"admin", roles.Admin, http.StatusCreated},
		{"manager", roles.Manager, http.StatusCreated},
		{"member", roles.Member, http.StatusCreated},
		{"inviter", roles.Inviter, http.StatusCreated},
		{"guest", roles.Guest, http.StatusForbidden},
		{"outsider", roles.Outsider, http.StatusForbidden},
		{"instance admin", roles.InstanceAdmin, http.StatusForbidden},
	} {
		t.Run(tc.role, func(t *testing.T) {
			c := *tc.c
			c.T = t
			resp := c.Post("/api/v1/groups", dto.CreateGroupRequest{Name: "Launch", OrgID: roles.Org.ID})
			if tc.want == http.StatusForbidden {
				resp.RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
				return
			}
			room := testkit.Decode[dto.Room](resp.RequireStatus(http.StatusCreated))
			created++
			require.Equal(t, roles.Org.ID, room.OrgID, "the group belongs to the organization")
			require.Equal(t, []any{dto.ConversationKindGroup, dto.LevelManage}, []any{room.Kind, room.MyLevel})
			again := testkit.Decode[dto.Room](c.Get("/api/v1/rooms/" + room.ID).RequireStatus(http.StatusOK))
			require.Equal(t, roles.Org.ID, again.OrgID)
			if tc.role != "admin" { // the admins of the organization run every group of it
				require.Equal(t, dto.LevelManage, testkit.Decode[dto.Room](roles.Admin.Get("/api/v1/rooms/"+room.ID).RequireStatus(http.StatusOK)).MyLevel)
			}
			roles.Peer.Get("/api/v1/rooms/"+room.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound) // a colleague outside the room
		})
	}
	require.EqualValues(t, created, orgGroups(t, env, roles.Org.ID), "the refused requests created nothing")
	require.Equal(t, 4, created)

	t.Run("an organization that does not exist is as forbidden as one the caller is not in", func(t *testing.T) {
		c := *roles.Member
		c.T = t
		c.Post("/api/v1/groups", dto.CreateGroupRequest{Name: "Launch", OrgID: store.NewID()}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		c.Post("/api/v1/groups", dto.CreateGroupRequest{Name: "Launch", OrgID: "no-such-org"}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	})
	t.Run("the organization is named by id or by slug", func(t *testing.T) {
		c := *roles.Member
		c.T = t
		bySlug := testkit.Decode[dto.Room](c.Post("/api/v1/groups", dto.CreateGroupRequest{Name: "By slug", Org: "acme"}).RequireStatus(http.StatusCreated))
		require.Equal(t, roles.Org.ID, bySlug.OrgID)
		bySlugInOrgID := testkit.Decode[dto.Room](c.Post("/api/v1/groups", dto.CreateGroupRequest{Name: "By org_id", OrgID: "ACME"}).RequireStatus(http.StatusCreated))
		require.Equal(t, roles.Org.ID, bySlugInOrgID.OrgID, "slugs ignore case")
	})
	t.Run("without an organization the group goes to the creator's default one", func(t *testing.T) {
		c := *roles.Member
		c.T = t
		personal := c.PersonalOrg().ID
		first := testkit.Decode[dto.Room](c.Post("/api/v1/groups", dto.CreateGroupRequest{Name: "Default"}).RequireStatus(http.StatusCreated))
		require.Equal(t, personal, first.OrgID)
		require.NoError(t, env.App.Orgs.SetDefault(context.Background(), c.User.ID, roles.Org.ID))
		second := testkit.Decode[dto.Room](c.Post("/api/v1/groups", dto.CreateGroupRequest{Name: "Default"}).RequireStatus(http.StatusCreated))
		require.Equal(t, roles.Org.ID, second.OrgID)
	})
	t.Run("the default organization of a guest does not let them create", func(t *testing.T) {
		c := *roles.Guest
		c.T = t
		require.NoError(t, env.App.Orgs.SetDefault(context.Background(), c.User.ID, roles.Org.ID))
		c.Post("/api/v1/groups", dto.CreateGroupRequest{Name: "Default"}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	})
	t.Run("a guest keeps the groups of their own organization", func(t *testing.T) {
		c := *roles.Guest
		c.T = t
		own := testkit.Decode[dto.Room](c.Post("/api/v1/groups", dto.CreateGroupRequest{Name: "Mine", OrgID: c.PersonalOrg().ID}).RequireStatus(http.StatusCreated))
		require.Equal(t, c.PersonalOrg().ID, own.OrgID)
	})
}

// ORG-03, AGRP-02: the members of a new group must belong to its organization, guests included (a guest
// added to a group posts in it): otherwise the whole request is refused with 422 members_refused naming
// them and the reason, and nothing is created. A user or AI of another organization is not_org_member; an
// AI the creator cannot see is not_found, as unknown as an id that does not exist. Each reason is tested
// on its own, so that no other refusal can hide the one under test.
func TestCreateGroupMembersMustBelongToTheOrganization(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	creator := roles.Member
	atlas := env.NewAIIn(creator, roles.Org.ID, "atlas")
	wanderer := env.NewAI(creator, "wanderer") // the creator's AI, but of the creator's own organization
	ghost := env.NewAI(roles.Outsider, "ghost")
	unknown := store.NewID()
	create := func(orgID string, members ...string) *testkit.Response {
		return creator.Post("/api/v1/groups", dto.CreateGroupRequest{Name: "Launch", OrgID: orgID, Members: members})
	}
	refuse := func(t *testing.T, want []dto.RefusedMember, members ...string) {
		t.Helper()
		c := *creator
		c.T = t
		e := c.Post("/api/v1/groups", dto.CreateGroupRequest{Name: "Launch", OrgID: roles.Org.ID, Members: members}).
			RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeMembersRefused)
		require.Equal(t, want, e.Refused)
	}

	t.Run("the members of the organization, guests and AIs included, join and post", func(t *testing.T) {
		room := testkit.Decode[dto.Room](create(roles.Org.ID, roles.Target.User.ID, roles.Peer.User.ID, roles.Guest.User.ID, roles.Manager.User.ID, atlas).RequireStatus(http.StatusCreated))
		require.Equal(t, 5, room.MemberCount)
		for _, c := range []*testkit.Client{roles.Target, roles.Guest, roles.Manager} {
			cc := *c
			cc.T = t
			cc.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "hello"}).RequireStatus(http.StatusCreated)
		}
		roles.Inviter.Get("/api/v1/rooms/"+room.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound) // a colleague who was not chosen
	})
	t.Run("a user who is not a member of the organization", func(t *testing.T) {
		refuse(t, []dto.RefusedMember{{ID: roles.Outsider.User.ID, Reason: dto.RefusedReasonNotOrgMember}}, roles.Target.User.ID, roles.Outsider.User.ID)
	})
	t.Run("a user of another organization the creator shares is still not a member of this one", func(t *testing.T) {
		side, err := env.App.Orgs.Create(context.Background(), creator.User.ID, "side")
		require.NoError(t, err)
		env.AddMember(side.ID, roles.Outsider.User.ID, orgs.PermissionMember, "")
		refuse(t, []dto.RefusedMember{{ID: roles.Outsider.User.ID, Reason: dto.RefusedReasonNotOrgMember}}, roles.Outsider.User.ID)
		room := testkit.Decode[dto.Room](create(side.ID, roles.Outsider.User.ID).RequireStatus(http.StatusCreated)) // of the right one it works
		require.Equal(t, side.ID, room.OrgID)
	})
	t.Run("an AI of the creator that belongs to another organization", func(t *testing.T) {
		refuse(t, []dto.RefusedMember{{ID: wanderer, Reason: dto.RefusedReasonNotOrgMember}}, wanderer)
	})
	t.Run("an AI the creator cannot see is as unknown as an id that does not exist", func(t *testing.T) {
		refuse(t, []dto.RefusedMember{{ID: ghost, Reason: dto.RefusedReasonNotFound}}, ghost)
		refuse(t, []dto.RefusedMember{{ID: unknown, Reason: dto.RefusedReasonNotFound}}, unknown)
	})
	t.Run("all the refused members, in the order asked, and none of the valid ones", func(t *testing.T) {
		refuse(t, []dto.RefusedMember{
			{ID: roles.Outsider.User.ID, Reason: dto.RefusedReasonNotOrgMember},
			{ID: ghost, Reason: dto.RefusedReasonNotFound},
			{ID: wanderer, Reason: dto.RefusedReasonNotOrgMember},
			{ID: unknown, Reason: dto.RefusedReasonNotFound},
		}, roles.Outsider.User.ID, roles.Target.User.ID, ghost, atlas, wanderer, unknown)
	})
}

// ORG-03, ACL-05 replaced (AD-016): adding one member to a group needs the same: a user or AI of the
// group's organization. A user who is not a member is 422 members_refused / not_org_member; an AI the
// manager cannot see, and an AI that does not exist, are 403 forbidden as before; an unknown user is a
// validation error as before.
func TestAddingAMemberNeedsOrganizationMembership(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	creator := roles.Member
	room := groupIn(t, env, creator, roles.Org.ID, "ops")
	path := "/api/v1/rooms/" + room.ID + "/members"
	atlas := env.NewAIIn(creator, roles.Org.ID, "atlas")
	wanderer := env.NewAI(creator, "wanderer")
	ghost := env.NewAI(roles.Outsider, "ghost")

	creator.Post(path, dto.AddMemberRequest{Kind: dto.MemberKindUser, MemberID: roles.Peer.User.ID}).RequireStatus(http.StatusCreated)
	creator.Post(path, dto.AddMemberRequest{Kind: dto.MemberKindUser, MemberID: roles.Guest.User.ID}).RequireStatus(http.StatusCreated)
	creator.Post(path, dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: atlas}).RequireStatus(http.StatusCreated)

	e := creator.Post(path, dto.AddMemberRequest{Kind: dto.MemberKindUser, MemberID: roles.Outsider.User.ID}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeMembersRefused)
	require.Equal(t, []dto.RefusedMember{{ID: roles.Outsider.User.ID, Reason: dto.RefusedReasonNotOrgMember}}, e.Refused)
	e = creator.Post(path, dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: wanderer}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeMembersRefused)
	require.Equal(t, []dto.RefusedMember{{ID: wanderer, Reason: dto.RefusedReasonNotOrgMember}}, e.Refused)
	creator.Post(path, dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: ghost}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	creator.Post(path, dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: store.NewID()}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	unknown := creator.Post(path, dto.AddMemberRequest{Kind: dto.MemberKindUser, MemberID: store.NewID()}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, unknown.Fields, "member_id")

	require.Len(t, testkit.Decode[dto.List[dto.RoomMember]](creator.Get(path).RequireStatus(http.StatusOK)).Items, 3, "only the three members of the organization joined")

	// The admins of the organization add members too, with the same rule.
	roles.Admin.Post(path, dto.AddMemberRequest{Kind: dto.MemberKindUser, MemberID: roles.Target.User.ID}).RequireStatus(http.StatusCreated)
	roles.Admin.Post(path, dto.AddMemberRequest{Kind: dto.MemberKindUser, MemberID: roles.Outsider.User.ID}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeMembersRefused)
}

// OMEM-04: a guest takes part in the groups it is added to - it reads and posts there, and the group is in
// its list - and talks to everyone in the organization: it opens a DM with any member, user or AI, and posts
// in it. It cannot create a group (403), nor run the one it is in (403 on a rename).
func TestAGuestTakesPartInGroupsAndTalksToTheOrganization(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	guest := roles.Guest
	room := groupIn(t, env, roles.Member, roles.Org.ID, "ops")
	addToRoom(t, env, roles.Member, room.ID, guest)

	seen := testkit.Decode[dto.Room](guest.Get("/api/v1/rooms/" + room.ID).RequireStatus(http.StatusOK))
	require.Equal(t, dto.LevelPost, seen.MyLevel)
	require.Equal(t, []string{room.ID}, roomIDs(roomListOf(t, guest)))
	guest.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "hello"}).RequireStatus(http.StatusCreated)
	guest.Patch("/api/v1/rooms/"+room.ID, dto.UpdateRoomRequest{Name: ptr("mine")}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	guest.Post("/api/v1/groups", dto.CreateGroupRequest{Name: "Nope", OrgID: roles.Org.ID}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)

	for _, other := range []*testkit.Client{roles.Admin, roles.Manager, roles.Peer, roles.Member, roles.Inviter} {
		dm := dmWith(guest, other.User.ID, http.StatusCreated)
		say(guest, dm.ID, "hi")
		say(other, dm.ID, "hello guest")
	}
	// ... and with the AIs of the organization, whoever hosts them.
	withAI := dmWith(guest, roles.TargetAI, http.StatusCreated)
	say(guest, withAI.ID, "hello AI")
	// A stranger to the organization gets nothing of the kind.
	dmWithError := openDM(guest, roles.Outsider.NewAI("elsewhere"))
	dmWithError.RequireError(http.StatusNotFound, dto.ErrorCodeContactNotFound)
}

// AGRP-01, ORG-03, OMEM-04: an AI acts as itself when it creates a group (create_group). Without org the
// group goes to its home organization; with one it must be a member or an admin there - a guest AI is
// refused whatever the organization, and so is an organization it does not belong to or that does not
// exist, with 403 forbidden and nothing created. The response names the organization.
func TestAnAIAsItselfCreatesGroupsInItsOrganizations(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	owner := roles.Member
	atlas := env.NewAIIn(owner, roles.Org.ID, "atlas")
	side, err := env.App.Orgs.Create(context.Background(), owner.User.ID, "side")
	require.NoError(t, err)
	env.AddMember(side.ID, atlas, orgs.PermissionMember, "") // atlas belongs to a second organization
	tok := owner.Token(dto.TokenKindAgent, "laptop").AsAgent("atlas")
	ask := func(c *testkit.Client, req dto.CreateGroupRequest) *testkit.Response {
		return c.Post("/api/v1/agent/groups", req)
	}

	home := testkit.Decode[dto.CreatedGroup](ask(tok, dto.CreateGroupRequest{Name: "Home", Members: []string{roles.Target.User.ID}}).RequireStatus(http.StatusCreated))
	require.Equal(t, []any{roles.Org.ID, roles.Org.Slug, roles.Org.Name}, []any{home.OrgID, home.OrgSlug, home.OrgName}, "without org: the AI's home organization")
	other := testkit.Decode[dto.CreatedGroup](ask(tok, dto.CreateGroupRequest{Name: "Side", Org: "side"}).RequireStatus(http.StatusCreated))
	require.Equal(t, []any{side.ID, side.Slug}, []any{other.OrgID, other.OrgSlug}, "with org: the organization it names, by slug")
	byID := testkit.Decode[dto.CreatedGroup](ask(tok, dto.CreateGroupRequest{Name: "Side again", Org: side.ID}).RequireStatus(http.StatusCreated))
	require.Equal(t, side.ID, byID.OrgID, "or by id")
	require.Equal(t, roles.Org.ID, testkit.Decode[dto.Room](roles.Admin.Get("/api/v1/rooms/"+home.Room).RequireStatus(http.StatusOK)).OrgID)
	require.Equal(t, dto.LevelManage, testkit.Decode[dto.Room](roles.Admin.Get("/api/v1/rooms/"+home.Room).RequireStatus(http.StatusOK)).MyLevel,
		"the admins of the organization run the group the AI made")
	roles.Member.Get("/api/v1/rooms/"+home.Room).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound) // its host is no member: the AI is not its owner's hand

	before := orgGroups(t, env, roles.Org.ID)
	t.Run("an organization it does not belong to", func(t *testing.T) {
		c := *tok
		c.T = t
		ask(&c, dto.CreateGroupRequest{Name: "Nope", Org: roles.Outsider.PersonalOrg().ID}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	})
	t.Run("an unknown organization", func(t *testing.T) {
		c := *tok
		c.T = t
		ask(&c, dto.CreateGroupRequest{Name: "Nope", Org: store.NewID()}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	})

	// A guest AI cannot create groups, in its home organization or in any other.
	require.NoError(t, env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
		for _, orgID := range []string{roles.Org.ID, side.ID} {
			if err := q.UpdateOrgMemberPermission(context.Background(), db.UpdateOrgMemberPermissionParams{
				Permission: string(orgs.PermissionGuest), InviterIid: sql.NullString{String: owner.User.ID, Valid: true}, OrgID: orgID, Iid: atlas,
			}); err != nil {
				return err
			}
		}
		return nil
	}))
	ask(tok, dto.CreateGroupRequest{Name: "Nope"}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	ask(tok, dto.CreateGroupRequest{Name: "Nope", Org: "side"}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	require.Equal(t, before, orgGroups(t, env, roles.Org.ID), "the refused requests created nothing")

	// Another user's AI creates in its own home, and in no organization it does not belong to.
	roles.Outsider.NewAI("elsewhere")
	stranger := roles.Outsider.Token(dto.TokenKindAgent, "desk").AsAgent("elsewhere")
	foreign := testkit.Decode[dto.CreatedGroup](ask(stranger, dto.CreateGroupRequest{Name: "Theirs"}).RequireStatus(http.StatusCreated))
	require.Equal(t, roles.Outsider.PersonalOrg().ID, foreign.OrgID)
	ask(stranger, dto.CreateGroupRequest{Name: "Nope", Org: roles.Org.ID}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
}
