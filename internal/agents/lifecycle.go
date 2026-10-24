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
	actor := p.IID()
	q := r.store.R()
	a, err := q.GetAgent(ctx, agentID)
	if errors.Is(err, sql.ErrNoRows) {
		return access.ErrNotFound
	}
	if err != nil {
		return err
	}
	shared, err := q.ListSharedOrgIDs(ctx, db.ListSharedOrgIDsParams{FirstIid: actor, SecondIid: agentID})
	if err != nil {
		return err
	}
	managed := false
	for _, orgID := range shared {
		chart, err := orgs.Load(ctx, q, orgID)
		if err != nil {
			return err
		}
		if chart.Manages(actor, agentID) {
			managed = true
			break
		}
	}
	switch {
	case managed:
	case len(shared) > 0 || (actor != "" && a.OwnerID == actor):
		return access.ErrForbidden
	default:
		return access.ErrNotFound
	}
	who, err := q.GetIntelligence(ctx, actor)
	if err != nil {
		return err
	}
	r.presence.Kick(ctx, agentID, CloseTakenOver, ReasonEndedByPrefix+who.Handle)
	return nil
}
