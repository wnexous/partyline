package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// openDM calls POST /conversations/dm as c.
func openDM(c *testkit.Client, with string) *testkit.Response {
	c.T.Helper()
	return c.Post("/api/v1/conversations/dm", dto.OpenDMRequest{With: with})
}

// dmWith opens the DM and decodes the room, requiring status (201 created, 200 existing).
func dmWith(c *testkit.Client, with string, status int) dto.Room {
	c.T.Helper()
	return testkit.Decode[dto.Room](openDM(c, with).RequireStatus(status))
}

// shareAI gives user use on the AI, as its owner would from the web app.
func shareAI(owner *testkit.Client, aiID string, users ...*testkit.Client) {
	owner.T.Helper()
	shared := make([]dto.SharingUser, len(users))
	for i, u := range users {
		shared[i] = dto.SharingUser{UserID: u.User.ID, Level: dto.LevelUse}
	}
	owner.Put("/api/v1/agents/"+aiID+"/sharing", dto.Sharing{Mode: dto.VisibilityShared, Users: shared}).RequireStatus(http.StatusOK)
}

func disableUser(admin *testkit.Client, userID string, disabled bool) {
	admin.T.Helper()
	admin.Patch("/api/v1/admin/users/"+userID, dto.UpdateUserRequest{Disabled: &disabled}).RequireStatus(http.StatusOK)
}

func roomIDs(rs []dto.Room) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}

// DM-01: opening a DM creates it (201) and opening it again, from either side, returns the same
// room (200). A DM is a room of kind dm without a name whose participants are its two
// intelligences, and each of them lists it.
func TestOpenDMReturnsOneRoomPerPair(t *testing.T) {
	env := testkit.New(t)
	alice, bob, carol := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol")

	created := dmWith(alice, bob.User.ID, http.StatusCreated)
	require.Equal(t, dto.ConversationKindDM, created.Kind)
	require.Empty(t, created.Name, "a DM has no name; the other participant titles it")
	require.Equal(t, dto.LevelPost, created.MyLevel)
	require.Equal(t, dto.VisibilityPrivate, created.Visibility)
	require.Equal(t, 2, created.MemberCount)
	require.Equal(t, alice.User.ID, created.Owner.ID, "the user who opened it")
	want := []string{alice.User.ID, bob.User.ID}
	sort.Strings(want)
	got := make([]string, len(created.Participants))
	for i, p := range created.Participants {
		got[i] = p.ID
		require.Equal(t, dto.IntelligenceRoleUser, p.Role)
		require.Equal(t, dto.IntelligenceKindHuman, p.Kind)
		require.NotEmpty(t, p.Handle)
		require.NotEmpty(t, p.DisplayName)
	}
	require.Equal(t, want, got, "participants are the two intelligences in id order")

	require.Equal(t, created.ID, dmWith(alice, bob.User.ID, http.StatusOK).ID, "again")
	fromBob := dmWith(bob, alice.User.ID, http.StatusOK)
	require.Equal(t, created.ID, fromBob.ID, "the pair is the same from the other side")
	require.Equal(t, dto.LevelPost, fromBob.MyLevel)

	for _, c := range []*testkit.Client{alice, bob} {
		list := testkit.Decode[dto.List[dto.Room]](c.Get("/api/v1/rooms").RequireStatus(http.StatusOK)).Items
		require.Equal(t, []string{created.ID}, roomIDs(list), "each participant lists the DM")
		require.Equal(t, dto.ConversationKindDM, list[0].Kind)
	}
	members := membersOf(t, alice, created.ID)
	require.Len(t, members, 2)
	for _, id := range []string{alice.User.ID, bob.User.ID} {
		require.Equal(t, dto.MemberKindUser, members[id].Kind)
	}

	// Another pair is another DM; both can talk in theirs and nobody else reads it.
	other := dmWith(carol, alice.User.ID, http.StatusCreated)
	require.NotEqual(t, created.ID, other.ID)
	alice.Post("/api/v1/rooms/"+created.ID+"/messages", dto.PostMessageRequest{Text: "hello bob"}).RequireStatus(http.StatusCreated)
	bob.Post("/api/v1/rooms/"+created.ID+"/messages", dto.PostMessageRequest{Text: "hello alice"}).RequireStatus(http.StatusCreated)
	require.Len(t, timelineOf(t, alice, created.ID), 2)
	carol.Get("/api/v1/rooms/"+created.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	carol.Get("/api/v1/rooms/"+created.ID+"/messages").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Empty(t, timelineOf(t, carol, other.ID), "carol's DM with alice is a different, empty conversation")

	// A group stays a group: kind group and an empty participant list, not null.
	group := createRoom(alice, "ops")
	require.Equal(t, dto.ConversationKindGroup, group.Kind)
	require.NotNil(t, group.Participants)
	require.Empty(t, group.Participants)
}

// DM-01: concurrent opens of one pair, from both sides, create exactly one DM: one 201, the rest
// 200, one room id, two members.
func TestOpenDMConcurrentlyCreatesOneRoom(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	const callers = 12
	statuses, ids := make([]int, callers), make([]string, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, with := alice, bob.User.ID
			if i%2 == 1 {
				c, with = bob, alice.User.ID
			}
			resp := openDM(c, with)
			var room dto.Room
			_ = json.Unmarshal(resp.Body, &room)
			statuses[i], ids[i] = resp.Status, room.ID
		}()
	}
	wg.Wait()

	created := 0
	for i := range callers {
		switch statuses[i] {
		case http.StatusCreated:
			created++
		case http.StatusOK:
		default:
			t.Fatalf("caller %d got status %d", i, statuses[i])
		}
		require.NotEmpty(t, ids[i])
		require.Equal(t, ids[0], ids[i], "every caller got the same room")
	}
	require.Equal(t, 1, created, "exactly one caller created it")
	list := testkit.Decode[dto.List[dto.Room]](alice.Get("/api/v1/rooms").RequireStatus(http.StatusOK)).Items
	require.Equal(t, []string{ids[0]}, roomIDs(list), "one DM row")
	require.Len(t, membersOf(t, bob, ids[0]), 2, "one member row per participant")
}

