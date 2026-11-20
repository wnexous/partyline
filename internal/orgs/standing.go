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
	// Permission is the identity's in the organization.
	Permission Permission
}

// StandingOf reads the standing of iid in the organization. ok is false when the organization does not
// exist or iid is not a member of it.
func (z *Authz) StandingOf(ctx context.Context, orgID, iid string) (s Standing, ok bool, err error) {
	r, err := z.store.R().GetOrgMembership(ctx, db.GetOrgMembershipParams{OrgID: orgID, Iid: iid})
	if errors.Is(err, sql.ErrNoRows) {
		return Standing{}, false, nil
	}
	if err != nil {
		return Standing{}, false, err
	}
	return Standing{Org: r.Organization, Permission: Permission(r.Permission)}, true, nil
}

// DefaultStandingOf is StandingOf in the organization DefaultFor names, the one a new AI of iid joins when
// --org is not given. ok is false when iid has no default organization.
func (z *Authz) DefaultStandingOf(ctx context.Context, iid string) (Standing, bool, error) {
	org, err := DefaultFor(ctx, z.store.R(), iid)
	if err != nil || org == "" {
		return Standing{}, false, err
	}
	return z.StandingOf(ctx, org, iid)
}
