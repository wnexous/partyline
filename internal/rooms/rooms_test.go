package rooms

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

var t0 = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

type fixture struct {
	st    *store.Store
	raw   *sql.DB // a separate connection for inserting fixtures and counting rows
	bus   *events.Bus
	az    *access.Authorizer
	svc   *Service
	clock *clock
	dir   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "partyline.db")
	st, err := store.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(context.Background()))
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	c := &clock{now: t0}
	bus := events.NewBus()
	az := access.New(st, access.Options{})
	f := &fixture{st: st, raw: raw, bus: bus, az: az, clock: c, dir: dir}
	f.svc = New(Deps{Store: st, Access: az, Bus: bus, Now: c.Now})
	return f
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

// intelligence gives an account or an AI its profile, the one place a name lives: the display
// name people read and the @handle.
func (f *fixture) intelligence(t *testing.T, id, kind, display, handle string) {
	t.Helper()
	f.exec(t, "INSERT INTO intelligences (id, kind, display_name, handle, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		id, kind, display, handle, t0.UnixMilli(), t0.UnixMilli())
}

// user creates an account, named like its username with an upper-case first letter, and returns
// its principal.
func (f *fixture) user(t *testing.T, name string, role dto.Role) authn.Principal {
	t.Helper()
	id := store.NewID()
	f.exec(t, "INSERT INTO users (id, username, password_hash, role, created_at) VALUES (?, ?, 'x', ?, ?)",
		id, name, string(role), t0.UnixMilli())
	f.intelligence(t, id, "human", strings.ToUpper(name[:1])+name[1:], name)
	// Every account has a personal organization it administers (AD-021), where its groups go.
	require.NoError(t, f.st.WithTx(context.Background(), func(q *db.Queries) error {
		_, err := orgs.CreatePersonal(context.Background(), q, orgs.PersonalParams{UserID: id, Name: name, Username: name, Now: t0})
		return err
	}))
	return authn.Principal{Kind: authn.KindUser, UserID: id, Role: string(role)}
}

// agent creates an AI named name for owner, placed as the app places a new AI: in the owner's personal
// organization, which is its home, as a member the owner created. It returns its id.
func (f *fixture) agent(t *testing.T, owner authn.Principal, name string) string {
	t.Helper()
	id := store.NewID()
	orgID := f.orgOf(t, owner)
	f.exec(t, "INSERT INTO agents (id, owner_id, name, created_at, org_id, creator_iid) VALUES (?, ?, ?, ?, ?, ?)", id, owner.UserID, name, t0.UnixMilli(), orgID, owner.UserID)
	f.intelligence(t, id, "ai", name, "ai-"+name)
	f.exec(t, "INSERT INTO org_members (org_id, iid, permission, joined_at) VALUES (?, ?, 'member', ?)", orgID, id, t0.UnixMilli())
	return id
}

// chat creates a connector owned by owner with one chat and returns (connectorID, chatID).
func (f *fixture) chat(t *testing.T, owner authn.Principal, title string) (string, string) {
	t.Helper()
	conn, chat := store.NewID(), store.NewID()
	f.exec(t, "INSERT INTO connectors (id, owner_id, kind, name, created_at, updated_at) VALUES (?, ?, 'telegram', 'tg', ?, ?)",
		conn, owner.UserID, t0.UnixMilli(), t0.UnixMilli())
	f.exec(t, "INSERT INTO chats (id, connector_id, external_id, title, kind, created_at) VALUES (?, ?, ?, ?, 'group', ?)",
		chat, conn, "ext-"+chat, title, t0.UnixMilli())
	return conn, chat
}

// orgOf is the personal organization of the user (AD-021), where their groups go.
func (f *fixture) orgOf(t *testing.T, p authn.Principal) string {
	t.Helper()
	o, err := f.st.R().GetPersonalOrg(context.Background(), p.UserID)
	require.NoError(t, err)
	return o.ID
}

// join makes the user a member of the organization with the permission.
func (f *fixture) join(t *testing.T, orgID string, p authn.Principal, perm orgs.Permission) {
	t.Helper()
	f.exec(t, "INSERT INTO org_members (org_id, iid, permission, joined_at) VALUES (?, ?, ?, ?)", orgID, p.UserID, string(perm), t0.UnixMilli())
}

// home makes the organization the home of the AI, created by creator, and the AI a member of it with no
// position: its creator manages it (POS-03).
func (f *fixture) home(t *testing.T, orgID, aiID string, creator authn.Principal) {
	t.Helper()
	f.exec(t, "UPDATE agents SET org_id = ?, creator_iid = ? WHERE id = ?", orgID, creator.UserID, aiID)
	f.exec(t, "INSERT OR IGNORE INTO org_members (org_id, iid, permission, joined_at) VALUES (?, ?, 'member', ?)", orgID, aiID, t0.UnixMilli())
}

// colleague creates an account that joins the organization with the permission.
func (f *fixture) colleague(t *testing.T, orgID, name string, perm orgs.Permission) authn.Principal {
	t.Helper()
	p := f.user(t, name, dto.RoleMember)
	f.join(t, orgID, p, perm)
	return p
}

func (f *fixture) room(t *testing.T, owner authn.Principal, name string) Room {
	t.Helper()
	r, err := f.svc.Create(context.Background(), owner, CreateInput{Name: name})
	require.NoError(t, err)
	return r
}

// dm plants the direct message between two users and returns its room id.
func (f *fixture) dm(t *testing.T, a, b authn.Principal) string {
	t.Helper()
	x, y := a.UserID, b.UserID
	if x > y {
		x, y = y, x
	}
	id := store.NewID()
	f.exec(t, "INSERT INTO rooms (id, owner_id, name, kind, dm_a, dm_b, created_at, updated_at) VALUES (?, ?, '', 'dm', ?, ?, ?, ?)",
		id, a.UserID, x, y, t0.UnixMilli(), t0.UnixMilli())
	for _, p := range []authn.Principal{a, b} {
		f.exec(t, "INSERT INTO room_members (id, room_id, kind, member_id, added_by, created_at) VALUES (?, ?, 'user', ?, ?, ?)",
			store.NewID(), id, p.UserID, a.UserID, t0.UnixMilli())
	}
	return id
}

// dmAI plants the direct message between a user and an AI and returns its room id.
func (f *fixture) dmAI(t *testing.T, a authn.Principal, ai string) string {
	t.Helper()
	x, y := a.UserID, ai
	if x > y {
		x, y = y, x
	}
	id := store.NewID()
	f.exec(t, "INSERT INTO rooms (id, owner_id, name, kind, dm_a, dm_b, created_at, updated_at) VALUES (?, ?, '', 'dm', ?, ?, ?, ?)",
		id, a.UserID, x, y, t0.UnixMilli(), t0.UnixMilli())
	f.exec(t, "INSERT INTO room_members (id, room_id, kind, member_id, added_by, created_at) VALUES (?, ?, 'user', ?, ?, ?)", store.NewID(), id, a.UserID, a.UserID, t0.UnixMilli())
	f.exec(t, "INSERT INTO room_members (id, room_id, kind, member_id, added_by, created_at) VALUES (?, ?, 'agent', ?, ?, ?)", store.NewID(), id, ai, a.UserID, t0.UnixMilli())
	return id
}

func ptr[T any](v T) *T { return &v }

// ACL-01: the creator owns the room with manage.
func TestCreateRoomMakesCallerOwner(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	sub, cancel := f.bus.Subscribe(8)
	defer cancel()

	r, err := f.svc.Create(ctx, alice, CreateInput{Name: "  Ops  ", Description: "on call"})
	require.NoError(t, err)
	require.Equal(t, alice.UserID, r.OwnerID)
	require.Equal(t, dto.LevelManage, r.Level)
	require.Equal(t, "Ops", r.Name)
	require.Equal(t, "on call", r.Description)
	require.Equal(t, int64(DefaultRetentionDays), r.RetentionDays)
	require.Equal(t, "alice", r.OwnerUsername)
	require.Equal(t, int64(0), r.LastSeq())
	require.Equal(t, events.RoomChanged{RoomID: r.ID}, <-sub)
	require.Equal(t, f.orgOf(t, alice), r.OrgID.String, "the group belongs to the creator's default organization (ORG-02)")
	require.Equal(t, alice.UserID, r.CreatorIid.String, "and records who created it")

	got, err := f.svc.Get(ctx, alice, r.ID)
	require.NoError(t, err)
	require.Equal(t, dto.LevelManage, got.Level)

	keep, err := f.svc.Create(ctx, alice, CreateInput{Name: "archive", RetentionDays: ptr(0)})
	require.NoError(t, err)
	require.Equal(t, int64(0), keep.RetentionDays, "0 keeps messages forever")
}

func TestCreateRoomValidation(t *testing.T) {
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	cases := []struct {
		in    CreateInput
		field string
	}{
		{CreateInput{Name: "   "}, "name"},
		{CreateInput{Name: strings.Repeat("n", MaxNameLength+1)}, "name"},
		{CreateInput{Name: "ok", Description: strings.Repeat("d", MaxDescriptionLength+1)}, "description"},
		{CreateInput{Name: "ok", RetentionDays: ptr(-1)}, "retention_days"},
	}
	for _, tc := range cases {
		_, err := f.svc.Create(context.Background(), alice, tc.in)
		var verr *validation.Error
		require.ErrorAs(t, err, &verr)
		require.Contains(t, verr.Fields, tc.field)
	}
	require.Zero(t, f.count(t, "SELECT COUNT(*) FROM rooms"))
}

// ACL-03, ACL-08: without view (strangers, admins without membership, missing rooms) every
// read is ErrNotFound.
func TestStrangerAndAdminGetNotFound(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	r := f.room(t, alice, "private")

	for _, p := range []authn.Principal{f.user(t, "stranger", dto.RoleMember), f.user(t, "root", dto.RoleAdmin)} {
		_, err := f.svc.Get(ctx, p, r.ID)
		require.ErrorIs(t, err, access.ErrNotFound)
		_, err = f.svc.Members(ctx, p, r.ID)
		require.ErrorIs(t, err, access.ErrNotFound)
		_, err = f.svc.Update(ctx, p, r.ID, UpdateInput{Name: ptr("x")})
		require.ErrorIs(t, err, access.ErrNotFound)
		require.ErrorIs(t, f.svc.Delete(ctx, p, r.ID), access.ErrNotFound)
		list, err := f.svc.List(ctx, p)
		require.NoError(t, err)
		require.Empty(t, list)
	}
	_, err := f.svc.Get(ctx, alice, store.NewID())
	require.ErrorIs(t, err, access.ErrNotFound)
}

// ACL-02/ACL-04 in the service: a room member posts and reads but cannot manage, guests included; a
// member of the organization who is not in the room cannot even see it.
func TestRoomMemberWithoutManageIsForbidden(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	acme := f.orgOf(t, alice)
	r := f.room(t, alice, "shared")
	_, err := f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: dto.MemberKindAgent, MemberID: f.agent(t, alice, "builder")})
	require.NoError(t, err)

	for _, perm := range []orgs.Permission{orgs.PermissionMember, orgs.PermissionGuest} {
		g := f.colleague(t, acme, "g-"+string(perm), perm)
		_, err := f.svc.Get(ctx, g, r.ID)
		require.ErrorIs(t, err, access.ErrNotFound, "not in the room yet")
		_, err = f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: dto.MemberKindUser, MemberID: g.UserID})
		require.NoError(t, err)

		got, err := f.svc.Get(ctx, g, r.ID)
		require.NoError(t, err)
		require.Equal(t, dto.LevelPost, got.Level)
		members, err := f.svc.Members(ctx, g, r.ID)
		require.NoError(t, err)
		require.NotEmpty(t, members)
		// Taking their own row out is leaving, which a member may do; anyone else's needs manage.
		var notMine string
		for _, m := range members {
			if m.MemberID != g.UserID {
				notMine = m.ID
			}
		}
		require.NotEmpty(t, notMine)

		_, err = f.svc.Update(ctx, g, r.ID, UpdateInput{Name: ptr("x")})
		require.ErrorIs(t, err, access.ErrForbidden)
		require.ErrorIs(t, f.svc.Delete(ctx, g, r.ID), access.ErrForbidden)
		_, err = f.svc.AddMember(ctx, g, r.ID, MemberInput{Kind: dto.MemberKindUser, MemberID: alice.UserID})
		require.ErrorIs(t, err, access.ErrForbidden)
		_, err = f.svc.UpdateMember(ctx, g, r.ID, members[0].ID, MemberUpdate{DisplayName: ptr("x")})
		require.ErrorIs(t, err, access.ErrForbidden)
		require.ErrorIs(t, f.svc.RemoveMember(ctx, g, r.ID, notMine), access.ErrForbidden)
	}
}

