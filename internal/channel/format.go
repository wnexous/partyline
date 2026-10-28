// Package channel is the agent-host side of Partyline: `partyline channel`, an MCP stdio
// server that Claude Code loads as a channel (AD-004). It holds the agent WebSocket, turns room
// messages into channel notifications and serves the room tools over the agent REST API.
package channel

import (
	"encoding/json"
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
// sender_id; other authors carry none of them (CTX-02). A message that asks a question or answers
// one also names its stored document in interactive or answers, as the compact JSON the server
// keeps (QR-21); the content stays the human text.
//
// A message an AI's session wrote carries author_session, the session's label, when it is not the
// principal (SESS-41), so two sessions of one AI can be told apart; from_own_session="true" marks one
// written by another session of the receiving AI itself (FormatFor).
func Format(m dto.AgentMessage) Notification { return FormatFor(m, "") }

// FormatFor is Format for the AI whose intelligence id is selfID: a message its own intelligence wrote
// (so, from another session of it) also carries from_own_session="true". An empty selfID marks nothing.
func FormatFor(m dto.AgentMessage, selfID string) Notification {
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
	if msg.AuthorLabel != "" {
		meta["author_session"] = msg.AuthorLabel
	}
	if selfID != "" && msg.AuthorKind == dto.AuthorKindAgent && msg.Author.ID == selfID {
		meta["from_own_session"] = "true"
	}
	if msg.ReplyTo != "" {
		meta["reply_to"] = msg.ReplyTo
	}
	if q := msg.Interactive; q != nil {
		data, _ := json.Marshal(q)
		meta["interactive"] = string(data)
	}
	if a := msg.Answers; a != nil {
		data, _ := json.Marshal(a)
		meta["answers"] = string(data)
	}
	switch {
	case msg.DeletedAt != nil:
		meta["deleted"] = "true"
	case msg.EditedAt != nil:
		meta["edited"] = "true"
	}
	if c := msg.Chat; c != nil && msg.AuthorKind == dto.AuthorKindChat {
		meta["chat_id"] = c.ID
		meta["chat_title"] = c.Title
		meta["chat_kind"] = string(c.Kind)
		meta["platform"] = c.Platform
		meta["sender_id"] = msg.SenderID
	}
	content := contentOf(msg.Text, msg.Attachments)
	if msg.DeletedAt != nil {
		content = DeletedPlaceholder
	}
	if m.ForLabel != "" {
		// The note is server-authored: the label is a delivery fact, never taken from the text
		// (SESS-22, AD-006).
		content = "(addressed to :" + m.ForLabel + ")\n" + content
		meta["for_label"] = m.ForLabel
	}
	return Notification{Content: content, Meta: meta}
}

// DeletedPlaceholder is the content of a deleted message in history, pending messages and the backlog: the
// message stays in its place, with its id, and nothing of what it said (DLT-05). A notification is never empty.
const DeletedPlaceholder = "[message deleted]"

// The type of the notification of a live edit or delete, and the sentences its content is made of. The content
// is words, so every adapter frames it as it frames any message, whatever version it is (EDT-09, DLT-08).
const (
	EditedType  = "edited"
	DeletedType = "deleted"

	editedLead  = "This message was edited. New text:\n"
	editedEmpty = "This message was edited. It no longer has any text."
	deletedText = "This message was deleted."
)

// FormatUpdate renders the live edit or delete of a message (EDT-09, DLT-08): t is
// dto.AgentFrameTypeMessageEdited or dto.AgentFrameTypeMessageDeleted, m the message as it is now. The
// notification is about the original message: it has its message_id and the other keys of Format, so the AI can
// still reply to it, and the trusted flag of its author for this AI; its type is "edited" or "deleted" and its
// content says what happened and, for an edit, gives the new text. It is never acknowledged: the message was
// acknowledged when it was first delivered.
func FormatUpdate(t dto.AgentFrameType, m dto.AgentMessage) Notification {
	return FormatUpdateFor(t, m, "")
}

// FormatUpdateFor is FormatUpdate for the AI whose intelligence id is selfID (see FormatFor).
func FormatUpdateFor(t dto.AgentFrameType, m dto.AgentMessage, selfID string) Notification {
	n := FormatFor(m, selfID)
	switch t {
	case dto.AgentFrameTypeMessageDeleted:
		n.Meta["type"], n.Meta["deleted"] = DeletedType, "true"
		n.Content = deletedText
	default:
		n.Meta["type"], n.Meta["edited"] = EditedType, "true"
		n.Content = editedEmpty
		if m.Message.Text != "" {
			n.Content = editedLead + m.Message.Text
		}
	}
	return n
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
