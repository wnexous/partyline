package api_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// storeDM writes the DM of two intelligences the way opening one does (the row keyed by the sorted
// pair, one member row per participant) so that access can be tested before the routes that open
// DMs exist; it returns the room id.
func storeDM(t *testing.T, env *testkit.Env, ownerID, a, b string) string {
	t.Helper()
	ctx := context.Background()
	id := store.NewID()
	lo, hi := a, b
	if lo > hi {
		lo, hi = hi, lo
	}
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateDMRoom(ctx, db.CreateDMRoomParams{
			ID: id, OwnerID: ownerID, DmA: sql.NullString{String: lo, Valid: true}, DmB: sql.NullString{String: hi, Valid: true},
		}); err != nil {
			return err
		}
		for _, p := range []string{a, b} {
			kind := dto.MemberKindUser
			if _, err := q.GetAgent(ctx, p); err == nil {
				kind = dto.MemberKindAgent
			}
			if err := q.CreateRoomMember(ctx, db.CreateRoomMemberParams{ID: store.NewID(), RoomID: id, Kind: string(kind), MemberID: p}); err != nil {
				return err
			}
		}
		return nil
	}))
	return id
}

var dmFixtureCounter atomic.Int32

// dmRoomSetup returns the setup of the DM matrix: a fresh DM between the role "target" and a new AI that
// the role "manager" created and hosts, like the AI of testkit.OrgRoles (a pair has one DM, so every
// setup needs its own AI), with a message of the participant so that reading has something to return.
func dmRoomSetup(env *testkit.Env, roles *testkit.OrgRoles) func(t *testing.T) map[string]string {
	return func(t *testing.T) map[string]string {
		target, manager := *roles.Target, *roles.Manager
		target.T, manager.T = t, t
		ai := env.NewAIIn(&manager, roles.Org.ID, fmt.Sprintf("dm-ai-%d", dmFixtureCounter.Add(1)))
		room := storeDM(t, env, target.User.ID, target.User.ID, ai)
		target.Post("/api/v1/rooms/"+room+"/messages", dto.PostMessageRequest{Text: "hi"}).RequireStatus(http.StatusCreated)
		return map[string]string{"room": room}
	}
}

// DM-08, AD-011, AD-016 over HTTP: the participant posts; whoever manages the AI in its home organization
// (the manager that created it, and the admin whose position is above the creator's) only views;
// everybody else - a peer, a member, an inviter, a guest, an outsider, an instance admin - gets 404; and
// nobody manages a DM. Nobody manages it, yet everybody who sees it resumes it after the loop guard (DLV-09):
// there is no one else to, and the AIs it paused cannot.
func TestDMRoutesAuthMatrix(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	room := func(res map[string]string, suffix string) string { return "/api/v1/rooms/" + res["room"] + suffix }
	runRoleRoutes(t, roles, dmRoomSetup(env, roles), []roleRoute{
		{"get room", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Get(room(res, ""))
		}, dmWant(200, 200, 404)},
		{"list messages", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Get(room(res, "/messages"))
		}, dmWant(200, 200, 404)},
		{"list members", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Get(room(res, "/members"))
		}, dmWant(200, 200, 404)},
		{"mark read", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Put(room(res, "/read"), dto.MarkReadRequest{})
		}, dmWant(200, 200, 404)},
		{"post message", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Post(room(res, "/messages"), dto.PostMessageRequest{Text: "hello"})
		}, dmWant(201, 403, 404)},
		{"update room", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Patch(room(res, ""), dto.UpdateRoomRequest{Name: ptr("renamed")})
		}, dmWant(403, 403, 404)},
		{"delete room", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Delete(room(res, ""))
		}, dmWant(403, 403, 404)},
		{"resume", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Post(room(res, "/resume"), nil)
		}, dmWant(200, 200, 404)},
	})

	// The DM lists for its participant only: the managers view it by id.
	res := dmRoomSetup(env, roles)(t)
	for _, role := range roles.All() {
		list := testkit.Decode[dto.List[dto.Room]](role.Client.Get("/api/v1/rooms").RequireStatus(http.StatusOK))
		ids := []string{}
		for _, r := range list.Items {
			ids = append(ids, r.ID)
		}
		require.Equal(t, role.Name == "target", contains(ids, res["room"]), role.Name)
	}

	// The tools of the AI itself reach agent routes only: its token is no way into the user routes of a DM.
	token := roles.Manager.Token(dto.TokenKindAgent, "tools").AsAgent("acme-ai")
	token.Get("/api/v1/rooms/"+res["room"]).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
}

