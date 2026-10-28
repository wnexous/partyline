package delivery

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// SESS-21: a message addresses one session of an AI with "@handle:label". The handle matches
// case-insensitively on a word boundary (the rules of containsBounded/continuesHandle), the
// label is a-z0-9- (any case, read in lowercase) ending at a boundary, with 1 to 40 characters, an invalid or over-long label is not an address
// (the scan continues) and the first valid address wins.
func TestSessionLabel(t *testing.T) {
	longLabel := ""
	for range 41 {
		longLabel += "a"
	}
	cases := []struct {
		name string
		text string
		want string
	}{
		{"label after text", "hi @atlas:claude-2", "claude-2"},
		{"mention without a label", "@atlas", ""},
		{"empty label", "@atlas:", ""},
		{"uppercase label is read in lowercase", "@atlas:Bad", "bad"},
		{"longer label", "@atlas:claude-2x", "claude-2x"},
		{"label before punctuation", "@atlas:claude-2.", "claude-2"},
		{"invalid label then a valid one", "@atlas:_bad @atlas:wt", "wt"},
		{"first address wins", "@atlas:claude-2 @atlas:wt", "claude-2"},
		{"handle continued by a hyphen", "@atlas-two:x", ""},
		{"mention after a word character", "x@atlas:wt", ""},
		{"label before a closing parenthesis", "(@atlas:wt)", "wt"},
		{"handle in another case", "@ATLAS:wt", "wt"},
		{"uppercase label in another case", "@ATLAS:WT", "wt"},
		{"host name is no address", "@atlas:wt.example.com", ""},
		{"path after the label is no address", "@atlas:wt/x", ""},
		{"URL-embedded address is no address", "see https://example.com/@atlas:wt", ""},
		{"path-embedded address is no address", "x/@atlas:wt", ""},
		{"label before a sentence end", "ping @atlas:wt. Thanks", "wt"},
		{"second colon is no boundary", "@atlas:wt:x", ""},
		{"over-long label", "@atlas:" + longLabel, ""},
		{"empty text", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, SessionLabel(c.text, "atlas"))
		})
	}
	require.Empty(t, SessionLabel("@atlas:wt", ""), "a missing handle never matches")
}

// SESS-21: a plain mention names no session, so it still routes to the principal.
func TestSessionLabelLeavesAPlainMentionAlone(t *testing.T) {
	require.Empty(t, SessionLabel("@atlas hello", "atlas"))
	require.Empty(t, SessionLabel("@atlas, hello", "atlas"))
}

// SESS-26: the delivery mode still sees "@handle:label" as a mention of the handle, because
// the colon ends the handle; the parser only decides which session receives it.
func TestWantsStillCountsALabelledMention(t *testing.T) {
	prefs := MemberPrefs{Delivery: dto.DeliveryModeMentions, Handle: "atlas"}
	require.True(t, Wants(prefs, Delivered{Text: "@atlas:wt ship it"}))
}
