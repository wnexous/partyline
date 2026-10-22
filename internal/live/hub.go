// Package live serves the web app's live WebSocket (GET /api/v1/live). Each connection
// subscribes to the event bus and forwards an event only after checking that its user may
// access the resource (LIVE-02), rendering it as that user sees it.
package live

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
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

// Deps are the services the hub reads from.
type Deps struct {
	Bus    *events.Bus
	Access *access.Authorizer
	Rooms  *rooms.Service
	Store  *store.Store
	// Intelligences decides who sees a profile change; optional.
	Intelligences *intelligences.Service
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

	c := &conn{hub: h, ws: ws, p: p, visible: map[string]bool{}}
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

	case events.GrantsChanged:
		changed := frame{Type: dto.LiveEventTypeAccessChanged, Data: dto.AccessChangedData{ResourceKind: e.ResourceKind, ResourceID: e.ResourceID}}
		switch access.Kind(e.ResourceKind) {
		case access.KindRoom:
			return c.room(ctx, e.ResourceID, &changed)
		case access.KindAgent:
			return c.ifLevel(d.Access.AgentLevel(ctx, c.p.UserID, e.ResourceID))(changed)
		case access.KindConnector:
			return c.ifLevel(d.Access.ConnectorLevel(ctx, c.p.UserID, e.ResourceID))(changed)
		}
		return nil, nil

	case events.AgentPresence:
		return c.ifLevel(d.Access.AgentLevel(ctx, c.p.UserID, e.AgentID))(frame{
			Type: dto.LiveEventTypeAgentPresence, Data: dto.AgentPresence{AgentID: e.AgentID, Online: e.Online},
		})

	case events.ConnectorStatus:
		return c.ifLevel(d.Access.ConnectorLevel(ctx, c.p.UserID, e.ConnectorID))(frame{
			Type: dto.LiveEventTypeConnectorStatus,
			Data: dto.ConnectorStatusData{ConnectorID: e.ConnectorID, Status: dto.ConnectorStatus(e.Status), LastError: e.Detail},
		})

	case events.ChatDiscovered:
		l, err := d.Access.ConnectorLevel(ctx, c.p.UserID, e.ConnectorID)
		if err != nil || l == access.None {
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

	case events.OutboxChanged:
		ok, err := c.canViewRoom(ctx, e.RoomID)
		if err != nil || !ok {
			return nil, err
		}
		o, err := d.Store.R().GetOutbox(ctx, e.OutboxID)
		if err != nil {
			return nil, ignoreMissing(err)
		}
		return []frame{{Type: dto.LiveEventTypeOutboxStatus, Data: dto.OutboxUpdate{
			RoomID: e.RoomID, MessageID: e.MessageID, OutboxItem: convert.OutboxItem(o),
		}}}, nil

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

// ifLevel returns a function yielding f when the level grants any access.
func (c *conn) ifLevel(l access.Level, err error) func(frame) ([]frame, error) {
	return func(f frame) ([]frame, error) {
		if err != nil || l == access.None {
			return nil, err
		}
		return []frame{f}, nil
	}
}

func ignoreMissing(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}
