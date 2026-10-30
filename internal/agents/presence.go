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
	// CloseRoleChanged ends a connection because the principal role moved to another session (SESS-59):
	// unlike CloseTakenOver the channel does not stop, it connects again and takes the role it now has, the
	// principal's or a parallel session's.
	CloseRoleChanged websocket.StatusCode = 4004
)

// Close reasons sent with the close codes.
const (
	ReasonDeleted   = "deleted"
	ReasonTakenOver = "taken_over"
	// ReasonRoleChanged closes a connection whose session must connect again with the role it now has.
	ReasonRoleChanged = "role_changed"
	// ReasonReconnected closes the old socket of a process that connected again (code 1000).
	ReasonReconnected = "reconnected"
)

// retiredTTL is how long a process keeps the label of a session that ended, so that a reconnect within
// it keeps the label a person is addressing (SESS-14).
const retiredTTL = 10 * time.Minute

// sessionLabelTTL is how long a session with a harness session id keeps the label it had after it ended:
// the id outlives the process (`claude --resume` is a new process of the same session), and the label a
// person addresses must come back with it (SESS-54).
const sessionLabelTTL = 24 * time.Hour

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
	// sessionID is the harness session id the session connected with ("" when its harness has none).
	sessionID string
	// clientVersion is the version of the partyline client that holds the connection, as it sent it; ""
	// for a client older than the header (SESS-63).
	clientVersion string
	since         time.Time
}

// setSessionLabel tells a session the label it attached with, when it wants to know (a gateway
// connection does). It runs under the presence lock, before the session can be found.
func setSessionLabel(s Session, label string) {
	if l, ok := s.(interface{ setLabel(string) }); ok {
		l.setLabel(label)
	}
}

// live is the exported view of an entry.
func (e presenceEntry) live() Live {
	return Live{
		Label: e.label, Harness: e.harness, Since: e.since, CanBePrincipal: e.sessionID != "",
		Outdated: sessionOutdated(e.harness, e.sessionID, e.clientVersion),
	}
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
	// CanBePrincipal is whether the session has a harness session id, which the principal role is pinned
	// to: only such a session can be made the principal (SESS-59).
	CanBePrincipal bool
	// Outdated is whether the session's client is too old to send its harness session id (a harness that
	// has one), or older than the first version that sends it: only Claude Code and pi can be outdated
	// (SESS-63).
	Outdated bool
}

// Presence tracks which agents are connected. One AI has any number of labelled live sessions, at most
// one per label; the principal session's label is "" (SESS-10, SESS-11). Allocation and the insert happen
// under one lock, so labels are unique among an AI's live sessions (SESS-12). Every session change (a new
// session attaching or an existing one detaching) publishes AgentPresence with Online = any live session,
// so a profile or an autocomplete refreshes while the AI stays online (SESS-32, SESS-33); the agent's
// last_seen_at is stamped as before.
type Presence struct {
	store *store.Store
	bus   *events.Bus
	now   func() time.Time

	mu       sync.Mutex
	sessions map[string]map[string]presenceEntry // AI id -> label -> entry
	retired  map[string]map[string]retiredLabel  // AI id -> live id -> remembered label
	// remembered is the same memory by harness session id, which survives a new process (SESS-54).
	remembered map[string]map[string]retiredLabel // AI id -> harness session id -> remembered label
	// principalGone is when the AI's principal session was last seen leaving (SESS-61); in memory like
	// the sessions themselves.
	principalGone map[string]time.Time
	// ended runs for each session Kick closes, after the AI is offline and the sockets are closed: the
	// gateway sweeps the delivery rows that connection owned. Set before the first connection; nil leaves
	// the sweep to the connection's own end.
	ended func(ctx context.Context, agentID string, s Session)
}

// OnEnded makes f run for every session Kick ends, once its socket is closed (nil clears it). A session
// that ends alone is Detach's concern: its connection sweeps itself.
func (p *Presence) OnEnded(f func(ctx context.Context, agentID string, s Session)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ended = f
}

// HoldsLive reports whether a live session of the agent belongs to the process liveID: a process that
// reconnected replaced its socket, so the connection it left owns no end.
func (p *Presence) HoldsLive(agentID, liveID string) bool {
	if liveID == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.sessions[agentID] {
		if e.liveID == liveID {
			return true
		}
	}
	return false
}

// Find returns the agent's live session for which match is true, if any. match runs under the presence
// lock and must not call back into Presence.
func (p *Presence) Find(agentID string, match func(Session) bool) (Session, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.sessions[agentID] {
		if match(e.session) {
			return e.session, true
		}
	}
	return nil, false
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

		remembered:    map[string]map[string]retiredLabel{},
		principalGone: map[string]time.Time{},
	}
}

