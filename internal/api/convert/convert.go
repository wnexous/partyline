// Package convert turns service values into API DTOs. It is shared by the HTTP handlers and
// the live hub so both render rooms and messages identically.
package convert

import (
	"database/sql"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store/db"
)

// Time converts unix milliseconds to a UTC time.
func Time(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

// Room renders a room as seen by the caller (MyLevel).
func Room(r rooms.Room) dto.Room {
	participants := make([]dto.IntelligenceRef, len(r.Participants))
	for i, p := range r.Participants {
		participants[i] = IntelligenceRef(p.Intelligence, dto.IntelligenceRole(p.Role), p.Platform)
	}
	return dto.Room{
		ID: r.ID, Kind: dto.ConversationKind(r.Kind), Participants: participants,
		Name: r.Name, Description: r.Description, Instructions: r.Instructions,
		Owner:      dto.User{ID: r.OwnerID, Username: r.OwnerUsername, DisplayName: r.OwnerIntelligence.DisplayName, Intelligence: ownerRef(r)},
		Visibility: r.Visibility, MyLevel: r.Level, BridgeChats: r.BridgeChats, Broadcast: r.Broadcast,
		CatchAll: r.CatchAll, RetentionDays: int(r.RetentionDays), AgentsPaused: r.AgentsPaused, LastSeq: r.LastSeq(),
		MemberCount: int(r.MemberCount),
		CreatedAt:   Time(r.CreatedAt), UpdatedAt: Time(r.UpdatedAt),
	}
}

// Message renders a message with its attachments and outbound deliveries.
func Message(m rooms.Message) dto.Message {
	out := dto.Message{
		ID: m.ID, RoomID: m.RoomID, Seq: m.Seq, AuthorKind: dto.AuthorKind(m.AuthorKind), AuthorID: m.AuthorID,
		AuthorName: m.AuthorName, Author: messageAuthor(m), AuthorUserID: m.AuthorUserID, SenderID: m.SenderID, SenderAltID: m.SenderAltID,
		SenderName: m.SenderName, Kind: dto.MessageKind(m.Kind), Text: m.Text, ReplyTo: m.ReplyTo,
		Recipients: m.Recipients(), ExternalID: m.ExternalID.String,
		Attachments: make([]dto.Attachment, len(m.Attachments)), CreatedAt: Time(m.CreatedAt), Warning: m.Warning,
	}
	for i, a := range m.Attachments {
		out.Attachments[i] = dto.Attachment{ID: a.ID, Name: a.Name, Mime: a.Mime, Size: a.Size, Status: dto.AttachmentStatus(a.Status)}
	}
	for _, o := range m.Outbox {
		out.Outbox = append(out.Outbox, OutboxItem(o))
	}
	if m.Chat != nil {
		out.Chat = &dto.ChatRef{ID: m.Chat.ID, Title: m.Chat.Title, Kind: dto.ChatKind(m.Chat.Kind), Platform: m.Chat.Platform}
	}
	return out
}

// ownerRef renders the room owner's intelligence; nil when the owner has none.
func ownerRef(r rooms.Room) *dto.IntelligenceRef {
	if r.OwnerIntelligence.ID == "" {
		return nil
	}
	ref := IntelligenceRef(r.OwnerIntelligence, dto.IntelligenceRoleUser, "")
	return &ref
}

// messageAuthor renders the intelligence that wrote a message from the author columns of the
// message view. A user or agent author without author_iid (written before it was recorded) is
// its own intelligence; a chat message has one only when author_iid is set, the linked user, the
// person of the sender, or the operator of the connector for a message of its own account
// (ACC-05). The display name is the name the timeline shows (AuthorName).
func messageAuthor(m rooms.Message) dto.IntelligenceRef {
	ref := dto.IntelligenceRef{DisplayName: m.AuthorName}
	id := m.AuthorIid
	var role dto.IntelligenceRole
	switch dto.AuthorKind(m.AuthorKind) {
	case dto.AuthorKindUser:
		role = dto.IntelligenceRoleUser
		if id == "" {
			id = m.AuthorID
		}
	case dto.AuthorKindAgent:
		role = dto.IntelligenceRoleAI
		if id == "" {
			id = m.AuthorID
		}
	case dto.AuthorKindChat:
		role = dto.IntelligenceRolePerson
		switch {
		case id != "" && id == m.AuthorUserID:
			role = dto.IntelligenceRoleUser // a sender linked to a user is that user (PEOP-02)
		case dto.IntelligenceKind(m.AuthorIkind) == dto.IntelligenceKindAI:
			role = dto.IntelligenceRoleAI // the AI operating the connector's account wrote it (ACC-05)
		}
	}
	if id == "" {
		return ref
	}
	ref.ID, ref.Role, ref.Handle = id, role, m.AuthorHandle
	ref.Kind = dto.IntelligenceKind(m.AuthorIkind)
	if ref.Kind == "" { // no intelligence row: the role tells
		ref.Kind = dto.IntelligenceKindHuman
		if role == dto.IntelligenceRoleAI {
			ref.Kind = dto.IntelligenceKindAI
		}
	}
	ref.AvatarURL = AvatarURL(id, m.AuthorAvatar)
	if role == dto.IntelligenceRolePerson && m.Chat != nil {
		ref.Platform = m.Chat.Platform
	}
	return ref
}

// FileAuthor renders the intelligence that wrote the message of a room file, as Message renders
// its author (PROF-09): the same columns of the message view, and the platform of the chat that
// wrote it for a person.
func FileAuthor(f db.ListRoomFilesRow) dto.IntelligenceRef {
	m := rooms.Message{MessageView: db.MessageView{
		AuthorKind: f.AuthorKind, AuthorID: f.AuthorID, AuthorUserID: f.AuthorUserID, AuthorIid: f.AuthorIid,
		AuthorName: f.AuthorName, AuthorHandle: f.AuthorHandle, AuthorIkind: f.AuthorIkind, AuthorAvatar: f.AuthorAvatar,
	}}
	if f.ChatPlatform != "" {
		m.Chat = &db.ListChatRefsRow{ID: f.AuthorID, Platform: f.ChatPlatform}
	}
	return messageAuthor(m)
}

// OutboxItem renders one outbound delivery.
func OutboxItem(o db.Outbox) dto.OutboxItem {
	item := dto.OutboxItem{
		ID: o.ID, ChatID: o.ChatID, Status: dto.OutboxStatus(o.Status), Attempts: int(o.Attempts),
		LastError: o.LastError, UpdatedAt: Time(o.UpdatedAt),
	}
	if o.Purpose == string(dto.OutboxPurposeNotify) { // an ordinary delivery carries none
		item.Purpose = dto.OutboxPurposeNotify
	}
	return item
}

// OptTime converts a nullable unix-milliseconds column to a time pointer.
func OptTime(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := Time(v.Int64)
	return &t
}

// Chat renders a chat with its connector's kind and name.
func Chat(c db.Chat, conn db.Connector) dto.Chat {
	return dto.Chat{
		ID: c.ID, ConnectorID: c.ConnectorID, ConnectorKind: conn.Kind, ConnectorName: conn.Name,
		ExternalID: c.ExternalID, Title: c.Title, Kind: dto.ChatKind(c.Kind),
		LastSeenAt: OptTime(c.LastSeenAt), CreatedAt: Time(c.CreatedAt),
	}
}

// Identity renders a verified chat identity.
func Identity(i db.Identity) dto.Identity {
	return dto.Identity{
		ID: i.ID, ConnectorID: i.ConnectorID, ConnectorKind: i.ConnectorKind, ExternalID: i.ExternalID, AltExternalID: i.AltExternalID,
		Display: i.Display, CreatedAt: Time(i.CreatedAt),
	}
}

// IntelligenceRef renders an intelligence as the public summary shown next to every name;
// platform is the connector kind of a person.
func IntelligenceRef(i db.Intelligence, role dto.IntelligenceRole, platform string) dto.IntelligenceRef {
	return dto.IntelligenceRef{
		ID: i.ID, Kind: dto.IntelligenceKind(i.Kind), Role: role, DisplayName: i.DisplayName, Handle: i.Handle,
		AvatarURL: AvatarURL(i.ID, i.AvatarSha256), Platform: platform,
	}
}

// ProfileRef renders the summary of a profile.
func ProfileRef(p intelligences.Profile) dto.IntelligenceRef {
	return IntelligenceRef(p.Intelligence, dto.IntelligenceRole(p.Role), p.Platform)
}

// AvatarURL is the URL of an intelligence's avatar, empty without one. The content hash is a
// cache key: a new avatar has a new URL.
func AvatarURL(id, sha string) string {
	if sha == "" {
		return ""
	}
	return "/api/v1/intelligences/" + id + "/avatar?v=" + sha
}
