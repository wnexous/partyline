package api_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/testkit"
)

// createGroup calls POST /groups as c.
func createGroup(c *testkit.Client, name string, members ...string) *testkit.Response {
	c.T.Helper()
	return c.Post("/api/v1/groups", dto.CreateGroupRequest{Name: name, Members: members})
}

func roomListOf(t *testing.T, c *testkit.Client) []dto.Room {
	t.Helper()
	return testkit.Decode[dto.List[dto.Room]](c.Get("/api/v1/rooms").RequireStatus(http.StatusOK)).Items
}

// LAY-04, ORG-03: a group is created by the user, in their organization, with every chosen user and AI of
// it as a member; the members see it and the chosen users post in it, while the owners of the member AIs,
// who were not chosen, and strangers do not.
func TestCreateGroupWithMembers(t *testing.T) {
	env := testkit.New(t)
	alice, bob, carol, dave := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol"), env.NewUser("dave")
	atlas := env.NewAI(alice, "atlas")
	orion := env.NewAI(carol, "orion")
	acme := alice.PersonalOrg().ID
	env.AddMember(acme, bob.User.ID, orgs.PermissionMember, "")
	env.AddMember(acme, orion, orgs.PermissionMember, "") // carol's AI works in alice's organization; carol does not

	room := testkit.Decode[dto.Room](createGroup(alice, "Launch", bob.User.ID, atlas, orion).RequireStatus(http.StatusCreated))
	require.Equal(t, dto.ConversationKindGroup, room.Kind)
	require.Equal(t, "Launch", room.Name)
	require.Equal(t, alice.User.ID, room.Owner.ID)
	require.Equal(t, acme, room.OrgID)
	require.Equal(t, dto.LevelManage, room.MyLevel)
	require.Equal(t, 3, room.MemberCount)
	require.Equal(t, dto.VisibilityPrivate, room.Visibility)
	require.Empty(t, room.Participants)
	require.Equal(t, 90, room.RetentionDays, "the defaults of POST /rooms")

	members := membersOf(t, alice, room.ID)
	require.Len(t, members, 3)
	for id, kind := range map[string]dto.MemberKind{bob.User.ID: dto.MemberKindUser, atlas: dto.MemberKindAgent, orion: dto.MemberKindAgent} {
		require.Equal(t, kind, members[id].Kind, id)
	}
	require.Equal(t, dto.DeliveryModeAll, members[atlas].Delivery)
	require.Equal(t, dto.DeliveryModeAll, members[orion].Delivery)

	// The members see it: bob reads, lists and posts; the AI lists it among its rooms.
	seen := testkit.Decode[dto.Room](bob.Get("/api/v1/rooms/" + room.ID).RequireStatus(http.StatusOK))
	require.Equal(t, dto.LevelPost, seen.MyLevel)
	require.Equal(t, []string{room.ID}, roomIDs(roomListOf(t, bob)))
	bob.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "ready"}).RequireStatus(http.StatusCreated)
	atlasRooms := testkit.Decode[dto.List[dto.AgentRoom]](alice.Token(dto.TokenKindAgent, "laptop").AsAgent("atlas").Get("/api/v1/agent/rooms").RequireStatus(http.StatusOK))
	require.Len(t, atlasRooms.Items, 1)
	require.Equal(t, room.ID, atlasRooms.Items[0].ID)
	require.Len(t, atlasRooms.Items[0].Members, 3)

	// Neither the owner of a member AI who was not chosen nor a stranger can reach it.
	carol.Get("/api/v1/rooms/"+room.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	dave.Get("/api/v1/rooms/"+room.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Empty(t, roomListOf(t, dave))
	require.Equal(t, []string{room.ID}, roomIDs(roomListOf(t, alice)))

	// The owner may list themself; a group without members is fine; repeated ids count once.
	own := testkit.Decode[dto.Room](createGroup(alice, "Solo", alice.User.ID, alice.User.ID).RequireStatus(http.StatusCreated))
	require.Equal(t, 1, own.MemberCount)
	empty := testkit.Decode[dto.Room](createGroup(alice, "Empty").RequireStatus(http.StatusCreated))
	require.Zero(t, empty.MemberCount)
	require.NotNil(t, testkit.Decode[map[string]any](createGroup(alice, "Nil members").RequireStatus(http.StatusCreated))["participants"])
}

// LAY-04, AGRP-02: a member the creator may not add refuses the whole group with 422
// members_refused and the list of refused ids; nothing is created, for the valid members either.
// Each reason is tested on its own, so no other refusal can hide the one under test. (not_org_member is
// tested with the organization roles in groups_org_test.go.)
func TestCreateGroupRefusesTheMembersItsCreatorCannotAdd(t *testing.T) {
	env := testkit.New(t)
	admin := env.SetupAdmin()
	alice, bob, carol, dave := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol"), env.NewUser("dave")
	atlas := env.NewAI(alice, "atlas")
	orion := env.NewAI(bob, "orion") // an AI alice shares no organization with
	env.AddMember(alice.PersonalOrg().ID, bob.User.ID, orgs.PermissionMember, "")
	env.AddMember(alice.PersonalOrg().ID, carol.User.ID, orgs.PermissionMember, "")
	env.AddMember(alice.PersonalOrg().ID, dave.User.ID, orgs.PermissionMember, "") // then disabled
	connector, _ := storeChat(t, env, alice.User.ID, "chat")
	person := seedPerson(t, env, connector, "5511999990000", "Contact")
	disableUser(admin, dave.User.ID, true)
	unknown := store.NewID()

	refuse := func(t *testing.T, want []dto.RefusedMember, members ...string) dto.Error {
		t.Helper()
		e := createGroup(alice, "Launch", members...).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeMembersRefused)
		require.Equal(t, want, e.Refused)
		require.NotEmpty(t, e.Message)
		return e
	}
	t.Run("an AI the creator cannot see", func(t *testing.T) {
		refuse(t, []dto.RefusedMember{{ID: orion, Reason: dto.RefusedReasonNotFound}}, carol.User.ID, atlas, orion)
	})
	t.Run("an id that does not exist", func(t *testing.T) {
		refuse(t, []dto.RefusedMember{{ID: unknown, Reason: dto.RefusedReasonNotFound}}, bob.User.ID, unknown)
	})
	t.Run("a disabled user", func(t *testing.T) {
		refuse(t, []dto.RefusedMember{{ID: dave.User.ID, Reason: dto.RefusedReasonNotFound}}, bob.User.ID, dave.User.ID)
	})
	t.Run("an external person", func(t *testing.T) {
		refuse(t, []dto.RefusedMember{{ID: person, Reason: dto.RefusedReasonPerson}}, bob.User.ID, person)
	})
	t.Run("a person the caller cannot see is as unknown as any id", func(t *testing.T) {
		seen := createGroup(alice, "Launch", person).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeMembersRefused)
		hidden := createGroup(carol, "Launch", person).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeMembersRefused)
		missing := createGroup(carol, "Launch", store.NewID()).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeMembersRefused)
		require.Equal(t, dto.RefusedReasonPerson, seen.Refused[0].Reason, "the connector's owner is told it is a person")
		require.Equal(t, dto.RefusedReasonNotFound, hidden.Refused[0].Reason, "carol has no access to that connector")
		require.Equal(t, hidden.Message, missing.Message)
		require.Empty(t, roomListOf(t, carol), "nothing was created")
	})
	t.Run("the refusals look alike for a hidden AI and an id that does not exist", func(t *testing.T) {
		hidden := createGroup(alice, "Launch", orion).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeMembersRefused)
		missing := createGroup(alice, "Launch", store.NewID()).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeMembersRefused)
		require.Equal(t, hidden.Refused[0].Reason, missing.Refused[0].Reason)
		require.Equal(t, hidden.Message, missing.Message)
	})
	t.Run("all the refused members, in the order asked, and none of the valid ones", func(t *testing.T) {
		refuse(t, []dto.RefusedMember{
			{ID: person, Reason: dto.RefusedReasonPerson},
			{ID: orion, Reason: dto.RefusedReasonNotFound},
			{ID: dave.User.ID, Reason: dto.RefusedReasonNotFound},
		}, person, bob.User.ID, orion, atlas, dave.User.ID, orion)
	})

	for _, c := range []*testkit.Client{alice, bob, carol} {
		require.Empty(t, roomListOf(t, c), "nothing was created")
	}
	// An AI of the organization is addable, a use grant is not enough; so is the same request without the refused ones.
	shareAI(bob, orion, alice)
	createGroup(alice, "Launch", bob.User.ID, atlas, orion).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeMembersRefused)
	env.AddMember(alice.PersonalOrg().ID, orion, orgs.PermissionMember, "")
	ok := testkit.Decode[dto.Room](createGroup(alice, "Launch", bob.User.ID, atlas, orion).RequireStatus(http.StatusCreated))
	require.Equal(t, 3, ok.MemberCount)
}