// AttachRequest is what a connection says about itself when it attaches (see Presence.Attach).
type AttachRequest struct {
	// Harness is the app the session runs in (cleanHarness already applied).
	Harness string
	// LiveID is the id of the process that holds the connection; empty when the client sent none.
	LiveID string
	// Preferred is the label the session asks for: "" in the AI's own folder, the worktree's slug otherwise.
	Preferred string
	// Takeover asks to replace the live principal session (--take-over).
	Takeover bool
	// SessionID is the harness session id the connection carries (X-Partyline-Harness-Session); empty when
	// its harness has none.
	SessionID string
	// Principal asks to be the AI's principal session (--principal).
	Principal bool
	// StoredLabel is the label the database remembers for SessionID (agent_session_labels): what a session
	// gets back after a restart of the server, when presence remembers nothing (SESS-54).
	StoredLabel string
	// Pinned is the AI's pinned principal session id as stored when the attach starts ("" pins none).
	Pinned string
	// ClientVersion is the partyline client version the connection sent (X-Partyline-Client-Version);
	// empty for a client older than the header (SESS-63).
	ClientVersion string
	// HadSessionID is whether the AI has had a session with a harness session id before: its pin names
	// one, or agent_session_labels remembers a parallel one. A session that sends no id then waits as a
	// parallel session instead of taking the principal role (SESS-64).
	HadSessionID bool
}

// Attached is the outcome of Presence.Attach.
type Attached struct {
	// Live is the session the connection became.
	Live Live
	// Fresh reports whether a new session started (everything but a process reconnecting).
	Fresh bool
	// Pin is the new pinned principal session id the caller must store (an empty string unpins), nil when
	// the pin does not change (SESS-52, SESS-57).
	Pin *string
}

// decideRole says whether the connection takes the principal label, and the pin that results. The principal
// role belongs to the session whose harness session id is pinned and to nobody else (SESS-53):
//   - a worktree session (preferred label) is never the principal;
//   - --principal always is, and pins its id; --take-over is when a principal of another process is live
//     (SESS-57, SESS-58);
//   - with an id pinned, only a session with that id is, whoever is connected and whether or not the label
//     is free: no session is promoted because the principal left (SESS-53, SESS-55);
//   - a session that sends no harness session id never takes the role once the AI has had an identified
//     session (a pin or a stored parallel label), so an old client cannot take it from the pinned one
//     (SESS-64);
//   - with nothing pinned and no identified session before, the folder's first live session acts as the
//     principal, as before the feature (SESS-56), and the id of the first session that holds the role
//     gets pinned (SESS-52).
func decideRole(req AttachRequest, main presenceEntry, mainLive bool) (principal bool, pin *string) {
	if req.Preferred != "" {
		return false, nil
	}
	set := func(id string) *string {
		if id == req.Pinned {
			return nil
		}
		return &id
	}
	sameProcess := mainLive && req.LiveID != "" && main.liveID == req.LiveID
	sameSession := mainLive && req.SessionID != "" && main.sessionID == req.SessionID
	switch {
	case req.Principal || (req.Takeover && mainLive && !sameProcess):
		return true, set(req.SessionID)
	case req.Pinned != "":
		return req.SessionID == req.Pinned, nil
	case req.SessionID == "" && req.HadSessionID:
		return false, nil
	case !mainLive || sameProcess || sameSession:
		if req.SessionID == "" {
			return true, nil
		}
		return true, set(req.SessionID)
	case main.sessionID != "":
		return false, set(main.sessionID) // the holder's id is the first one seen: it is the pinned one
	}
	return false, nil
}

// AttachLabeled is Attach with the arguments a connection that sends no harness session id has.
func (p *Presence) AttachLabeled(ctx context.Context, agent db.Agent, s Session, harness, liveID, preferred string, takeover bool) (live Live, fresh bool) {
	out := p.Attach(ctx, agent, s, AttachRequest{Harness: harness, LiveID: liveID, Preferred: preferred, Takeover: takeover, Pinned: agent.PrincipalSession})
	return out.Live, out.Fresh
}