// ACL-02, AD-016: List returns the rooms the user can view with the resolved level: the groups they are a
// member of (post), created (manage) or administer (manage).
func TestListVisibleRooms(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	acme := f.orgOf(t, alice)
	bob := f.colleague(t, acme, "bob", orgs.PermissionMember)
	carol := f.colleague(t, acme, "carol", orgs.PermissionMember) // a colleague who is in no room
	dave := f.user(t, "dave", dto.RoleMember)                     // of no organization of alice's
	f.room(t, alice, "a-own")
	shared := f.room(t, alice, "b-shared")
	f.room(t, bob, "d-bob")
	_, err := f.svc.AddMember(ctx, alice, shared.ID, MemberInput{Kind: dto.MemberKindUser, MemberID: bob.UserID})
	require.NoError(t, err)

	type entry struct {
		name  string
		level dto.Level
	}
	listOf := func(p authn.Principal) []entry {
		list, err := f.svc.List(ctx, p)
		require.NoError(t, err)
		out := []entry{}
		for _, r := range list {
			out = append(out, entry{r.Name, r.Level})
		}
		return out
	}
	require.Equal(t, []entry{{"a-own", dto.LevelManage}, {"b-shared", dto.LevelManage}}, listOf(alice),
		"alice created two groups; bob's group is in bob's own organization, which alice does not belong to")
	require.Equal(t, []entry{{"b-shared", dto.LevelPost}, {"d-bob", dto.LevelManage}}, listOf(bob), "a member of one group and the creator of another")
	require.Empty(t, listOf(carol), "a colleague who is in no room lists nothing")
	require.Empty(t, listOf(dave), "a user of no organization of alice's lists nothing")
}