// LAY-04: the fields are validated before the members are looked at, with the limits of rooms;
// at most 50 distinct members, repeated ids counting once.
func TestCreateGroupValidation(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	orion := env.NewAI(bob, "orion") // not shared: a refused member next to every invalid field

	for name, tc := range map[string]struct {
		req   dto.CreateGroupRequest
		field string
	}{
		"empty name":       {dto.CreateGroupRequest{Name: "  ", Members: []string{orion}}, "name"},
		"name over 100":    {dto.CreateGroupRequest{Name: strings.Repeat("n", 101), Members: []string{orion}}, "name"},
		"description over": {dto.CreateGroupRequest{Name: "ok", Description: strings.Repeat("d", 1001), Members: []string{orion}}, "description"},
		"blank member id":  {dto.CreateGroupRequest{Name: "ok", Members: []string{bob.User.ID, " "}}, "members"},
	} {
		t.Run(name, func(t *testing.T) {
			e := alice.Post("/api/v1/groups", tc.req).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
			require.Contains(t, e.Fields, tc.field)
		})
	}
	maxName := strings.Repeat("n", 100)
	testkit.Decode[dto.Room](createGroup(alice, maxName).RequireStatus(http.StatusCreated))

	t.Run("body", func(t *testing.T) {
		c := *alice
		c.T = t
		c.Do(http.MethodPost, "/api/v1/groups", map[string]any{"name": "ok", "members": "bob"}).RequireError(http.StatusBadRequest, dto.ErrorCodeInvalid)
		c.Do(http.MethodPost, "/api/v1/groups", map[string]any{"name": 7}).RequireError(http.StatusBadRequest, dto.ErrorCodeInvalid)
	})

	t.Run("member limit", func(t *testing.T) {
		ids := make([]string, 0, dto.MaxGroupMembers+1)
		for i := range dto.MaxGroupMembers + 1 {
			member := env.NewUser(fmt.Sprintf("member%02d", i))
			env.AddMember(alice.PersonalOrg().ID, member.User.ID, orgs.PermissionMember, "")
			ids = append(ids, member.User.ID)
		}
		e := createGroup(alice, "Big", ids...).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, e.Fields, "members")
		require.Empty(t, roomListOf(t, alice)[1:], "only the earlier group exists")

		repeated := slices.Concat(ids[:dto.MaxGroupMembers], []string{ids[0], ids[1]})
		ok := testkit.Decode[dto.Room](createGroup(alice, "Big", repeated...).RequireStatus(http.StatusCreated))
		require.Equal(t, dto.MaxGroupMembers, ok.MemberCount, "50 distinct members, two repeated ids")
	})
}

