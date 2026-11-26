package channel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/store/db"
)

// pausedText is what a post tool says in a room whose agent delivery the loop guard holds (LG-02).
func pausedText(messageID string) string {
	return "posted (message_id " + messageID + "), but NOT delivered to the other AIs: agent delivery in this room is " +
		"paused (too many AI messages in a row). A person who can see the room must resume it (Resume in the app)."
}

// pause sets the room's loop guard state to paused, as the guard leaves it.
func (e *chanEnv) pause() {
	e.t.Helper()
	ctx := context.Background()
	require.NoError(e.t, e.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.SetRoomLoopState(ctx, db.SetRoomLoopStateParams{AgentStreak: 25, AgentsPaused: true, ID: e.room.ID})
	}))
}

// LG-02: in a room whose agent delivery is paused every post tool says the message was posted but not delivered
// to the other AIs, instead of a plain "sent".
func TestPostToolsSayTheRoomIsPaused(t *testing.T) {
	e := newChanEnv(t)
	family := e.chatMember("Family")
	target := e.fromChat(family, "5511999990000", "Example", "status?")
	o := e.secretOptions()
	s := e.connected(o)
	_ = s.notification() // the chat message
	e.pause()

	text, isErr := s.call("send_message", toolArgs("room", e.room.ID, "text", "mock ready"))
	require.False(t, isErr, text)
	require.Equal(t, pausedText(e.messages(e.room.ID)[0].ID), text)

	text, isErr = s.call("reply", toolArgs("message_id", target.ID, "text", "all green"))
	require.False(t, isErr, text)
	require.Equal(t, pausedText(e.messages(e.room.ID)[0].ID), text)

	text, isErr = s.call("ask", toolArgs("room", e.room.ID, "text", "Ship?", "quick_replies", []string{"Yes", "No"}))
	require.False(t, isErr, text)
	require.Equal(t, pausedText(e.messages(e.room.ID)[0].ID), text)

	path := writeFile(t, t.TempDir(), "shot.png", []byte("not really a png"))
	text, isErr = s.call("send_file", toolArgs("room", e.room.ID, "path", path, "caption", "screenshot"))
	require.False(t, isErr, text)
	require.Equal(t, pausedText(e.messages(e.room.ID)[0].ID), text)

	text, isErr = s.call("react", toolArgs("message_id", target.ID, "emoji", "👍"))
	require.False(t, isErr, text)
	require.Contains(t, text, "but NOT delivered to the other AIs")

	res := requestSecret(t, s, e.room.ID, "EXAMPLE_API_KEY", "To run the weekly report.")
	require.Contains(t, res["delivery"], "but NOT delivered to the other AIs")
}

// LG-02, LG-03: the post that reaches the limit says the room is paused, and every live AI member - the author
// too - gets a notice naming the room. The post before it is a plain "sent".
func TestThePostThatPausesSaysSoAndTheAIGetsANotice(t *testing.T) {
	e := newChanEnv(t)
	s := e.connected(e.options())
	for i := 1; i < delivery.DefaultLoopLimit; i++ {
		text, isErr := s.call("send_message", toolArgs("room", e.room.ID, "text", "step"))
		require.False(t, isErr, text)
		require.Equal(t, "sent (message_id "+e.messages(e.room.ID)[0].ID+")", text, "message %d", i)
	}
	text, isErr := s.call("send_message", toolArgs("room", e.room.ID, "text", "the 25th"))
	require.False(t, isErr, text)
	last := e.messages(e.room.ID)[1] // the pause notice follows it
	require.Equal(t, "the 25th", last.Text)
	require.Equal(t, pausedText(last.ID), text)

	n := s.notification()
	require.Equal(t, delivery.PausedNotice(delivery.DefaultLoopLimit), n.Content)
	require.Equal(t, e.room.ID, n.Meta["room"])
	require.Equal(t, string(dto.AuthorKindSystem), n.Meta["author_kind"])
	require.Empty(t, n.Meta["message_id"], "a notice, not a message")

	// The notice is no message: it did not count toward the streak.
	room, err := e.env.App.Store.R().GetRoom(context.Background(), e.room.ID)
	require.NoError(t, err)
	require.Equal(t, int64(delivery.DefaultLoopLimit), room.AgentStreak)
	require.True(t, room.AgentsPaused)
}
