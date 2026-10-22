package connectors

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/attachments"
	"github.com/wnexous/partyline/internal/connectors/safehttp"
	"github.com/wnexous/partyline/internal/conversations"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/identities"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// TruncatedMarker ends inbound text that was cut to the message limit (CON-14).
const TruncatedMarker = "…[truncated]"

// pairingCommand matches "!link CODE" and "/link CODE" (IDN-02), case-insensitively.
var pairingCommand = regexp.MustCompile(`(?i)^\s*[!/]link\s+([A-Z2-9]{6})\s*$`)

// PipelineDeps are the collaborators of the inbound Pipeline.
type PipelineDeps struct {
	Store      *store.Store
	Bus        *events.Bus
	Rooms      *rooms.Service
	Blobs      *attachments.Store
	Identities *identities.Service
	// People records who writes (PEOP-01); the pipeline builds one from Store, Bus and Now
	// when nil.
	People *People
	// Conversations makes the DMs of platform DM chats (ACC-02); without it new DM chats are
	// routed to the catch-all room only.
	Conversations *conversations.Service
	// Sent remembers what Partyline sent, to tell its echoes from the operator's own messages
	// (ACC-05); nil remembers nothing, and only the outbox's ids then tell them apart.
	Sent *SentIDs
	// Wait sleeps for d or until ctx is done; defaults to a timer. Tests pass a fake clock's.
	Wait func(ctx context.Context, d time.Duration) error
	// MaxUploadBytes bounds stored media (CON-11).
	MaxUploadBytes int64
	Logger         *slog.Logger
	// Now returns the current time; defaults to time.Now.
	Now func() time.Time
}

// Pipeline turns messages received by connectors into chats and room messages.
type Pipeline struct {
	d PipelineDeps
	// mu guards routes.
	mu sync.Mutex
	// routes holds the catch-all routing state of each connector (ROUTE-08, ROUTE-11).
	routes map[string]*routeState
	// routeHook, when set, runs once a catch-all route passed the caps and before the chat
	// is added (tests hold routes with it).
	routeHook func(connectorID string)
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
	if d.People == nil {
		d.People = NewPeople(PeopleDeps{Store: d.Store, Bus: d.Bus, Now: d.Now})
	}
	return &Pipeline{d: d, routes: map[string]*routeState{}}
}

// routeState serializes one connector's catch-all additions, so its caps hold under
// concurrent messages without holding up other connectors (ROUTE-11), and keeps when it
// auto-added chats within the last hour (ROUTE-08).
type routeState struct {
	mu     sync.Mutex
	routed []time.Time
}

// routeState returns the connector's routing state, creating it on first use.
func (p *Pipeline) routeState(connectorID string) *routeState {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.routes[connectorID]
	if st == nil {
		st = &routeState{}
		p.routes[connectorID] = st
	}
	return st
}

// Handle processes one received message: messages of the connector's own account are the
// operator's (CON-12, ACC-05; see handleSelf), pairing commands link the sender on this
// connector and stop (IDN-02, IDN-03, IDN-05), the chat is recorded (CON-07) unless a catch-all
// cap refuses it (ROUTE-11), a chat in no room may join the connector's catch-all room
// (ROUTE-01) and, when the chat belongs to rooms, the message is stored once per room with its
// media (CON-08, CON-10, CON-11). Failures are logged without message content.
func (p *Pipeline) Handle(ctx context.Context, src Source, m Inbound) {
	if err := p.handle(ctx, src, m); err != nil && ctx.Err() == nil {
		p.d.Logger.Error("connectors: inbound message failed", "connector_id", src.ConnectorID, "err", err)
	}
}