// Attach makes s a live session of the agent (SESS-10) and returns the session it became:
//   - the same process (LiveID is not empty and live) replaces its own socket silently, which is closed
//     normally, and keeps its label, harness and start time (SESS-14); when the principal rules say its role
//     changed (it is now the pinned session, or no longer is) it takes the role it has now instead;
//   - a session the rules give the principal label (decideRole) takes it, and replaces the live holder: the
//     old socket is closed with CloseTakenOver when the replacement is explicit or the same harness session,
//     with CloseRoleChanged otherwise, so that it connects again as a parallel session (SESS-13, SESS-53);
//   - any other connection gets AllocateLabel(preferred, harness, taken, remembered): the preferred label
//     while it is free, the label its harness session or its process remembers from before a drop, or the
//     first free "<harness>-<n>" (SESS-11, SESS-54). The principal label is never allocated to it.
//
// The whole check, allocation and insert run under one lock, so two connections racing for the principal
// label end with two different labels (SESS-12).
func (p *Presence) Attach(ctx context.Context, agent db.Agent, s Session, req AttachRequest) Attached {
	p.mu.Lock()
	now := p.now()
	p.pruneLocked(agent.ID, now)
	held := p.sessions[agent.ID]
	main, mainLive := held[""]
	wantMain, pin := decideRole(req, main, mainLive)
	var replaced *presenceEntry
	// The same process replaces its own socket: same label, harness and start time (SESS-14).
	if req.LiveID != "" {
		for label, prev := range held {
			if prev.liveID != req.LiveID {
				continue
			}
			if (label == "") != wantMain && req.Preferred == "" {
				// Its role changed while it was away: it is taken out and attaches as what it is now.
				delete(held, label)
				replaced = &prev
				break
			}
			next := prev
			next.session = s
			next.sessionID = req.SessionID
			next.clientVersion = req.ClientVersion
			setSessionLabel(s, next.label)
			held[label] = next
			delete(p.retired[agent.ID], req.LiveID)
			p.mu.Unlock()
			prev.session.Close(websocket.StatusNormalClosure, ReasonReconnected)
			_ = p.touch(ctx, agent.ID) // best effort, like Detach: last_seen_at is informational
			return Attached{Live: next.live(), Pin: pin}
		}
	}
	if held == nil {
		held = map[string]presenceEntry{}
	}
	entry := presenceEntry{ownerID: agent.OwnerID, session: s, harness: req.Harness, liveID: req.LiveID, sessionID: req.SessionID, clientVersion: req.ClientVersion, since: now}
	memory, remembered := p.memoryLocked(agent.ID, req.LiveID, req.SessionID)
	if !remembered && req.StoredLabel != "" {
		memory = retiredLabel{label: req.StoredLabel, since: now} // the database's memory; no start time to keep
	}
	var closed Session
	closeCode, closeReason := CloseTakenOver, ReasonTakenOver
	if wantMain {
		if mainLive && (req.LiveID == "" || main.liveID != req.LiveID) {
			closed = main.session
			sameSession := req.SessionID != "" && main.sessionID == req.SessionID
			if !req.Principal && !req.Takeover && !sameSession {
				closeCode, closeReason = CloseRoleChanged, ReasonRoleChanged
			}
		}
		entry.label = ""
		if remembered && memory.label == "" {
			entry.since = memory.since // a reconnect keeps the start time (SESS-14)
		}
	} else {
		taken := func(label string) bool {
			_, ok := held[label]
			return label == "" || ok // the principal label is never allocated to a session that is not the principal
		}
		entry.label = AllocateLabel(req.Preferred, req.Harness, taken, memory.label)
		if remembered && entry.label == memory.label {
			entry.since = memory.since // a reconnect keeps the start time (SESS-14)
		}
	}
	if replaced != nil && replaced.label == entry.label {
		entry.since = replaced.since
	}
	if remembered {
		delete(p.retired[agent.ID], req.LiveID) // live again: its next drop writes a new memory
		delete(p.remembered[agent.ID], req.SessionID)
	}
	setSessionLabel(s, entry.label) // before the session is visible: a lookup by label sees it set
	held[entry.label] = entry
	p.sessions[agent.ID] = held
	if _, principalNow := held[""]; principalNow {
		delete(p.principalGone, agent.ID)
	} else if _, known := p.principalGone[agent.ID]; !known {
		p.principalGone[agent.ID] = now // the first sign that the principal is away
	}
	p.mu.Unlock()
	if closed != nil {
		closed.Close(closeCode, closeReason)
	}
	if replaced != nil {
		replaced.session.Close(websocket.StatusNormalClosure, ReasonReconnected)
	}
	_ = p.touch(ctx, agent.ID) // best effort, like Detach: last_seen_at is informational
	// A fresh session is a change of the AI's sessions even when it was already online (SESS-32).
	p.bus.Publish(events.AgentPresence{AgentID: agent.ID, OwnerID: agent.OwnerID, Online: true})
	return Attached{Live: entry.live(), Fresh: true, Pin: pin}
}