// ACL-07, AD-016: an admin of the organization (not only the creator) may change settings; the creator stays the
// creator.
func TestAdminOfTheOrganizationUpdatesRoom(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.colleague(t, f.orgOf(t, alice), "bob", orgs.PermissionAdmin)
	r := f.room(t, alice, "ops")
	sub, cancel := f.bus.Subscribe(8)
	defer cancel()

	got, err := f.svc.Update(ctx, bob, r.ID, UpdateInput{Name: ptr("ops-2"), Description: ptr("d"), RetentionDays: ptr(7)})
	require.NoError(t, err)
	require.Equal(t, "ops-2", got.Name)
	require.Equal(t, "d", got.Description)
	require.Equal(t, int64(7), got.RetentionDays)
	require.Equal(t, alice.UserID, got.OwnerID)
	require.Equal(t, alice.UserID, got.CreatorIid.String)
	require.Equal(t, events.RoomChanged{RoomID: r.ID}, <-sub)

	_, err = f.svc.Update(ctx, bob, r.ID, UpdateInput{Name: ptr("")})
	var verr *validation.Error
	require.ErrorAs(t, err, &verr)
	require.Contains(t, verr.Fields, "name")
}

// ACL-05 replaced (AD-016, ORG-03): adding an agent needs an AI the caller can see - one they host or one
// of an organization they belong to - that belongs to the group's organization. An AI they cannot see, or
// that does not exist, is ErrForbidden; one they can see that is not of the organization is a
// *MembersRefusedError; the caller's own AI is addable whoever created the group.
func TestAddAgentNeedsAnAIOfTheGroupsOrganization(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.user(t, "bob", dto.RoleMember)
	acme := f.orgOf(t, alice)
	mine := f.agent(t, alice, "builder")
	bobs := f.agent(t, bob, "reviewer")
	r := f.room(t, alice, "ops")
	sub, cancel := f.bus.Subscribe(8)
	defer cancel()

	m, err := f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: dto.MemberKindAgent, MemberID: mine})
	require.NoError(t, err)
	require.Equal(t, dto.MemberKindAgent, dto.MemberKind(m.Kind))
	require.Equal(t, mine, m.MemberID)
	require.Equal(t, "builder", m.Name)
	require.Equal(t, "alice", m.AgentOwner)
	require.Equal(t, events.MembersChanged{RoomID: r.ID}, <-sub)

	_, err = f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: dto.MemberKindAgent, MemberID: bobs})
	require.ErrorIs(t, err, access.ErrForbidden, "bob's AI is not one alice can see")
	_, err = f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: dto.MemberKindAgent, MemberID: store.NewID()})
	require.ErrorIs(t, err, access.ErrForbidden, "and an AI that does not exist is the same answer")

	// Once bob's AI joins alice's organization she sees it, and it is of the group's organization.
	f.exec(t, "INSERT INTO org_members (org_id, iid, permission, joined_at) VALUES (?, ?, 'member', ?)", acme, bobs, t0.UnixMilli())
	m, err = f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: dto.MemberKindAgent, MemberID: bobs})
	require.NoError(t, err)
	require.Equal(t, "bob", m.AgentOwner)

	// An AI alice hosts that belongs to another organization is refused, with the reason.
	elsewhere := f.agent(t, alice, "wanderer")
	f.exec(t, "DELETE FROM org_members WHERE org_id = ? AND iid = ?", acme, elsewhere)
	_, err = f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: dto.MemberKindAgent, MemberID: elsewhere})
	var refused *MembersRefusedError
	require.ErrorAs(t, err, &refused)
	require.Equal(t, []dto.RefusedMember{{ID: elsewhere, Reason: dto.RefusedReasonNotOrgMember}}, refused.Refused)

	// The check is on the caller, not on the room's creator: an admin adds their own AI of the organization.
	carol := f.colleague(t, acme, "carol", orgs.PermissionAdmin)
	carols := f.agent(t, carol, "c")
	f.exec(t, "INSERT INTO org_members (org_id, iid, permission, joined_at) VALUES (?, ?, 'member', ?)", acme, carols, t0.UnixMilli())
	_, err = f.svc.AddMember(ctx, carol, r.ID, MemberInput{Kind: dto.MemberKindAgent, MemberID: carols})
	require.NoError(t, err)
}

