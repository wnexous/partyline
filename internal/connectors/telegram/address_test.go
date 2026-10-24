package telegram

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/connectors"
)

// CID-31: the operator may write to a Telegram chat id no chat has been seen at: a user's id is a
// direct chat and a negative one (a group, a supergroup, a channel) a group chat, named the way
// updates from it name it. Telegram itself decides whether the bot may write there: it cannot start a
// conversation with a user who never wrote to it.
func TestAddressAcceptsTelegramChatIDs(t *testing.T) {
	for raw, want := range map[string]connectors.ChatInfo{
		"123456789":            {ID: "123456789", Kind: "dm"},
		" 123456789 ":          {ID: "123456789", Kind: "dm"},
		"-1001234567890":       {ID: "-1001234567890", Kind: "group"},
		"-4000000000":          {ID: "-4000000000", Kind: "group"},
		"1":                    {ID: "1", Kind: "dm"},
		"9223372036854775807":  {ID: "9223372036854775807", Kind: "dm"},
		"-9223372036854775808": {ID: "-9223372036854775808", Kind: "group"},
	} {
		got, ok := address(connectors.Config{}, raw)
		require.True(t, ok, "%q", raw)
		require.Equal(t, want, got, "%q", raw)
	}
}

// CID-31: what is no chat id is refused: a username, a link, a number that is not an id, a plus, a
// leading zero, more than 64 bits and the id of a chat of Partyline.
func TestAddressRefusesWhatTelegramCannotAddress(t *testing.T) {
	for _, raw := range []string{
		"", " ", "@channel", "t.me/channel", "https://t.me/channel", "12ab", "+123456789", "0", "-0", "-", "007",
		"-007", "1.5", "1e3", "9223372036854775808", "-9223372036854775809", "99999999999999999999",
		"123 456", "123\n456", "0198c2f4-7a3b-7c3e-9b1d-2f4a5b6c7d8e", "٣٤٥",
	} {
		_, ok := address(connectors.Config{}, raw)
		require.False(t, ok, "%q", raw)
	}
}

func TestKindAcceptsNewChats(t *testing.T) {
	require.NotNil(t, Kind().Address)
}
