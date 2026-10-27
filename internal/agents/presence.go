package agents

import (
	"context"
	"sort"
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
	// CloseTakenOver ends a connection because another process took the AI's session over, or
	// because the AI's session was ended (SES-02, POS-04).
	CloseTakenOver websocket.StatusCode = 4002
)

// Close reasons sent with the close codes.
const (
	ReasonDeleted   = "deleted"
	ReasonTakenOver = "taken_over"
	// ReasonReconnected closes the old socket of a process that connected again (code 1000).
	ReasonReconnected = "reconnected"
)

// retiredTTL is how long a process keeps the label of a session that ended, so that a reconnect within
// it keeps the label a person is addressing (SESS-14).
const retiredTTL = 10 * time.Minute

// Session is one live agent connection held by Presence.
type Session interface {
	// Close ends the connection with a WebSocket close code and reason. It must not block.
	Close(code websocket.StatusCode, reason string)
}

// presenceEntry is one live session of an AI; the inner key of Presence.sessions is its label.
type presenceEntry struct {
	ownerID string
	session Session
	label   string // "" for the principal
	harness string
	liveID  string
	since   time.Time
}

// live is the exported view of an entry.
func (e presenceEntry) live() Live {
	return Live{Label: e.label, Harness: e.harness, Since: e.since}
}

// retiredLabel is the memory a dropped socket leaves: the label and the start time its process gets back
// on a reconnect until expires (SESS-14).
type retiredLabel struct {
	label   string
	since   time.Time
	expires time.Time
}

// Live describes one live session of an AI.
type Live struct {
	// Label is "" for the principal session and the session's label otherwise (SESS-11).
	Label string
	// Harness is the app the session reported when it connected.
	Harness string
	// Since is when the session started; a process reconnecting keeps it.
	Since time.Time
}

// Presence tracks which agents are connected. One AI has any number of labelled live sessions, at most
// one per label; the principal session's label is "" (SESS-10, SESS-11). Allocation and the insert happen
// under one lock, so labels are unique among an AI's live sessions (SESS-12). Transitions between offline
// and online publish AgentPresence and stamp the agent's last_seen_at.
type Presence struct {
	store *store.Store
	bus   *events.Bus
	now   func() time.Time

	mu       sync.Mutex
	sessions map[string]map[string]presenceEntry // AI id -> label -> entry
	retired  map[string]map[string]retiredLabel  // AI id -> live id -> remembered label
}

// NewPresence returns an empty Presence. now defaults to time.Now.
func NewPresence(st *store.Store, bus *events.Bus, now func() time.Time) *Presence {
	if now == nil {
		now = time.Now
	}
	return &Presence{
		store:    st,
		bus:      bus,
		now:      now,
		sessions: map[string]map[string]presenceEntry{},
		retired:  map[string]map[string]retiredLabel{},
	}
}