func (p *Pipeline) handle(ctx context.Context, src Source, m Inbound) error {
	if m.ChatID == "" {
		return nil
	}
	if m.FromSelf {
		return p.handleSelf(ctx, src, m)
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
	info := ChatInfo{ID: m.ChatID, Title: m.ChatTitle, Kind: m.ChatKind}
	upsert := func() (db.Chat, error) {
		return UpsertChat(ctx, p.d.Store, p.d.Bus, src.ConnectorID, info, at, p.d.Now())
	}
	known, found, err := p.findChat(ctx, src.ConnectorID, m.ChatID)
	if err != nil {
		return err
	}
	var roomIDs, dms []string // the chat's rooms, and those that are DMs
	if found {
		chatRooms, err := p.d.Store.R().ListChatRooms(ctx, known.ID)
		if err != nil {
			return err
		}
		for _, r := range chatRooms {
			roomIDs = append(roomIDs, r.ID)
			if r.Kind == string(dto.ConversationKindDM) {
				dms = append(dms, r.ID)
			}
		}
	}
	if strings.TrimSpace(m.Text) == "" && len(m.Media) == 0 {
		_, err := upsert()
		return err
	}
	kind := info.Kind
	if kind == "" {
		kind = known.Kind
	}
	who := &authorship{}
	chat, roomIDs, err := p.route(ctx, src, m, routing{known: known, found: found, kind: kind, roomIDs: roomIDs, dms: dms, at: at}, upsert, who)
	if err != nil || len(roomIDs) == 0 {
		return err // CON-07: unrouted chats keep no message
	}
	if roomIDs, err = p.pending(ctx, chat.ID, m.MessageID, roomIDs); err != nil || len(roomIDs) == 0 {
		return err
	}
	if err := p.author(ctx, src, chat, m, at, who); err != nil {
		return err
	}
	// A verified sender is the user (PEOP-02), any other the person (GRP-03).
	return p.post(ctx, src, chat, m, roomIDs, who.senderUser, who.iid())
}

// post stores the message once in each room with its media (CON-08, CON-10, CON-11), as written
// by the intelligence author, and by the user senderUser when it resolves to one (DLV-06).
func (p *Pipeline) post(ctx context.Context, src Source, chat db.Chat, m Inbound, roomIDs []string, senderUser, author string) error {
	files := p.saveMedia(ctx, src, m.Media)
	msgKind := dto.MessageKind(m.Kind)
	if msgKind == "" {
		msgKind = dto.MessageKindText
	}
	for _, roomID := range roomIDs {
		replyTo := ""
		if m.ReplyToID != "" {
			var err error
			replyTo, err = p.d.Store.R().FindChatMessageRef(ctx, db.FindChatMessageRefParams{
				RoomID: roomID, ChatID: chat.ID, ExternalID: sql.NullString{String: m.ReplyToID, Valid: true},
			})
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		_, err := p.d.Rooms.PostFromChat(ctx, roomID, rooms.ChatMessage{
			ChatID: chat.ID, SenderID: m.SenderID, SenderAltID: m.SenderAltID, SenderName: m.SenderName,
			SenderUserID: senderUser, AuthorIID: author, ExternalID: m.MessageID, Kind: msgKind, Text: truncate(m.Text), ReplyTo: replyTo, Files: files,
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

// authorship is who wrote a message, resolved once per message: the user the sender is verified
// as and the sender's person, recorded only for a message that reaches rooms.
type authorship struct {
	senderUser string
	userDone   bool
	person     db.Person
	seen       bool
}

// iid is the intelligence that wrote the message: the verified user (PEOP-02), else the person
// (GRP-03); empty for a sender without an id.
func (a *authorship) iid() string {
	if a.senderUser != "" {
		return a.senderUser
	}
	return a.person.ID
}

// verified resolves the user the sender is verified as through this connector (AD-008).
func (p *Pipeline) verified(ctx context.Context, src Source, m Inbound, a *authorship) error {
	if a.userDone {
		return nil
	}
	user, err := p.d.Identities.ResolveSender(ctx, src.ConnectorID, m.SenderID, m.SenderAltID)
	if err != nil {
		return err
	}
	a.senderUser, a.userDone = user, true
	return nil
}

// author resolves who wrote the message and records the sender as a person (PEOP-01).
func (p *Pipeline) author(ctx context.Context, src Source, chat db.Chat, m Inbound, at time.Time, a *authorship) error {
	if err := p.verified(ctx, src, m, a); err != nil {
		return err
	}
	if a.seen {
		return nil
	}
	person, err := p.d.People.Seen(ctx, Sighting{
		ConnectorID: src.ConnectorID, ConnectorKind: src.Kind, Chat: chat,
		SenderID: m.SenderID, AltID: m.SenderAltID, Name: m.SenderName, At: at,
	})
	if err != nil {
		return err
	}
	a.person, a.seen = person, true
	return nil
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
