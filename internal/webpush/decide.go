package webpush

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// The account's group notification setting (users.push_groups, PUSH-06).
const (
	// PushGroupsAll notifies every group message.
	PushGroupsAll = "all"
	// PushGroupsMentions notifies only the messages that mention the user; it is the default.
	PushGroupsMentions = "mentions"
	// PushGroupsOff notifies no group message; direct messages still notify.
	PushGroupsOff = "off"
)

// dmRoomKind is the rooms.kind of a direct message, whose participants always notify each other.
const dmRoomKind = "dm"

// previewLimit is the maximum number of runes of a notification preview (PUSH-10).
const previewLimit = 120

// Decide reports whether a message notifies userID: a direct message of the user always does, and a
// group message does when the user's setting allows it (PUSH-08). The author is never notified, a
// message the user has already read never is (PUSH-09), and a system notice never is. An unknown
// setting behaves as mentions, the safe default, never as all.
func Decide(mode, roomKind, authorIID, userID string, unread, mentioned bool) bool {
	if !unread || authorIID == userID {
		return false
	}
	if roomKind == dmRoomKind {
		return true
	}
	switch mode {
	case PushGroupsAll:
		return true
	case PushGroupsOff:
		return false
	default:
		return mentioned
	}
}

// Mentions reports whether text mentions @handle, ignoring case: there is no letter, digit or
// underscore right before the at sign and no letter, digit or hyphen right after the handle, so
// "@ana-2" and "@anax" do not mention "ana" (PUSH-08). An empty handle mentions nothing.
func Mentions(text, handle string) bool {
	if handle == "" {
		return false
	}
	needle := "@" + strings.ToLower(handle)
	lower := strings.ToLower(text)
	for i := 0; ; {
		j := strings.Index(lower[i:], needle)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(needle)
		before, _ := utf8.DecodeLastRuneInString(lower[:start])
		after, _ := utf8.DecodeRuneInString(lower[end:])
		if (start == 0 || !isWordRune(before)) && (end == len(lower) || !continuesHandle(after)) {
			return true
		}
		i = start + 1
	}
}

// isWordRune reports whether r is part of a word: a letter, a digit or an underscore.
func isWordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// continuesHandle reports whether r continues a handle that precedes it: a-z, 0-9 or a hyphen.
func continuesHandle(r rune) bool {
	return r == '-' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

// Preview is the notification's short form of a message text: every run of whitespace, newlines
// included, collapses to one space, the ends are trimmed and at most 120 runes remain, the 120th
// being an ellipsis when the text is longer (PUSH-10).
func Preview(text string) string {
	collapsed := strings.Join(strings.Fields(text), " ")
	runes := []rune(collapsed)
	if len(runes) <= previewLimit {
		return collapsed
	}
	return string(runes[:previewLimit-1]) + "…"
}
