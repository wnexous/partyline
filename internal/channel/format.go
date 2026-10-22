// Package channel is the agent-host side of Partyline: `partyline channel`, an MCP stdio
// server that Claude Code loads as a channel (AD-004). It holds the agent WebSocket, turns room
// messages into channel notifications and serves the room tools over the agent REST API.
package channel

import (
	"strconv"
	"strings"

	"github.com/wnexous/partyline/internal/api/dto"
)

// NotificationMethod is the MCP notification Claude Code reads channel events from.
const NotificationMethod = "notifications/claude/channel"

// Notification is the params object of a channel notification. Claude Code renders it as
// <channel source="partyline" key="value" ...>content</channel>; meta keys must be
// identifiers (letters, digits, underscores) and values strings (AGT-06).
type Notification struct {
	Content string            `json:"content"`
	Meta    map[string]string `json:"meta"`
}

// Format renders a delivered message (AGT-05): content is the text plus one line per
// attachment; meta carries the room, message and author fields and the recipient-relative
// trusted flag ("true"/"false"). conversation is "dm" for a direct message (room_name is then
// the other participant) and "group" otherwise (DM-03, GRP-04); author_id and author_handle name
// the author's intelligence when it is known. A chat-authored message also names its chat
// (CTX-01): chat_id (usable as a send_message recipient), chat_title, chat_kind, platform and
// sender_id; other authors carry none of them (CTX-02).
func Format(m dto.AgentMessage) Notification {
	msg := m.Message
	lines := make([]string, 0, 1+len(msg.Attachments))
	if msg.Text != "" {
		lines = append(lines, msg.Text)
	}
	for _, a := range msg.Attachments {
		lines = append(lines, "[attachment "+a.ID+": "+a.Name+", "+a.Mime+"]")
	}
	conversation := dto.ConversationKindGroup
	if m.Room.Kind == dto.ConversationKindDM {
		conversation = dto.ConversationKindDM
	}
	meta := map[string]string{
		"conversation": string(conversation),
		"room":         m.Room.ID,
		"room_name":    m.Room.Name,
		"message_id":   msg.ID,
		"author":       msg.AuthorName,
		"author_kind":  string(msg.AuthorKind),
		"sender_name":  msg.SenderName,
		"trusted":      strconv.FormatBool(m.Trusted),
		"kind":         string(msg.Kind),
	}
	if a := msg.Author; a.ID != "" {
		meta["author_id"] = a.ID
		if a.Handle != "" {
			meta["author_handle"] = a.Handle
		}
	}
	if msg.ReplyTo != "" {
		meta["reply_to"] = msg.ReplyTo
	}
	if c := msg.Chat; c != nil && msg.AuthorKind == dto.AuthorKindChat {
		meta["chat_id"] = c.ID
		meta["chat_title"] = c.Title
		meta["chat_kind"] = string(c.Kind)
		meta["platform"] = c.Platform
		meta["sender_id"] = msg.SenderID
	}
	return Notification{Content: strings.Join(lines, "\n"), Meta: meta}
}

// FormatNotice renders a server notice (e.g. skipped backlog, AGT-09) as a system event of
// its room. Notices have no message id and are never acknowledged.
func FormatNotice(n dto.AgentNoticeFrame) Notification {
	return Notification{Content: n.Text, Meta: map[string]string{
		"room":        n.Room.ID,
		"room_name":   n.Room.Name,
		"author":      "Partyline",
		"author_kind": string(dto.AuthorKindSystem),
		"sender_name": "",
		"trusted":     "false",
		"kind":        string(dto.MessageKindSystem),
	}}
}
