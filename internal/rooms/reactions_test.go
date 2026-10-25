package rooms

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/validation"
)

// Reactions to the messages of a room (RCT-03..RCT-05): added and taken back by whoever may post,
// listed in the order they were made, refused beyond twelve different emoji, and told to the
// room as they change.

// reactionFixture is a group "ops" of alice and bob with one message of alice's.
type reactionFixture struct {
	*fixture
	alice, bob authn.Principal
	ops        Room
	message    Message
}

func newReactionFixture(t *testing.T) *reactionFixture {
	t.Helper()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.colleague(t, f.orgOf(t, alice), "bob", orgs.PermissionMember)
	r := f.room(t, alice, "ops")
	f.addMember(t, alice, r.ID, dto.MemberKindUser, bob.UserID)
	return &reactionFixture{fixture: f, alice: alice, bob: bob, ops: r, message: f.post(t, alice, r.ID, "the build is green")}
}

func (f *reactionFixture) react(t *testing.T, p authn.Principal, emoji string) []Reaction {
	t.Helper()
	list, err := f.svc.React(context.Background(), p, f.ops.ID, f.message.ID, emoji)
	require.NoError(t, err)
	return list
}

func (f *reactionFixture) unreact(t *testing.T, p authn.Principal, emoji string) []Reaction {
	t.Helper()
	list, err := f.svc.Unreact(context.Background(), p, f.ops.ID, f.message.ID, emoji)
	require.NoError(t, err)
	return list
}

func emojis(list []Reaction) []string {
	out := make([]string, len(list))
	for i, r := range list {
		out[i] = r.Emoji
	}
	return out
}

func TestReactAddsTheCallersReactionAndAnswersTheListAfterwards(t *testing.T) {
	f := newReactionFixture(t)
	got := f.react(t, f.bob, "👍")
	require.Equal(t, []Reaction{{Emoji: "👍", By: []string{f.bob.UserID}}}, got)
	// The message as the room lists it carries them too (RCT-05).
	m, err := f.svc.GetMessage(context.Background(), f.alice, f.ops.ID, f.message.ID)
	require.NoError(t, err)
	require.Equal(t, got, m.Reactions)
}

func TestReactingTwiceWithTheSameEmojiChangesNothing(t *testing.T) {
	f := newReactionFixture(t)
	f.react(t, f.bob, "👍")
	sub, cancel := f.bus.Subscribe(8)
	defer cancel()
	again := f.react(t, f.bob, "👍")
	require.Equal(t, []Reaction{{Emoji: "👍", By: []string{f.bob.UserID}}}, again)
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM message_reactions"))
	require.Empty(t, drain(sub), "nothing changed, so nothing is told")
}

func TestUnreactTakesTheCallersReactionBackAndOnlyTheirs(t *testing.T) {
	f := newReactionFixture(t)
	f.react(t, f.alice, "👍")
	f.react(t, f.bob, "👍")
	got := f.unreact(t, f.bob, "👍")
	require.Equal(t, []Reaction{{Emoji: "👍", By: []string{f.alice.UserID}}}, got)
	got = f.unreact(t, f.alice, "👍")
	require.Empty(t, got)
	require.Equal(t, 0, f.count(t, "SELECT COUNT(*) FROM message_reactions"))
}

func TestTakingBackAReactionThatIsNotThereChangesNothing(t *testing.T) {
	f := newReactionFixture(t)
	f.react(t, f.alice, "👍")
	sub, cancel := f.bus.Subscribe(8)
	defer cancel()
	got := f.unreact(t, f.bob, "👍")
	require.Equal(t, []Reaction{{Emoji: "👍", By: []string{f.alice.UserID}}}, got)
	require.Equal(t, got, f.unreact(t, f.bob, "🎉"), "an emoji nobody used")
	require.Empty(t, drain(sub))
}

func TestReactionsComeInTheOrderTheyWereMade(t *testing.T) {
	f := newReactionFixture(t)
	f.react(t, f.alice, "🎉")
	f.react(t, f.bob, "👍")
	f.react(t, f.bob, "🎉")
	got := f.react(t, f.alice, "👍")
	// The emoji in the order each was first used, each with whoever used it in order.
	require.Equal(t, []Reaction{
		{Emoji: "🎉", By: []string{f.alice.UserID, f.bob.UserID}},
		{Emoji: "👍", By: []string{f.bob.UserID, f.alice.UserID}},
	}, got)
	// Who takes a reaction back and makes it again goes to the end of that emoji.
	f.unreact(t, f.alice, "🎉")
	got = f.react(t, f.alice, "🎉")
	require.Equal(t, []string{f.bob.UserID, f.alice.UserID}, got[0].By)
}

func TestAnEmojiThatLostAllItsReactionsLeavesTheList(t *testing.T) {
	f := newReactionFixture(t)
	f.react(t, f.alice, "👍")
	f.react(t, f.alice, "🎉")
	got := f.unreact(t, f.alice, "👍")
	require.Equal(t, []string{"🎉"}, emojis(got))
}

