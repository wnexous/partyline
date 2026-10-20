package connectors

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/attachments"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/identities"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// pairingCommand matches "!link CODE" and "/link CODE" (IDN-02), case-insensitively.
var pairingCommand = regexp.MustCompile(`(?i)^\s*[!/]link\s+([A-Z2-9]{6})\s*$`)

// PipelineDeps are the collaborators of the inbound Pipeline.
type PipelineDeps struct {
	Store      *store.Store
	Bus        *events.Bus
	Rooms      *rooms.Service
	Blobs      *attachments.Store
	Identities *identities.Service
	// MaxUploadBytes bounds stored media (CON-11).
	MaxUploadBytes int64
	Logger         *slog.Logger
	// Now returns the current time; defaults to time.Now.
	Now func() time.Time
}

// Pipeline turns messages received by connectors into chats and room messages.
type Pipeline struct {
	d PipelineDeps
}

// NewPipeline returns a Pipeline.
func NewPipeline(d PipelineDeps) *Pipeline {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Pipeline{d: d}
}

// Handle processes one received message: own messages are dropped (CON-12), pairing
// commands link the sender on this connector and stop (IDN-02, IDN-03, IDN-05), the chat is recorded (CON-07) and,
// when the chat belongs to rooms, the message is stored once per room with its media
// (CON-08, CON-10, CON-11). Failures are logged without message content.
func (p *Pipeline) Handle(ctx context.Context, src Source, m Inbound) {
	if err := p.handle(ctx, src, m); err != nil && ctx.Err() == nil {
		p.d.Logger.Error("connectors: inbound message failed", "connector_id", src.ConnectorID, "err", err)
	}
}

func (p *Pipeline) handle(ctx context.Context, src Source, m Inbound) error {
	if m.FromSelf || m.ChatID == "" {
		return nil
	}
	if match := pairingCommand.FindStringSubmatch(m.Text); match != nil {
		onlyUser := ""
		if src.LinkOwnerOnly {
			if src.OwnerID == "" {
				return nil
			}
			onlyUser = src.OwnerID
		}
		_, _, err := p.d.Identities.Link(ctx, match[1], identities.Account{
			ConnectorID: src.ConnectorID, ConnectorKind: src.Kind, SenderID: m.SenderID, AltID: m.SenderAltID, Display: m.SenderName,
		}, onlyUser)
		return err
	}
	at := m.At
	if at.IsZero() {
		at = p.d.Now()
	}
	chat, err := UpsertChat(ctx, p.d.Store, p.d.Bus, src.ConnectorID, ChatInfo{ID: m.ChatID, Title: m.ChatTitle, Kind: m.ChatKind}, at, p.d.Now())
	if err != nil {
		return err
	}
	roomIDs, err := p.d.Store.R().ListChatRoomIDs(ctx, chat.ID)
	if err != nil || len(roomIDs) == 0 {
		return err // CON-07: unrouted chats keep no message
	}
	if strings.TrimSpace(m.Text) == "" && len(m.Media) == 0 {
		return nil
	}
	if roomIDs, err = p.pending(ctx, chat.ID, m.MessageID, roomIDs); err != nil || len(roomIDs) == 0 {
		return err
	}
	senderUser, err := p.d.Identities.ResolveSender(ctx, src.ConnectorID, m.SenderID, m.SenderAltID)
	if err != nil {
		return err
	}
	files := p.saveMedia(ctx, src, m.Media)
	kind := dto.MessageKind(m.Kind)
	if kind == "" {
		kind = dto.MessageKindText
	}
	for _, roomID := range roomIDs {
		replyTo := ""
		if m.ReplyToID != "" {
			replyTo, err = p.d.Store.R().FindChatMessageRef(ctx, db.FindChatMessageRefParams{
				RoomID: roomID, ChatID: chat.ID, ExternalID: sql.NullString{String: m.ReplyToID, Valid: true},
			})
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		_, err := p.d.Rooms.PostFromChat(ctx, roomID, rooms.ChatMessage{
			ChatID: chat.ID, SenderID: m.SenderID, SenderAltID: m.SenderAltID, SenderName: m.SenderName,
			SenderUserID: senderUser, ExternalID: m.MessageID, Kind: kind, Text: m.Text, ReplyTo: replyTo, Files: files,
		})
		var verr *validation.Error
		switch {
		case errors.As(err, &verr):
			p.d.Logger.Warn("connectors: inbound message rejected", "connector_id", src.ConnectorID, "room_id", roomID, "err", err)
		case errors.Is(err, access.ErrNotFound): // the chat left the room meanwhile
		case err != nil:
			return err
		}
	}
	return nil
}

// pending drops the rooms that already hold the message, so repeats download nothing (CON-10).
func (p *Pipeline) pending(ctx context.Context, chatID, externalID string, roomIDs []string) ([]string, error) {
	if externalID == "" {
		return roomIDs, nil
	}
	var out []string
	for _, roomID := range roomIDs {
		_, err := p.d.Store.R().GetMessageIDByExternalID(ctx, db.GetMessageIDByExternalIDParams{
			RoomID: roomID, AuthorKind: string(dto.AuthorKindChat), AuthorID: chatID,
			ExternalID: sql.NullString{String: externalID, Valid: true},
		})
		switch {
		case errors.Is(err, sql.ErrNoRows):
			out = append(out, roomID)
		case err != nil:
			return nil, err
		}
	}
	return out, nil
}

// saveMedia stores each media once in the blob store (CON-11). Media over the upload limit
// becomes a too_large record without a file; a failed download becomes unavailable.
func (p *Pipeline) saveMedia(ctx context.Context, src Source, media []Media) []rooms.File {
	files := make([]rooms.File, 0, len(media))
	limit := p.d.MaxUploadBytes
	for _, md := range media {
		f := rooms.File{Name: md.Name, Mime: md.Mime, Size: md.Size}
		if f.Name == "" {
			f.Name = "file"
		}
		if f.Mime == "" {
			f.Mime = "application/octet-stream"
		}
		if md.Size > limit {
			f.Status = dto.AttachmentStatusTooLarge
			files = append(files, f)
			continue
		}
		sha, size, err := p.download(ctx, md, limit)
		switch {
		case errors.Is(err, attachments.ErrTooLarge):
			f.Status = dto.AttachmentStatusTooLarge
		case err != nil:
			p.d.Logger.Warn("connectors: media download failed", "connector_id", src.ConnectorID, "err", err)
			f.Status = dto.AttachmentStatusUnavailable
		default:
			f.SHA256, f.Size, f.Status = sha, size, dto.AttachmentStatusStored
		}
		files = append(files, f)
	}
	return files
}

func (p *Pipeline) download(ctx context.Context, md Media, limit int64) (string, int64, error) {
	if md.Open == nil {
		return "", 0, errors.New("media without content")
	}
	r, err := md.Open(ctx)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = r.Close() }()
	return p.d.Blobs.Put(r, limit)
}