// AttachLabeled makes s a live session of the agent (SESS-10) and returns the session it became:
//   - the same process (liveID is not empty and live) replaces its own socket silently, which is closed
//     normally, and keeps its label, harness and start time (SESS-14);
//   - a takeover of another process with the principal label preferred replaces the principal: the old
//     socket is closed with CloseTakenOver and every other label stays (SESS-13); a takeover of any other
//     label is ignored;
//   - otherwise the connection gets AllocateLabel(preferred, harness, taken, remembered): the preferred
//     label while it is free, the label its process remembers from before a drop, or the first free
//     "<harness>-<n>" (SESS-11).
//
// The whole check, allocation and insert run under one lock, so two connections racing for the principal
// label end with two different labels (SESS-12). fresh reports whether a new session started (everything
// but a process reconnecting).
func (p *Presence) AttachLabeled(ctx context.Context, agent db.Agent, s Session, harness, liveID, preferred string, takeover bool) (live Live, fresh bool) {
	p.mu.Lock()
	now := p.now()
	p.pruneLocked(agent.ID, now)
	held := p.sessions[agent.ID]
	// The same process replaces its own socket: same label, harness and start time (SESS-14).
	if liveID != "" {
		for label, prev := range held {
			if prev.liveID != liveID {
				continue
			}
			next := prev
			next.session = s
			held[label] = next
			delete(p.retired[agent.ID], liveID)
			p.mu.Unlock()
			prev.session.Close(websocket.StatusNormalClosure, ReasonReconnected)
			_ = p.touch(ctx, agent.ID) // best effort, like Detach: last_seen_at is informational
			return next.live(), false
		}
	}
	main, mainLive := held[""]
	if held == nil {
		held = map[string]presenceEntry{}
	}
	online := len(held) == 0
	entry := presenceEntry{ownerID: agent.OwnerID, session: s, harness: harness, liveID: liveID, since: now}
	var closed Session
	if takeover && preferred == "" && mainLive {
		// The principal label is the only one a takeover replaces (SESS-13).
		closed = main.session
		entry.label = ""
	} else {
		memory, remembered := p.retired[agent.ID][liveID] // the process's drop memory, if it has one
		taken := func(label string) bool { _, ok := held[label]; return ok }
		entry.label = AllocateLabel(preferred, harness, taken, memory.label)
		if remembered && entry.label == memory.label {
			entry.since = memory.since // a reconnect keeps the start time (SESS-14)
		}
		if remembered {
			delete(p.retired[agent.ID], liveID) // live again: its next drop writes a new memory
		}
	}
	held[entry.label] = entry
	p.sessions[agent.ID] = held
	p.mu.Unlock()
	if closed != nil {
		closed.Close(CloseTakenOver, ReasonTakenOver)
	}
	_ = p.touch(ctx, agent.ID) // best effort, like Detach: last_seen_at is informational
	if online {
		p.bus.Publish(events.AgentPresence{AgentID: agent.ID, OwnerID: agent.OwnerID, Online: true})
	}
	return entry.live(), true
}

// Live returns the agent's principal session, if any.
func (p *Presence) Live(agentID string) (Live, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.sessions[agentID][""]
	return e.live(), ok
}

// Main returns the agent's principal session ("", SESS-11), if any.
func (p *Presence) Main(agentID string) (Session, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.sessions[agentID][""]
	return e.session, ok
}

// Sessions returns the agent's live sessions, the principal first and the labelled ones in the lexical
// order of their label; nil when the agent has none (SESS-32).
func (p *Presence) Sessions(agentID string) []Live {
	p.mu.Lock()
	defer p.mu.Unlock()
	held := p.sessions[agentID]
	if len(held) == 0 {
		return nil
	}
	labels := make([]string, 0, len(held))
	for label := range held {
		labels = append(labels, label)
	}
	sort.Strings(labels) // "" sorts first: the principal
	lives := make([]Live, len(labels))
	for i, label := range labels {
		lives[i] = held[label].live()
	}
	return lives
}

// SessionByLabel returns the agent's live session with label, if any: the lookup a message addressed to
// one session routes through (SESS-20, SESS-22).
func (p *Presence) SessionByLabel(agentID, label string) (Session, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.sessions[agentID][label]
	return e.session, ok
}

// SessionLabel returns the label of the agent's live session that the process liveID holds, if any:
// the lookup the author label of a tool call resolves through (SESS-29).
func (p *Presence) SessionLabel(agentID, liveID string) (string, bool) {
	if liveID == "" {
		return "", false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for held, e := range p.sessions[agentID] {
		if e.liveID == liveID {
			return held, true
		}
	}
	return "", false
}

// Detach removes s when it is still one of the agent's live sessions and remembers its label for a
// reconnect of its process; the agent goes offline when the last session ends. It reports whether s was
// the current session (false after a takeover or a reconnect), which the end-of-session sweep needs.
func (p *Presence) Detach(ctx context.Context, agentID string, s Session) bool {
	return p.detach(ctx, agentID, s, true)
}

// detach is Detach; stamp tells whether the agent's last_seen_at is written, which is a write on the
// single writer connection.
func (p *Presence) detach(ctx context.Context, agentID string, s Session, stamp bool) bool {
	p.mu.Lock()
	now := p.now()
	p.pruneLocked(agentID, now)
	held := p.sessions[agentID]
	var cur presenceEntry
	found := false
	for label, e := range held {
		if e.session != s {
			continue
		}
		cur, found = e, true
		delete(held, label)
		break
	}
	if !found {
		p.mu.Unlock()
		return false
	}
	if cur.liveID != "" {
		if p.retired[agentID] == nil {
			p.retired[agentID] = map[string]retiredLabel{}
		}
		p.retired[agentID][cur.liveID] = retiredLabel{label: cur.label, since: cur.since, expires: now.Add(retiredTTL)}
	}
	offline := len(held) == 0
	if offline {
		delete(p.sessions, agentID)
	}
	p.mu.Unlock()
	if stamp {
		_ = p.touch(ctx, agentID) // best effort: the agent row may be gone already
	}
	if offline {
		p.bus.Publish(events.AgentPresence{AgentID: agentID, OwnerID: cur.ownerID, Online: false})
	}
	return true
}

// Online reports whether the agent has a live session of any label.
func (p *Presence) Online(agentID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.sessions[agentID]) > 0
}

