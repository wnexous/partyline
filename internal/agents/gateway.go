package agents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

const (
	// BacklogLimit is the most messages per room delivered on connect (AGT-08).
	BacklogLimit = 50
	// QueueSize is the per-connection live push queue; overflow closes the connection and the
	// reconnect backlog recovers the messages.
	QueueSize = 256
	// DefaultPingInterval keeps idle agent connections open through proxies.
	DefaultPingInterval = 25 * time.Second
	writeTimeout        = 10 * time.Second
)

// ErrAgentTokenRequired answers user credentials on agent endpoints (403).
var ErrAgentTokenRequired = httpx.NewError(http.StatusForbidden, dto.ErrorCodeForbidden, "agent token required")

// SkippedNotice is the notice text when more than BacklogLimit messages are pending (AGT-09).
func SkippedNotice(n int64) string {
	return fmt.Sprintf("%d older messages skipped; use the history tool", n)
}

// GatewayDeps are the collaborators of the Gateway.
type GatewayDeps struct {
	Store    *store.Store
	Registry *Registry
	Presence *Presence
	Rooms    *rooms.Service
	Logger   *slog.Logger
	// PingInterval defaults to DefaultPingInterval.
	PingInterval time.Duration
}

// Gateway serves the agent WebSocket (GET /api/v1/agent/ws): it registers the agent, sends
// the welcome frame and the backlog, then pushes live messages and records acks.
type Gateway struct {
	d    GatewayDeps
	ctx  context.Context
	stop context.CancelFunc

	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

// NewGateway returns a Gateway.
func NewGateway(d GatewayDeps) *Gateway {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.PingInterval <= 0 {
		d.PingInterval = DefaultPingInterval
	}
	ctx, stop := context.WithCancel(context.Background())
	return &Gateway{d: d, ctx: ctx, stop: stop}
}

// Close ends every agent connection and waits for them to finish.
func (g *Gateway) Close() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
	g.stop()
	g.wg.Wait()
}

func (g *Gateway) track() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	g.wg.Add(1)
	return true
}

// Push queues a live message for the agent's connection without blocking. It reports false
// when the agent is offline or its queue overflowed (the connection is then closed).
func (g *Gateway) Push(agentID string, m dto.AgentMessage) bool {
	s, ok := g.d.Presence.Session(agentID)
	if !ok {
		return false
	}
	c, ok := s.(*conn)
	return ok && c.push(m)
}

// Online reports whether the agent is connected.
func (g *Gateway) Online(agentID string) bool { return g.d.Presence.Online(agentID) }

// ServeHTTP upgrades a request authenticated by an agent token.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := authn.PrincipalFrom(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.NewError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized, "authentication required"))
		return
	}
	if p.Kind != authn.KindAgent {
		httpx.WriteError(w, ErrAgentTokenRequired)
		return
	}
	if !g.track() {
		httpx.WriteError(w, httpx.NewError(http.StatusServiceUnavailable, dto.ErrorCodeUnavailable, "shutting down"))
		return
	}
	defer g.wg.Done()
	row, err := g.register(r.Context(), p)
	if err != nil {
		g.d.Logger.ErrorContext(r.Context(), "agent: register failed", "err", err, "request_id", httpx.RequestID(r.Context()))
		httpx.WriteError(w, err)
		return
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already wrote the response
	}

	ctx, cancel := context.WithCancel(g.ctx)
	defer cancel()
	stopReq := context.AfterFunc(r.Context(), cancel)
	defer stopReq()

	c := &conn{ws: ws, queue: make(chan dto.AgentMessage, QueueSize), cancel: cancel}
	if err := g.d.Presence.Attach(ctx, row.Agent, c); err != nil {
		g.d.Logger.ErrorContext(ctx, "agent: attach failed", "agent_id", row.Agent.ID, "err", err)
	}
	defer g.d.Presence.Detach(ctx, row.Agent.ID, c)
	go g.readAcks(ctx, c, row.Agent.ID)

	err = g.run(ctx, c, row)
	if err != nil && ctx.Err() == nil {
		g.d.Logger.Debug("agent connection ended", "agent_id", row.Agent.ID, "err", err)
	}
	switch {
	case c.closing():
		<-ctx.Done() // the close handshake owns the connection
	case g.ctx.Err() != nil:
		_ = ws.Close(websocket.StatusGoingAway, "server shutting down")
	default:
		_ = ws.CloseNow()
	}
}

// register upserts the agent named by the principal and returns it with its owner's name.
func (g *Gateway) register(ctx context.Context, p authn.Principal) (db.GetAgentWithOwnerRow, error) {
	a, err := g.d.Registry.Upsert(ctx, p.UserID, p.AgentName)
	if err != nil {
		return db.GetAgentWithOwnerRow{}, err
	}
	return g.d.Store.R().GetAgentWithOwner(ctx, a.ID)
}

// conn is one agent connection; it is the agent's Session in Presence.
type conn struct {
	ws     *websocket.Conn
	queue  chan dto.AgentMessage
	cancel context.CancelFunc

	mu     sync.Mutex
	closed bool
}

// Close starts the close handshake with code and reason without blocking.
func (c *conn) Close(code websocket.StatusCode, reason string) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.mu.Unlock()
	go func() {
		_ = c.ws.Close(code, reason)
		c.cancel()
	}()
}

