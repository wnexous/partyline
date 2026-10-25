package whatsapp

import (
	"strings"

	"github.com/wnexous/partyline/internal/connectors"
)

// address is the Kind's Address (CID-31): the operator may write to a WhatsApp address no chat of the
// connection has been seen at. It is a phone number with its country code (digits only, a leading plus
// allowed), or a JID: a personal one (7 to 15 digits, with or without the device suffix), a LID or a
// group. It names the chat the way messages from it do, on the connection's own instance (one number
// per connection, CID-04), so that what the person answers lands in the same chat, and a number is
// titled with its last four digits as a DM without a push name is (CTX-06). A connection that does not
// name exactly one instance, and anything that is not a chat (status, a broadcast list, a newsletter),
// is refused. Whether the number is on WhatsApp is evolution-go's to say; the send fails when it is not.
func address(cfg connectors.Config, raw string) (connectors.ChatInfo, bool) {
	instances := cfg.Strings("instances")
	if len(instances) != 1 {
		return connectors.ChatInfo{}, false
	}
	user, server, isJID := strings.Cut(strings.TrimSpace(raw), "@")
	if !isJID {
		user, server = strings.TrimPrefix(user, "+"), "s.whatsapp.net"
	} else {
		user, _, _ = strings.Cut(user, ":") // the device a JID may name is not part of the chat
	}
	switch {
	case server == "s.whatsapp.net" && onlyDigits(user, 7, 15):
		return connectors.ChatInfo{ID: ChatID(instances[0], user+"@"+server), Title: maskNumber(user), Kind: "dm"}, true
	case server == "lid" && isJID && onlyDigits(user, 5, 20):
		return connectors.ChatInfo{ID: ChatID(instances[0], user+"@"+server), Kind: "dm"}, true
	case server == "g.us" && isJID && groupID(user):
		return connectors.ChatInfo{ID: ChatID(instances[0], user+"@"+server), Kind: "group"}, true
	}
	return connectors.ChatInfo{}, false
}

// groupID reports whether s is the user part of a group JID: digits, or the creator's number and the
// time of creation joined by a hyphen (the form of older groups).
func groupID(s string) bool {
	creator, created, old := strings.Cut(s, "-")
	return onlyDigits(creator, 1, 30) && (!old || onlyDigits(created, 1, 30))
}

// onlyDigits reports whether s has between lo and hi characters and they are all ASCII digits.
func onlyDigits(s string, lo, hi int) bool {
	return len(s) >= lo && len(s) <= hi && strings.Trim(s, "0123456789") == ""
}