// CID-19 (L-018: each case isolates its guard): a chat is never a member, whoever owns its
// connection, whether it exists or not, whatever the member's settings say: AddMember answers
// ErrChatsNotMembers for a manager of the room, adds nothing and publishes nothing. The caller's
// rights on the room are checked first: a user who cannot see the room gets not found, a user who
// can see but not manage it forbidden, and a direct message answers that its members are fixed.
// Group creation refuses a chat too, and creates no room. The chat branch of ACL-06 is retired with it.
func TestChatsAreNeverMembers(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.user(t, "bob", dto.RoleMember)
	carol := f.user(t, "carol", dto.RoleMember)
	_, mine := f.chat(t, alice, "family")
	_, bobs := f.chat(t, bob, "team")
	r := f.room(t, alice, "ops")
	sub, cancel := f.bus.Subscribe(8)
	defer cancel()

	for name, in := range map[string]MemberInput{
		"a chat of the caller's connection":   {Kind: dto.MemberKindChat, MemberID: mine},
		"a chat of another user's connection": {Kind: dto.MemberKindChat, MemberID: bobs},
		"a chat that does not exist":          {Kind: dto.MemberKindChat, MemberID: store.NewID()},
		"a chat with no member id at all":     {Kind: dto.MemberKindChat},
	} {
		_, err := f.svc.AddMember(ctx, alice, r.ID, in)
		require.ErrorIs(t, err, ErrChatsNotMembers, name)
	}
	members, err := f.svc.Members(ctx, alice, r.ID)
	require.NoError(t, err)
	require.Empty(t, members, "nothing was added")
	require.Empty(t, sub, "and nothing was published")

	// The rights on the room come first.
	_, err = f.svc.AddMember(ctx, carol, r.ID, MemberInput{Kind: dto.MemberKindChat, MemberID: mine})
	require.ErrorIs(t, err, access.ErrNotFound, "a user who cannot see the room")
	f.join(t, f.orgOf(t, alice), carol, orgs.PermissionMember)
	_, err = f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: dto.MemberKindUser, MemberID: carol.UserID})
	require.NoError(t, err)
	_, err = f.svc.AddMember(ctx, carol, r.ID, MemberInput{Kind: dto.MemberKindChat, MemberID: mine})
	require.ErrorIs(t, err, access.ErrForbidden, "a user who can post in the room but not manage it")
	dm := f.dm(t, alice, bob)
	_, err = f.svc.AddMember(ctx, alice, dm, MemberInput{Kind: dto.MemberKindChat, MemberID: mine})
	require.ErrorIs(t, err, ErrDMMembersFixed, "the members of a direct message are fixed whatever the kind")

	// The other kinds still join (a member of the organization), and a chat is as refused next to them.
	f.join(t, f.orgOf(t, alice), bob, orgs.PermissionMember)
	_, err = f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: dto.MemberKindUser, MemberID: bob.UserID})
	require.NoError(t, err)

	// Group creation: a chat among the members refuses the whole group.
	rooms := f.count(t, "SELECT COUNT(*) FROM rooms")
	_, err = f.svc.CreateWithMembers(ctx, alice, CreateInput{Name: "team"}, []MemberInput{
		{Kind: dto.MemberKindUser, MemberID: bob.UserID}, {Kind: dto.MemberKindChat, MemberID: mine},
	})
	require.ErrorIs(t, err, ErrChatsNotMembers)
	require.Equal(t, rooms, f.count(t, "SELECT COUNT(*) FROM rooms"), "no room was created")
	require.Zero(t, f.count(t, "SELECT COUNT(*) FROM rooms WHERE name = 'team'"))
	reason, err := f.svc.CheckMember(ctx, alice, f.orgOf(t, alice), MemberInput{Kind: dto.MemberKindChat, MemberID: mine})
	require.ErrorIs(t, err, ErrChatsNotMembers)
	require.Empty(t, reason)
}

