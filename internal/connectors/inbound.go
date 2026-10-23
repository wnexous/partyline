package connectors

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/attachments"
	"github.com/wnexous/partyline/internal/connectors/safehttp"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/identities"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// TruncatedMarker ends inbound text that was cut to the message limit (CON-14).
const TruncatedMarker = "…[truncated]"

// pairingCommand matches "!link CODE" and "/link CODE" (IDN-02), case-insensitively.
var pairingCommand = regexp.MustCompile(`(?i)^\s*[!/]link\s+([A-Z2-9]{6})\s*$`)

// PipelineDeps are the collaborators of the inbound Pipeline.
type PipelineDeps struct {
	Store      *store.Store
	Bus        *events.Bus
	Blobs      *attachments.Store
	Identities *identities.Service
	// Queue takes the messages that pass the connection's filters, for the AI that operates it
	// (AD-015).
	Queue *platform.Queue
	// Sent remembers what Partyline sent, to tell its echoes from what was typed on the account
	// (CID-14); nil remembers nothing, and only the outbox's ids then tell them apart.
	Sent *SentIDs
	// Wait sleeps for d or until ctx is done; defaults to a timer. Tests pass a fake clock's.
	Wait func(ctx context.Context, d time.Duration) error
	// MaxUploadBytes bounds stored media (CON-11).
	MaxUploadBytes int64
	Logger         *slog.Logger
	// Now returns the current time; defaults to time.Now.
	Now func() time.Time
}

// Pipeline turns messages received by connectors into items of their operator's platform queue.
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
	if d.Wait == nil {
		d.Wait = sleep
	}
	return &Pipeline{d: d}
}

// Handle processes one received message (AD-015, design "Inbound"):
//
//  1. a pairing command links the sender on this connector and stops (IDN-02, IDN-03, IDN-05);
//  2. the chat is recorded, which keeps only its id, title and kind (CON-07);
//  3. a connection nobody operates drops the message (CID-02);
//  4. a message of the connection's own account is dropped when it is the echo of a Partyline
//     send, and otherwise goes on as the operator's own typing, from_self and never trusted
//     (CID-14);
//  5. the connection's deliver setting drops the chats it excludes;
//  6. mentions-only drops a group message that neither mentions the account nor replies to a
//     message Partyline sent (CID-12);
//  7. the sender is resolved to the operator's owner or not (CID-13), the media are stored as
//     blobs and the message is queued for the operator, once (CID-26).
//
// Nothing becomes a message of a conversation, a person or a DM (CID-07). Failures are logged
// without message content.
func (p *Pipeline) Handle(ctx context.Context, src Source, m Inbound) {
	if err := p.handle(ctx, src, m); err != nil && ctx.Err() == nil {
		p.d.Logger.Error("connectors: inbound message failed", "connector_id", src.ConnectorID, "err", err)
	}
}

func (p *Pipeline) handle(ctx context.Context, src Source, m Inbound) error {
	if m.ChatID == "" {
		return nil
	}
	if match := pairingCommand.FindStringSubmatch(m.Text); match != nil {
		if m.FromSelf { // the account's own message is no pairing command
			return nil
		}
		return p.pair(ctx, src, m, match[1])
	}
	at := m.At
	if at.IsZero() {
		at = p.d.Now()
	}
	chat, err := UpsertChat(ctx, p.d.Store, p.d.Bus, src.ConnectorID, ChatInfo{ID: m.ChatID, Title: m.ChatTitle, Kind: m.ChatKind}, at, p.d.Now())
	if err != nil || (strings.TrimSpace(m.Text) == "" && len(m.Media) == 0) {
		return err
	}
	conn, err := p.d.Store.R().GetConnector(ctx, src.ConnectorID)
	if err != nil || !conn.OperatorIid.Valid {
		return err
	}
	if m.FromSelf {
		if echo, err := p.settle(ctx, chat.ID, m.MessageID); err != nil || echo {
			return err
		}
	}
	if !delivers(dto.ConnectorDeliver(conn.Deliver), chat.Kind) {
		return nil
	}
	if conn.GroupsMentionsOnly && isGroup(chat.Kind) && !m.MentionsAccount {
		if replies, err := p.repliesToAccount(ctx, chat.ID, m.ReplyToID); err != nil || !replies {
			return err
		}
	}
	return p.enqueue(ctx, src, conn, chat, m)
}

// pair links the sender to the user whose pairing code the message carries (IDN-02, IDN-03).
func (p *Pipeline) pair(ctx context.Context, src Source, m Inbound, code string) error {
	onlyUser := ""
	if src.LinkOwnerOnly {
		if src.OwnerID == "" {
			return nil
		}
		onlyUser = src.OwnerID
	}
	_, _, err := p.d.Identities.Link(ctx, code, identities.Account{
		ConnectorID: src.ConnectorID, ConnectorKind: src.Kind, SenderID: m.SenderID, AltID: m.SenderAltID, Display: m.SenderName,
	}, onlyUser)
	return err
}

