// Package live serves the web app's live WebSocket (GET /api/v1/live). Each connection
// subscribes to the event bus and forwards an event only after checking that its user may
// access the resource (LIVE-02), rendering it as that user sees it. What a user sees of AIs and
// connections follows the organizations they belong to (AD-016), checked for each event.
package live

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store"
)

const (
	// DefaultPingInterval keeps idle connections open through proxies.
	DefaultPingInterval = 25 * time.Second
	writeTimeout        = 10 * time.Second
	// subscriberBuffer is the per-connection event backlog before a lagged frame.
	subscriberBuffer = 256
	// DefaultSessionCheckInterval bounds how long a socket outlives a session whose
	// revocation event it missed.
	DefaultSessionCheckInterval = time.Minute
)

// errSessionRevoked ends a connection whose session (or token, or user) no longer
// authenticates; errSessionCheckFailed one whose re-check could not run (fail closed).
var (
	errSessionRevoked     = errors.New("session revoked")
	errSessionCheckFailed = errors.New("session check failed")
)

// PrincipalChecker reports whether a connection's principal still authenticates.
type PrincipalChecker interface {
	PrincipalActive(ctx context.Context, p authn.Principal) (bool, error)
}

// kindOrganization is the resource_kind of an access.changed about an organization (CHART-02): its
// members, positions, chart or invitations changed, or the user joined or left it.
const kindOrganization = "organization"

// Deps are the services the hub reads from.
type Deps struct {
	Bus    *events.Bus
	Access *access.Authorizer
	Rooms  *rooms.Service
	Store  *store.Store
	// Authz decides what a user sees of AIs and connections: the members of an organization see its AIs
	// and connections, and a host sees the AI it runs (AD-016). Defaults to the facade over Store.
	Authz *orgs.Authz
	// Intelligences decides who sees a profile change; optional.
	Intelligences *intelligences.Service
	// Orgs renders an invitation for the identity that answers it; without it invitations are not announced.
	Orgs *orgs.Service
	// PublicURL is the instance URL; upgrades must come from its origin (LIVE-03).
	PublicURL *url.URL
	Logger    *slog.Logger
	// Sessions re-checks each connection's principal: on a SessionsRevoked event naming its
	// session, after a lagged bus and every SessionCheckInterval. Optional.
	Sessions PrincipalChecker
	// Now defaults to time.Now; PingInterval to DefaultPingInterval; SessionCheckInterval to
	// DefaultSessionCheckInterval.
	Now                  func() time.Time
	PingInterval         time.Duration
	SessionCheckInterval time.Duration
}

// Hub accepts live connections. Close ends all of them.
type Hub struct {
	d      Deps
	origin string
	ctx    context.Context
	stop   context.CancelFunc

	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

// New returns a hub.
func New(d Deps) *Hub {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.PingInterval <= 0 {
		d.PingInterval = DefaultPingInterval
	}
	if d.SessionCheckInterval <= 0 {
		d.SessionCheckInterval = DefaultSessionCheckInterval
	}
	if d.Authz == nil && d.Store != nil {
		d.Authz = orgs.NewAuthz(d.Store)
	}
	ctx, stop := context.WithCancel(context.Background())
	return &Hub{d: d, origin: d.PublicURL.Scheme + "://" + d.PublicURL.Host, ctx: ctx, stop: stop}
}

// SessionCheckInterval reports how often live sockets re-check their session.
func (h *Hub) SessionCheckInterval() time.Duration { return h.d.SessionCheckInterval }

// Close closes every open connection and waits for them to finish.
func (h *Hub) Close() {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	h.stop()
	h.wg.Wait()
}

// track registers a connection unless the hub is closed.
func (h *Hub) track() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	h.wg.Add(1)
	return true
}