// DM-01, DM-08: a user opens a DM with an AI they own or may use; the AI is a member that
// receives everything (delivery all), the participants are a user and an AI, and the AI's owner
// only views a DM somebody else has with it. Each user has their own DM with the AI.
func TestOpenDMWithAnAI(t *testing.T) {
	env := testkit.New(t)
	owner, alice := env.NewUser("owner"), env.NewUser("alice")
	atlas := env.NewAI(owner, "atlas")
	shareAI(owner, atlas, alice)

	mine := dmWith(owner, atlas, http.StatusCreated)
	hers := dmWith(alice, atlas, http.StatusCreated)
	require.NotEqual(t, mine.ID, hers.ID, "one DM per (user, AI) pair")
	require.Equal(t, hers.ID, dmWith(alice, atlas, http.StatusOK).ID)

	type who struct {
		Role dto.IntelligenceRole
		Kind dto.IntelligenceKind
	}
	roles := map[string]who{}
	for _, p := range hers.Participants {
		roles[p.ID] = who{p.Role, p.Kind}
	}
	require.Equal(t, map[string]who{
		alice.User.ID: {dto.IntelligenceRoleUser, dto.IntelligenceKindHuman},
		atlas:         {dto.IntelligenceRoleAI, dto.IntelligenceKindAI},
	}, roles)

	members := membersOf(t, alice, hers.ID)
	require.Len(t, members, 2)
	require.Equal(t, dto.MemberKindAgent, members[atlas].Kind)
	require.Equal(t, dto.DeliveryModeAll, members[atlas].Delivery)
	require.True(t, members[atlas].Outbound)
	require.Equal(t, dto.MemberKindUser, members[alice.User.ID].Kind)

	seen := testkit.Decode[dto.Room](owner.Get("/api/v1/rooms/" + hers.ID).RequireStatus(http.StatusOK))
	require.Equal(t, dto.LevelView, seen.MyLevel, "the AI's owner views alice's DM with it (AD-011)")
	require.Equal(t, dto.LevelPost, mine.MyLevel)
}

