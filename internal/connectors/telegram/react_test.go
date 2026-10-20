package telegram

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/connectors"
)

// REACT-01: setMessageReaction gets the chat id, the target message id and one emoji
// reaction; a variation selector is dropped to match Telegram's emoji list.
func TestReactSetsMessageReaction(t *testing.T) {
	f := newFakeBotAPI(t)
	c := newConnector(t, f)
	for _, tc := range []struct{ in, want string }{{"👍", "👍"}, {"❤️", "❤"}, {"👨‍💻", "👨‍💻"}} {
		require.NoError(t, c.React(context.Background(), connectors.Reaction{ChatID: strconv.Itoa(groupChat), TargetID: "42", Emoji: tc.in}))
		sent := f.next("setMessageReaction")
		require.Equal(t, userAgent, sent.UserAgent)
		require.Equal(t, map[string]any{
			"chat_id": float64(groupChat), "message_id": 42.0,
			"reaction": []any{map[string]any{"type": "emoji", "emoji": tc.want}},
		}, sent.JSON)
	}
}

// REACT-01: an emoji outside Telegram's reaction set fails with a clear error and calls
// nothing; a platform error is reported without the token.
func TestReactRefusesUnsupportedEmoji(t *testing.T) {
	f := newFakeBotAPI(t)
	c := newConnector(t, f)
	err := c.React(context.Background(), connectors.Reaction{ChatID: strconv.Itoa(groupChat), TargetID: "42", Emoji: "🦀"})
	require.ErrorIs(t, err, ErrReactionNotAllowed)
	require.Equal(t, "setMessageReaction: emoji is not one of Telegram's allowed reactions", err.Error())
	require.Empty(t, f.calls, "nothing was called")

	f.fail("setMessageReaction", http.StatusBadRequest)
	err = c.React(context.Background(), connectors.Reaction{ChatID: strconv.Itoa(groupChat), TargetID: "42", Emoji: "👍"})
	require.Equal(t, "setMessageReaction: error 400", err.Error())
	require.NotContains(t, err.Error(), botToken)
}