// ServeHTTP upgrades a logged-in user's request whose Origin is the instance's own.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := authn.PrincipalFrom(r.Context())
	if !ok || p.Kind != authn.KindUser {
		httpx.WriteError(w, httpx.NewError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized, "authentication required"))
		return
	}
	if r.Header.Get("Origin") != h.origin {
		httpx.WriteError(w, httpx.NewError(http.StatusForbidden, dto.ErrorCodeForbidden, "origin not allowed"))
		return
	}
	if !h.track() {
		httpx.WriteError(w, httpx.NewError(http.StatusServiceUnavailable, dto.ErrorCodeUnavailable, "shutting down"))
		return
	}
	defer h.wg.Done()
	// The Origin was checked above against the public URL, which may differ from Host behind a proxy.
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{h.d.PublicURL.Host}})
	if err != nil {
		return // Accept already wrote the response
	}

	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	stopRead := context.AfterFunc(r.Context(), cancel)
	defer stopRead()
	ctx = ws.CloseRead(ctx) // handles pings and closes; cancelled when the peer goes away

	c := &conn{hub: h, ws: ws, p: p, visible: map[string]bool{}, standing: map[string]standing{}}
	err = c.run(ctx)
	if errors.Is(err, errSessionRevoked) {
		_ = ws.Close(websocket.StatusPolicyViolation, "session revoked")
		return
	}
	if errors.Is(err, errSessionCheckFailed) {
		// The client reconnects, and the upgrade authenticates it again.
		_ = ws.Close(websocket.StatusTryAgainLater, "session check failed")
		return
	}
	if err != nil && ctx.Err() == nil {
		h.d.Logger.Debug("live connection ended", "user_id", p.UserID, "err", err)
	}
	if h.ctx.Err() != nil {
		_ = ws.Close(websocket.StatusGoingAway, "server shutting down")
		return
	}
	_ = ws.CloseNow()
}

// conn is one live connection of a user.
type conn struct {
	hub *Hub
	ws  *websocket.Conn
	p   authn.Principal
	// visible holds the rooms this connection knows the user can view, so losing access or a
	// deletion can be announced to exactly those who knew the room.
	visible map[string]bool
	// standing is what the user was in each organization at the last look: their permission and position.
	// A change of it, or a membership that began or ended, is what makes the connection look at the
	// user's groups again (OMEM-06).
	standing map[string]standing
}

// standing is a user's permission and position in an organization.
type standing struct {
	permission orgs.Permission
	position   string
}

type frame struct {
	Type dto.LiveEventType `json:"type"`
	Data any               `json:"data,omitempty"`
}

func (c *conn) run(ctx context.Context) error {
	sub, cancel := c.hub.d.Bus.Subscribe(subscriberBuffer)
	defer cancel()

	rs, err := c.hub.d.Rooms.List(ctx, c.p)
	if err != nil {
		return err
	}
	for _, r := range rs {
		c.visible[r.ID] = true
	}
	if err := c.loadStanding(ctx); err != nil {
		return err
	}
	if err := c.write(ctx, frame{Type: dto.LiveEventTypeHello, Data: dto.HelloData{ServerTime: c.hub.d.Now().UTC()}}); err != nil {
		return err
	}

	ping := time.NewTicker(c.hub.d.PingInterval)
	defer ping.Stop()
	recheck := time.NewTicker(c.hub.d.SessionCheckInterval)
	defer recheck.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-recheck.C:
			if err := c.checkPrincipal(ctx); err != nil {
				return err
			}
		case <-ping.C:
			pctx, pcancel := context.WithTimeout(ctx, writeTimeout)
			err := c.ws.Ping(pctx)
			pcancel()
			if err != nil {
				return err
			}
		case e, ok := <-sub:
			if !ok {
				return nil
			}
			if err := c.checkRevoked(ctx, e); err != nil {
				return err
			}
			frames, err := c.render(ctx, e)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				c.hub.d.Logger.Error("live: render event failed", "user_id", c.p.UserID, "err", err)
				continue
			}
			for _, f := range frames {
				if err := c.write(ctx, f); err != nil {
					return err
				}
			}
		}
	}
}

// checkRevoked ends the connection when e revokes its session; a lagged bus may have dropped
// such an event, so it re-checks the principal then.
func (c *conn) checkRevoked(ctx context.Context, e events.Event) error {
	switch e := e.(type) {
	case events.SessionsRevoked:
		if e.Revokes(c.p.UserID, c.p.SessionID, c.p.TokenID) {
			return errSessionRevoked
		}
	case events.Lagged:
		return c.checkPrincipal(ctx)
	}
	return nil
}

// checkPrincipal ends the connection when its principal no longer authenticates, or when
// that cannot be checked (fail closed).
func (c *conn) checkPrincipal(ctx context.Context) error {
	if c.hub.d.Sessions == nil {
		return nil
	}
	ok, err := c.hub.d.Sessions.PrincipalActive(ctx, c.p)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		c.hub.d.Logger.Error("live: session check failed", "user_id", c.p.UserID, "err", err)
		return errSessionCheckFailed
	}
	if !ok {
		return errSessionRevoked
	}
	return nil
}

