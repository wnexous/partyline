package telegram

import (
	"strconv"
	"strings"

	"github.com/wnexous/partyline/internal/connectors"
)

// address is the Kind's Address (CID-31): the operator may write to a Telegram chat id no chat of the
// connection has been seen at. The id is the number Telegram uses, in the form updates give it (no
// plus, no leading zeros), up to 64 bits: a positive one is a user, so a direct chat, and a negative
// one a group, a supergroup or a channel. Whether the bot may write there is Telegram's to say; it
// cannot start a conversation with a user who never wrote to it, and the send then fails.
func address(_ connectors.Config, raw string) (connectors.ChatInfo, bool) {
	raw = strings.TrimSpace(raw)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id == 0 || strconv.FormatInt(id, 10) != raw {
		return connectors.ChatInfo{}, false
	}
	if id < 0 {
		return connectors.ChatInfo{ID: raw, Kind: "group"}, true
	}
	return connectors.ChatInfo{ID: raw, Kind: "dm"}, true
}
