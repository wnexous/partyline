package agents

import (
	"context"
	"database/sql"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

var t0 = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// fixtureClock is the registry's clock in the tests: it stands at t0 until a test advances it.
type fixtureClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fixtureClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fixtureClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type fixture struct {
	st       *store.Store
	raw      *sql.DB // separate connection for inserting fixtures and counting rows
	bus      *events.Bus
	az       *access.Authorizer
	presence *Presence
	reg      *Registry
	clock    *fixtureClock
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "partyline.db")
	st, err := store.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(context.Background()))
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	clock := &fixtureClock{now: t0}
	now := clock.Now
	bus := events.NewBus()
	az := access.New(st, bus, access.Options{Now: now})
	presence := NewPresence(st, bus, now)
	reg := NewRegistry(Deps{Store: st, Access: az, Bus: bus, Presence: presence, Audit: audit.New(st, now), Now: now})
	return &fixture{st: st, raw: raw, bus: bus, az: az, presence: presence, reg: reg, clock: clock}
}

func (f *fixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	_, err := f.raw.ExecContext(context.Background(), query, args...)
	require.NoError(t, err)
}

func (f *fixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, f.raw.QueryRowContext(context.Background(), query, args...).Scan(&n))
	return n
}

// user creates an account the way the app does: with its intelligence and its personal organization
// (PROF-01, AD-021), which is where its first AIs go (ORG-04). The handle is not the username, so that it
// never takes the handle an AI of the same name gets.
func (f *fixture) user(t *testing.T, name string) authn.Principal {
	t.Helper()
	ctx := context.Background()
	id := store.NewID()
	f.exec(t, "INSERT INTO users (id, username, password_hash, role, created_at) VALUES (?, ?, 'x', 'member', ?)",
		id, name, t0.UnixMilli())
	f.exec(t, "INSERT INTO intelligences (id, kind, display_name, handle, created_at, updated_at) VALUES (?, 'human', ?, ?, ?, ?)",
		id, name, "user-"+name, t0.UnixMilli(), t0.UnixMilli())
	require.NoError(t, f.st.WithTx(ctx, func(q *db.Queries) error {
		_, err := orgs.CreatePersonal(ctx, q, orgs.PersonalParams{UserID: id, Name: name, Username: name, Now: t0})
		return err
	}))
	return authn.Principal{Kind: authn.KindUser, UserID: id, Role: string(dto.RoleMember)}
}

func (f *fixture) grant(t *testing.T, agentID, subject string) {
	t.Helper()
	f.exec(t, "INSERT INTO grants (resource_kind, resource_id, subject, level, created_at) VALUES ('agent', ?, ?, 'use', ?)",
		agentID, subject, t0.UnixMilli())
}

func (f *fixture) room(t *testing.T, owner authn.Principal) string {
	t.Helper()
	id := store.NewID()
	f.exec(t, "INSERT INTO rooms (id, owner_id, name, created_at, updated_at) VALUES (?, ?, 'Support', ?, ?)",
		id, owner.UserID, t0.UnixMilli(), t0.UnixMilli())
	return id
}

// upsert creates the owner's AI of the folder /work/<name> on the machine "devbox" and returns
// its id.
func (f *fixture) upsert(t *testing.T, owner authn.Principal, name string) string {
	t.Helper()
	a, err := f.reg.ResolveFolder(context.Background(), owner.UserID, FolderID{Machine: "devbox", Folder: "/work/" + name}, false)
	require.NoError(t, err)
	return a.ID
}

// fakeSession records how Presence closed it.
type fakeSession struct {
	mu     sync.Mutex
	code   websocket.StatusCode
	reason string
	closed int
}

func (s *fakeSession) Close(code websocket.StatusCode, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.code, s.reason = code, reason
	s.closed++
}

func (s *fakeSession) state() (websocket.StatusCode, string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.code, s.reason, s.closed
}

