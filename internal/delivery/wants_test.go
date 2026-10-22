package delivery

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
)

// MEM-03, MEM-04 and the spec's mention definition: all delivers everything; trusted only
// trusted messages; mentions only "@<display name>" or "@<agent name>" (case-insensitive, on
// word boundaries), replies to the agent's messages, keywords on word boundaries, and every
// message of a DM chat.
func TestWantsTable(t *testing.T) {
	prefs := func(mode dto.DeliveryMode) MemberPrefs {
		return MemberPrefs{Delivery: mode, Names: []string{"Lollerie Bot", "builder"}, Keywords: []string{"deploy", "on call"}}
	}
	cases := []struct {
		name string
		mode dto.DeliveryMode
		d    Delivered
		want bool
	}{
		{"all: plain group text", dto.DeliveryModeAll, Delivered{Text: "hello", ChatKind: "group"}, true},
		{"all: untrusted", dto.DeliveryModeAll, Delivered{Text: "hello"}, true},
		{"empty mode means all", "", Delivered{Text: "hello"}, true},

		{"trusted: trusted", dto.DeliveryModeTrusted, Delivered{Text: "hello", Trusted: true}, true},
		{"trusted: untrusted", dto.DeliveryModeTrusted, Delivered{Text: "hello"}, false},
		{"trusted: untrusted mention", dto.DeliveryModeTrusted, Delivered{Text: "@builder hi"}, false},
		{"trusted: untrusted DM", dto.DeliveryModeTrusted, Delivered{Text: "hi", ChatKind: "dm"}, false},

		{"mentions: plain", dto.DeliveryModeMentions, Delivered{Text: "hello everyone"}, false},
		{"mentions: trusted alone is not a mention", dto.DeliveryModeMentions, Delivered{Text: "hello", Trusted: true}, false},
		{"mentions: DM chat", dto.DeliveryModeMentions, Delivered{Text: "oi", ChatKind: "dm"}, true},
		{"mentions: group chat", dto.DeliveryModeMentions, Delivered{Text: "oi", ChatKind: "group"}, false},
		{"mentions: channel chat", dto.DeliveryModeMentions, Delivered{Text: "oi", ChatKind: "channel"}, false},
		{"mentions: reply to me", dto.DeliveryModeMentions, Delivered{Text: "ok", ReplyToAuthorIsMe: true}, true},
		{"mentions: @agent name", dto.DeliveryModeMentions, Delivered{Text: "hey @builder ship it"}, true},
		{"mentions: @agent name, other case", dto.DeliveryModeMentions, Delivered{Text: "hey @BUILDER"}, true},
		{"mentions: @name at start, punctuation after", dto.DeliveryModeMentions, Delivered{Text: "@builder, ship it"}, true},
		{"mentions: @display name with a space", dto.DeliveryModeMentions, Delivered{Text: "oi @lollerie bot!"}, true},
		{"mentions: name without @", dto.DeliveryModeMentions, Delivered{Text: "the builder is down"}, false},
		{"mentions: @name inside a longer word", dto.DeliveryModeMentions, Delivered{Text: "ask @builders"}, false},
		{"mentions: @name after a word character", dto.DeliveryModeMentions, Delivered{Text: "mail x@builder now"}, false},
		{"mentions: @name with a digit after", dto.DeliveryModeMentions, Delivered{Text: "@builder2 go"}, false},
		{"mentions: @name continued by a hyphen", dto.DeliveryModeMentions, Delivered{Text: "@builder-2 go"}, false},
		{"mentions: @name before a spaced hyphen", dto.DeliveryModeMentions, Delivered{Text: "@builder - go"}, true},
		{"mentions: second occurrence matches", dto.DeliveryModeMentions, Delivered{Text: "@builders and @builder"}, true},
		{"mentions: keyword", dto.DeliveryModeMentions, Delivered{Text: "please deploy now"}, true},
		{"mentions: keyword, other case and punctuation", dto.DeliveryModeMentions, Delivered{Text: "DEPLOY!"}, true},
		{"mentions: keyword inside a longer word", dto.DeliveryModeMentions, Delivered{Text: "redeploy it"}, false},
		{"mentions: keyword as a prefix", dto.DeliveryModeMentions, Delivered{Text: "deployment done"}, false},
		{"mentions: keyword between letters with accents", dto.DeliveryModeMentions, Delivered{Text: "édeployé"}, false},
		{"mentions: multi-word keyword", dto.DeliveryModeMentions, Delivered{Text: "who is on call today"}, true},
		{"mentions: keyword with an underscore after", dto.DeliveryModeMentions, Delivered{Text: "deploy_bot"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, Wants(prefs(c.mode), c.d))
		})
	}
}

// Empty names and keywords never match ("@" alone is not a mention of an agent without a
// display name).
func TestWantsIgnoresEmptyNames(t *testing.T) {
	p := MemberPrefs{Delivery: dto.DeliveryModeMentions, Names: []string{"", "builder"}, Keywords: []string{""}}
	require.False(t, Wants(p, Delivered{Text: "@ hello"}))
	require.False(t, Wants(p, Delivered{Text: "hello"}))
	require.True(t, Wants(p, Delivered{Text: "@builder"}))
}

// Prefs decodes a member row's settings: the name set in the room and the profile's display
// name are mention names, the profile's handle is the handle, and the keywords come from the
// stored JSON. The internal agent name is no mention (AID-06).
func TestPrefsFromMember(t *testing.T) {
	profile := db.Intelligence{ID: "i1", Kind: "ai", DisplayName: "Orion Bot", Handle: "nova"}
	p := Prefs(profile, "Lolo", "mentions", `["deploy"]`)
	require.Equal(t, MemberPrefs{Delivery: dto.DeliveryModeMentions, Names: []string{"Lolo", "Orion Bot"}, Handle: "nova", Keywords: []string{"deploy"}}, p)
	require.True(t, Wants(p, Delivered{Text: "@lolo"}), "the name set in the room")
	require.True(t, Wants(p, Delivered{Text: "@orion bot"}), "the profile name")
	require.True(t, Wants(p, Delivered{Text: "@nova"}), "the handle")
	require.True(t, Wants(p, Delivered{Text: "deploy"}), "the keywords")
	require.False(t, Wants(p, Delivered{Text: "@builder"}), "the internal agent name")
}

// A project member still answers to its project's name, next to the active session's profile.
func TestProjectPrefsKeepTheProjectName(t *testing.T) {
	profile := db.Intelligence{DisplayName: "Orion Bot", Handle: "nova"}
	p := ProjectPrefs(profile, "Proj", "", "mentions", "[]")
	require.True(t, Wants(p, Delivered{Text: "@proj"}))
	require.True(t, Wants(p, Delivered{Text: "@nova"}))
	require.True(t, Wants(p, Delivered{Text: "@orion bot"}))
}
