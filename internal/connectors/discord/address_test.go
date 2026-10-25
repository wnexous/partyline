package discord

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/connectors"
)

// CID-31: the operator may write to a Discord channel id no chat has been seen at: a snowflake, 17 to
// 20 digits, named the way messages from it name it. Whether the bot may write in that channel is
// Discord's to say; a channel the bot cannot see fails when the message is sent.
func TestAddressAcceptsDiscordChannelIDs(t *testing.T) {
	for raw, want := range map[string]connectors.ChatInfo{
		"123456789012345678":    {ID: "123456789012345678", Kind: "channel"},
		" 123456789012345678\n": {ID: "123456789012345678", Kind: "channel"},
		"12345678901234567":     {ID: "12345678901234567", Kind: "channel"},
		"12345678901234567890":  {ID: "12345678901234567890", Kind: "channel"},
	} {
		got, ok := address(connectors.Config{}, raw)
		require.True(t, ok, "%q", raw)
		require.Equal(t, want, got, "%q", raw)
	}
}

// CID-31: what is no snowflake is refused: a name, a mention, a link, too few or too many digits, a
// sign and the id of a chat of Partyline.
func TestAddressRefusesWhatDiscordCannotAddress(t *testing.T) {
	for _, raw := range []string{
		"", " ", "general", "#general", "<#123456789012345678>", "123456789012345", "1234567890123456",
		"123456789012345678901", "-123456789012345678", "+123456789012345678", "12345678901234567a",
		"https://discord.com/channels/123456789012345678/123456789012345678",
		"0198c2f4-7a3b-7c3e-9b1d-2f4a5b6c7d8e", "١٢٣٤٥٦٧٨٩٠١٢٣٤٥٦٧٨",
	} {
		_, ok := address(connectors.Config{}, raw)
		require.False(t, ok, "%q", raw)
	}
}

func TestKindAcceptsNewChats(t *testing.T) {
	require.NotNil(t, Kind().Address)
}
