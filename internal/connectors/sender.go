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
	sent    *SentIDs
}

// NewSender returns a Sender. sent, when given, remembers the platform ids of what it sends, so
// that the inbound pipeline can tell an echo of it from the operator's own message (ACC-05).
func NewSender(st *store.Store, rs *rooms.Service, blobs *attachments.Store, m *Manager, sent *SentIDs) *Sender {
	return &Sender{store: st, rooms: rs, blobs: blobs, manager: m, sent: sent}
}

var _ delivery.Sender = (*Sender)(nil)

// Send loads the message, its stored attachments and the chat, adds the quote when the
// replied message came from or was sent to the same chat, sets the author from the agent
// member's settings (MEM-01, MEM-02) unless the author operates the connector (ACC-03, ACC-04),
// and sends it through the chat's connector. A stopped
// connector fails with ErrNotRunning, so the outbox retries (CON-05). Reaction messages go to
// the connector's Reactor instead (REACT-01).
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
		Created: func(id string) { s.sent.Add(chat.ID, id) },
	}
	if msg.AuthorKind == string(dto.AuthorKindAgent) {
		// MEM-01: an agent member with its signature off sends without the author header;
		// AuthorName is already its display name in the room when it has one (MEM-02).
		m, err := s.authorMember(ctx, msg.RoomID, msg.AuthorID)
		switch {
		case err == nil && !m.SignOutbound:
			out.AuthorName = ""
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return "", err
		}
	}
	// ACC-03, ACC-04: the operator writes through its own account, so its messages carry no
	// author line (an AI through its own account, the owner through theirs); anyone else is named.
	// The author's name still goes to platforms that carry it as data (the webhook payload).
	conn, err := s.store.R().GetConnector(ctx, chat.ConnectorID)
	if err != nil {
		return "", err
	}
	if author := msg.AuthorIid; author != "" && author == operatorID(conn) {
		out.Unsigned = true
	}
	if msg.ReplyTo != "" {
		if out.ReplyToID, out.ReplyToSenderID, err = s.quote(ctx, msg.ReplyTo, chat.ID); err != nil {
			return "", err
		}
	}
	if msg.Kind == string(dto.MessageKindReaction) {
		// REACT-01: a reaction (text = emoji, reply_to = target) goes to the connector's
		// Reactor with the target's platform id; it has no platform id of its own.
		if out.ReplyToID == "" {
			return "", ErrReactionTarget
		}
		return "", s.manager.React(ctx, chat.ConnectorID, Reaction{
			ChatID: out.ChatID, TargetID: out.ReplyToID, TargetSenderID: out.ReplyToSenderID, Emoji: msg.Text,
			MessageID: out.MessageID, RoomID: out.RoomID, AuthorName: out.AuthorName, CreatedAt: out.CreatedAt,
		})
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

// authorMember returns the room member row whose settings sign an agent's message: its own
// membership, else the membership of its project (PROJ-07); sql.ErrNoRows when neither exists.
func (s *Sender) authorMember(ctx context.Context, roomID, agentID string) (db.RoomMember, error) {
	q := s.store.R()
	m, err := q.GetRoomMemberByMember(ctx, db.GetRoomMemberByMemberParams{RoomID: roomID, Kind: string(dto.MemberKindAgent), MemberID: agentID})
	if !errors.Is(err, sql.ErrNoRows) {
		return m, err
	}
	a, err := q.GetAgent(ctx, agentID)
	if err != nil {
		return db.RoomMember{}, err
	}
	if !a.ProjectID.Valid {
		return db.RoomMember{}, sql.ErrNoRows
	}
	return q.GetRoomMemberByMember(ctx, db.GetRoomMemberByMemberParams{RoomID: roomID, Kind: string(dto.MemberKindProject), MemberID: a.ProjectID.String})
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
