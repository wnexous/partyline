package discord

import (
	"strings"

	"github.com/wnexous/partyline/internal/connectors"
)

// address is the Kind's Address (CID-31): the operator may write to a Discord channel id no chat of
// the connection has been seen at. The id is a snowflake, 17 to 20 digits as Discord writes it. Whether
// the bot can see the channel and may write in it is Discord's to say; the send fails when it cannot.
func address(_ connectors.Config, raw string) (connectors.ChatInfo, bool) {
	raw = strings.TrimSpace(raw)
	if n := len(raw); n < 17 || n > 20 || strings.Trim(raw, "0123456789") != "" {
		return connectors.ChatInfo{}, false
	}
	return connectors.ChatInfo{ID: raw, Kind: "channel"}, true
}
