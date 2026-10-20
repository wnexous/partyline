package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/wnexous/partyline/internal/connectors"
)

var _ connectors.Reactor = (*Connector)(nil)

// allowedReactions is the Bot API's set of emoji accepted in ReactionTypeEmoji, written
// without variation selectors (U+FE0F), as the Bot API lists them.
var allowedReactions = map[string]bool{}

func init() {
	for _, e := range strings.Fields("❤ 👍 👎 🔥 🥰 👏 😁 🤔 🤯 😱 🤬 😢 🎉 🤩 🤮 💩 🙏 👌 🕊 🤡 🥱 🥴 😍 🐳 ❤‍🔥 🌚 🌭 💯 🤣 ⚡ 🍌 🏆 💔 🤨 😐 🍓 🍾 💋 🖕 😈 😴 😭 🤓 👻 👨‍💻 👀 🎃 🙈 😇 😨 🤝 ✍ 🤗 🫡 🎅 🎄 ☃ 💅 🤪 🗿 🆒 💘 🙉 🦄 😘 💊 🙊 😎 👾 🤷‍♂ 🤷 🤷‍♀ 😡") {
		allowedReactions[e] = true
	}
}

// ErrReactionNotAllowed means Telegram does not accept the emoji as a reaction.
var ErrReactionNotAllowed = errors.New("emoji is not one of Telegram's allowed reactions")

// reactionEmoji returns the emoji as the Bot API lists it (variation selectors dropped), or
// ErrReactionNotAllowed.
func reactionEmoji(emoji string) (string, error) {
	e := strings.ReplaceAll(emoji, "️", "")
	if !allowedReactions[e] {
		return "", fmt.Errorf("setMessageReaction: %w", ErrReactionNotAllowed)
	}
	return e, nil
}

// React sets the bot's reaction on the target message with setMessageReaction (REACT-01).
// Telegram accepts only its own set of reaction emoji; any other emoji fails with
// ErrReactionNotAllowed before calling the API.
func (c *Connector) React(ctx context.Context, r connectors.Reaction) error {
	emoji, err := reactionEmoji(r.Emoji)
	if err != nil {
		return err
	}
	messageID, err := strconv.ParseInt(r.TargetID, 10, 64)
	if err != nil {
		return errors.New("setMessageReaction: invalid message id")
	}
	var chatID any = r.ChatID
	if id, err := strconv.ParseInt(r.ChatID, 10, 64); err == nil {
		chatID = id
	}
	return c.call(ctx, "setMessageReaction", map[string]any{
		"chat_id": chatID, "message_id": messageID,
		"reaction": []map[string]any{{"type": "emoji", "emoji": emoji}},
	}, nil)
}
