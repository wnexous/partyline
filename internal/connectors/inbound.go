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
	"github.com/wnexous/partyline/internal/authn"
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

// Handle processes one received message: own messages are dropped (CON-12), pairing
// commands link the sender on this connector and stop (IDN-02, IDN-03, IDN-05), the chat is
// recorded (CON-07) unless a catch-all cap refuses it (ROUTE-11), a chat in no room may join
// the connector's catch-all room (ROUTE-01)
// and, when the chat belongs to rooms, the message is stored once per room with its media
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
	senderUser, author := who.senderUser, who.iid() // a verified sender is the user (PEOP-02), any other the person (GRP-03)
	files := p.saveMedia(ctx, src, m.Media)
	msgKind := dto.MessageKind(m.Kind)
	if msgKind == "" {
		msgKind = dto.MessageKindText
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

// routing is what the pipeline knows of a chat before it routes a message from it.
type routing struct {
	known   db.Chat
	found   bool
	kind    string
	roomIDs []string // the rooms the chat belongs to
	dms     []string // those that are DMs
	at      time.Time
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

// route records the chat (unless a cap refuses it) and returns the rooms the message belongs to:
// the chat's own rooms, or the conversation made for it when the connector routes new chats and
// the chat belongs to none. A connector without a catch-all room gives a DM chat the DM with its
// operator (ACC-02); one with a catch-all room keeps adding chats to it (ROUTE-01).
func (p *Pipeline) route(ctx context.Context, src Source, m Inbound, r routing, upsert func() (db.Chat, error), who *authorship) (db.Chat, []string, error) {
	if r.kind == string(dto.ChatKindDM) && p.d.Conversations != nil {
		c, err := p.d.Store.R().GetConnector(ctx, src.ConnectorID)
		if err != nil {
			return db.Chat{}, nil, err
		}
		if scopeMatches(dto.AutoRouteScope(c.AutoRouteScope), r.kind) && !c.AutoRouteRoomID.Valid {
			if len(r.dms) == len(r.roomIDs) { // in no room, or only in the DMs this routing keeps
				return p.platformDM(ctx, src, c, m, r, upsert, who)
			}
		}
	}
	if len(r.roomIDs) > 0 {
		chat, err := upsert()
		return chat, r.roomIDs, err
	}
	chat, roomID, err := p.autoRoute(ctx, src, m.ChatID, r.kind, upsert)
	if err != nil || roomID == "" {
		return chat, nil, err
	}
	return chat, []string{roomID}, nil
}

// platformDM puts a DM chat in the DM between its sender and the connector's operator (ACC-02),
// creating the DM, and follows the operator when it changed: the chat leaves its DM with the old
// one. The chat is not routed, and its sender not recorded, when the sender is the operator (no DM
// with oneself) or has no id, and when the connector already added 60 chats this hour (ROUTE-08);
// a capped new chat is not even recorded (ROUTE-11). Additions of one connector are serialized,
// so concurrent first messages make one DM and the cap holds.
func (p *Pipeline) platformDM(ctx context.Context, src Source, c db.Connector, m Inbound, r routing, upsert func() (db.Chat, error), who *authorship) (db.Chat, []string, error) {
	operator := conversations.Party{ID: c.OwnerID, Kind: dto.MemberKindUser}
	if c.OperatorIid.Valid {
		operator = conversations.Party{ID: c.OperatorIid.String, Kind: dto.MemberKindAgent}
	}
	if err := p.verified(ctx, src, m, who); err != nil {
		return db.Chat{}, nil, err
	}
	if who.senderUser != "" && who.senderUser == operator.ID { // the operator writing to their own account
		chat, err := upsert()
		return chat, nil, err
	}
	// Is the chat where it should be already? Then nothing is added and nothing is counted.
	known := who.senderUser
	if known == "" {
		var err error
		if known, err = p.d.People.Find(ctx, src.ConnectorID, m.SenderID, m.SenderAltID); err != nil {
			return db.Chat{}, nil, err
		}
	}
	placed := false
	if r.found && known != "" && len(r.dms) == 1 {
		room, ok, err := p.d.Conversations.PlatformDMRoom(ctx, known, operator.ID)
		if err != nil {
			return db.Chat{}, nil, err
		}
		placed = ok && room == r.dms[0]
	}
	st := p.routeState(c.ID)
	if !placed {
		st.mu.Lock()
		defer st.mu.Unlock()
		if st.recent(p.d.Now()) >= AutoRoutePerHour {
			var chat db.Chat
			if r.found { // a known chat stays recorded; a new one is not
				var err error
				if chat, err = upsert(); err != nil {
					return db.Chat{}, nil, err
				}
			}
			return chat, nil, p.setDetail(ctx, c, AutoRouteLimited)
		}
	}
	chat, err := upsert()
	if err != nil {
		return db.Chat{}, nil, err
	}
	if placed {
		return chat, r.roomIDs, nil
	}
	if err := p.author(ctx, src, chat, m, r.at, who); err != nil {
		return db.Chat{}, nil, err
	}
	if who.iid() == "" { // a sender without an id: nobody to make a DM with
		return chat, nil, nil
	}
	author := conversations.Party{ID: who.person.ID}
	if who.senderUser != "" {
		author = conversations.Party{ID: who.senderUser, Kind: dto.MemberKindUser}
	}
	route, err := p.d.Conversations.EnsurePlatformDM(ctx, conversations.PlatformDM{OwnerID: c.OwnerID, ChatID: chat.ID, Author: author, Operator: operator})
	switch {
	case errors.Is(err, conversations.ErrDMSelf):
		return chat, nil, nil
	case err != nil:
		return db.Chat{}, nil, err
	}
	if route.Changed() {
		st.routed = append(st.routed, p.d.Now())
		return chat, []string{route.RoomID}, p.clearDetail(ctx, c)
	}
	return chat, []string{route.RoomID}, nil
}

// AutoRouteUnavailable is the connector status detail when its catch-all room is gone or its
// owner lost manage on it (ROUTE-03).
const AutoRouteUnavailable = dto.AutoRouteUnavailable

// Catch-all caps (ROUTE-08): a connector auto-adds at most AutoRoutePerHour chats in any hour,
// and a catch-all room takes auto-added chats while it has fewer than AutoRouteRoomMax chats.
// Beyond them the connector status detail is AutoRouteLimited.
const (
	AutoRoutePerHour = 60
	AutoRouteRoomMax = 1000
	AutoRouteLimited = dto.AutoRouteLimited
)

// autoRoute handles a message with content from a chat that belongs to no room. It records
// the chat with upsert (CON-07) and, when its kind matches the connector's catch-all scope and
// no skip holds it out, adds it to the catch-all room with the connector owner's rights
// (ROUTE-01, ROUTE-02) and returns the room id; it returns "" when the chat is not routed. A
// missing room or an owner without manage sets the status detail AutoRouteUnavailable
// (ROUTE-03). A chat refused by a cap is not recorded at all: only the status detail
// AutoRouteLimited says so (ROUTE-08, ROUTE-11). Additions of one connector are serialized,
// so concurrent first messages add a chat once and the caps hold.
func (p *Pipeline) autoRoute(ctx context.Context, src Source, externalID, kind string, upsert func() (db.Chat, error)) (db.Chat, string, error) {
	c, err := p.d.Store.R().GetConnector(ctx, src.ConnectorID)
	if err != nil {
		return db.Chat{}, "", err
	}
	if !scopeMatches(dto.AutoRouteScope(c.AutoRouteScope), kind) {
		chat, err := upsert()
		return chat, "", err
	}
	st := p.routeState(c.ID)
	st.mu.Lock()
	defer st.mu.Unlock()
	known, found, err := p.findChat(ctx, c.ID, externalID)
	if err != nil {
		return db.Chat{}, "", err
	}
	if found {
		until, err := p.d.Store.R().GetAutoRouteSkip(ctx, db.GetAutoRouteSkipParams{ConnectorID: c.ID, ChatID: known.ID})
		switch {
		case err == nil && until > p.d.Now().UnixMilli():
			chat, err := upsert()
			return chat, "", err
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return db.Chat{}, "", err
		}
	}
	if !c.AutoRouteRoomID.Valid { // no catch-all room: chats get a conversation of their own
		chat, err := upsert()
		return chat, "", err
	}
	roomID := c.AutoRouteRoomID.String
	if found {
		n, err := p.d.Store.R().CountRoomMember(ctx, db.CountRoomMemberParams{RoomID: roomID, Kind: string(dto.MemberKindChat), MemberID: known.ID})
		if err != nil {
			return db.Chat{}, "", err
		}
		if n > 0 { // a concurrent first message added it
			chat, err := upsert()
			return chat, roomID, err
		}
	}
	now := p.d.Now()
	full, err := p.d.Store.R().CountRoomChatMembers(ctx, roomID)
	if err != nil {
		return db.Chat{}, "", err
	}
	if full >= AutoRouteRoomMax || st.recent(now) >= AutoRoutePerHour {
		return db.Chat{}, "", p.setDetail(ctx, c, AutoRouteLimited)
	}
	if p.routeHook != nil {
		p.routeHook(c.ID)
	}
	chat, err := upsert()
	if err != nil {
		return db.Chat{}, "", err
	}
	owner := authn.Principal{Kind: authn.KindUser, UserID: c.OwnerID}
	on := true
	_, err = p.d.Rooms.AddMember(ctx, owner, roomID, rooms.MemberInput{Kind: dto.MemberKindChat, MemberID: chat.ID, Outbound: &on})
	switch {
	case err == nil:
		st.routed = append(st.routed, now)
		return chat, roomID, p.clearDetail(ctx, c)
	case errors.Is(err, rooms.ErrAlreadyMember):
		return chat, roomID, p.clearDetail(ctx, c)
	case errors.Is(err, access.ErrNotFound), errors.Is(err, access.ErrForbidden):
		return chat, "", p.setDetail(ctx, c, AutoRouteUnavailable)
	default:
		return db.Chat{}, "", err
	}
}

// findChat returns the connector's chat with the platform id, if it is recorded.
func (p *Pipeline) findChat(ctx context.Context, connectorID, externalID string) (db.Chat, bool, error) {
	chat, err := p.d.Store.R().GetChatByExternalID(ctx, db.GetChatByExternalIDParams{ConnectorID: connectorID, ExternalID: externalID})
	switch {
	case err == nil:
		return chat, true, nil
	case errors.Is(err, sql.ErrNoRows):
		return db.Chat{}, false, nil
	default:
		return db.Chat{}, false, err
	}
}

// recent drops the connector's auto-routes older than an hour and counts the rest. Callers
// hold mu.
func (st *routeState) recent(now time.Time) int {
	i := 0
	for i < len(st.routed) && !st.routed[i].After(now.Add(-time.Hour)) {
		i++
	}
	st.routed = st.routed[i:]
	return len(st.routed)
}

// scopeMatches tells whether a catch-all scope takes a chat kind.
func scopeMatches(scope dto.AutoRouteScope, kind string) bool {
	switch scope {
	case dto.AutoRouteScopeAll:
		return true
	case dto.AutoRouteScopeDMs:
		return kind == string(dto.ChatKindDM)
	case dto.AutoRouteScopeGroups:
		return kind == string(dto.ChatKindGroup) || kind == string(dto.ChatKindChannel)
	default:
		return false
	}
}

// setDetail sets the connector's status detail to an auto-route problem (ROUTE-03,
// ROUTE-08) and publishes it once.
func (p *Pipeline) setDetail(ctx context.Context, c db.Connector, detail string) error {
	if c.LastError == detail {
		return nil
	}
	err := p.d.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.SetConnectorLastError(ctx, db.SetConnectorLastErrorParams{LastError: detail, ID: c.ID})
	})
	if err != nil {
		return err
	}
	p.d.Logger.Warn("connectors: "+detail, "connector_id", c.ID)
	p.d.Bus.Publish(events.ConnectorStatus{ConnectorID: c.ID, Status: c.Status, Detail: detail})
	return nil
}

// clearDetail removes an auto-route status detail once a chat is routed again (ROUTE-08,
// ROUTE-09); other details are left to the connector's status reports.
func (p *Pipeline) clearDetail(ctx context.Context, c db.Connector) error {
	if !IsAutoRouteDetail(c.LastError) {
		return nil
	}
	err := p.d.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.SetConnectorLastError(ctx, db.SetConnectorLastErrorParams{LastError: "", ID: c.ID})
	})
	if err != nil {
		return err
	}
	p.d.Bus.Publish(events.ConnectorStatus{ConnectorID: c.ID, Status: c.Status})
	return nil
}

// IsAutoRouteDetail tells whether a status detail was set by catch-all routing.
func IsAutoRouteDetail(detail string) bool {
	return detail == AutoRouteUnavailable || detail == AutoRouteLimited
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