// DM-01: the target must exist and be visible; the refusals look alike so that they leak nothing,
// and nothing is created.
func TestOpenDMRefusals(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	secret := env.NewAI(bob, "secret")

	self := openDM(alice, alice.User.ID).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeDMSelf)
	require.NotEmpty(t, self.Message)

	unknown := openDM(alice, store.NewID()).RequireError(http.StatusNotFound, dto.ErrorCodeContactNotFound)
	hidden := openDM(alice, secret).RequireError(http.StatusNotFound, dto.ErrorCodeContactNotFound)
	require.Equal(t, unknown, hidden, "an AI alice cannot see answers like an id that does not exist")

	missing := openDM(alice, "").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, missing.Fields, "with")
	alice.Do(http.MethodPost, "/api/v1/conversations/dm", map[string]any{"with": 7}).RequireError(http.StatusBadRequest, dto.ErrorCodeInvalid)

	require.Empty(t, testkit.Decode[dto.List[dto.Room]](alice.Get("/api/v1/rooms").RequireStatus(http.StatusOK)).Items, "nothing was created")
	require.Empty(t, testkit.Decode[dto.List[dto.Room]](bob.Get("/api/v1/rooms").RequireStatus(http.StatusOK)).Items)

	// Once bob shares the AI, it is a contact.
	shareAI(bob, secret, alice)
	dmWith(alice, secret, http.StatusCreated)
}

// DM-05 (refusal part): an external person is reached through a platform conversation of its
// connector, never by a plain DM; people alice cannot see are not found at all.
func TestOpenDMWithAPersonHasNoRoute(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	connector, _ := storeChat(t, env, alice.User.ID, "chat")
	person := seedPerson(t, env, connector, "5511999990000", "Contact")

	e := openDM(alice, person).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeNoRouteToPerson)
	require.NotEmpty(t, e.Message)
	openDM(bob, person).RequireError(http.StatusNotFound, dto.ErrorCodeContactNotFound)
	require.Empty(t, testkit.Decode[dto.List[dto.Room]](alice.Get("/api/v1/rooms").RequireStatus(http.StatusOK)).Items)
}

// Authorization matrix of POST /conversations/dm against an AI of the owner that the viewer may
// use: the owner and the grantee open a DM; a user without access, an admin without a grant, an
// anonymous caller and an agent token do not.
func TestOpenDMAuthorizationMatrix(t *testing.T) {
	m := newMatrix(t)
	owner, viewer := m.as(t, roleOwner), m.as(t, roleViewer)
	atlas := m.env.NewAI(owner, "atlas")
	shareAI(owner, atlas, viewer)

	for _, tc := range []struct {
		role   matrixRole
		status int
		code   dto.ErrorCode
	}{
		{roleOwner, http.StatusCreated, ""},
		{roleViewer, http.StatusCreated, ""},
		{rolePoster, http.StatusNotFound, dto.ErrorCodeContactNotFound},
		{roleStranger, http.StatusNotFound, dto.ErrorCodeContactNotFound},
		{roleAdmin, http.StatusNotFound, dto.ErrorCodeContactNotFound},
	} {
		t.Run(string(tc.role), func(t *testing.T) {
			resp := openDM(m.as(t, tc.role), atlas)
			if tc.code == "" {
				resp.RequireStatus(tc.status)
				return
			}
			resp.RequireError(tc.status, tc.code)
		})
	}
	t.Run("agent token", func(t *testing.T) {
		agent := m.as(t, roleOwner).Token(dto.TokenKindAgent, "laptop")
		openDM(agent, m.as(t, roleViewer).User.ID).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	})
	t.Run("anonymous", func(t *testing.T) {
		anon := m.env.Anon()
		anon.T = t
		openDM(anon, atlas).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	})
}

