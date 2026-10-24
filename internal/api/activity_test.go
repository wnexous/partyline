package api_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/testkit"
)

func activity(t *testing.T, c *testkit.Client, query string) []dto.ActivityItem {
	t.Helper()
	return testkit.Decode[dto.List[dto.ActivityItem]](c.Get("/api/v1/activity" + query).RequireStatus(http.StatusOK)).Items
}

// GET /activity: the latest messages across the rooms the caller can view (ACL-03, ACL-08), as a room
// member or as an admin of the group's organization, newest first, each with its room's id and name.
func TestActivityFeed(t *testing.T) {
	clock := testkit.NewClock(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	env := testkit.New(t, testkit.WithClock(clock.Now))
	admin := env.SetupAdmin()
	alice, bob, carol := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol")

	private := createRoom(alice, "alice-private")
	shared := createRoom(alice, "shared-with-bob")
	addToRoom(t, env, alice, shared.ID, bob)
	own := createRoom(bob, "bob-own")
	// carol makes a group in bob's organization, which bob administers without being in the group.
	env.AddMember(bob.PersonalOrg().ID, carol.User.ID, orgs.PermissionMember, "")
	public := groupIn(t, env, carol, bob.PersonalOrg().ID, "town-square")

	post := func(c *testkit.Client, room dto.Room, text string) dto.Message {
		clock.Advance(time.Second)
		return testkit.Decode[dto.Message](c.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
	}
	m1 := post(alice, shared, "one")
	post(alice, private, "secret")
	m3 := post(bob, own, "three")
	m4 := post(carol, public, "four")
	m5 := post(alice, shared, "five")

	got := activity(t, bob, "")
	ids := make([]string, len(got))
	for i, it := range got {
		ids[i] = it.Message.ID
	}
	require.Equal(t, []string{m5.ID, m4.ID, m3.ID, m1.ID}, ids, "newest first, only rooms bob can view: as a member, as an admin and as the creator")
	require.Equal(t, dto.RoomRef{ID: shared.ID, Name: "shared-with-bob"}, got[0].Room)
	require.Equal(t, dto.RoomRef{ID: public.ID, Name: "town-square"}, got[1].Room)
	require.Equal(t, "five", got[0].Message.Text)
	require.Equal(t, "alice", got[0].Message.AuthorName, "the message is rendered as in the room")
	require.Equal(t, shared.ID, got[0].Message.RoomID)

	// An admin of the instance who belongs to none of these organizations sees no room (ACL-08).
	require.Empty(t, activity(t, admin, ""))

	// Anonymous callers are rejected; agent tokens reach only agent endpoints.
	env.Anon().Get("/api/v1/activity").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	bob.Token(dto.TokenKindAgent, "bot").Get("/api/v1/activity").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
}

// GET /activity?limit=: 20 by default, at most 50, a positive integer.
func TestActivityFeedLimit(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	room := createRoom(alice, "busy")
	for i := range 55 {
		alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: fmt.Sprint("m", i)}).RequireStatus(http.StatusCreated)
	}
	require.Len(t, activity(t, alice, ""), 20)
	require.Len(t, activity(t, alice, "?limit=5"), 5)
	require.Len(t, activity(t, alice, "?limit=500"), 50)
	alice.Get("/api/v1/activity?limit=0").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	alice.Get("/api/v1/activity?limit=x").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Empty(t, activity(t, env.NewUser("dave"), ""), "a user without rooms gets an empty feed")
}