func (c *conn) closing() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *conn) push(m dto.AgentMessage) bool {
	select {
	case c.queue <- m:
		return true
	default:
		c.Close(websocket.StatusTryAgainLater, "lagged")
		return false
	}
}

func (c *conn) write(ctx context.Context, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return c.ws.Write(wctx, websocket.MessageText, data)
}

// run sends the welcome frame and the backlog, then live pushes and pings until ctx ends.
func (g *Gateway) run(ctx context.Context, c *conn, row db.GetAgentWithOwnerRow) error {
	a := row.Agent
	if err := c.write(ctx, dto.AgentWelcomeFrame{
		Type: dto.AgentFrameTypeWelcome, Agent: dto.AgentInfo{ID: a.ID, Name: a.Name, Owner: row.OwnerUsername},
	}); err != nil {
		return err
	}
	sent, err := g.backlog(ctx, c, a)
	if err != nil {
		return err
	}
	ping := time.NewTicker(g.d.PingInterval)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := c.ws.Ping(pctx)
			cancel()
			if err != nil {
				return err
			}
		case m := <-c.queue:
			if m.Message.Seq <= sent[m.Room.ID] {
				continue // already delivered by the backlog
			}
			if err := c.write(ctx, dto.AgentMessageFrame{Type: dto.AgentFrameTypeMessage, AgentMessage: m}); err != nil {
				return err
			}
		}
	}
}

// backlog delivers, per room of the agent (paused rooms excepted), the messages after the
// agent's cursor, oldest first and at most BacklogLimit (AGT-08). When more are pending it
// first sends a notice with the skipped count and moves the cursor past them (AGT-09). It
// returns the highest sequence number sent per room.
func (g *Gateway) backlog(ctx context.Context, c *conn, a db.Agent) (map[string]int64, error) {
	sent := map[string]int64{}
	states, err := g.d.Store.R().ListAgentRoomStates(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	for _, st := range states {
		if st.AgentsPaused {
			continue // DLV-08: no agent delivery while the loop guard holds the room
		}
		ref := dto.RoomRef{ID: st.ID, Name: st.Name}
		pending, err := g.d.Store.R().CountAgentBacklog(ctx, db.CountAgentBacklogParams{RoomID: st.ID, AfterSeq: st.Cursor, AgentID: a.ID})
		if err != nil {
			return nil, err
		}
		if pending == 0 {
			continue
		}
		views, err := g.d.Store.R().ListAgentBacklog(ctx, db.ListAgentBacklogParams{RoomID: st.ID, AfterSeq: st.Cursor, AgentID: a.ID, Limit: BacklogLimit})
		if err != nil {
			return nil, err
		}
		slices.Reverse(views) // oldest first
		if skipped := pending - int64(len(views)); skipped > 0 {
			if err := c.write(ctx, dto.AgentNoticeFrame{Type: dto.AgentFrameTypeNotice, Room: ref, Text: SkippedNotice(skipped)}); err != nil {
				return nil, err
			}
			if err := g.advance(ctx, a.ID, st.ID, views[0].Seq-1); err != nil {
				return nil, err
			}
		}
		msgs, err := g.d.Rooms.Details(ctx, views)
		if err != nil {
			return nil, err
		}
		for _, m := range msgs {
			frame := dto.AgentMessageFrame{Type: dto.AgentFrameTypeMessage, AgentMessage: dto.AgentMessage{
				Room: ref, Trusted: delivery.Trusted(m.AuthorUserID, a.OwnerID), Message: convert.Message(m),
			}}
			if err := c.write(ctx, frame); err != nil {
				return nil, err
			}
			sent[st.ID] = m.Seq
		}
	}
	return sent, nil
}

// readAcks reads frames from the agent and records acks (AGT-07) until the connection ends.
func (g *Gateway) readAcks(ctx context.Context, c *conn, agentID string) {
	defer c.cancel()
	for {
		_, data, err := c.ws.Read(ctx)
		if err != nil {
			return
		}
		var f dto.AgentAckFrame
		if json.Unmarshal(data, &f) != nil || f.Type != dto.AgentFrameTypeAck || f.MessageID == "" {
			continue
		}
		if err := g.ack(ctx, agentID, f.MessageID); err != nil && ctx.Err() == nil {
			g.d.Logger.Error("agent: ack failed", "agent_id", agentID, "err", err)
		}
	}
}

// ack moves the agent's cursor in the message's room to the message, never backwards.
// Messages of rooms the agent is not in are ignored.
func (g *Gateway) ack(ctx context.Context, agentID, messageID string) error {
	m, err := g.d.Store.R().GetMessageRoomSeq(ctx, messageID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	n, err := g.d.Store.R().CountRoomMember(ctx, db.CountRoomMemberParams{RoomID: m.RoomID, Kind: string(dto.MemberKindAgent), MemberID: agentID})
	if err != nil || n == 0 {
		return err
	}
	return g.advance(ctx, agentID, m.RoomID, m.Seq)
}

func (g *Gateway) advance(ctx context.Context, agentID, roomID string, seq int64) error {
	return g.d.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.AdvanceAgentCursor(ctx, db.AdvanceAgentCursorParams{AgentID: agentID, RoomID: roomID, Seq: seq})
	})
}