// DM-02: nobody adds, changes or removes a member of a DM (422 dm_members_fixed, also for the AI's
// owner who views it), nobody manages it (403), and a stranger cannot tell it exists (404).
// Each request is valid except for the DM: the same calls on a group would work.
func TestDMMembersAreFixed(t *testing.T) {
	env := testkit.New(t)
	owner, alice, bob, dave := env.NewUser("owner"), env.NewUser("alice"), env.NewUser("bob"), env.NewUser("dave")
	atlas := env.NewAI(owner, "atlas")
	shareAI(owner, atlas, alice)
	room := dmWith(alice, atlas, http.StatusCreated)
	path := "/api/v1/rooms/" + room.ID
	before := membersOf(t, alice, room.ID)
	row := before[atlas].ID

	add := dto.AddMemberRequest{Kind: dto.MemberKindUser, MemberID: bob.User.ID}
	update := dto.UpdateMemberRequest{Outbound: ptr(false)}
	for name, c := range map[string]*testkit.Client{"participant": alice, "AI owner": owner} {
		t.Run(name, func(t *testing.T) {
			c := *c
			c.T = t
			c.Post(path+"/members", add).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeDMMembersFixed)
			c.Patch(path+"/members/"+row, update).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeDMMembersFixed)
			c.Delete(path+"/members/"+row).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeDMMembersFixed)
			// Nobody manages a DM.
			c.Patch(path, dto.UpdateRoomRequest{Name: ptr("renamed")}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
			c.Delete(path).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		})
	}
	dave.Post(path+"/members", add).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	dave.Patch(path+"/members/"+row, update).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	dave.Delete(path+"/members/"+row).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	require.Equal(t, before, membersOf(t, alice, room.ID), "nothing changed")
	got := testkit.Decode[dto.Room](alice.Get(path).RequireStatus(http.StatusOK))
	require.Empty(t, got.Name)
	require.Equal(t, 2, got.MemberCount)

	// The same requests on a group are not refused for being a DM.
	group := createRoom(alice, "ops")
	alice.Post("/api/v1/rooms/"+group.ID+"/members", add).RequireStatus(http.StatusCreated)
}

// DM-02: when the other participant is a disabled user the DM stays readable and posts are
// refused with 403 counterpart_disabled, from a user and from the AI; rooms that are not DMs and
// DMs whose counterpart is enabled are not affected.
func TestPostInADMWithADisabledCounterpartIsRefused(t *testing.T) {
	env := testkit.New(t)
	admin := env.SetupAdmin()
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	atlas := env.NewAI(alice, "atlas")
	room := dmWith(alice, bob.User.ID, http.StatusCreated)
	path := "/api/v1/rooms/" + room.ID
	alice.Post(path+"/messages", dto.PostMessageRequest{Text: "before"}).RequireStatus(http.StatusCreated)
	bob.Post(path+"/messages", dto.PostMessageRequest{Text: "answer"}).RequireStatus(http.StatusCreated)

	group := createRoom(alice, "ops")
	alice.Post("/api/v1/rooms/"+group.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindUser, MemberID: bob.User.ID}).RequireStatus(http.StatusCreated)
	withAI := dmWith(alice, atlas, http.StatusCreated)

	disableUser(admin, bob.User.ID, true)
	alice.Get(path).RequireStatus(http.StatusOK)
	require.Len(t, timelineOf(t, alice, room.ID), 2, "still readable")
	require.Len(t, membersOf(t, alice, room.ID), 2)
	e := alice.Post(path+"/messages", dto.PostMessageRequest{Text: "anybody there?"}).RequireError(http.StatusForbidden, dto.ErrorCodeCounterpartDisabled)
	require.NotEmpty(t, e.Message)
	require.Len(t, timelineOf(t, alice, room.ID), 2, "the refused post stored nothing")

	// The AI of alice posting in a DM with the disabled user is refused too (service level: the
	// AI's tools reach Post with an agent principal).
	botDM := dmByService(t, env, authn.Principal{Kind: authn.KindAgent, UserID: alice.User.ID, AgentID: atlas}, bob.User.ID)
	_, err := env.App.Rooms.Post(context.Background(), authn.Principal{Kind: authn.KindAgent, UserID: alice.User.ID, AgentID: atlas}, botDM, rooms.PostInput{Text: "hi"})
	require.ErrorIs(t, err, rooms.ErrCounterpartDisabled)

	// Other rooms are untouched: the group with the disabled member and the DM with the AI.
	alice.Post("/api/v1/rooms/"+group.ID+"/messages", dto.PostMessageRequest{Text: "group"}).RequireStatus(http.StatusCreated)
	alice.Post("/api/v1/rooms/"+withAI.ID+"/messages", dto.PostMessageRequest{Text: "to my AI"}).RequireStatus(http.StatusCreated)

	disableUser(admin, bob.User.ID, false)
	alice.Post(path+"/messages", dto.PostMessageRequest{Text: "welcome back"}).RequireStatus(http.StatusCreated)
}