func TestAddMemberValidationAndDuplicates(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.user(t, "bob", dto.RoleMember)
	r := f.room(t, alice, "ops")

	// A user who is not a member of the group's organization is refused, with the reason (ORG-03).
	_, err := f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: dto.MemberKindUser, MemberID: bob.UserID})
	var refused *MembersRefusedError
	require.ErrorAs(t, err, &refused)
	require.Equal(t, []dto.RefusedMember{{ID: bob.UserID, Reason: dto.RefusedReasonNotOrgMember}}, refused.Refused)
	f.join(t, f.orgOf(t, alice), bob, orgs.PermissionMember)

	m, err := f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: dto.MemberKindUser, MemberID: bob.UserID})
	require.NoError(t, err)
	require.Equal(t, "Bob", m.Name)
	_, err = f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: dto.MemberKindUser, MemberID: bob.UserID})
	require.ErrorIs(t, err, ErrAlreadyMember)

	var verr *validation.Error
	_, err = f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: "robot", MemberID: bob.UserID})
	require.ErrorAs(t, err, &verr)
	require.Contains(t, verr.Fields, "kind")
	_, err = f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: dto.MemberKindUser, MemberID: store.NewID()})
	require.ErrorAs(t, err, &verr)
	require.Contains(t, verr.Fields, "member_id")
}

