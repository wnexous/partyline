package orgs

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/attachments"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// fixture is a migrated database with the organization authorization on top: the tests of this
// package build their users, AIs, organizations and charts through it with the store's queries only,
// so they do not depend on any service that arrives later.
type fixture struct {
	t     *testing.T
	st    *store.Store
	raw   *sql.DB // a second connection for the assertions and set-ups the store's queries do not offer
	z     *Authz
	bus   *events.Bus
	audit *audit.Log
	blobs *attachments.Store
	clock *fakeClock
	svc   *Service
}

// t0 is the moment every fixture's clock starts at.
var t0 = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

// fakeClock is a clock a test moves by hand.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func newFixture(t *testing.T) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "partyline.db")
	st, err := store.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(context.Background()))
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	require.NoError(t, err)
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	blobs, err := attachments.Open(filepath.Join(t.TempDir(), "blobs"))
	require.NoError(t, err)
	f := &fixture{t: t, st: st, raw: raw, z: NewAuthz(st), bus: events.NewBus(), blobs: blobs, clock: &fakeClock{now: t0}}
	f.audit = audit.New(st, f.clock.Now)
	f.svc = NewService(Deps{Store: st, Authz: f.z, Bus: f.bus, Audit: f.audit, Blobs: blobs, Now: f.clock.Now})
	return f
}

// events subscribes to the bus; the returned function drains what was published since the last call.
func (f *fixture) events() func() []events.Event {
	f.t.Helper()
	ch, cancel := f.bus.Subscribe(64)
	f.t.Cleanup(cancel)
	return func() []events.Event {
		var out []events.Event
		for {
			select {
			case e := <-ch:
				out = append(out, e)
			default:
				return out
			}
		}
	}
}

// audited lists the audit entries as "action target" in the order they were recorded.
func (f *fixture) audited() []string {
	f.t.Helper()
	rows, _, err := f.audit.List(context.Background(), "")
	require.NoError(f.t, err)
	out := make([]string, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		out = append(out, rows[i].Action+" "+rows[i].TargetID)
	}
	return out
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

// exec runs a statement on the raw connection.
func (f *fixture) exec(query string, args ...any) {
	f.t.Helper()
	_, err := f.raw.Exec(query, args...)
	require.NoError(f.t, err, query)
}

// count returns the single number a query selects.
func (f *fixture) count(query string, args ...any) int {
	f.t.Helper()
	var n int
	require.NoError(f.t, f.raw.QueryRow(query, args...).Scan(&n), query)
	return n
}

// str returns the single text a query selects ("" for NULL).
func (f *fixture) str(query string, args ...any) string {
	f.t.Helper()
	var s sql.NullString
	require.NoError(f.t, f.raw.QueryRow(query, args...).Scan(&s), query)
	return s.String
}

// connector creates a connection owned by owner that belongs to org ("" for none).
func (f *fixture) connector(id, owner, org string) {
	f.t.Helper()
	f.exec(`INSERT INTO connectors (id, owner_id, kind, name, created_at, updated_at, org_id) VALUES (?, ?, 'telegram', ?, 1, 1, ?)`,
		id, owner, id, nullable(org))
}

// group creates a group created by creator that belongs to org ("" for none, like a DM).
func (f *fixture) group(id, creator, org string) {
	f.t.Helper()
	f.exec(`INSERT INTO rooms (id, owner_id, name, created_at, updated_at, org_id, creator_iid) VALUES (?, ?, ?, 1, 1, ?, ?)`,
		id, creator, id, nullable(org), nullable(creator))
}

// adminOf adds a new admin to the organization and returns it.
func (f *fixture) adminOf(org string) string {
	f.t.Helper()
	u := f.user("admin-" + org[2:])
	f.join(org, u, PermissionAdmin, "", "")
	return u
}