// DM-06 source: the room list holds the DMs a user takes part in, with the level they have, and
// not the DMs they only view through an AI, also when the row names the AI's host as its owner
// (an AI opened it).
func TestRoomListHoldsOwnDMsOnly(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	res := dmRoomSetup(env, roles)(t) // the role "target" and an AI of the role "manager"
	// A DM an AI of the manager opened with a plain member of the organization: its row names the host.
	byAI := storeDM(t, env, roles.Manager.User.ID, roles.TargetAI, roles.Member.User.ID)

	listed := func(c *testkit.Client, id string) *dto.Room {
		t.Helper()
		list := testkit.Decode[dto.List[dto.Room]](c.Get("/api/v1/rooms").RequireStatus(http.StatusOK))
		for i, r := range list.Items {
			if r.ID == id {
				return &list.Items[i]
			}
		}
		return nil
	}
	require.NotNil(t, listed(roles.Member, byAI), "only its participant lists the DM an AI opened")
	require.Nil(t, listed(roles.Manager, byAI), "the AI's manager views it and does not list it")
	require.Nil(t, listed(roles.Admin, byAI), "neither does the admin above the manager")
	roles.Manager.Get("/api/v1/rooms/" + byAI).RequireStatus(http.StatusOK) // viewed, by id
	roles.Admin.Get("/api/v1/rooms/" + byAI).RequireStatus(http.StatusOK)

	for _, role := range roles.All() {
		got := listed(role.Client, res["room"])
		if role.Name != "target" {
			require.Nil(t, got, "%s does not list the DM", role.Name)
			continue
		}
		require.NotNil(t, got, "the participant lists the DM")
		require.Equal(t, dto.LevelPost, got.MyLevel)
	}
}