// ROOM-01: removed members leave the member list that delivery plans from.
func TestUpdateAndRemoveMember(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	agent := f.agent(t, alice, "builder")
	r := f.room(t, alice, "ops")
	other := f.room(t, alice, "other")
	m := f.addMember(t, alice, r.ID, dto.MemberKindAgent, agent)
	var err error

	m, err = f.svc.UpdateMember(ctx, alice, r.ID, m.ID, MemberUpdate{DisplayName: ptr("Builder")})
	require.NoError(t, err)
	require.Equal(t, "Builder", m.DisplayName)
	_, err = f.svc.UpdateMember(ctx, alice, other.ID, m.ID, MemberUpdate{DisplayName: ptr("Elsewhere")})
	require.ErrorIs(t, err, access.ErrNotFound, "member ids are scoped to their room")

	require.ErrorIs(t, f.svc.RemoveMember(ctx, alice, other.ID, m.ID), access.ErrNotFound)
	require.NoError(t, f.svc.RemoveMember(ctx, alice, r.ID, m.ID))
	members, err := f.svc.Members(ctx, alice, r.ID)
	require.NoError(t, err)
	require.Empty(t, members)
	require.ErrorIs(t, f.svc.RemoveMember(ctx, alice, r.ID, m.ID), access.ErrNotFound)
}