// MigrateChat changes a chat's platform id from oldID to newID, keeping the chat record
// and therefore its rooms (TG-01 edge case: group upgraded to a supergroup). Nothing changes
// when oldID is unknown or newID is already a chat of the connector.
func (p *Pipeline) MigrateChat(ctx context.Context, src Source, oldID, newID string) {
	if oldID == "" || newID == "" || oldID == newID {
		return
	}
	var n int64
	err := p.d.Store.WithTx(ctx, func(q *db.Queries) error {
		var err error
		n, err = q.MigrateChatExternalID(ctx, db.MigrateChatExternalIDParams{
			NewExternalID: newID, ConnectorID: src.ConnectorID, OldExternalID: oldID,
		})
		return err
	})
	if err != nil {
		p.d.Logger.Error("connectors: chat migration failed", "connector_id", src.ConnectorID, "err", err)
		return
	}
	if n == 0 {
		return
	}
	chat, err := p.d.Store.R().GetChatByExternalID(ctx, db.GetChatByExternalIDParams{ConnectorID: src.ConnectorID, ExternalID: newID})
	if err == nil {
		p.d.Bus.Publish(events.ChatDiscovered{ConnectorID: src.ConnectorID, ChatID: chat.ID})
	}
}

// UpsertChat records a chat of a connector (title and kind are kept when given empty) and
// publishes ChatDiscovered when it is new or its title or kind changed. seen, when not
// zero, becomes its last_seen_at.
func UpsertChat(ctx context.Context, st *store.Store, bus *events.Bus, connectorID string, info ChatInfo, seen, now time.Time) (db.Chat, error) {
	var chat db.Chat
	changed := false
	err := st.WithTx(ctx, func(q *db.Queries) error {
		before, err := q.GetChatByExternalID(ctx, db.GetChatByExternalIDParams{ConnectorID: connectorID, ExternalID: info.ID})
		isNew := errors.Is(err, sql.ErrNoRows)
		if err != nil && !isNew {
			return err
		}
		lastSeen := sql.NullInt64{}
		if !seen.IsZero() {
			lastSeen = sql.NullInt64{Int64: seen.UnixMilli(), Valid: true}
		}
		chat, err = q.UpsertChat(ctx, db.UpsertChatParams{
			ID: store.NewID(), ConnectorID: connectorID, ExternalID: info.ID, Title: info.Title, Kind: info.Kind,
			LastSeenAt: lastSeen, CreatedAt: now.UnixMilli(),
		})
		changed = isNew || chat.Title != before.Title || chat.Kind != before.Kind
		return err
	})
	if err != nil {
		return db.Chat{}, err
	}
	if changed {
		bus.Publish(events.ChatDiscovered{ConnectorID: connectorID, ChatID: chat.ID})
	}
	return chat, nil
}