// AD-011 over HTTP: a user member of a group reads, lists and posts in it; managing stays with
// the owner; removing the member ends the access.
func TestUserMemberOfAGroupPosts(t *testing.T) {
	env := testkit.New(t)
	alice, bob, carol := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol")
	room := createRoom(alice, "ops")
	path := "/api/v1/rooms/" + room.ID
	bob.Get(path).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	member := addToRoom(t, env, alice, room.ID, bob)

	got := testkit.Decode[dto.Room](bob.Get(path).RequireStatus(http.StatusOK))
	require.Equal(t, dto.LevelPost, got.MyLevel)
	list := testkit.Decode[dto.List[dto.Room]](bob.Get("/api/v1/rooms").RequireStatus(http.StatusOK))
	require.Len(t, list.Items, 1)
	require.Equal(t, room.ID, list.Items[0].ID)
	require.Equal(t, dto.LevelPost, list.Items[0].MyLevel)
	posted := testkit.Decode[dto.Message](bob.Post(path+"/messages", dto.PostMessageRequest{Text: "hello from bob"}).RequireStatus(http.StatusCreated))
	require.Equal(t, bob.User.ID, posted.AuthorID)
	require.Len(t, timelineOf(t, alice, room.ID), 1)
	require.Len(t, membersOf(t, bob, room.ID), 1)

	// Not a manager.
	bob.Patch(path, dto.UpdateRoomRequest{Name: ptr("mine")}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	bob.Delete(path).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	bob.Post(path+"/members", dto.AddMemberRequest{Kind: dto.MemberKindUser, MemberID: carol.User.ID}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	bob.Delete(path+"/members/"+member.ID).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	bob.Post(path+"/resume", nil).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	// A non-member sees nothing.
	carol.Get(path).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	carol.Post(path+"/messages", dto.PostMessageRequest{Text: "x"}).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	// The member belongs to the organization, so it sees the organization's AIs, in the room or not, and
	// cannot edit them; carol, who belongs to none of it, sees nothing.
	atlas := env.NewAI(alice, "atlas")
	profile := testkit.Decode[dto.IntelligenceProfile](bob.Get("/api/v1/intelligences/" + atlas).RequireStatus(http.StatusOK))
	require.False(t, profile.CanEdit)
	carol.Get("/api/v1/intelligences/"+atlas).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	alice.Post(path+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: atlas}).RequireStatus(http.StatusCreated)
	carol.Get("/api/v1/intelligences/"+atlas).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	alice.Delete(path + "/members/" + member.ID).RequireStatus(http.StatusNoContent)
	bob.Get(path).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	bob.Post(path+"/messages", dto.PostMessageRequest{Text: "again"}).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Empty(t, testkit.Decode[dto.List[dto.Room]](bob.Get("/api/v1/rooms").RequireStatus(http.StatusOK)).Items)
}

// AD-011, LIVE-02: adding a user member tells their sockets about the room with their level and
// delivers its messages; removing the member says room.deleted and stops them.
func TestLiveUserMembershipFollowsAccess(t *testing.T) {
	env := testkit.New(t)
	alice, bob, carol := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol")
	room := createRoom(alice, "ops")
	path := "/api/v1/rooms/" + room.ID
	bobLive, carolLive := dialLive(t, bob), dialLive(t, carol)

	member := addToRoom(t, env, alice, room.ID, bob)
	var seen dto.Room
	bobLive.Expect(dto.LiveEventTypeRoomChanged, &seen, liveWait)
	require.Equal(t, room.ID, seen.ID)
	require.Equal(t, dto.LevelPost, seen.MyLevel)

	alice.Post(path+"/messages", dto.PostMessageRequest{Text: "welcome"}).RequireStatus(http.StatusCreated)
	var msg dto.Message
	bobLive.Expect(dto.LiveEventTypeRoomMessage, &msg, liveWait)
	require.Equal(t, "welcome", msg.Text)
	carolLive.ExpectNone(quiet)

	alice.Delete(path + "/members/" + member.ID).RequireStatus(http.StatusNoContent)
	var gone dto.RoomIDData
	bobLive.Expect(dto.LiveEventTypeRoomDeleted, &gone, liveWait)
	require.Equal(t, room.ID, gone.RoomID)
	alice.Post(path+"/messages", dto.PostMessageRequest{Text: "secret"}).RequireStatus(http.StatusCreated)
	bobLive.ExpectNone(quiet)
}

// DM-08, LIVE-02: a DM's messages reach its participant and, as a view, the managers of its AI; a member with no
// relation to the AI and an outsider receive nothing.
func TestLiveDMMessagesReachParticipantsAndViewers(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	res := dmRoomSetup(env, roles)(t)
	participant, manager, admin := dialLive(t, roles.Target), dialLive(t, roles.Manager), dialLive(t, roles.Admin)
	member, outsider := dialLive(t, roles.Member), dialLive(t, roles.Outsider)

	roles.Target.Post("/api/v1/rooms/"+res["room"]+"/messages", dto.PostMessageRequest{Text: "ping"}).RequireStatus(http.StatusCreated)
	for _, c := range []*testkit.LiveConn{participant, manager, admin} {
		var got dto.Message
		c.Expect(dto.LiveEventTypeRoomMessage, &got, liveWait)
		require.Equal(t, "ping", got.Text)
		require.Equal(t, res["room"], got.RoomID)
	}
	member.ExpectNone(quiet)
	outsider.ExpectNone(quiet)
}
