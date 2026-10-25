package webpush_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/webpush"
)

// PUSH-08: every row of the design's decision table, in order, including one case per column that
// excludes on its own.
func TestDecideMatrix(t *testing.T) {
	const (
		alice = "iid-alice"
		bob   = "iid-bob"
		dm    = "dm"
		group = "group"
	)
	tests := []struct {
		name      string
		mode      string
		kind      string
		authorIID string
		userID    string
		unread    bool
		mentioned bool
		want      bool
	}{
		{"DM any setting unread not the author", webpush.PushGroupsOff, dm, alice, bob, true, false, true},
		{"DM mentions setting", webpush.PushGroupsMentions, dm, alice, bob, true, false, true},
		{"DM all setting", webpush.PushGroupsAll, dm, alice, bob, true, false, true},
		{"DM whose author is the user", webpush.PushGroupsAll, dm, alice, alice, true, true, false},
		{"group all", webpush.PushGroupsAll, group, alice, bob, true, false, true},
		{"group mentions with a mention", webpush.PushGroupsMentions, group, alice, bob, true, true, true},
		{"group mentions without a mention", webpush.PushGroupsMentions, group, alice, bob, true, false, false},
		{"group off with a mention", webpush.PushGroupsOff, group, alice, bob, true, true, false},
		{"group off without a mention", webpush.PushGroupsOff, group, alice, bob, true, false, false},
		{"group whose author is the user", webpush.PushGroupsAll, group, alice, alice, true, true, false},
		{"unknown setting with a mention", "corrupted", group, alice, bob, true, true, true},
		{"unknown setting without a mention", "corrupted", group, alice, bob, true, false, false},
		{"DM read", webpush.PushGroupsAll, dm, alice, bob, false, true, false},
		{"group all read", webpush.PushGroupsAll, group, alice, bob, false, false, false},
		{"group mentions read", webpush.PushGroupsMentions, group, alice, bob, false, true, false},
		{"group off read", webpush.PushGroupsOff, group, alice, bob, false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want,
				webpush.Decide(tt.mode, tt.kind, tt.authorIID, tt.userID, tt.unread, tt.mentioned))
		})
	}
}

// PUSH-08: the mention boundary rule is the delivery one applied to the handle.
func TestMentions(t *testing.T) {
	tests := []struct {
		text   string
		handle string
		want   bool
	}{
		{"@ana", "ana", true},
		{"hi @ANA", "ana", true},
		{"(@ana)", "ana", true},
		{"@ana-2", "ana", false},
		{"@anax", "ana", false},
		{"x@ana", "ana", false},
		{"@ana,", "ana", true},
		{"", "ana", false},
		{"@ana hi", "", false},
		{"@ana_2", "ana", true},
		{"@ANA-and-bob", "ana", false},
		{"say @ana and @bob", "bob", true},
	}
	for _, tt := range tests {
		t.Run(tt.text+"/"+tt.handle, func(t *testing.T) {
			require.Equal(t, tt.want, webpush.Mentions(tt.text, tt.handle))
		})
	}
}

// PUSH-10: a preview collapses whitespace, trims and is at most 120 runes, the 120th being an
// ellipsis when the text is longer.
func TestPreview(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"120 runes stay", strings.Repeat("a", 120), strings.Repeat("a", 120)},
		{"121 runes lose the last and gain an ellipsis", strings.Repeat("a", 121), strings.Repeat("a", 119) + "…"},
		{"whitespace runs collapse", "a\n\nb\t c", "a b c"},
		{"leading and trailing whitespace is trimmed", "  a b  \n", "a b"},
		{"file only", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := webpush.Preview(tt.text)
			require.Equal(t, tt.want, got)
			require.LessOrEqual(t, len([]rune(got)), 120)
		})
	}
}
