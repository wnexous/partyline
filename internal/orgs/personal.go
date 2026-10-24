package orgs

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/wnexous/partyline/internal/handles"
	"github.com/wnexous/partyline/internal/store/db"
)

// PersonalParams describe the personal organization of a new account (AD-021).
type PersonalParams struct {
	UserID string
	// Name is the account's display name; an empty one falls back to Username.
	Name     string
	Username string
	Now      time.Time
}

// CreatePersonal gives an account its own organization, in the transaction that creates the account:
// named after it (the display name, else the username, cut to 100 characters), at the first free
// address derived from the username, with the account as its admin holding the root position Admin, and
// it becomes the account's default organization (AD-021, ORG-04). The caller audits it, since it knows the
// account.
func CreatePersonal(ctx context.Context, q *db.Queries, p PersonalParams) (Membership, error) {
	m, err := createOrg(ctx, q, orgParams{
		Creator: p.UserID, Name: handles.PersonalOrgName(p.Name, p.Username), SlugBase: p.Username, Now: p.Now,
	})
	if err != nil {
		return Membership{}, err
	}
	if err := q.SetUserDefaultOrg(ctx, db.SetUserDefaultOrgParams{DefaultOrgID: nullable(m.ID), ID: p.UserID}); err != nil {
		return Membership{}, err
	}
	return m, nil
}

// DefaultFor is the organization a new AI joins when --org is not given (ORG-04). For a user it is the
// default organization they chose, if they still belong to it; else the organization they have
// administered the longest; else the one they have belonged to the longest. For an AI it is its home
// organization, and nothing else: an AI without one has none. The result is empty when there is none.
func DefaultFor(ctx context.Context, q *db.Queries, iid string) (string, error) {
	home, err := q.GetAgentOrg(ctx, iid)
	if err == nil {
		return home, nil // an AI
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if org, err := q.GetUserDefaultOrg(ctx, iid); err == nil {
		return org, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	for _, permission := range []string{string(PermissionAdmin), ""} {
		org, err := q.GetOldestOrgOf(ctx, db.GetOldestOrgOfParams{Iid: iid, Permission: permission})
		if err == nil {
			return org, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	return "", nil
}

// DefaultOrg is the organization a new AI of the identity joins when --org is not given (see DefaultFor), ""
// when there is none.
func (s *Service) DefaultOrg(ctx context.Context, iid string) (string, error) {
	return DefaultFor(ctx, s.store.R(), iid)
}

// SetDefault chooses the organization new AIs of the user join when --org is not given. The user must
// be a member of it, with any permission; anyone else - another organization, one that does not exist,
// an AI, an unknown identity - is ErrForbidden, so a refusal tells nothing about organizations the user
// does not belong to.
func (s *Service) SetDefault(ctx context.Context, userIID, orgID string) error {
	return s.store.WithTx(ctx, func(q *db.Queries) error {
		if ok, err := activeUser(ctx, q, userIID); err != nil || !ok {
			if err == nil {
				err = ErrForbidden
			}
			return err
		}
		if _, ok, err := memberOf(ctx, q, orgID, userIID); err != nil || !ok {
			if err == nil {
				err = ErrForbidden
			}
			return err
		}
		return q.SetUserDefaultOrg(ctx, db.SetUserDefaultOrgParams{DefaultOrgID: nullable(orgID), ID: userIID})
	})
}
