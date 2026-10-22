package agents

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// WebSocket close codes the server uses on agent connections.
const (
	// CloseDeleted ends a connection because its agent was deleted.
	CloseDeleted websocket.StatusCode = 4001
	// CloseTakenOver ends a connection because another process took the AI's live session over
	// (SES-02).
	CloseTakenOver websocket.StatusCode = 4002
	// CloseBusy ends a new connection that lost a race: another process attached between the
	// pre-upgrade check and the attach (SES-04).
	CloseBusy websocket.StatusCode = 4003
)

// Close reasons sent with the close codes.
const (
	ReasonDeleted   = "deleted"
	ReasonTakenOver = "taken_over"
	ReasonBusy      = "identity_busy"
	// ReasonReconnected closes the old socket of a process that connected again (code 1000).
	ReasonReconnected = "reconnected"
)

// ErrBusy means another process holds the AI's live session and the connection did not ask to
// take it over (SES-04).
var ErrBusy = errors.New("the agent has a live session in another process")

// Session is one live agent connection held by Presence.
type Session interface {
	// Close ends the connection with a WebSocket close code and reason. It must not block.
	Close(code websocket.StatusCode, reason string)
}

type presenceEntry struct {
	ownerID string
	session Session
	liveID  string
	since   time.Time
}

// Live describes the live session of an AI.
type Live struct {
	// Since is when the session started; a process reconnecting keeps it.
	Since time.Time
}

// Presence tracks which agents are connected: at most one live session per agent, held by one
// process (SES-02, SES-04). Transitions between offline and online publish AgentPresence and
// stamp the agent's last_seen_at.
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

// Attach makes s the agent's live session, checking and swapping under one lock, so racing
// connections end with exactly one live session:
//   - nobody holds the agent: s is attached and the agent comes online;
//   - the same process holds it (liveID is not empty and equal): s replaces the old socket of
//     that process silently, which is closed normally, and the session keeps its start time;
//   - another process holds it: ErrBusy and nothing changes, unless takeover is set, in which case
//     s is attached and the old session is closed with CloseTakenOver (SES-02).
//
// fresh reports whether a new session started (everything but a process reconnecting). The only
// error is ErrBusy.
func (p *Presence) Attach(ctx context.Context, agent db.Agent, s Session, liveID string, takeover bool) (fresh bool, err error) {
	p.mu.Lock()
	prev, held := p.sessions[agent.ID]
	same := held && liveID != "" && prev.liveID == liveID
	if held && !same && !takeover {
		p.mu.Unlock()
		return false, ErrBusy
	}
	since := p.now()
	if same {
		since = prev.since
	}
	p.sessions[agent.ID] = presenceEntry{ownerID: agent.OwnerID, session: s, liveID: liveID, since: since}
	p.mu.Unlock()
	switch {
	case same:
		prev.session.Close(websocket.StatusNormalClosure, ReasonReconnected)
	case held:
		prev.session.Close(CloseTakenOver, ReasonTakenOver)
	}
	_ = p.touch(ctx, agent.ID) // best effort, like Detach: last_seen_at is informational
	if !held {
		p.bus.Publish(events.AgentPresence{AgentID: agent.ID, OwnerID: agent.OwnerID, Online: true})
	}
	return !same, nil
}

// Busy reports the live session that stops a connection of the process liveID from attaching
// without taking over: the agent is held by another process (SES-04). A connection that names no
// process is another process.
func (p *Presence) Busy(agentID, liveID string) (Live, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.sessions[agentID]
	if !ok || (liveID != "" && e.liveID == liveID) {
		return Live{}, false
	}
	return Live{Since: e.since}, true
}

// Live returns the agent's live session, if any.
func (p *Presence) Live(agentID string) (Live, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.sessions[agentID]
	return Live{Since: e.since}, ok
}

// Detach removes s when it is still the agent's session and marks the agent offline. It
// reports whether s was the current session (false after a takeover or a reconnect).
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
