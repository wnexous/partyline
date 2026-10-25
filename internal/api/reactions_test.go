package api_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/testkit"
)

// Reacting to a message of a room (RCT-03..RCT-06): the two routes, what they refuse, the
// reactions every message carries, and what the room is told.

func reactionsPath(room, message string) string {
	return "/api/v1/rooms/" + room + "/messages/" + message + "/reactions"
}

// reactionPath is the route that takes a reaction back: the emoji is percent-encoded in the path.
func reactionPath(room, message, emoji string) string {
	return reactionsPath(room, message) + "/" + url.PathEscape(emoji)
}

func react(c *testkit.Client, room, message, emoji string) *testkit.Response {
	c.T.Helper()
	return c.Post(reactionsPath(room, message), dto.AddReactionRequest{Emoji: emoji})
}

func unreact(c *testkit.Client, room, message, emoji string) *testkit.Response {
	c.T.Helper()
	return c.Delete(reactionPath(room, message, emoji))
}

func reactionList(r *testkit.Response) []dto.Reaction {
	return testkit.Decode[dto.List[dto.Reaction]](r.RequireStatus(http.StatusOK)).Items
}

// reactionFixture is the group "ops" of alice (who manages it) and bob (a member), with one message
// of alice's, and carol, who is in the organization and not in the room.
type reactionFixture struct {
	env               *testkit.Env
	alice, bob, carol *testkit.Client
	room              dto.Room
	message           dto.Message
}

func newReactionFixture(t *testing.T) *reactionFixture {
	t.Helper()
	env := testkit.New(t)
	alice, bob, carol := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol")
	room := createRoom(alice, "ops")
	addToRoom(t, env, alice, room.ID, bob)
	joinOrgOf(t, env, room.ID, carol, orgs.PermissionMember)
	message := testkit.Decode[dto.Message](alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "deploy at 5"}).RequireStatus(http.StatusCreated))
	return &reactionFixture{env: env, alice: alice, bob: bob, carol: carol, room: room, message: message}
}

func TestReactAddsTheCallersReactionAndAnswersTheReactionsAfterwards(t *testing.T) {
	f := newReactionFixture(t)
	got := reactionList(react(f.bob, f.room.ID, f.message.ID, "👍"))
	require.Equal(t, []dto.Reaction{{Emoji: "👍", By: []string{f.bob.User.ID}}}, got)
	got = reactionList(react(f.alice, f.room.ID, f.message.ID, "👍"))
	require.Equal(t, []dto.Reaction{{Emoji: "👍", By: []string{f.bob.User.ID, f.alice.User.ID}}}, got)
	got = reactionList(react(f.alice, f.room.ID, f.message.ID, "🎉"))
	require.Equal(t, []string{"👍", "🎉"}, []string{got[0].Emoji, got[1].Emoji})
}

func TestReactingAgainChangesNothingAndStillAnswers(t *testing.T) {
	f := newReactionFixture(t)
	first := reactionList(react(f.bob, f.room.ID, f.message.ID, "👍"))
	again := reactionList(react(f.bob, f.room.ID, f.message.ID, "👍"))
	require.Equal(t, first, again)
}

func TestTakingAReactionBackRemovesJustTheCallers(t *testing.T) {
	f := newReactionFixture(t)
	react(f.alice, f.room.ID, f.message.ID, "👍").RequireStatus(http.StatusOK)
	react(f.bob, f.room.ID, f.message.ID, "👍").RequireStatus(http.StatusOK)
	got := reactionList(unreact(f.bob, f.room.ID, f.message.ID, "👍"))
	require.Equal(t, []dto.Reaction{{Emoji: "👍", By: []string{f.alice.User.ID}}}, got)
	// Again: nothing to take back, and still 200 with the reactions as they are.
	require.Equal(t, got, reactionList(unreact(f.bob, f.room.ID, f.message.ID, "👍")))
	require.Empty(t, reactionList(unreact(f.alice, f.room.ID, f.message.ID, "👍")))
}

