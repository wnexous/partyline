// Package channel is the agent-host side of Partyline: `partyline channel`, an MCP stdio
// server that Claude Code loads as a channel (AD-004). It holds the agent WebSocket, turns room
// messages into channel notifications and serves the room tools over the agent REST API.
package channel

import (
	"strconv"
	"strings"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/handles"
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
	return Notification{Content: contentOf(msg.Text, msg.Attachments), Meta: meta}
}

// contentOf is the content of a notification: the text, then one line per attachment.
func contentOf(text string, attachments []dto.Attachment) string {
	lines := make([]string, 0, 1+len(attachments))
	if text != "" {
		lines = append(lines, text)
	}
	for _, a := range attachments {
		lines = append(lines, "[attachment "+a.ID+": "+a.Name+", "+a.Mime+"]")
	}
	return strings.Join(lines, "\n")
}

// PlatformType is the type of the notification, and of the line `partyline listen` prints, of a
// platform event; AuthorKindPlatform is its author_kind: the author wrote on the platform, and is
// no member of a Partyline conversation.
const (
	PlatformType       = "platform"
	AuthorKindPlatform = "platform"
)

// FormatPlatform renders a platform event (CID-06): a message that arrived on a connection the AI
// operates. The PlatformEvent fields go onto the keys of a room message, so that every adapter
// frames it like one: the sender is the author, the room is "<Platform> · <chat title>", and
// message_id is the id reply answers; the chat, the connection and the flags follow as chat_id,
// chat_title, chat_kind, platform, connection_id, trusted and from_self (true for what was typed
// on the account itself). Attachments are listed in the content like those of a message.
func FormatPlatform(e dto.PlatformEvent) Notification {
	room := platformLabel(e.Platform)
	if e.ChatTitle != "" {
		room += " · " + e.ChatTitle
	}
	return Notification{Content: contentOf(e.Text, e.Attachments), Meta: map[string]string{
		"type":          PlatformType,
		"room_name":     room,
		"message_id":    e.ID,
		"author":        e.SenderName,
		"author_kind":   AuthorKindPlatform,
		"sender_name":   e.SenderName,
		"sender_id":     e.SenderID,
		"trusted":       strconv.FormatBool(e.Trusted),
		"from_self":     strconv.FormatBool(e.FromSelf),
		"kind":          string(e.Kind),
		"platform":      e.Platform,
		"connection_id": e.ConnectionID,
		"chat_id":       e.ChatID,
		"chat_title":    e.ChatTitle,
		"chat_kind":     string(e.ChatKind),
	}}
}

// platformLabel names a connector kind the way people write it; other kinds are capitalized.
func platformLabel(kind string) string {
	switch kind {
	case "whatsapp":
		return "WhatsApp"
	case "telegram":
		return "Telegram"
	case "discord":
		return "Discord"
	case "webhook":
		return "Webhook"
	case "":
		return "Chat"
	}
	return handles.DisplayName(kind)
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
