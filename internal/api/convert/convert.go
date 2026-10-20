// Package convert turns service values into API DTOs. It is shared by the HTTP handlers and
// the live hub so both render rooms and messages identically.
package convert

import (
	"database/sql"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store/db"
)

// Time converts unix milliseconds to a UTC time.
func Time(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

// Room renders a room as seen by the caller (MyLevel).
func Room(r rooms.Room) dto.Room {
	return dto.Room{
		ID: r.ID, Name: r.Name, Description: r.Description, Instructions: r.Instructions,
		Owner:      dto.User{ID: r.OwnerID, Username: r.OwnerUsername, DisplayName: r.OwnerDisplayName},
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
		AuthorName: m.AuthorName, AuthorUserID: m.AuthorUserID, SenderID: m.SenderID, SenderAltID: m.SenderAltID,
		SenderName: m.SenderName, Kind: dto.MessageKind(m.Kind), Text: m.Text, ReplyTo: m.ReplyTo,
		Recipients: m.Recipients(), ExternalID: m.ExternalID.String,
		Attachments: make([]dto.Attachment, len(m.Attachments)), CreatedAt: Time(m.CreatedAt),
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

// OutboxItem renders one outbound delivery.
func OutboxItem(o db.Outbox) dto.OutboxItem {
	return dto.OutboxItem{
		ID: o.ID, ChatID: o.ChatID, Status: dto.OutboxStatus(o.Status), Attempts: int(o.Attempts),
		LastError: o.LastError, UpdatedAt: Time(o.UpdatedAt),
	}
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