// ROOM-02: deleting a room deletes its messages, attachments, cursors and members, and nothing of
// other rooms.
func TestDeleteRoomRemovesEverything(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	agent := f.agent(t, alice, "builder")
	_, chat := f.chat(t, alice, "family")
	doomed := f.room(t, alice, "doomed")
	kept := f.room(t, alice, "kept")
	for _, r := range []Room{doomed, kept} {
		f.seedChatMember(t, alice, r.ID, chat)
		msg := store.NewID()
		f.exec(t, "INSERT INTO messages (id, room_id, seq, author_kind, author_id, text, created_at) VALUES (?, ?, 1, 'user', ?, 'hi', ?)", msg, r.ID, alice.UserID, t0.UnixMilli())
		f.exec(t, "INSERT INTO attachments (id, message_id, sha256, mime, name, size, created_at) VALUES (?, ?, 'abc', 'text/plain', 'a.txt', 3, ?)", store.NewID(), msg, t0.UnixMilli())
		f.exec(t, "INSERT INTO agent_cursors (agent_id, room_id, seq) VALUES (?, ?, 1)", agent, r.ID)
	}
	sub, cancel := f.bus.Subscribe(8)
	defer cancel()

	require.NoError(t, f.svc.Delete(ctx, alice, doomed.ID))
	require.Equal(t, events.RoomChanged{RoomID: doomed.ID, Deleted: true}, <-sub)

	countFor := func(roomID string) map[string]int {
		return map[string]int{
			"rooms":       f.count(t, "SELECT COUNT(*) FROM rooms WHERE id = ?", roomID),
			"members":     f.count(t, "SELECT COUNT(*) FROM room_members WHERE room_id = ?", roomID),
			"messages":    f.count(t, "SELECT COUNT(*) FROM messages WHERE room_id = ?", roomID),
			"attachments": f.count(t, "SELECT COUNT(*) FROM attachments a JOIN messages m ON m.id = a.message_id WHERE m.room_id = ?", roomID),
			"cursors":     f.count(t, "SELECT COUNT(*) FROM agent_cursors WHERE room_id = ?", roomID),
		}
	}
	require.Equal(t, map[string]int{"rooms": 0, "members": 0, "messages": 0, "attachments": 0, "cursors": 0}, countFor(doomed.ID))
	require.Equal(t, map[string]int{"rooms": 1, "members": 1, "messages": 1, "attachments": 1, "cursors": 1}, countFor(kept.ID))
	// Orphaned rows would hide behind the joins above: check the totals too.
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM attachments"))

	_, err := f.svc.Get(ctx, alice, doomed.ID)
	require.ErrorIs(t, err, access.ErrNotFound)
}