func (c *conn) write(ctx context.Context, f frame) error {
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return c.ws.Write(wctx, websocket.MessageText, data)
}

// render returns the frames event e produces for this connection's user: none when the user
// has no access to the resource it is about (LIVE-02).
func (c *conn) render(ctx context.Context, e events.Event) ([]frame, error) {
	d := c.hub.d
	switch e := e.(type) {
	case events.Lagged:
		return []frame{{Type: dto.LiveEventTypeLagged}}, nil

	case events.MessageCreated:
		m, err := d.Rooms.GetMessage(ctx, c.p, e.RoomID, e.MessageID)
		if errors.Is(err, access.ErrNotFound) {
			return c.lost(e.RoomID), nil
		}
		if err != nil {
			return nil, err
		}
		c.visible[e.RoomID] = true
		return []frame{{Type: dto.LiveEventTypeRoomMessage, Data: convert.Message(m)}}, nil

	case events.MessageUpdated:
		// The message as it is now, under the same view rule as a new one (EDT-06, DLT-06).
		m, err := d.Rooms.GetMessage(ctx, c.p, e.RoomID, e.MessageID)
		if errors.Is(err, access.ErrNotFound) {
			return c.lost(e.RoomID), nil
		}
		if err != nil {
			return nil, err
		}
		c.visible[e.RoomID] = true
		return []frame{{Type: dto.LiveEventTypeRoomMessageUpdated, Data: convert.Message(m)}}, nil

	case events.ReactionsChanged:
		m, err := d.Rooms.GetMessage(ctx, c.p, e.RoomID, e.MessageID)
		if errors.Is(err, access.ErrNotFound) {
			// The user lost the room, or the message went away (retention): only the first is news.
			if ok, err := c.canViewRoom(ctx, e.RoomID); err != nil || ok {
				return nil, err
			}
			return c.lost(e.RoomID), nil
		}
		if err != nil {
			return nil, err
		}
		c.visible[e.RoomID] = true
		return []frame{{Type: dto.LiveEventTypeRoomReactions, Data: dto.ReactionsData{
			RoomID: e.RoomID, MessageID: e.MessageID, Reactions: convert.Reactions(m.Reactions),
		}}}, nil

	case events.RoomChanged:
		if e.Deleted {
			return c.lost(e.RoomID), nil
		}
		return c.room(ctx, e.RoomID, nil)

	case events.ReadChanged:
		if e.UserID != c.p.UserID {
			return nil, nil // a read position is the reader's own business
		}
		return []frame{{Type: dto.LiveEventTypeRoomRead, Data: dto.ReadData{RoomID: e.RoomID, Seq: e.Seq}}}, nil

	case events.MembersChanged:
		ok, err := c.canViewRoom(ctx, e.RoomID)
		if err != nil || !ok {
			return nil, err
		}
		return []frame{{Type: dto.LiveEventTypeMembersChanged, Data: dto.RoomIDData{RoomID: e.RoomID}}}, nil

	case events.AccessChanged:
		changed := frame{Type: dto.LiveEventTypeAccessChanged, Data: dto.AccessChangedData{ResourceKind: e.ResourceKind, ResourceID: e.ResourceID}}
		switch access.Kind(e.ResourceKind) {
		case access.KindRoom:
			return c.room(ctx, e.ResourceID, &changed)
		case access.KindAgent:
			return c.ifSeen(c.canSeeAgent(ctx, e.ResourceID, ""))(changed)
		case access.KindConnector:
			return c.ifSeen(c.canSeeConnector(ctx, e.ResourceID))(changed)
		}
		return nil, nil

	case events.OrgChanged:
		return c.orgChanged(ctx, e.OrgID)

	case events.InvitationReceived:
		return c.invitationReceived(ctx, e.InvitationID)

	case events.AgentPresence:
		return c.ifSeen(c.canSeeAgent(ctx, e.AgentID, e.OwnerID))(frame{
			Type: dto.LiveEventTypeAgentPresence, Data: dto.AgentPresence{AgentID: e.AgentID, Online: e.Online},
		})

	case events.ConnectorStatus:
		return c.ifSeen(c.canSeeConnector(ctx, e.ConnectorID))(frame{
			Type: dto.LiveEventTypeConnectorStatus,
			Data: dto.ConnectorStatusData{ConnectorID: e.ConnectorID, Status: dto.ConnectorStatus(e.Status), LastError: e.Detail},
		})

	case events.ChatDiscovered:
		ok, err := c.canSeeConnector(ctx, e.ConnectorID)
		if err != nil || !ok {
			return nil, err
		}
		chat, err := d.Store.R().GetChat(ctx, e.ChatID)
		if err != nil {
			return nil, ignoreMissing(err)
		}
		connector, err := d.Store.R().GetConnector(ctx, e.ConnectorID)
		if err != nil {
			return nil, ignoreMissing(err)
		}
		return []frame{{Type: dto.LiveEventTypeChatDiscovered, Data: convert.Chat(chat, connector)}}, nil

	case events.IntelligenceChanged:
		if d.Intelligences == nil {
			return nil, nil
		}
		prof, err := d.Intelligences.Get(ctx, c.p, e.ID)
		if errors.Is(err, access.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return []frame{{Type: dto.LiveEventTypeIntelligenceChanged, Data: convert.ProfileRef(prof)}}, nil

	case events.IdentityLinked:
		if e.UserID != c.p.UserID {
			return nil, nil
		}
		ident, err := d.Store.R().GetIdentity(ctx, e.IdentityID)
		if err != nil {
			return nil, ignoreMissing(err)
		}
		return []frame{{Type: dto.LiveEventTypeIdentityLinked, Data: convert.Identity(ident)}}, nil
	}
	return nil, nil
}

// room renders the room as the user sees it (room.changed, plus extra when given), or
// room.deleted when the user just lost access to a room the connection knew.
func (c *conn) room(ctx context.Context, roomID string, extra *frame) ([]frame, error) {
	r, err := c.hub.d.Rooms.Get(ctx, c.p, roomID)
	if errors.Is(err, access.ErrNotFound) {
		return c.lost(roomID), nil
	}
	if err != nil {
		return nil, err
	}
	c.visible[roomID] = true
	out := []frame{{Type: dto.LiveEventTypeRoomChanged, Data: convert.Room(r)}}
	if extra != nil {
		out = append(out, *extra)
	}
	return out, nil
}

// lost announces room.deleted once for a room the connection knew.
func (c *conn) lost(roomID string) []frame {
	if !c.visible[roomID] {
		return nil
	}
	delete(c.visible, roomID)
	return []frame{{Type: dto.LiveEventTypeRoomDeleted, Data: dto.RoomIDData{RoomID: roomID}}}
}

func (c *conn) canViewRoom(ctx context.Context, roomID string) (bool, error) {
	l, err := c.hub.d.Access.UserRoomLevel(ctx, c.p.UserID, roomID)
	if err != nil {
		return false, err
	}
	return l != access.None, nil
}

// ifSeen returns a function yielding f when the user may see the resource.
func (c *conn) ifSeen(ok bool, err error) func(frame) ([]frame, error) {
	return func(f frame) ([]frame, error) {
		if err != nil || !ok {
			return nil, err
		}
		return []frame{f}, nil
	}
}

// canSeeAgent reports whether the user sees the AI: its host does, and so does a member of any
// organization the AI belongs to (AD-016). hostID is the AI's host when the event names it, else it is read.
func (c *conn) canSeeAgent(ctx context.Context, agentID, hostID string) (bool, error) {
	d := c.hub.d
	if hostID == "" {
		a, err := d.Store.R().GetAgent(ctx, agentID)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		hostID = a.OwnerID
	}
	if hostID == c.p.UserID {
		return true, nil
	}
	if d.Authz == nil {
		return false, nil
	}
	return d.Authz.SharesOrg(ctx, c.p.UserID, agentID)
}

// canSeeConnector reports whether the user sees the connection: the members of its organization do,
// guests included (CHART-02). A connection no organization owns yet, as one made before connections
// belonged to organizations, is its owner's.
func (c *conn) canSeeConnector(ctx context.Context, connectorID string) (bool, error) {
	d := c.hub.d
	conn, err := d.Store.R().GetConnector(ctx, connectorID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !conn.OrgID.Valid {
		return conn.OwnerID == c.p.UserID, nil
	}
	if d.Authz == nil {
		return false, nil
	}
	_, member, err := d.Authz.Member(ctx, conn.OrgID.String, c.p.UserID)
	return member, err
}

// loadStanding reads the organizations the user belongs to, so that a later change of one is told apart
// from the first look.
func (c *conn) loadStanding(ctx context.Context) error {
	if c.hub.d.Store == nil {
		return nil
	}
	mine, err := c.hub.d.Store.R().ListOrgsOfMember(ctx, c.p.UserID)
	if err != nil {
		return err
	}
	for _, m := range mine {
		c.standing[m.Organization.ID] = standing{permission: orgs.Permission(m.Permission), position: m.PositionID}
	}
	return nil
}

// accessChanged is an access.changed frame about a resource of the kind.
func accessChanged(kind, id string) frame {
	return frame{Type: dto.LiveEventTypeAccessChanged, Data: dto.AccessChangedData{ResourceKind: kind, ResourceID: id}}
}

// orgChanged renders a change of an organization for the connection's user (CHART-02, OMEM-06).
//
// A user who is not a member, and was not at the last look, hears nothing of it. A member, and one who
// has just left, hears that the organization changed (access.changed, kind organization), and looks at
// the organization again. When the user's own standing changed (they joined or left, or their permission
// or position did), what they see through it may have changed as well: the connection reads the user's
// groups again, announcing those they lost (room.deleted) and those they can now view (room.changed),
// each with an access.changed, and tells a member which AIs and connections of the organization to look
// at again. A user who left is told no ids of what they can no longer see.
func (c *conn) orgChanged(ctx context.Context, orgID string) ([]frame, error) {
	d := c.hub.d
	if d.Authz == nil {
		return nil, nil
	}
	m, member, err := d.Authz.Member(ctx, orgID, c.p.UserID)
	if err != nil {
		return nil, err
	}
	before, was := c.standing[orgID]
	now := standing{permission: m.Permission, position: m.PositionID}
	if member {
		c.standing[orgID] = now
	} else {
		delete(c.standing, orgID)
	}
	if !member && !was {
		return nil, nil
	}
	out := []frame{accessChanged(kindOrganization, orgID)}
	if member && was && before == now {
		return out, nil
	}
	roomFrames, err := c.rereadRooms(ctx)
	if err != nil {
		return nil, err
	}
	out = append(out, roomFrames...)
	if !member {
		return out, nil
	}
	agents, err := d.Store.R().ListOrgAgentIDs(ctx, orgID)
	if err != nil {
		return nil, err
	}
	for _, id := range agents {
		out = append(out, accessChanged(string(access.KindAgent), id))
	}
	connectors, err := d.Store.R().ListOrgConnectorIDs(ctx, sql.NullString{String: orgID, Valid: true})
	if err != nil {
		return nil, err
	}
	for _, id := range connectors {
		out = append(out, accessChanged(string(access.KindConnector), id))
	}
	return out, nil
}

// invitationReceived renders an invitation for the user who answers it: the invitee, or for an invited AI
// whoever manages it in its home organization (OMEM-01). Everybody else, the inviter included, hears nothing.
func (c *conn) invitationReceived(ctx context.Context, invitationID string) ([]frame, error) {
	if c.hub.d.Orgs == nil {
		return nil, nil
	}
	inv, err := c.hub.d.Orgs.InvitationFor(ctx, c.p.UserID, invitationID)
	if errors.Is(err, orgs.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return []frame{{Type: dto.LiveEventTypeInvitationReceived, Data: convert.Invitation(inv)}}, nil
}

// rereadRooms compares the rooms the user lists now with those the connection knows: a room that is
// gone from the list and that the user cannot view is announced as deleted, and one the connection did
// not know is announced as changed, each with an access.changed. Both come in the order of their ids.
func (c *conn) rereadRooms(ctx context.Context) ([]frame, error) {
	list, err := c.hub.d.Rooms.List(ctx, c.p)
	if err != nil {
		return nil, err
	}
	listed := make(map[string]bool, len(list))
	var gained, lost []string
	for _, r := range list {
		listed[r.ID] = true
		if !c.visible[r.ID] {
			gained = append(gained, r.ID)
		}
	}
	for id := range c.visible {
		if listed[id] {
			continue
		}
		// Not listed is not lost: a direct message the user only views through an AI is not listed.
		if ok, err := c.canViewRoom(ctx, id); err != nil {
			return nil, err
		} else if !ok {
			lost = append(lost, id)
		}
	}
	sort.Strings(gained)
	sort.Strings(lost)
	var out []frame
	for _, id := range lost {
		out = append(out, c.lost(id)...)
		out = append(out, accessChanged(string(access.KindRoom), id))
	}
	for _, id := range gained {
		changed := accessChanged(string(access.KindRoom), id)
		frames, err := c.room(ctx, id, &changed)
		if err != nil {
			return nil, err
		}
		out = append(out, frames...)
	}
	return out, nil
}

func ignoreMissing(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}