// Authorization of POST /groups: any user of the instance creates a group in their own organization,
// managing it; an agent token is not a user (403) and an anonymous caller is 401. (The organization roles
// are in groups_org_test.go.)
func TestCreateGroupAuthorization(t *testing.T) {
	m := newMatrix(t)
	for _, role := range matrixRoles {
		t.Run(string(role), func(t *testing.T) {
			c := m.as(t, role)
			room := testkit.Decode[dto.Room](createGroup(c, "Group of "+string(role)).RequireStatus(http.StatusCreated))
			require.Equal(t, c.User.ID, room.Owner.ID)
			require.Equal(t, dto.LevelManage, room.MyLevel)
		})
	}
	t.Run("agent token", func(t *testing.T) {
		agent := m.as(t, roleOwner).Token(dto.TokenKindAgent, "laptop")
		createGroup(agent, "From an AI token").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	})
	t.Run("anonymous", func(t *testing.T) {
		anon := m.env.Anon()
		anon.T = t
		createGroup(anon, "Nobody").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	})
}

// LAY-04, LIVE-02: the sockets of the chosen users learn of the new group, at the level a member
// has; those of other users hear nothing.
func TestCreateGroupAnnouncesTheRoomLive(t *testing.T) {
	env := testkit.New(t)
	alice, bob, carol := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol")
	env.AddMember(alice.PersonalOrg().ID, bob.User.ID, orgs.PermissionMember, "")
	bobLive, carolLive := dialLive(t, bob), dialLive(t, carol)

	room := testkit.Decode[dto.Room](createGroup(alice, "Launch", bob.User.ID).RequireStatus(http.StatusCreated))
	var seen dto.Room
	bobLive.Expect(dto.LiveEventTypeRoomChanged, &seen, liveWait)
	require.Equal(t, room.ID, seen.ID)
	require.Equal(t, dto.LevelPost, seen.MyLevel)
	carolLive.ExpectNone(quiet)

	alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "welcome"}).RequireStatus(http.StatusCreated)
	var msg dto.Message
	bobLive.Expect(dto.LiveEventTypeRoomMessage, &msg, liveWait)
	require.Equal(t, "welcome", msg.Text)
}