func drain(ch <-chan events.Event) []events.Event {
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

// AGT-01: names are slugified; an empty slug becomes "agent".
func TestSlug(t *testing.T) {
	pattern := regexp.MustCompile(dto.AgentNamePattern)
	cases := map[string]string{
		"Billing API!":            "billing-api",
		"":                        "agent",
		"!!!":                     "agent",
		"  My Project  ":          "my-project",
		"web.app_v2":              "web.app_v2",
		"-leading-and-trailing-":  "leading-and-trailing",
		"Café Órdenes":            "caf-rdenes",
		strings.Repeat("a", 60):   strings.Repeat("a", 48),
		strings.Repeat("ab ", 30): strings.TrimRight(strings.Repeat("ab-", 16), "-"),
	}
	for in, want := range cases {
		got := Slug(in)
		require.Equal(t, want, got, "Slug(%q)", in)
		require.Regexp(t, pattern, got)
	}
}

// Visible list = owned + use grants (own or everyone).
func TestListVisibleOwnedAndUseGrants(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob, carol := f.user(t, "alice"), f.user(t, "bobby"), f.user(t, "carol")
	own := f.upsert(t, alice, "own")
	shared := f.upsert(t, bob, "shared")
	public := f.upsert(t, carol, "public")
	f.upsert(t, carol, "private")
	f.grant(t, shared, alice.UserID)
	f.grant(t, public, access.Everyone)

	list, err := f.reg.ListVisible(ctx, alice)
	require.NoError(t, err)
	got := map[string]access.Level{}
	for _, a := range list {
		got[a.ID] = a.Level
	}
	require.Equal(t, map[string]access.Level{own: dto.LevelManage, shared: dto.LevelUse, public: dto.LevelUse}, got)

	list, err = f.reg.ListVisible(ctx, bob)
	require.NoError(t, err)
	ids := []string{}
	for _, a := range list {
		ids = append(ids, a.ID)
	}
	require.ElementsMatch(t, []string{shared, public}, ids)
}

// Delete removes memberships, grants and cursors, and only the organization lets someone delete the AI (POS-05): an
// admin of its home. A member who sees it (here the grantee the upgrade made a member) is refused, and a stranger
// does not see it. Every organization the AI belonged to is told its chart changed (POS-06).
func TestDeleteRemovesMembershipsAndCursors(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob, stranger := f.user(t, "alice"), f.user(t, "bobby"), f.user(t, "stranger")
	agent := f.upsert(t, alice, "builder")
	roomA, roomB := f.room(t, alice), f.room(t, alice)
	for _, room := range []string{roomA, roomB} {
		f.exec(t, "INSERT INTO room_members (id, room_id, kind, member_id, added_by, created_at) VALUES (?, ?, 'agent', ?, ?, ?)",
			store.NewID(), room, agent, alice.UserID, t0.UnixMilli())
		f.exec(t, "INSERT INTO agent_cursors (agent_id, room_id, seq) VALUES (?, ?, 7)", agent, room)
	}
	f.grant(t, agent, bob.UserID)
	home := f.joinAs(t, alice, bob, orgs.PermissionMember)

	require.ErrorIs(t, f.reg.Delete(ctx, bob, agent), access.ErrForbidden)
	require.ErrorIs(t, f.reg.Delete(ctx, stranger, agent), access.ErrNotFound)

	session := &fakeSession{}
	_, err := f.presence.Attach(ctx, mustAgent(t, f, agent).Agent, session, "process-one", false)
	require.NoError(t, err)
	ch, cancel := f.bus.Subscribe(16)
	defer cancel()

	require.NoError(t, f.reg.Delete(ctx, alice, agent))
	require.Equal(t, 0, f.count(t, "SELECT COUNT(*) FROM agents WHERE id = ?", agent))
	require.Equal(t, 0, f.count(t, "SELECT COUNT(*) FROM room_members WHERE kind = 'agent' AND member_id = ?", agent))
	require.Equal(t, 0, f.count(t, "SELECT COUNT(*) FROM agent_cursors WHERE agent_id = ?", agent))
	require.Equal(t, 0, f.count(t, "SELECT COUNT(*) FROM grants WHERE resource_kind = 'agent' AND resource_id = ?", agent))
	require.Equal(t, 0, f.count(t, "SELECT COUNT(*) FROM org_members WHERE iid = ?", agent))

	code, reason, closed := session.state()
	require.Equal(t, 1, closed)
	require.Equal(t, CloseDeleted, code)
	require.Equal(t, "deleted", reason)
	require.False(t, f.presence.Online(agent))
	require.ElementsMatch(t, []events.Event{
		events.AgentPresence{AgentID: agent, OwnerID: alice.UserID, Online: false},
		events.MembersChanged{RoomID: roomA},
		events.MembersChanged{RoomID: roomB},
		events.OrgChanged{OrgID: home},
	}, drain(ch))
}

// joinAs makes the user a member of the owner's own organization, as the upgrade does for whoever was granted
// something of the owner (OUPG-01), and returns the organization.
func (f *fixture) joinAs(t *testing.T, owner, user authn.Principal, perm orgs.Permission) string {
	t.Helper()
	ctx := context.Background()
	org, err := f.st.R().GetPersonalOrg(ctx, owner.UserID)
	require.NoError(t, err)
	require.NoError(t, f.st.WithTx(ctx, func(q *db.Queries) error {
		return orgs.AddMember(ctx, q, orgs.AddMemberInput{OrgID: org.ID, IID: user.UserID, Permission: perm, Now: t0})
	}))
	return org.ID
}

func mustAgent(t *testing.T, f *fixture, id string) Agent {
	t.Helper()
	row, err := f.st.R().GetAgentWithOwner(context.Background(), id)
	require.NoError(t, err)
	return Agent{Agent: row.Agent}
}

// Only the owner changes the description; it is limited to 1000 characters.
func TestUpdateDescription(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob := f.user(t, "alice"), f.user(t, "bobby")
	agent := f.upsert(t, alice, "builder")
	f.grant(t, agent, bob.UserID)

	a, err := f.reg.Update(ctx, alice, agent, AgentUpdate{Description: ptr("Builds things")})
	require.NoError(t, err)
	require.Equal(t, "Builds things", a.Description)
	require.Equal(t, dto.LevelManage, a.Level)

	_, err = f.reg.Update(ctx, bob, agent, AgentUpdate{Description: ptr("hijack")})
	require.ErrorIs(t, err, access.ErrForbidden)

	_, err = f.reg.Update(ctx, alice, agent, AgentUpdate{Description: ptr(strings.Repeat("x", 1001))})
	var verr *validation.Error
	require.ErrorAs(t, err, &verr)
	require.Contains(t, verr.Fields, "description")
}

func ptr[T any](v T) *T { return &v }