// delivers tells whether the deliver setting takes a chat of the kind: dms take direct chats,
// groups take group and channel chats, all takes every chat, also one of unknown kind.
func delivers(deliver dto.ConnectorDeliver, kind string) bool {
	switch deliver {
	case dto.ConnectorDeliverAll:
		return true
	case dto.ConnectorDeliverDMs:
		return kind == string(dto.ChatKindDM)
	case dto.ConnectorDeliverGroups:
		return isGroup(kind)
	default:
		return false
	}
}

// isGroup tells whether a chat kind is a group chat: a group or a channel.
func isGroup(kind string) bool {
	return kind == string(dto.ChatKindGroup) || kind == string(dto.ChatKindChannel)
}

// repliesToAccount tells whether a message answers one that Partyline sent to the chat through
// the connection: the replied platform id is one of its sends (CID-12). The sends it remembers
// are those of the last minutes and those the room outbox and the AI's sends recorded.
func (p *Pipeline) repliesToAccount(ctx context.Context, chatID, replyToID string) (bool, error) {
	return p.sentByPartyline(ctx, chatID, replyToID)
}

// enqueue stores the media as blobs and queues the message for the connection's operator, once
// (CID-26): a repeat downloads nothing and queues nothing. The sender is trusted only when it is
// a verified account of the operator's owner on this connector, never the account's own typing
// (CID-13, CID-14).
func (p *Pipeline) enqueue(ctx context.Context, src Source, conn db.Connector, chat db.Chat, m Inbound) error {
	operator, err := p.d.Store.R().GetAgent(ctx, conn.OperatorIid.String)
	if errors.Is(err, sql.ErrNoRows) { // the operator was deleted meanwhile: nobody to deliver to
		return nil
	}
	if err != nil {
		return err
	}
	if repeat, err := p.d.Queue.Has(ctx, src.ConnectorID, chat.ID, m.MessageID); err != nil || repeat {
		return err
	}
	trusted := false
	if !m.FromSelf {
		if trusted, err = p.trusted(ctx, src, operator.OwnerID, m); err != nil {
			return err
		}
	}
	kind := dto.MessageKind(m.Kind)
	if kind == "" {
		kind = dto.MessageKindText
	}
	// ReceivedAt is when partyline got the message, not the platform's timestamp: a message
	// synced late with an old timestamp still waits 7 days for its AI (CID-10).
	_, _, err = p.d.Queue.Enqueue(ctx, platform.Item{
		AgentID: conn.OperatorIid.String, ConnectorID: src.ConnectorID, ChatID: chat.ID, ExternalID: m.MessageID,
		SenderID: m.SenderID, SenderName: m.SenderName, Trusted: trusted, FromSelf: m.FromSelf,
		Kind: kind, Text: truncate(m.Text), ReceivedAt: p.d.Now(), Attachments: p.saveMedia(ctx, src, m.Media),
	})
	return err
}

// trusted tells whether the sender resolves, through this connector (AD-008), to owner, the user
// who owns the operator AI (AD-006, CID-13).
func (p *Pipeline) trusted(ctx context.Context, src Source, owner string, m Inbound) (bool, error) {
	user, err := p.d.Identities.ResolveSender(ctx, src.ConnectorID, m.SenderID, m.SenderAltID)
	return user != "" && user == owner, err
}

// truncate cuts text longer than the message limit (dto.MaxTextLength code points) to that
// limit, ending it with TruncatedMarker (CON-14).
func truncate(text string) string {
	if utf8.RuneCountInString(text) <= dto.MaxTextLength {
		return text
	}
	keep := dto.MaxTextLength - utf8.RuneCountInString(TruncatedMarker)
	return string([]rune(text)[:keep]) + TruncatedMarker
}

// saveMedia stores each media once in the blob store (CON-11). Media over the upload limit
// becomes a too_large record without a file; a failed download becomes unavailable.
func (p *Pipeline) saveMedia(ctx context.Context, src Source, media []Media) []platform.Attachment {
	files := make([]platform.Attachment, 0, len(media))
	limit := p.d.MaxUploadBytes
	for _, md := range media {
		f := platform.Attachment{Name: md.Name, Mime: md.Mime, Size: md.Size}
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
		case errors.Is(err, attachments.ErrTooLarge), errors.Is(err, safehttp.ErrResponseTooLarge):
			f.Status = dto.AttachmentStatusTooLarge // over the limit, or cut off by the client cap
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
