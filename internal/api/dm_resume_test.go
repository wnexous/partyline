package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// pingPong makes the two AIs answer each other in the room, atlas first, until the loop guard has the 25th
// AI message in a row (DLV-08); every post goes through the agent API, as the AIs' sessions make them. It
// returns the id of the room, opened by atlas's first message when roomID is empty.
func pingPong(t *testing.T, asAtlas, asOrion *testkit.Client, orion, roomID string) string {
	t.Helper()
	for i := 0; i < 25; i++ {
		if roomID == "" {
			first := testkit.Decode[dto.Message](asAtlas.Post("/api/v1/agent/dms", dto.AgentDMRequest{To: orion, Text: "hello"}).RequireStatus(http.StatusCreated))
			roomID = first.RoomID
			continue
		}
		speaker := asAtlas
		if i%2 == 1 {
			speaker = asOrion
		}
		speaker.Post("/api/v1/agent/rooms/"+roomID+"/messages", dto.PostMessageRequest{Text: "ping"}).RequireStatus(http.StatusCreated)
	}
	return roomID
}

func roomOf(t *testing.T, c *testkit.Client, id string) dto.Room {
	t.Helper()
	return testkit.Decode[dto.Room](c.Get("/api/v1/rooms/" + id).RequireStatus(http.StatusOK))
}

// DLV-09, POS-03, AD-016: two AIs of one user answer each other in their DM until the loop guard pauses
// it. Nobody manages a DM, so the user who created both AIs - who manages them, sees the DM and is an admin
// of their organization - used to get 403 on /resume and the DM stayed dead. They resume it now (200) and
// the AIs are delivered to again. Whoever cannot see the DM gets 404 as before, an unrelated member of the
// organization included, and the AIs cannot call the route at all.
func TestTheCreatorOfTwoAIsResumesTheirPausedDM(t *testing.T) {
	env := testkit.New(t)
	alice, bob, carol := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol")
	atlas, orion := env.NewAI(alice, "atlas"), env.NewAI(alice, "orion")
	alice.ShareAI(atlas, bob) // an unrelated member of alice's organization: bob manages neither AI
	agent := alice.Token(dto.TokenKindAgent, "laptop")
	asAtlas, asOrion := agent.AsAgent("atlas"), agent.AsAgent("orion")

	room := pingPong(t, asAtlas, asOrion, orion, "")
	path := "/api/v1/rooms/" + room
	got := roomOf(t, alice, room)
	require.Equal(t, dto.ConversationKindDM, got.Kind)
	require.True(t, got.AgentsPaused, "the guard paused the DM after 25 AI messages in a row")
	require.Equal(t, dto.LevelView, got.MyLevel, "alice only views a DM she is no participant of")
	require.True(t, got.CanResume, "and the server says she resumes it")
	last := testkit.Decode[dto.Page[dto.Message]](alice.Get(path + "/messages?limit=1").RequireStatus(http.StatusOK))
	require.Equal(t, dto.AuthorKindSystem, last.Items[0].AuthorKind, "the pause notice")

	// Orion connects while the room is paused and gets nothing, and what atlas says meanwhile is not pushed.
	live, resp := agent.DialAgent("orion")
	require.NotNil(t, live, "upgrade refused: %d %s", resp.Status, resp.Body)
	live.Welcome(wsWait)
	live.ExpectNone(300 * time.Millisecond)
	asAtlas.Post("/api/v1/agent/rooms/"+room+"/messages", dto.PostMessageRequest{Text: "anyone there?"}).RequireStatus(http.StatusCreated)
	live.ExpectNone(300 * time.Millisecond)

	// Who cannot see the DM cannot resume it: it does not exist for them. An AI reaches no user route.
	for who, c := range map[string]*testkit.Client{"an unrelated member of the organization": bob, "a stranger": carol} {
		c.Post(path+"/resume", nil).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		c.Get(path).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		require.True(t, roomOf(t, alice, room).AgentsPaused, "%s did not resume it", who)
	}
	asAtlas.Post(path+"/resume", nil).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	asOrion.Post(path+"/resume", nil).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	require.True(t, roomOf(t, alice, room).AgentsPaused, "the AIs did not resume it")
	env.Anon().Post(path+"/resume", nil).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)

	resumed := testkit.Decode[dto.Room](alice.Post(path+"/resume", nil).RequireStatus(http.StatusOK))
	require.False(t, resumed.AgentsPaused)
	require.True(t, resumed.CanResume)
	require.False(t, roomOf(t, alice, room).AgentsPaused)

	// Delivery restarts: orion, live, gets what atlas says next. The dispatcher works on its own goroutine, so a
	// message of the pause it was late with may come first.
	next := testkit.Decode[dto.Message](asAtlas.Post("/api/v1/agent/rooms/"+room+"/messages", dto.PostMessageRequest{Text: "back"}).RequireStatus(http.StatusCreated))
	pushed := live.Message(wsWait)
	for pushed.Message.ID != next.ID {
		pushed = live.Message(wsWait)
	}
	require.Equal(t, room, pushed.Room.ID)
	require.Equal(t, dto.ConversationKindDM, pushed.Room.Kind)

	// A second resume of a room that is not paused is harmless.
	alice.Post(path+"/resume", nil).RequireStatus(http.StatusOK)
}

