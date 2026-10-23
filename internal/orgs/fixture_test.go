package orgs

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// fixture is a migrated database with the organization authorization on top: the tests of this
// package build their users, AIs, organizations and charts through it with the store's queries only,
// so they do not depend on any service that arrives later.
type fixture struct {
	t  *testing.T
	st *store.Store
	z  *Authz
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "partyline.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(context.Background()))
	return &fixture{t: t, st: st, z: NewAuthz(st)}
}

func (f *fixture) tx(fn func(ctx context.Context, q *db.Queries) error) {
	f.t.Helper()
	ctx := context.Background()
	require.NoError(f.t, f.st.WithTx(ctx, func(q *db.Queries) error { return fn(ctx, q) }))
}

// user creates an account and its intelligence; the id is "u-<name>" and the handle <name>.
func (f *fixture) user(name string) string {
	f.t.Helper()
	id := "u-" + name
	f.tx(func(ctx context.Context, q *db.Queries) error {
		if err := q.CreateUser(ctx, db.CreateUserParams{ID: id, Username: name, PasswordHash: "x", Role: "member", CreatedAt: 1}); err != nil {
			return err
		}
		return q.CreateIntelligence(ctx, db.CreateIntelligenceParams{ID: id, Kind: "human", DisplayName: name, Handle: name, CreatedAt: 1, UpdatedAt: 1})
	})
	return id
}

// ai creates an AI hosted by the user owner, with its intelligence; the id is "a-<name>". It belongs to no
// organization until home puts it in one.
func (f *fixture) ai(name, owner string) string {
	f.t.Helper()
	id := "a-" + name
	f.tx(func(ctx context.Context, q *db.Queries) error {
		if err := q.CreateAgent(ctx, db.CreateAgentParams{ID: id, OwnerID: owner, Name: id, CreatedAt: 1}); err != nil {
			return err
		}
		return q.CreateIntelligence(ctx, db.CreateIntelligenceParams{ID: id, Kind: "ai", DisplayName: name, Handle: name, CreatedAt: 1, UpdatedAt: 1})
	})
	return id
}

// home makes org the home organization of the AI, created by creator ("" for nobody), and a member of
// it with the permission and position.
func (f *fixture) home(ai, org, creator string, perm Permission, position string) {
	f.t.Helper()
	f.tx(func(ctx context.Context, q *db.Queries) error {
		if err := q.SetAgentHome(ctx, db.SetAgentHomeParams{OrgID: nullable(org), ID: ai}); err != nil {
			return err
		}
		if err := q.SetAgentCreator(ctx, db.SetAgentCreatorParams{CreatorIid: nullable(creator), ID: ai}); err != nil {
			return err
		}
		return q.CreateOrgMember(ctx, db.CreateOrgMemberParams{OrgID: org, Iid: ai, Permission: string(perm), PositionID: nullable(position), JoinedAt: 1})
	})
}

// org creates an organization; the id is "o-<slug>".
func (f *fixture) org(slug string) string {
	f.t.Helper()
	id := "o-" + slug
	f.tx(func(ctx context.Context, q *db.Queries) error {
		return q.CreateOrganization(ctx, db.CreateOrganizationParams{ID: id, Name: slug, Slug: slug, CreatedAt: 1, UpdatedAt: 1})
	})
	return id
}

// position creates a position of org under parent ("" for a root); the id is "p-<org slug>-<name>".
func (f *fixture) position(org, name, parent string) string {
	f.t.Helper()
	id := "p-" + org[2:] + "-" + name
	f.tx(func(ctx context.Context, q *db.Queries) error {
		return q.CreatePosition(ctx, db.CreatePositionParams{ID: id, OrgID: org, Name: name, ParentID: nullable(parent), CreatedAt: 1})
	})
	return id
}

// join makes iid a member of org.
func (f *fixture) join(org, iid string, perm Permission, position, inviter string) {
	f.t.Helper()
	f.tx(func(ctx context.Context, q *db.Queries) error {
		return q.CreateOrgMember(ctx, db.CreateOrgMemberParams{
			OrgID: org, Iid: iid, Permission: string(perm), PositionID: nullable(position), InviterIid: nullable(inviter), JoinedAt: 1,
		})
	})
}
