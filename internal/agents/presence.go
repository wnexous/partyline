package agents

import (
	"context"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// WebSocket close codes the server uses on agent connections.
const (
	// CloseReplaced ends a connection because a newer one authenticated as the same agent (AGT-04).
	CloseReplaced websocket.StatusCode = 4000
	// CloseDeleted ends a connection because its agent was deleted.
	CloseDeleted websocket.StatusCode = 4001
)

// Close reasons sent with the close codes.
const (
	ReasonReplaced = "replaced"
	ReasonDeleted  = "deleted"
)

// Session is one live agent connection held by Presence.
type Session interface {
	// Close ends the connection with a WebSocket close code and reason. It must not block.
	Close(code websocket.StatusCode, reason string)
}

type presenceEntry struct {
	ownerID string
	session Session
}

// Presence tracks which agents are connected: at most one session per agent. Transitions
// between offline and online publish AgentPresence and stamp the agent's last_seen_at.
type Presence struct {
	store *store.Store
	bus   *events.Bus
	now   func() time.Time

	mu       sync.Mutex
	sessions map[string]presenceEntry
}

// NewPresence returns an empty Presence. now defaults to time.Now.
func NewPresence(st *store.Store, bus *events.Bus, now func() time.Time) *Presence {
	if now == nil {
		now = time.Now
	}
	return &Presence{store: st, bus: bus, now: now, sessions: map[string]presenceEntry{}}
}

// Attach makes s the agent's session. A previous session is closed with reason "replaced"
// (AGT-04); the agent stays online across a replacement.
func (p *Presence) Attach(ctx context.Context, agent db.Agent, s Session) error {
	p.mu.Lock()
	prev, wasOnline := p.sessions[agent.ID]
	p.sessions[agent.ID] = presenceEntry{ownerID: agent.OwnerID, session: s}
	p.mu.Unlock()
	if wasOnline {
		prev.session.Close(CloseReplaced, ReasonReplaced)
	}
	err := p.touch(ctx, agent.ID)
	if !wasOnline {
		p.bus.Publish(events.AgentPresence{AgentID: agent.ID, OwnerID: agent.OwnerID, Online: true})
	}
	return err
}

// Detach removes s when it is still the agent's session and marks the agent offline. It
// reports whether s was the current session (false after a replacement).
func (p *Presence) Detach(ctx context.Context, agentID string, s Session) bool {
	p.mu.Lock()
	cur, ok := p.sessions[agentID]
	if !ok || cur.session != s {
		p.mu.Unlock()
		return false
	}
	delete(p.sessions, agentID)
	p.mu.Unlock()
	_ = p.touch(ctx, agentID) // best effort: the agent row may be gone already
	p.bus.Publish(events.AgentPresence{AgentID: agentID, OwnerID: cur.ownerID, Online: false})
	return true
}

// Online reports whether the agent has a live session.
func (p *Presence) Online(agentID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.sessions[agentID]
	return ok
}

// Session returns the agent's live session, if any.
func (p *Presence) Session(agentID string) (Session, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.sessions[agentID]
	return e.session, ok
}

// Kick closes the agent's live session, if any, with code and reason and marks it offline.
func (p *Presence) Kick(ctx context.Context, agentID string, code websocket.StatusCode, reason string) {
	s, ok := p.Session(agentID)
	if !ok {
		return
	}
	s.Close(code, reason)
	p.Detach(ctx, agentID, s)
}

func (p *Presence) touch(ctx context.Context, agentID string) error {
	ctx = context.WithoutCancel(ctx) // runs while a connection closes
	return p.store.WithTx(ctx, func(q *db.Queries) error {
		return q.TouchAgent(ctx, db.TouchAgentParams{LastSeenAt: nullMillis(p.now()), ID: agentID})
	})
}