func TestOneEmojiOnly(t *testing.T) {
	f := newReactionFixture(t)
	for _, bad := range []string{"", "a", "👍👍", "👍a", " 👍", "👍 ", "thumbs up", "1"} {
		_, err := f.svc.React(context.Background(), f.alice, f.ops.ID, f.message.ID, bad)
		var verr *validation.Error
		require.ErrorAs(t, err, &verr, "%q", bad)
		require.Contains(t, verr.Fields, "emoji", "%q", bad)
		_, err = f.svc.Unreact(context.Background(), f.alice, f.ops.ID, f.message.ID, bad)
		require.ErrorAs(t, err, &verr, "%q", bad)
	}
	require.Equal(t, 0, f.count(t, "SELECT COUNT(*) FROM message_reactions"))
}

func TestWhatCountsAsOneEmoji(t *testing.T) {
	f := newReactionFixture(t)
	for _, one := range []string{"👍", "👍🏽", "❤️", "🧑‍💻", "🇧🇷", "1️⃣", "😀"} {
		f.react(t, f.alice, one)
	}
	require.Len(t, f.react(t, f.alice, "🎉"), 8)
}

func TestAMessageCarriesTwelveDifferentEmojiAtMost(t *testing.T) {
	f := newReactionFixture(t)
	twelve := []string{"👍", "👎", "🎉", "❤️", "😀", "😮", "😢", "🙏", "🔥", "👀", "✅", "🚀"}
	for _, e := range twelve {
		f.react(t, f.alice, e)
	}
	_, err := f.svc.React(context.Background(), f.bob, f.ops.ID, f.message.ID, "💡")
	require.ErrorIs(t, err, ErrReactionLimit)
	require.Equal(t, 12, f.count(t, "SELECT COUNT(DISTINCT emoji) FROM message_reactions"))

	// An emoji that is there already is no new place: others may still use it.
	got := f.react(t, f.bob, "👍")
	require.Equal(t, []string{f.alice.UserID, f.bob.UserID}, got[0].By)
	// Taking one away frees its place.
	f.unreact(t, f.alice, "🚀")
	require.Len(t, f.react(t, f.bob, "💡"), 12)
}

func TestTheLimitIsOfOneMessageNotOfTheRoom(t *testing.T) {
	f := newReactionFixture(t)
	other := f.post(t, f.alice, f.ops.ID, "a second one")
	for i := 0; i < 12; i++ {
		_, err := f.svc.React(context.Background(), f.alice, f.ops.ID, f.message.ID, []string{"👍", "👎", "🎉", "❤️", "😀", "😮", "😢", "🙏", "🔥", "👀", "✅", "🚀"}[i])
		require.NoError(t, err)
	}
	_, err := f.svc.React(context.Background(), f.alice, f.ops.ID, other.ID, "💡")
	require.NoError(t, err)
}

func TestReactingNeedsPostOnTheRoom(t *testing.T) {
	f := newFixture(t)
	owner := f.user(t, "owner", dto.RoleMember)
	alice := f.colleague(t, f.orgOf(t, owner), "alice", orgs.PermissionMember)
	stranger := f.user(t, "stranger", dto.RoleMember)
	// bob manages the AI that alice talks to, so he sees their DM and may not take part in it.
	bob := f.user(t, "bob", dto.RoleMember)
	atlas := f.agent(t, bob, "atlas")
	dm := f.dmAI(t, alice, atlas)
	said := f.post(t, alice, dm, "hello Atlas")
	r := f.room(t, owner, "ops")
	f.addMember(t, owner, r.ID, dto.MemberKindUser, alice.UserID)
	inGroup := f.post(t, owner, r.ID, "hello group")
	ctx := context.Background()

	// A member of the group may; the creator may.
	_, err := f.svc.React(ctx, alice, r.ID, inGroup.ID, "👍")
	require.NoError(t, err)
	_, err = f.svc.React(ctx, owner, r.ID, inGroup.ID, "👍")
	require.NoError(t, err)
	// Someone who cannot see the room does not even learn that it is there.
	_, err = f.svc.React(ctx, stranger, r.ID, inGroup.ID, "👍")
	require.ErrorIs(t, err, access.ErrNotFound)
	_, err = f.svc.Unreact(ctx, stranger, r.ID, inGroup.ID, "👍")
	require.ErrorIs(t, err, access.ErrNotFound)
	// Someone who only sees it may not react.
	_, err = f.svc.React(ctx, bob, dm, said.ID, "👍")
	require.ErrorIs(t, err, access.ErrForbidden)
	_, err = f.svc.Unreact(ctx, bob, dm, said.ID, "👍")
	require.ErrorIs(t, err, access.ErrForbidden)
	require.Equal(t, 2, f.count(t, "SELECT COUNT(*) FROM message_reactions"))
}

