package orgs

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/wnexous/partyline/internal/store/db"
)

// ResolveRef finds an organization by id or by slug (what --org and the {org} path segments take): the id
// wins, the slug ignores case and surrounding spaces. An unknown or empty reference is ErrNotFound; it
// says nothing about who may see the organization. q may belong to a transaction.
func ResolveRef(ctx context.Context, q *db.Queries, ref string) (db.Organization, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.Organization{}, ErrNotFound
	}
	o, err := q.GetOrganization(ctx, ref)
	if errors.Is(err, sql.ErrNoRows) {
		o, err = q.GetOrganizationBySlug(ctx, ref)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return db.Organization{}, ErrNotFound
	}
	return o, err
}