// memoryLocked returns what the AI remembers of a connection that ended: by its process first, then by its
// harness session, whose memory outlives the process (SESS-54). The caller holds p.mu.
func (p *Presence) memoryLocked(agentID, liveID, sessionID string) (retiredLabel, bool) {
	if m, ok := p.retired[agentID][liveID]; ok && liveID != "" {
		return m, true
	}
	if m, ok := p.remembered[agentID][sessionID]; ok && sessionID != "" {
		return m, true
	}
	return retiredLabel{}, false
}

// SessionID returns the harness session id of the agent's live session with label, when it has one: what a
// make-principal pins (SESS-59).
func (p *Presence) SessionID(agentID, label string) (id string, live bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.sessions[agentID][label]
	return e.sessionID, ok
}

// CloseForRoleChange closes the agent's live sessions that hold the given labels with CloseRoleChanged, so
// that they connect again and take the role the pin now gives them (SESS-59). Labels that are not live are
// skipped.
func (p *Presence) CloseForRoleChange(agentID string, labels ...string) {
	p.mu.Lock()
	var sessions []Session
	for _, label := range labels {
		if e, ok := p.sessions[agentID][label]; ok {
			sessions = append(sessions, e.session)
		}
	}
	p.mu.Unlock()
	for _, s := range sessions {
		s.Close(CloseRoleChanged, ReasonRoleChanged)
	}
}

// PrincipalGone returns when the agent's principal session was last seen leaving, while another of its
// sessions is live: the moment "principal offline since" shows (SESS-61). It reports false when the
// principal is live, when no session is live at all or when the moment is not known.
func (p *Presence) PrincipalGone(agentID string) (time.Time, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	held := p.sessions[agentID]
	if _, principal := held[""]; principal || len(held) == 0 {
		return time.Time{}, false
	}
	at, ok := p.principalGone[agentID]
	return at, ok
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
// the current session (false after a takeover, a reconnect or a Kick).
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
	if cur.sessionID != "" && cur.label != "" {
		// A parallel session keeps its label with its harness session id, beyond the process (SESS-54).
		if p.remembered[agentID] == nil {
			p.remembered[agentID] = map[string]retiredLabel{}
		}
		p.remembered[agentID][cur.sessionID] = retiredLabel{label: cur.label, since: cur.since, expires: now.Add(sessionLabelTTL)}
	}
	if cur.label == "" {
		p.principalGone[agentID] = now
	}
	offline := len(held) == 0
	if offline {
		delete(p.sessions, agentID)
	}
	p.mu.Unlock()
	if stamp {
		_ = p.touch(ctx, agentID) // best effort: the agent row may be gone already
	}
	// Every detach is a session change; Online stays true while another session remains (SESS-32).
	p.bus.Publish(events.AgentPresence{AgentID: agentID, OwnerID: cur.ownerID, Online: !offline})
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
			delete(p.remembered, agentID)
			delete(p.principalGone, agentID)
		}
		p.mu.Unlock()
		return
	}
	entries := make([]presenceEntry, 0, len(held))
	for _, e := range held {
		entries = append(entries, e)
	}
	ownerID := entries[0].ownerID
	if _, principal := held[""]; principal {
		p.principalGone[agentID] = p.now()
	}
	delete(p.sessions, agentID)
	if forget {
		delete(p.retired, agentID)
		delete(p.remembered, agentID)
		delete(p.principalGone, agentID)
	}
	ended := p.ended
	p.mu.Unlock()
	// The AI is offline before any socket is closed, so a close that panics cannot leave a live entry.
	defer func() {
		if stamp {
			_ = p.touch(ctx, agentID) // best effort: the agent row may be gone already
		}
		p.bus.Publish(events.AgentPresence{AgentID: agentID, OwnerID: ownerID, Online: false})
		// Each connection that was closed gives back what it owned. Drop is for an AI whose row is
		// gone (its delivery rows went with it) and never waits for the database.
		if ended != nil && stamp {
			for _, e := range entries {
				ended(ctx, agentID, e.session)
			}
		}
	}()
	for _, e := range entries {
		e.session.Close(code, reason)
	}
}

// pruneLocked forgets the labels of the agent's dropped processes and harness sessions whose time is over.
// The caller holds p.mu.
func (p *Presence) pruneLocked(agentID string, now time.Time) {
	for _, memory := range []map[string]map[string]retiredLabel{p.retired, p.remembered} {
		kept := memory[agentID]
		if kept == nil {
			continue
		}
		for key, retired := range kept {
			if !now.Before(retired.expires) {
				delete(kept, key)
			}
		}
		if len(kept) == 0 {
			delete(memory, agentID)
		}
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