func TestAReactionBelongsToTheRoomOfItsMessage(t *testing.T) {
	f := newReactionFixture(t)
	elsewhere := f.room(t, f.alice, "elsewhere")
	ctx := context.Background()
	// The message exists, in another room alice may post in: still not found here.
	_, err := f.svc.React(ctx, f.alice, elsewhere.ID, f.message.ID, "👍")
	require.ErrorIs(t, err, access.ErrNotFound)
	_, err = f.svc.React(ctx, f.alice, f.ops.ID, "no-such-message", "👍")
	require.ErrorIs(t, err, access.ErrNotFound)
	_, err = f.svc.Unreact(ctx, f.alice, f.ops.ID, "no-such-message", "👍")
	require.ErrorIs(t, err, access.ErrNotFound)
	require.Equal(t, 0, f.count(t, "SELECT COUNT(*) FROM message_reactions"))
}

func TestAuthorsMayReactToTheirOwnMessage(t *testing.T) {
	f := newReactionFixture(t)
	require.Equal(t, []string{f.alice.UserID}, f.react(t, f.alice, "👍")[0].By)
}

func TestReactingWorksInADirectMessage(t *testing.T) {
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.user(t, "bob", dto.RoleMember)
	dm := f.dm(t, alice, bob)
	said := f.post(t, alice, dm, "lunch?")
	got, err := f.svc.React(context.Background(), bob, dm, said.ID, "👍")
	require.NoError(t, err)
	require.Equal(t, []Reaction{{Emoji: "👍", By: []string{bob.UserID}}}, got)
}

func TestReactingToADirectMessageWithADisabledUserIsRefused(t *testing.T) {
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.user(t, "bob", dto.RoleMember)
	dm := f.dm(t, alice, bob)
	said := f.post(t, alice, dm, "lunch?")
	f.exec(t, "UPDATE users SET disabled_at = ? WHERE id = ?", t0.UnixMilli(), bob.UserID)
	_, err := f.svc.React(context.Background(), alice, dm, said.ID, "👍")
	require.ErrorIs(t, err, ErrCounterpartDisabled)
}

func TestTheRoomIsToldOnceForEachChange(t *testing.T) {
	f := newReactionFixture(t)
	sub, cancel := f.bus.Subscribe(8)
	defer cancel()
	f.react(t, f.bob, "👍")
	f.react(t, f.alice, "👍")
	f.unreact(t, f.bob, "👍")
	want := events.ReactionsChanged{RoomID: f.ops.ID, MessageID: f.message.ID}
	require.Equal(t, []events.Event{want, want, want}, drain(sub))
}

func TestARefusedReactionIsNotTold(t *testing.T) {
	f := newReactionFixture(t)
	sub, cancel := f.bus.Subscribe(8)
	defer cancel()
	_, err := f.svc.React(context.Background(), f.alice, f.ops.ID, f.message.ID, "nope")
	require.Error(t, err)
	require.Empty(t, drain(sub))
}

func TestMessagesAlwaysCarryTheirReactions(t *testing.T) {
	f := newReactionFixture(t)
	second := f.post(t, f.bob, f.ops.ID, "second")
	f.react(t, f.alice, "👍")
	f.react(t, f.bob, "🎉")
	_, err := f.svc.React(context.Background(), f.alice, f.ops.ID, second.ID, "🚀")
	require.NoError(t, err)
	third := f.post(t, f.bob, f.ops.ID, "third")

	list, more, err := f.svc.Messages(context.Background(), f.alice, f.ops.ID, Page{})
	require.NoError(t, err)
	require.False(t, more)
	byID := map[string][]Reaction{}
	for _, m := range list {
		byID[m.ID] = m.Reactions
	}
	require.Equal(t, []Reaction{{Emoji: "👍", By: []string{f.alice.UserID}}, {Emoji: "🎉", By: []string{f.bob.UserID}}}, byID[f.message.ID])
	require.Equal(t, []Reaction{{Emoji: "🚀", By: []string{f.alice.UserID}}}, byID[second.ID])
	require.Empty(t, byID[third.ID], "a message nobody reacted to has none")
}

func TestOneReactionCountsForOneMessageOnly(t *testing.T) {
	f := newReactionFixture(t)
	second := f.post(t, f.alice, f.ops.ID, "second")
	f.react(t, f.alice, "👍")
	got, err := f.svc.GetMessage(context.Background(), f.alice, f.ops.ID, second.ID)
	require.NoError(t, err)
	require.Empty(t, got.Reactions)
}

func TestManyPeopleManyEmojiKeepTheirOrder(t *testing.T) {
	f := newReactionFixture(t)
	var users []authn.Principal
	for i := 0; i < 5; i++ {
		u := f.colleague(t, f.orgOf(t, f.alice), fmt.Sprintf("user%d", i), orgs.PermissionMember)
		f.addMember(t, f.alice, f.ops.ID, dto.MemberKindUser, u.UserID)
		users = append(users, u)
	}
	for _, u := range users {
		f.react(t, u, "👍")
	}
	got := f.react(t, f.alice, "👍")
	var want []string
	for _, u := range users {
		want = append(want, u.UserID)
	}
	want = append(want, f.alice.UserID)
	require.Equal(t, want, got[0].By)
}