func TestAnEmojiOfSeveralCodePointsTravelsInThePath(t *testing.T) {
	f := newReactionFixture(t)
	has := func(list []dto.Reaction, emoji string) bool {
		for _, r := range list {
			if r.Emoji == emoji {
				return true
			}
		}
		return false
	}
	// Variation selectors, joiners, skin tones, flags and keycaps all survive being percent-encoded.
	for _, e := range []string{"❤️", "🧑‍💻", "👍🏽", "🇧🇷", "1️⃣"} {
		require.True(t, has(reactionList(react(f.bob, f.room.ID, f.message.ID, e)), e), "%q was added", e)
		require.False(t, has(reactionList(unreact(f.bob, f.room.ID, f.message.ID, e)), e), "%q was taken back", e)
	}
}

func TestEveryMessageCarriesItsReactionsAsAnArray(t *testing.T) {
	f := newReactionFixture(t)
	// A message nobody reacted to has an empty array, in what posting answers and in the list.
	var posted map[string]json.RawMessage
	f.alice.Post("/api/v1/rooms/"+f.room.ID+"/messages", dto.PostMessageRequest{Text: "second"}).RequireStatus(http.StatusCreated).Decode(&posted)
	require.JSONEq(t, "[]", string(posted["reactions"]))

	react(f.bob, f.room.ID, f.message.ID, "👍").RequireStatus(http.StatusOK)
	react(f.alice, f.room.ID, f.message.ID, "🎉").RequireStatus(http.StatusOK)
	list := testkit.Decode[dto.Page[dto.Message]](f.alice.Get("/api/v1/rooms/" + f.room.ID + "/messages").RequireStatus(http.StatusOK)).Items
	byText := map[string]dto.Message{}
	for _, m := range list {
		byText[m.Text] = m
	}
	require.Equal(t, []dto.Reaction{
		{Emoji: "👍", By: []string{f.bob.User.ID}},
		{Emoji: "🎉", By: []string{f.alice.User.ID}},
	}, byText["deploy at 5"].Reactions)
	require.Empty(t, byText["second"].Reactions)
	require.NotNil(t, byText["second"].Reactions, "an array, never null")
	// The activity feed lists messages too.
	feed := testkit.Decode[dto.List[dto.ActivityItem]](f.alice.Get("/api/v1/activity").RequireStatus(http.StatusOK)).Items
	require.NotEmpty(t, feed)
	for _, item := range feed {
		require.NotNil(t, item.Message.Reactions)
	}
}

func TestRefusalsOfReacting(t *testing.T) {
	f := newReactionFixture(t)
	path := reactionsPath(f.room.ID, f.message.ID)

	// Not exactly one emoji: 422 invalid, naming the field, on both routes.
	for _, bad := range []string{"", "a", "👍👍", "👍a"} {
		e := react(f.bob, f.room.ID, f.message.ID, bad).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, e.Fields, "emoji", "%q", bad)
	}
	f.bob.Delete(reactionPath(f.room.ID, f.message.ID, "a")).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	f.bob.Post(path, map[string]any{"emoji": 5}).RequireError(http.StatusBadRequest, dto.ErrorCodeInvalid)

	// Whoever cannot see the room gets 404, as for a message.
	react(f.carol, f.room.ID, f.message.ID, "👍").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	unreact(f.carol, f.room.ID, f.message.ID, "👍").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	// A message that is not in the room, and one that does not exist: 404 too.
	other := createRoom(f.alice, "elsewhere")
	react(f.alice, other.ID, f.message.ID, "👍").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	react(f.alice, f.room.ID, "no-such-message", "👍").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	unreact(f.alice, f.room.ID, "no-such-message", "👍").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	// Not signed in: 401.
	f.env.Anon().Post(path, dto.AddReactionRequest{Emoji: "👍"}).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	f.env.Anon().Delete(reactionPath(f.room.ID, f.message.ID, "👍")).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)

	// Nothing was stored by any of it.
	require.Empty(t, testkit.Decode[dto.Page[dto.Message]](f.alice.Get("/api/v1/rooms/" + f.room.ID + "/messages").RequireStatus(http.StatusOK)).Items[0].Reactions)
}

