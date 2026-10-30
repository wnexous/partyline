package agents

import (
	"context"
	"fmt"

	"github.com/wnexous/partyline/internal/store/db"
)

// Session identity for persistence (SN-01): a session is not a row anywhere, it is a live connection. What
// survives a reconnect and a restart of the server is the harness session id it sends
// (X-Partyline-Harness-Session, SESS-51), so that is the key its per-room state is stored under. A session
// without an id (an outdated client, or a harness that has none) is keyed by its label, which lives as long
// as the label does. The principal has no label: its key is the AI's pinned session id (SESS-52), or
// keyPrincipal when nothing is pinned, because its read position follows the role.
const (
	// keyPrincipal is the session key of the principal of an AI that pins no harness session id.
	keyPrincipal = "principal"
	// keyLabelPrefix marks the key of a session that has no harness session id: its label.
	keyLabelPrefix = "label:"
)

// The notification settings a session can have in a room (SN-02).
const (
	// NotifyOn notifies the session of every message the AI's delivery mode wants there: the principal's
	// default, which is today's behaviour.
	NotifyOn = "on"
	// NotifyMentions notifies the session only of the messages that address it: a parallel session's default.
	NotifyMentions = "mentions"
)

// SessionKey returns the key the per-room state of a session is stored under (SN-01): the harness session
// id it sent, else "label:<label>" for a parallel session, else, for the principal, the AI's pinned session
// id or "principal" when nothing is pinned. A session with an id keeps its state across reconnects, new
// processes and restarts of the server; one without an id keeps it while its label lives.
func SessionKey(harnessSessionID, label, pinnedPrincipal string) string {
	switch {
	case harnessSessionID != "":
		return harnessSessionID
	case label != "":
		return keyLabelPrefix + label
	case pinnedPrincipal != "":
		return pinnedPrincipal
	}
	return keyPrincipal
}

// DefaultNotify is the notification setting a session's state row is created with (SN-02): the principal is
// notified of everything its delivery mode wants, and a parallel session only of the messages that mention
// it, so that a sibling's traffic does not pull it away from its own work.
func DefaultNotify(isPrincipal bool) string {
	if isPrincipal {
		return NotifyOn
	}
	return NotifyMentions
}

// ValidNotify reports whether notify is one of the two settings a session can have in a room (SN-02).
func ValidNotify(notify string) bool { return notify == NotifyOn || notify == NotifyMentions }

// SessionKeyOf returns the key under which the per-room state of the agent's live session with label is
// stored, and whether that session is live (SN-01). pinnedPrincipal is the AI's pinned principal session id
// (agents.principal_session): the key of the principal is its harness session id, else that pin, else
// keyPrincipal.
func (p *Presence) SessionKeyOf(agentID, label, pinnedPrincipal string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.sessions[agentID][label]
	if !ok {
		return "", false
	}
	return SessionKey(e.sessionID, e.label, pinnedPrincipal), true
}

// EnsureRoomState creates the state row of a session in a room that has none (SN-01, SN-02): the session
// joins caught up, at its join point (the highest sequence the room has assigned), so a brand-new session
// never receives the whole history, and with the default setting of its role. A row that exists is left as
// it is, so a session that connects again keeps what it had read and its setting.
func (r *Registry) EnsureRoomState(ctx context.Context, agentID, sessionKey, roomID string, isPrincipal bool) error {
	if sessionKey == "" {
		return nil
	}
	now := r.now().UnixMilli()
	return r.store.WithTx(ctx, func(q *db.Queries) error {
		join, err := q.GetRoomHighSeq(ctx, roomID)
		if err != nil {
			return err
		}
		return q.CreateSessionRoomState(ctx, db.CreateSessionRoomStateParams{
			AgentID: agentID, SessionKey: sessionKey, RoomID: roomID,
			ReadSeq: join, Notify: DefaultNotify(isPrincipal), UpdatedAt: now,
		})
	})
}

// AdvanceSessionReadSeq moves a session's read position in one room forward, never back (SN-01): the
// acknowledgement of a pushed batch and the read of a catch-up both go through it. A session that has no
// state row there yet gets one with the default setting of its role.
func (r *Registry) AdvanceSessionReadSeq(ctx context.Context, agentID, sessionKey, roomID string, seq int64, isPrincipal bool) error {
	if sessionKey == "" {
		return nil
	}
	now := r.now().UnixMilli()
	return r.store.WithTx(ctx, func(q *db.Queries) error {
		return q.AdvanceSessionReadSeq(ctx, db.AdvanceSessionReadSeqParams{
			AgentID: agentID, SessionKey: sessionKey, RoomID: roomID, ReadSeq: seq,
			Notify: DefaultNotify(isPrincipal), UpdatedAt: now,
		})
	})
}

// SetSessionNotify changes the notification setting of a session in a room (SN-02). The state row is created
// first when the session has none, at its join point and with its default, so that turning a notification on
// never turns the whole history unread.
func (r *Registry) SetSessionNotify(ctx context.Context, agentID, sessionKey, roomID string, isPrincipal bool, notify string) error {
	if !ValidNotify(notify) {
		return fmt.Errorf("agents: unknown notification setting %q", notify)
	}
	if err := r.EnsureRoomState(ctx, agentID, sessionKey, roomID, isPrincipal); err != nil {
		return err
	}
	now := r.now().UnixMilli()
	return r.store.WithTx(ctx, func(q *db.Queries) error {
		_, err := q.SetSessionNotify(ctx, db.SetSessionNotifyParams{
			Notify: notify, UpdatedAt: now, AgentID: agentID, SessionKey: sessionKey, RoomID: roomID,
		})
		return err
	})
}