// DM-02 (L-044): the refusal does not depend on which participant sorts first. Here the AI was
// created before the disabled user, so it is the first participant (dm_a) of their DM; its post is
// refused, stores nothing, and works again once the user is enabled.
func TestPostByAnAIThatSortsBeforeTheDisabledUserIsRefused(t *testing.T) {
	env := testkit.New(t)
	admin := env.SetupAdmin()
	alice := env.NewUser("alice")
	atlas := env.NewAI(alice, "atlas")
	bob := env.NewUser("bob") // created after Atlas: Atlas sorts first
	require.Less(t, atlas, bob.User.ID, "Atlas is the first participant of the DM")
	as := authn.Principal{Kind: authn.KindAgent, UserID: alice.User.ID, AgentID: atlas}
	ctx := context.Background()
	room := dmByService(t, env, as, bob.User.ID)
	_, err := env.App.Rooms.Post(ctx, as, room, rooms.PostInput{Text: "before"})
	require.NoError(t, err)

	disableUser(admin, bob.User.ID, true)
	_, err = env.App.Rooms.Post(ctx, as, room, rooms.PostInput{Text: "anybody there?"})
	require.ErrorIs(t, err, rooms.ErrCounterpartDisabled)
	require.Len(t, timelineOf(t, alice, room), 1, "the refused post stored nothing; alice reads the AI's DM")

	disableUser(admin, bob.User.ID, false)
	_, err = env.App.Rooms.Post(ctx, as, room, rooms.PostInput{Text: "welcome back"})
	require.NoError(t, err)
}

// dmByService opens the DM of the principal with an intelligence through the service, for the
// agent principals whose HTTP route arrives with the AI tools.
func dmByService(t *testing.T, env *testkit.Env, p authn.Principal, with string) string {
	t.Helper()
	room, _, err := env.App.Conversations.OpenDM(context.Background(), p, with)
	require.NoError(t, err)
	return room.ID
}