// DLV-09: a group is resumed by its managers, as before: its creator, who also created the AIs, and the
// admins of its organization; a member who only posts gets 403 - the same user, in a DM, would resume - and
// whoever is no member of the group gets 404.
func TestResumingAPausedGroupStillNeedsManage(t *testing.T) {
	env := testkit.New(t)
	alice, bob, carol := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol")
	atlas, orion := env.NewAI(alice, "atlas"), env.NewAI(alice, "orion")
	alice.ShareAI(atlas, bob)
	group := createRoom(alice, "ops")
	path := "/api/v1/rooms/" + group.ID
	for _, ai := range []string{atlas, orion} {
		alice.Post(path+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: ai}).RequireStatus(http.StatusCreated)
	}
	addToRoom(t, env, alice, group.ID, bob)
	agent := alice.Token(dto.TokenKindAgent, "laptop")
	pingPong(t, agent.AsAgent("atlas"), agent.AsAgent("orion"), orion, group.ID)

	require.True(t, roomOf(t, alice, group.ID).AgentsPaused)
	require.True(t, roomOf(t, alice, group.ID).CanResume, "the creator manages the group")
	inRoom := roomOf(t, bob, group.ID)
	require.Equal(t, dto.LevelPost, inRoom.MyLevel)
	require.False(t, inRoom.CanResume, "a member who only posts does not")

	bob.Post(path+"/resume", nil).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	carol.Post(path+"/resume", nil).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.True(t, roomOf(t, alice, group.ID).AgentsPaused)
	alice.Post(path+"/resume", nil).RequireStatus(http.StatusOK)
	require.False(t, roomOf(t, alice, group.ID).AgentsPaused)
}

// DLV-09: can_resume tells what the route does, for every role of the organization, on a paused group and on
// a paused DM between a user and an AI: whoever the flag is true for gets 200 from /resume, whoever can view
// the room and has it false gets 403, and whoever cannot view it gets 404 on both.
func TestCanResumeAgreesWithTheResumeRoute(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	pause := func(t *testing.T, room string) {
		t.Helper()
		ctx := context.Background()
		require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
			return q.SetRoomLoopState(ctx, db.SetRoomLoopStateParams{AgentStreak: 25, AgentsPaused: true, ID: room})
		}))
	}
	cases := []struct {
		name  string
		setup func(t *testing.T) map[string]string
		// resumes lists the roles that resume; seeing is every role that can view the room.
		resumes, seeing []string
	}{
		{"group", roomSetup(env, roles), []string{"member", "admin"}, []string{"member", "admin", "target", "guest"}},
		{"dm", dmRoomSetup(env, roles), []string{"target", "manager", "admin"}, []string{"target", "manager", "admin"}},
	}
	in := func(list []string, name string) bool {
		for _, v := range list {
			if v == name {
				return true
			}
		}
		return false
	}
	for _, tc := range cases {
		for _, role := range roles.All() {
			t.Run(tc.name+"/"+role.Name, func(t *testing.T) {
				room := tc.setup(t)["room"]
				pause(t, room)
				c := *role.Client
				c.T = t
				path := "/api/v1/rooms/" + room
				if !in(tc.seeing, role.Name) {
					c.Get(path).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
					c.Post(path+"/resume", nil).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
					return
				}
				got := testkit.Decode[dto.Room](c.Get(path).RequireStatus(http.StatusOK))
				require.True(t, got.AgentsPaused)
				require.Equal(t, in(tc.resumes, role.Name), got.CanResume, "can_resume")
				if got.CanResume {
					require.False(t, testkit.Decode[dto.Room](c.Post(path+"/resume", nil).RequireStatus(http.StatusOK)).AgentsPaused)
					return
				}
				c.Post(path+"/resume", nil).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
			})
		}
	}
}