// Session returns the agent's principal live session, if any.
func (p *Presence) Session(agentID string) (Session, bool) {
	return p.Main(agentID)
}

// Kick closes every live session of the agent with code and reason and marks it offline.
func (p *Presence) Kick(ctx context.Context, agentID string, code websocket.StatusCode, reason string) {
	p.end(ctx, agentID, code, reason, true, false)
}

// Drop is Kick for an AI whose row is gone: the sessions end and the agent goes offline the same way, and
// nothing is written, so it never waits for the database. The cleanup of a delete uses it, which must not
// hold the answer of a delete that has already happened.
func (p *Presence) Drop(agentID string, code websocket.StatusCode, reason string) {
	p.end(context.Background(), agentID, code, reason, false, true)
}

// end closes every live session of the agent and detaches it, also when closing one of them panics: a
// session that failed to close must not stay in the presence as an agent that is online.
func (p *Presence) end(ctx context.Context, agentID string, code websocket.StatusCode, reason string, stamp, forget bool) {
	p.mu.Lock()
	held := p.sessions[agentID]
	if len(held) == 0 {
		if forget {
			delete(p.retired, agentID)
		}
		p.mu.Unlock()
		return
	}
	entries := make([]presenceEntry, 0, len(held))
	for _, e := range held {
		entries = append(entries, e)
	}
	ownerID := entries[0].ownerID
	delete(p.sessions, agentID)
	if forget {
		delete(p.retired, agentID)
	}
	p.mu.Unlock()
	// The AI is offline before any socket is closed, so a close that panics cannot leave a live entry.
	defer func() {
		if stamp {
			_ = p.touch(ctx, agentID) // best effort: the agent row may be gone already
		}
		p.bus.Publish(events.AgentPresence{AgentID: agentID, OwnerID: ownerID, Online: false})
	}()
	for _, e := range entries {
		e.session.Close(code, reason)
	}
}

// pruneLocked forgets the labels of the agent's dropped processes whose ten minutes are over. The caller
// holds p.mu.
func (p *Presence) pruneLocked(agentID string, now time.Time) {
	memory := p.retired[agentID]
	if memory == nil {
		return
	}
	for liveID, retired := range memory {
		if !now.Before(retired.expires) {
			delete(memory, liveID)
		}
	}
	if len(memory) == 0 {
		delete(p.retired, agentID)
	}
}

func (p *Presence) touch(ctx context.Context, agentID string) error {
	ctx = context.WithoutCancel(ctx) // runs while a connection closes
	return p.store.WithTx(ctx, func(q *db.Queries) error {
		return q.TouchAgent(ctx, db.TouchAgentParams{LastSeenAt: nullMillis(p.now()), ID: agentID})
	})
}

// SessionLabelOf is the label of the session a message came from (SESS-29), resolved server-side and
// never from the message text: the live session of the AI that holds liveID (the X-Partyline-Live
// header of a tool call), else the label of a worktree folder different from the AI's own (the
// tools-only process in a linked worktree, D10), else "" for the principal and for an AI whose row is
// gone.
func (r *Registry) SessionLabelOf(ctx context.Context, agentID, liveID, sessionFolder string) string {
	if agentID == "" {
		return ""
	}
	if label, ok := r.presence.SessionLabel(agentID, liveID); ok {
		return label
	}
	a, err := r.store.R().GetAgent(ctx, agentID)
	if err != nil || sessionFolder == "" || sessionFolder == a.Folder {
		return ""
	}
	return LabelForFolder(sessionFolder)
}
