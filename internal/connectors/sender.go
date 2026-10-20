package connectors

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/attachments"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// Sender delivers outbox entries through the connectors' Manager; it implements
// delivery.Sender for the outbox worker (DLV-07). Errors carry no secrets (the Manager
// redacts them) and no message text.
type Sender struct {
	store   *store.Store
	rooms   *rooms.Service
	blobs   *attachments.Store
	manager *Manager
}

// NewSender returns a Sender.
func NewSender(st *store.Store, rs *rooms.Service, blobs *attachments.Store, m *Manager) *Sender {
	return &Sender{store: st, rooms: rs, blobs: blobs, manager: m}
}

var _ delivery.Sender = (*Sender)(nil)

// Send loads the message, its stored attachments and the chat, adds the quote when the
// replied message came from or was sent to the same chat, sets the author from the agent
// member's settings (MEM-01, MEM-02), and sends it through the chat's connector. A stopped connector fails with ErrNotRunning, so the outbox retries (CON-05).
func (s *Sender) Send(ctx context.Context, d delivery.Delivery) (string, error) {
	chat, err := s.store.R().GetChat(ctx, d.ChatID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("chat no longer exists")
	}
	if err != nil {
		return "", err
	}
	view, err := s.store.R().GetMessageView(ctx, d.MessageID)
	if err != nil {
		return "", err
	}
	msgs, err := s.rooms.Details(ctx, []db.MessageView{view})
	if err != nil {
		return "", err
	}
	msg := msgs[0]
	out := Outbound{
		ChatID: chat.ExternalID, Text: msg.Text, MessageID: msg.ID, RoomID: msg.RoomID,
		AuthorName: msg.AuthorName, CreatedAt: time.UnixMilli(msg.CreatedAt).UTC(),
	}
	if msg.AuthorKind == string(dto.AuthorKindAgent) {
		// MEM-01: an agent member with its signature off sends without the author header;
		// AuthorName is already its display name in the room when it has one (MEM-02).
		m, err := s.store.R().GetRoomMemberByMember(ctx, db.GetRoomMemberByMemberParams{
			RoomID: msg.RoomID, Kind: string(dto.MemberKindAgent), MemberID: msg.AuthorID,
		})
		switch {
		case err == nil && !m.SignOutbound:
			out.AuthorName = ""
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return "", err
		}
	}
	if msg.ReplyTo != "" {
		if out.ReplyToID, out.ReplyToSenderID, err = s.quote(ctx, msg.ReplyTo, chat.ID); err != nil {
			return "", err
		}
	}
	for _, a := range msg.Attachments {
		if a.Status != string(dto.AttachmentStatusStored) {
			continue
		}
		sha := a.Sha256
		out.Files = append(out.Files, OutFile{
			ID: a.ID, Name: a.Name, Mime: a.Mime, Size: a.Size,
			Open: func() (io.ReadCloser, error) { return s.blobs.Open(sha) },
		})
	}
	return s.manager.Send(ctx, chat.ConnectorID, out)
}

// quote returns the platform id (and sender) of the replied message in the chat: the
// message itself when the chat sent it, or Partyline's delivery of it to the chat.
func (s *Sender) quote(ctx context.Context, messageID, chatID string) (string, string, error) {
	replied, err := s.store.R().GetMessageView(ctx, messageID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	if replied.AuthorKind == string(dto.AuthorKindChat) && replied.AuthorID == chatID && replied.ExternalID.Valid {
		return replied.ExternalID.String, replied.SenderID, nil
	}
	ext, err := s.store.R().GetSentExternalID(ctx, db.GetSentExternalIDParams{MessageID: messageID, ChatID: chatID})
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return ext, "", err
}