func TestWhoOnlyViewsARoomMayNotReactToIt(t *testing.T) {
	env := testkit.New(t)
	owner, alice := env.NewUser("owner"), env.NewUser("alice")
	atlas := env.NewAI(owner, "atlas")
	owner.ShareAI(atlas, alice)
	dm := testkit.Decode[dto.Room](alice.Post("/api/v1/conversations/dm", dto.OpenDMRequest{With: atlas}).RequireStatus(http.StatusCreated))
	said := testkit.Decode[dto.Message](alice.Post("/api/v1/rooms/"+dm.ID+"/messages", dto.PostMessageRequest{Text: "hello Atlas"}).RequireStatus(http.StatusCreated))

	// Alice takes part in the DM; the owner of the AI only sees it.
	reactionList(react(alice, dm.ID, said.ID, "👍"))
	react(owner, dm.ID, said.ID, "👍").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	unreact(owner, dm.ID, said.ID, "👍").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	// What they see, they see reacted to.
	seen := testkit.Decode[dto.Page[dto.Message]](owner.Get("/api/v1/rooms/" + dm.ID + "/messages").RequireStatus(http.StatusOK)).Items
	require.Equal(t, []dto.Reaction{{Emoji: "👍", By: []string{alice.User.ID}}}, seen[0].Reactions)
}

func TestAMessageCarriesTwelveDifferentEmojiAtMost(t *testing.T) {
	f := newReactionFixture(t)
	twelve := []string{"👍", "👎", "🎉", "❤️", "😀", "😮", "😢", "🙏", "🔥", "👀", "✅", "🚀"}
	for _, e := range twelve {
		react(f.alice, f.room.ID, f.message.ID, e).RequireStatus(http.StatusOK)
	}
	react(f.bob, f.room.ID, f.message.ID, "💡").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeReactionLimit)
	// One that is on it already is no new place.
	require.Len(t, reactionList(react(f.bob, f.room.ID, f.message.ID, "👍")), 12)
	// And a place frees up when its emoji leaves.
	unreact(f.alice, f.room.ID, f.message.ID, "🚀").RequireStatus(http.StatusOK)
	require.Len(t, reactionList(react(f.bob, f.room.ID, f.message.ID, "💡")), 12)
}

func TestTheRoomHearsOfReactionsAsTheyChange(t *testing.T) {
	f := newReactionFixture(t)
	aliceLive, bobLive, carolLive := dialLive(t, f.alice), dialLive(t, f.bob), dialLive(t, f.carol)

	react(f.bob, f.room.ID, f.message.ID, "👍").RequireStatus(http.StatusOK)
	// Everyone who sees the room hears of it, the one who reacted too; the message does not come again.
	for _, c := range []*testkit.LiveConn{aliceLive, bobLive} {
		var got dto.ReactionsData
		c.Expect(dto.LiveEventTypeRoomReactions, &got, liveWait)
		require.Equal(t, dto.ReactionsData{
			RoomID: f.room.ID, MessageID: f.message.ID,
			Reactions: []dto.Reaction{{Emoji: "👍", By: []string{f.bob.User.ID}}},
		}, got)
	}
	// Someone who cannot see the room hears nothing of it.
	carolLive.ExpectNone(quiet)

	// A call that changes nothing is not told.
	react(f.bob, f.room.ID, f.message.ID, "👍").RequireStatus(http.StatusOK)
	aliceLive.ExpectNone(quiet)

	// Taking it back is told with the reactions as they are then, whole.
	react(f.alice, f.room.ID, f.message.ID, "🎉").RequireStatus(http.StatusOK)
	var withBoth dto.ReactionsData
	bobLive.Expect(dto.LiveEventTypeRoomReactions, &withBoth, liveWait)
	require.Len(t, withBoth.Reactions, 2)
	unreact(f.bob, f.room.ID, f.message.ID, "👍").RequireStatus(http.StatusOK)
	var later dto.ReactionsData
	for _, c := range []*testkit.LiveConn{aliceLive, bobLive} {
		for {
			c.Expect(dto.LiveEventTypeRoomReactions, &later, liveWait)
			if len(later.Reactions) == 1 {
				break
			}
		}
		require.Equal(t, []dto.Reaction{{Emoji: "🎉", By: []string{f.alice.User.ID}}}, later.Reactions)
	}
}

func TestAMessagePostedLiveCarriesItsReactionsToo(t *testing.T) {
	f := newReactionFixture(t)
	bobLive := dialLive(t, f.bob)
	f.alice.Post("/api/v1/rooms/"+f.room.ID+"/messages", dto.PostMessageRequest{Text: "fresh"}).RequireStatus(http.StatusCreated)
	var got dto.Message
	bobLive.Expect(dto.LiveEventTypeRoomMessage, &got, liveWait)
	require.Equal(t, "fresh", got.Text)
	require.NotNil(t, got.Reactions)
	require.Empty(t, got.Reactions)
}
