package orgs

import (
	"context"
	"database/sql"
	"errors"

	"github.com/wnexous/partyline/internal/store/db"
)

// Standing is where one identity stands in one organization: the facts a session is told about itself
// (the welcome frame) and a client about its user (GET /agent/whoami).
type Standing struct {
	Org db.Organization
	// Position is the name of the position the identity holds; empty for none.
	Position string
	// Managers are the ids of the identities that manage it, nearest first (Chart.ManagersOf).
	Managers []string
}

// StandingOf reads the standing of iid in the organization, in one read snapshot, keeping the nearest
// limit managers (all of them when limit is negative). ok is false when the organization does not
// exist or iid is not a member of it.
func (z *Authz) StandingOf(ctx context.Context, orgID, iid string, limit int) (s Standing, ok bool, err error) {
	z.loads.Add(1)
	err = z.store.ReadTx(ctx, func(q *db.Queries) error {
		org, err := q.GetOrganization(ctx, orgID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		c, err := Load(ctx, q, orgID)
		if err != nil {
			return err
		}
		m, member := c.Member(iid)
		if !member {
			return nil
		}
		managers := c.ManagersOf(iid)
		if limit >= 0 && len(managers) > limit {
			managers = managers[:limit]
		}
		s, ok = Standing{Org: org, Position: c.Positions[m.PositionID].Name, Managers: managers}, true
		return nil
	})
	return s, ok, err
}

// DefaultStandingOf is StandingOf in the organization DefaultFor names, the one a new AI of iid joins when
// --org is not given. ok is false when iid has no default organization.
func (z *Authz) DefaultStandingOf(ctx context.Context, iid string, limit int) (Standing, bool, error) {
	org, err := DefaultFor(ctx, z.store.R(), iid)
	if err != nil || org == "" {
		return Standing{}, false, err
	}
	return z.StandingOf(ctx, org, iid, limit)
}
