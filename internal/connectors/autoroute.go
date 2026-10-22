package connectors

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/conversations"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store/db"
)

// routing is what the pipeline knows of a chat before it routes a message from it.
type routing struct {
	known   db.Chat
	found   bool
	kind    string
	roomIDs []string // the rooms the chat belongs to
	dms     []string // those that are DMs
	at      time.Time
}

// route records the chat (unless a cap refuses it) and returns the rooms the message belongs to:
// the chat's own rooms, or the conversation made for it when the connector routes new chats and
// the chat belongs to none. A connector without a catch-all room gives a DM chat the DM with its
// operator (ACC-02) and a group or channel chat a group of its own (ACC-06); one with a
// catch-all room keeps adding chats to it (ROUTE-01).
func (p *Pipeline) route(ctx context.Context, src Source, m Inbound, r routing, upsert func() (db.Chat, error), who *authorship) (db.Chat, []string, error) {
	dm := r.kind == string(dto.ChatKindDM)
	// Chats in rooms need the connector only to keep a DM chat with its operator.
	if (dm && p.d.Conversations != nil) || (!dm && len(r.roomIDs) == 0) {
		c, err := p.d.Store.R().GetConnector(ctx, src.ConnectorID)
		if err != nil {
			return db.Chat{}, nil, err
		}
		if scopeMatches(dto.AutoRouteScope(c.AutoRouteScope), r.kind) && !c.AutoRouteRoomID.Valid {
			switch {
			case !dm:
				return p.autoGroup(ctx, src, c, m, upsert)
			case len(r.dms) == len(r.roomIDs): // in no room, or only in the DMs this routing keeps
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

// autoGroup gives a group or channel chat that belongs to no room a group of its own (ACC-06):
// named after the chat, owned by the connector's owner, with the operator (the owner, or the
// owner's AI) and the chat as members, the chat with outbound on, so the operator receives the
// chat's messages and its replies go back. A chat a manager removed from its group waits 24 hours
// for the next (ROUTE-02), and the 60 new routes per hour of the connector are shared with the DMs
// (ROUTE-08, ROUTE-11): a capped new chat is not recorded. Additions of one connector are
// serialized and the chat's rooms are read again under the lock, so concurrent first messages
// make one group.
func (p *Pipeline) autoGroup(ctx context.Context, src Source, c db.Connector, m Inbound, upsert func() (db.Chat, error)) (db.Chat, []string, error) {
	st := p.routeState(c.ID)
	st.mu.Lock()
	defer st.mu.Unlock()
	known, found, err := p.findChat(ctx, c.ID, m.ChatID)
	if err != nil {
		return db.Chat{}, nil, err
	}
	if found {
		chatRooms, err := p.d.Store.R().ListChatRooms(ctx, known.ID)
		if err != nil {
			return db.Chat{}, nil, err
		}
		if len(chatRooms) > 0 { // a concurrent first message made its group
			ids := make([]string, len(chatRooms))
			for i, r := range chatRooms {
				ids[i] = r.ID
			}
			chat, err := upsert()
			return chat, ids, err
		}
		if skipped, err := p.skipped(ctx, c, known.ID); err != nil || skipped {
			if err != nil {
				return db.Chat{}, nil, err
			}
			chat, err := upsert()
			return chat, nil, err
		}
	}
	now := p.d.Now()
	if st.recent(now) >= AutoRoutePerHour {
		var chat db.Chat
		if found { // a known chat stays recorded; a new one is not
			if chat, err = upsert(); err != nil {
				return db.Chat{}, nil, err
			}
		}
		return chat, nil, p.setDetail(ctx, c, AutoRouteLimited)
	}
	if p.routeHook != nil {
		p.routeHook(c.ID)
	}
	chat, err := upsert()
	if err != nil {
		return db.Chat{}, nil, err
	}
	operator := rooms.MemberInput{Kind: dto.MemberKindUser, MemberID: c.OwnerID}
	if c.OperatorIid.Valid {
		operator = rooms.MemberInput{Kind: dto.MemberKindAgent, MemberID: c.OperatorIid.String}
	}
	on := true
	members := []rooms.MemberInput{operator, {Kind: dto.MemberKindChat, MemberID: chat.ID, Outbound: &on}}
	for _, mi := range members {
		ok, err := p.d.Rooms.CheckMember(ctx, c.OwnerID, mi)
		if err != nil {
			return db.Chat{}, nil, err
		}
		if !ok { // the owner can no longer put the operator or the chat in a group
			return chat, nil, p.setDetail(ctx, c, AutoRouteUnavailable)
		}
	}
	id, err := p.d.Rooms.CreateWithMembers(ctx, c.OwnerID, rooms.CreateInput{Name: roomName(chat, src.Kind)}, members)
	if err != nil {
		return db.Chat{}, nil, err
	}
	st.routed = append(st.routed, now)
	return chat, []string{id}, p.clearDetail(ctx, c)
}

// skipped tells whether the chat was taken out of its rooms less than AutoRouteSkip ago, so that
// it is not given a room again yet (ROUTE-02).
func (p *Pipeline) skipped(ctx context.Context, c db.Connector, chatID string) (bool, error) {
	until, err := p.d.Store.R().GetAutoRouteSkip(ctx, db.GetAutoRouteSkipParams{ConnectorID: c.ID, ChatID: chatID})
	switch {
	case err == nil:
		return until > p.d.Now().UnixMilli(), nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	}
	return false, err
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
		skipped, err := p.skipped(ctx, c, known.ID)
		if err != nil {
			return db.Chat{}, "", err
		}
		if skipped {
			chat, err := upsert()
			return chat, "", err
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