// DM-08 edge case: deleting an AI deletes its DMs, with their messages and members, from every
// participant's list, and tells their live sockets; its group memberships go as before.
func TestDeletingAnAIDeletesItsDMs(t *testing.T) {
	env := testkit.New(t)
	owner, alice := env.NewUser("owner"), env.NewUser("alice")
	atlas := env.NewAI(owner, "atlas")
	other := env.NewAI(owner, "orion")
	shareAI(owner, atlas, alice)

	hers := dmWith(alice, atlas, http.StatusCreated)
	mine := dmWith(owner, atlas, http.StatusCreated)
	untouched := dmWith(owner, other, http.StatusCreated)
	alice.Post("/api/v1/rooms/"+hers.ID+"/messages", dto.PostMessageRequest{Text: "hello atlas"}).RequireStatus(http.StatusCreated)
	group := createRoom(owner, "ops")
	owner.Post("/api/v1/rooms/"+group.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: atlas}).RequireStatus(http.StatusCreated)
	aliceLive, ownerLive := dialLive(t, alice), dialLive(t, owner)

	owner.Delete("/api/v1/agents/" + atlas).RequireStatus(http.StatusNoContent)

	for _, c := range []*testkit.Client{alice, owner} {
		c.Get("/api/v1/rooms/"+hers.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	}
	owner.Get("/api/v1/rooms/"+mine.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Empty(t, testkit.Decode[dto.List[dto.Room]](alice.Get("/api/v1/rooms").RequireStatus(http.StatusOK)).Items)
	require.ElementsMatch(t, []string{group.ID, untouched.ID}, roomIDs(testkit.Decode[dto.List[dto.Room]](owner.Get("/api/v1/rooms").RequireStatus(http.StatusOK)).Items))
	ctx := context.Background()
	for _, id := range []string{hers.ID, mine.ID} {
		members, err := env.App.Store.R().ListRoomMembers(ctx, id)
		require.NoError(t, err)
		require.Empty(t, members, "memberships went with the DM")
		views, err := env.App.Store.R().ListMessagesBefore(ctx, db.ListMessagesBeforeParams{RoomID: id, BeforeSeq: 1 << 40, Limit: 10})
		require.NoError(t, err)
		require.Empty(t, views, "so did its messages")
	}
	_, err := env.App.Store.R().GetDMRoom(ctx, db.GetDMRoomParams{DmA: sql.NullString{String: min(atlas, alice.User.ID), Valid: true}, DmB: sql.NullString{String: max(atlas, alice.User.ID), Valid: true}})
	require.ErrorIs(t, err, sql.ErrNoRows)
	require.NotContains(t, membersOf(t, owner, group.ID), atlas)

	// The sockets that listed a DM hear it is gone; the owner never listed alice's.
	var gone dto.RoomIDData
	aliceLive.Expect(dto.LiveEventTypeRoomDeleted, &gone, liveWait)
	require.Equal(t, hers.ID, gone.RoomID)
	ownerLive.Expect(dto.LiveEventTypeRoomDeleted, &gone, liveWait)
	require.Equal(t, mine.ID, gone.RoomID)
}

// DM-01, LIVE-02: opening a DM tells the other participant's live socket about the room, as a DM
// at their level; the AI's owner who may view it hears of it too, a stranger does not.
func TestOpenDMAnnouncesTheRoomLive(t *testing.T) {
	env := testkit.New(t)
	owner, alice, bob, carol := env.NewUser("owner"), env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol")
	atlas := env.NewAI(owner, "atlas")
	shareAI(owner, atlas, alice)
	bobLive, ownerLive, carolLive := dialLive(t, bob), dialLive(t, owner), dialLive(t, carol)

	room := dmWith(alice, bob.User.ID, http.StatusCreated)
	var seen dto.Room
	bobLive.Expect(dto.LiveEventTypeRoomChanged, &seen, liveWait)
	require.Equal(t, room.ID, seen.ID)
	require.Equal(t, dto.ConversationKindDM, seen.Kind)
	require.Equal(t, dto.LevelPost, seen.MyLevel)
	require.Len(t, seen.Participants, 2)
	carolLive.ExpectNone(quiet)

	viaAI := dmWith(alice, atlas, http.StatusCreated)
	ownerLive.Expect(dto.LiveEventTypeRoomChanged, &seen, liveWait)
	require.Equal(t, viaAI.ID, seen.ID)
	require.Equal(t, dto.LevelView, seen.MyLevel, "the AI's owner hears of alice's DM with it, as a viewer")
	carolLive.ExpectNone(quiet)
}
