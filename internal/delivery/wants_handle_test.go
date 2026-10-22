package delivery

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// AID-06: "@<handle>" mentions the AI (case-insensitive, with no letter, digit or underscore
// right before the "@") unless more handle characters (a-z, 0-9, "-") follow it, so "@atlas"
// does not reach "@atlas-2".
func TestWantsHandle(t *testing.T) {
	// The display name "Atlas" is the same as the handle until the AI is renamed.
	p := MemberPrefs{Delivery: dto.DeliveryModeMentions, Handle: "atlas", Names: []string{"Orion Bot", "Atlas"}}
	cases := []struct {
		text string
		want bool
	}{
		{"@atlas", true},
		{"@ATLAS, ship it", true},
		{"hey @Atlas.", true},
		{"(@atlas)", true},
		{"ping @atlas!", true},
		{"line one\n@atlas line two", true},
		{"@atlas-2 hello", false},
		{"@atlas2 hello", false},
		{"@atlasx hello", false},
		{"@atlas-bot hello", false},
		{"@atlas- hello", false},
		{"@at hello", false},
		{"@atlas-2 and then @atlas", true},
		{"x@atlas", false},
		{"mail me at joe@atlas.example", false},
		{"1@atlas", false},
		{"_@atlas", false},
		{"atlas without the at sign", false},
		{"@ atlas", false},
		{"@orion bot please", true},
	}
	for _, c := range cases {
		require.Equal(t, c.want, Wants(p, Delivered{Text: c.text}), "%q", c.text)
	}
	require.False(t, Wants(MemberPrefs{Delivery: dto.DeliveryModeMentions}, Delivered{Text: "@ and @@ and @-"}), "no handle, no mention")
	require.True(t, Wants(MemberPrefs{Delivery: dto.DeliveryModeAll, Handle: "atlas"}, Delivered{Text: "no mention needed"}), "all still delivers everything")
	require.False(t, Wants(MemberPrefs{Delivery: dto.DeliveryModeTrusted, Handle: "atlas"}, Delivered{Text: "@atlas"}), "trusted still needs a trusted message")
}
