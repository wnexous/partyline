// Package convert turns service values into API DTOs. It is shared by the HTTP handlers and
// the live hub so both render rooms and messages identically.
package convert

import (
	"database/sql"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/platform"
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
		Name: r.Name, Description: r.Description, Instructions: r.Instructions, OrgID: r.OrgID.String, OrgIDs: orgIDs(r.OrgIDs),
		Owner:         dto.User{ID: r.OwnerID, Username: r.OwnerUsername, DisplayName: r.OwnerIntelligence.DisplayName, Intelligence: ownerRef(r)},
		MyLevel:       r.Level,
		RetentionDays: int(r.RetentionDays), AgentsPaused: r.AgentsPaused, CanResume: r.CanResume, LastSeq: r.LastSeq(),
		MemberCount: int(r.MemberCount),
		CreatedAt:   Time(r.CreatedAt), UpdatedAt: Time(r.UpdatedAt),
	}
}

// orgIDs is the tags of a room, an empty list rather than null when it has none.
func orgIDs(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}

// Message renders a message with its attachments.
func Message(m rooms.Message) dto.Message {
	out := dto.Message{
		ID: m.ID, RoomID: m.RoomID, Seq: m.Seq, AuthorKind: dto.AuthorKind(m.AuthorKind), AuthorID: m.AuthorID,
		AuthorName: m.AuthorName, Author: messageAuthor(m), AuthorUserID: m.AuthorUserID, SenderID: m.SenderID, SenderAltID: m.SenderAltID,
		SenderName: m.SenderName, Kind: dto.MessageKind(m.Kind), Text: m.Text, ReplyTo: m.ReplyTo,
		Recipients: m.Recipients(), ExternalID: m.ExternalID.String,
		Attachments: make([]dto.Attachment, len(m.Attachments)), CreatedAt: Time(m.CreatedAt),
	}
	for i, a := range m.Attachments {
		out.Attachments[i] = dto.Attachment{ID: a.ID, Name: a.Name, Mime: a.Mime, Size: a.Size, Status: dto.AttachmentStatus(a.Status)}
	}
	if m.Chat != nil {
		out.Chat = &dto.ChatRef{ID: m.Chat.ID, Title: m.Chat.Title, Kind: dto.ChatKind(m.Chat.Kind), Platform: m.Chat.Platform}
	}
	return out
}

// PlatformEvent renders a queued platform message as the AI's session and the pending endpoint
// receive it (CID-06). The platform's own message id (ExternalID) and the blob hashes of the files
// stay inside the server.
func PlatformEvent(e platform.Event) dto.PlatformEvent {
	out := dto.PlatformEvent{
		ID: e.ID, Platform: e.Platform, ConnectionID: e.ConnectionID, ChatID: e.ChatID, ChatTitle: e.ChatTitle, ChatKind: e.ChatKind,
		SenderName: e.SenderName, SenderID: e.SenderID, Trusted: e.Trusted, FromSelf: e.FromSelf, Kind: e.Kind, Text: e.Text,
		Attachments: make([]dto.Attachment, len(e.Attachments)), ReceivedAt: e.ReceivedAt,
	}
	for i, a := range e.Attachments {
		out.Attachments[i] = dto.Attachment{ID: a.ID, Name: a.Name, Mime: a.Mime, Size: a.Size, Status: a.Status}
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

// ManagerRefs renders the identities ids, in that order, as the refs of the users and AIs they are (the
// role follows the kind); rows holds their intelligences by id and an id with no row is left out. The
// result is never nil.
func ManagerRefs(ids []string, rows map[string]db.Intelligence) []dto.IntelligenceRef {
	out := make([]dto.IntelligenceRef, 0, len(ids))
	for _, id := range ids {
		row, ok := rows[id]
		if !ok {
			continue
		}
		role := dto.IntelligenceRoleUser
		if dto.IntelligenceKind(row.Kind) == dto.IntelligenceKindAI {
			role = dto.IntelligenceRoleAI
		}
		out = append(out, IntelligenceRef(row, role, ""))
	}
	return out
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
