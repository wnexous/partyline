package agents

import (
	"context"
	"database/sql"
	"errors"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store/db"
)

// ReasonEndedByPrefix starts the close reason of a session a manager ended: "ended by @handle" (POS-04).
const ReasonEndedByPrefix = "ended by @"

// HomeOrganization returns the id of the organization the AI belongs to, or "" when it has none or the id names
// no AI. The API uses it to tell deleting an AI of an organization from removing anyone else (POS-06).
func (r *Registry) HomeOrganization(ctx context.Context, id string) (string, error) {
	home, err := r.store.R().GetAgentOrg(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return home, err
}

// authorizeDelete decides whether the principal may delete the AI in its home organization (POS-05): an admin
// of it may, and so may whoever manages the AI when it is a guest (orgs.Chart.CanDelete). An AI is seen by its
// host and by the members of the organizations it belongs to (AD-016): whoever sees it and may not delete it
// gets access.ErrForbidden, anyone else access.ErrNotFound. It reads in q, so it decides on the chart of the
// transaction that deletes.
func authorizeDelete(ctx context.Context, q *db.Queries, p authn.Principal, agentID string) (db.Agent, error) {
	actor := p.IID()
	a, err := q.GetAgent(ctx, agentID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Agent{}, access.ErrNotFound
	}
	if err != nil {
		return db.Agent{}, err
	}
	home := a.OrgID.String
	if home == "" {
		// No organization owns it (only a fixture: the upgrade gives every AI a home), so there is no chart to
		// ask and its host decides, as before organizations.
		if actor != "" && a.OwnerID == actor {
			return a, nil
		}
		return db.Agent{}, access.ErrNotFound
	}
	chart, err := orgs.Load(ctx, q, home)
	if err != nil {
		return db.Agent{}, err
	}
	if chart.CanDelete(actor, agentID) {
		return a, nil
	}
	seen := actor != "" && a.OwnerID == actor
	if !seen && actor != "" {
		n, err := q.CountSharedOrgs(ctx, db.CountSharedOrgsParams{FirstIid: actor, SecondIid: agentID})
		if err != nil {
			return db.Agent{}, err
		}
		seen = n > 0
	}
	if !seen {
		return db.Agent{}, access.ErrNotFound
	}
	return db.Agent{}, access.ErrForbidden
}

// EndSession ends the AI's live session on behalf of whoever manages it in an organization they share (POS-04):
// the connection is closed with CloseTakenOver, which the CLI obeys by stopping without reconnecting, and the
// reason is "ended by @handle" with the handle of the one who did it. The AI is not deleted and may connect again.
// An AI with no live session has nothing to end. Whoever cannot see the AI gets access.ErrNotFound, whoever sees it
// and does not manage it access.ErrForbidden.
func (r *Registry) EndSession(ctx context.Context, p authn.Principal, agentID string) error {
	if _, err := r.managedAgent(ctx, p, agentID); err != nil {
		return err
	}
	who, err := r.store.R().GetIntelligence(ctx, p.IID())
	if err != nil {
		return err
	}
	r.presence.Kick(ctx, agentID, CloseTakenOver, ReasonEndedByPrefix+who.Handle)
	return nil
}

// managedAgent loads the AI for a caller who must manage it in an organization they share (POS-04, SESS-59):
// whoever cannot see the AI gets access.ErrNotFound, whoever sees it and does not manage it access.ErrForbidden.
func (r *Registry) managedAgent(ctx context.Context, p authn.Principal, agentID string) (db.Agent, error) {
	actor := p.IID()
	q := r.store.R()
	a, err := q.GetAgent(ctx, agentID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Agent{}, access.ErrNotFound
	}
	if err != nil {
		return db.Agent{}, err
	}
	shared, err := q.ListSharedOrgIDs(ctx, db.ListSharedOrgIDsParams{FirstIid: actor, SecondIid: agentID})
	if err != nil {
		return db.Agent{}, err
	}
	for _, orgID := range shared {
		chart, err := orgs.Load(ctx, q, orgID)
		if err != nil {
			return db.Agent{}, err
		}
		if chart.Manages(actor, agentID) {
			return a, nil
		}
	}
	if len(shared) > 0 || (actor != "" && a.OwnerID == actor) {
		return db.Agent{}, access.ErrForbidden
	}
	return db.Agent{}, access.ErrNotFound
}

// Errors of MakePrincipal: the answer is a conflict, the request being right for another state of the AI.
var (
	// ErrSessionNotLive means no session of the AI is live with the label asked for.
	ErrSessionNotLive = errors.New("agents: that session is not live")
	// ErrSessionUnidentified means the session has no harness session id, which the principal role is pinned to.
	ErrSessionUnidentified = errors.New("agents: that session has no harness session id to pin")
)

// MakePrincipal makes the AI's live session with label the principal one on behalf of whoever manages the AI
// (SESS-59): its harness session id is pinned, and the sessions concerned (the new principal and the one that
// holds the role) are closed with CloseRoleChanged, so that both connect again and take the role the pin now
// gives them, in whatever order they come back. A label that is already the principal's only pins it. A
// session that is not live is ErrSessionNotLive and one with no harness session id ErrSessionUnidentified.
func (r *Registry) MakePrincipal(ctx context.Context, p authn.Principal, agentID, label string) error {
	if _, err := r.managedAgent(ctx, p, agentID); err != nil {
		return err
	}
	r.appMu.Lock()
	defer r.appMu.Unlock()
	id, live := r.presence.SessionID(agentID, label)
	switch {
	case !live:
		return ErrSessionNotLive
	case id == "":
		return ErrSessionUnidentified
	}
	if err := r.setPin(ctx, agentID, id); err != nil {
		return err
	}
	if label != "" {
		// The holder of the principal label is closed too: it comes back as a parallel session, whichever of the two
		// connects first.
		r.presence.CloseForRoleChange(agentID, "", label)
	}
	return nil
}
