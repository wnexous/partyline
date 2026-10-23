package access

import (
	"context"
	"database/sql"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

var t0 = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

type fixture struct {
	st  *store.Store
	bus *events.Bus
	az  *Authorizer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "partyline.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(context.Background()))
	bus := events.NewBus()
	return &fixture{st: st, bus: bus, az: New(st, bus, Options{Now: func() time.Time { return t0 }})}
}

func (f *fixture) exec(t *testing.T, fn func(q *db.Queries) error) {
	t.Helper()
	require.NoError(t, f.st.WithTx(context.Background(), fn))
}

func (f *fixture) user(t *testing.T, name string, role dto.Role) string {
	t.Helper()
	id := store.NewID()
	f.exec(t, func(q *db.Queries) error {
		return q.CreateUser(context.Background(), db.CreateUserParams{ID: id, Username: name, PasswordHash: "x", Role: string(role), CreatedAt: t0.UnixMilli()})
	})
	return id
}

func (f *fixture) disable(t *testing.T, userID string, disabled bool) {
	t.Helper()
	f.exec(t, func(q *db.Queries) error {
		return q.SetUserDisabledAt(context.Background(), db.SetUserDisabledAtParams{
			DisabledAt: sql.NullInt64{Int64: t0.UnixMilli(), Valid: disabled}, ID: userID,
		})
	})
}

func (f *fixture) room(t *testing.T, ownerID string) string {
	t.Helper()
	id := store.NewID()
	f.exec(t, func(q *db.Queries) error {
		return q.CreateRoom(context.Background(), db.CreateRoomParams{ID: id, OwnerID: ownerID, Name: "room", RetentionDays: 90, CreatedAt: t0.UnixMilli(), UpdatedAt: t0.UnixMilli()})
	})
	return id
}

func (f *fixture) agent(t *testing.T, ownerID string) string {
	t.Helper()
	id := store.NewID()
	f.exec(t, func(q *db.Queries) error {
		return q.CreateAgent(context.Background(), db.CreateAgentParams{ID: id, OwnerID: ownerID, Name: "agent-" + id[len(id)-6:], CreatedAt: t0.UnixMilli()})
	})
	return id
}

func (f *fixture) connector(t *testing.T, ownerID string) string {
	t.Helper()
	id := store.NewID()
	f.exec(t, func(q *db.Queries) error {
		return q.CreateConnector(context.Background(), db.CreateConnectorParams{ID: id, OwnerID: ownerID, Kind: "webhook", Name: "hook", ConfigJson: "{}", Enabled: true, CreatedAt: t0.UnixMilli(), UpdatedAt: t0.UnixMilli()})
	})
	return id
}

func (f *fixture) grant(t *testing.T, kind Kind, id, subject string, level Level) {
	t.Helper()
	f.exec(t, func(q *db.Queries) error {
		return q.CreateGrant(context.Background(), db.CreateGrantParams{ResourceKind: string(kind), ResourceID: id, Subject: subject, Level: string(level), CreatedAt: t0.UnixMilli()})
	})
}

func (f *fixture) addAgentMember(t *testing.T, roomID, agentID string) {
	t.Helper()
	f.exec(t, func(q *db.Queries) error {
		return q.CreateRoomMember(context.Background(), db.CreateRoomMemberParams{
			ID: store.NewID(), RoomID: roomID, Kind: string(dto.MemberKindAgent), MemberID: agentID, CreatedAt: t0.UnixMilli(),
		})
	})
}

func user(id string) authn.Principal { return authn.Principal{Kind: authn.KindUser, UserID: id} }

// grantRows returns the stored grants of a resource as "subject=level" strings, sorted.
func (f *fixture) grantRows(t *testing.T, kind Kind, id string) []string {
	t.Helper()
	rows, err := f.st.R().ListGrantsWithUsers(context.Background(), db.ListGrantsWithUsersParams{ResourceKind: string(kind), ResourceID: id})
	require.NoError(t, err)
	out := []string{}
	for _, r := range rows {
		out = append(out, r.Subject+"="+r.Level)
	}
	sort.Strings(out)
	return out
}

// ACL-02: a user's room level is the highest of ownership, their grant and the everyone grant.
func TestRoomLevelIsHighestOfOwnershipAndGrants(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	owner := f.user(t, "owner", dto.RoleMember)
	u := f.user(t, "user", dto.RoleMember)

	cases := []struct {
		name     string
		mine     Level // user's own grant
		everyone Level // the "*" grant
		want     Level
	}{
		{"no grants", None, None, None},
		{"own view", dto.LevelView, None, dto.LevelView},
		{"own post", dto.LevelPost, None, dto.LevelPost},
		{"own manage", dto.LevelManage, None, dto.LevelManage},
		{"everyone view", None, dto.LevelView, dto.LevelView},
		{"everyone post", None, dto.LevelPost, dto.LevelPost},
		{"own view, everyone post", dto.LevelView, dto.LevelPost, dto.LevelPost},
		{"own post, everyone view", dto.LevelPost, dto.LevelView, dto.LevelPost},
		{"own manage, everyone post", dto.LevelManage, dto.LevelPost, dto.LevelManage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			room := f.room(t, owner)
			if tc.mine != None {
				f.grant(t, KindRoom, room, u, tc.mine)
			}
			if tc.everyone != None {
				f.grant(t, KindRoom, room, Everyone, tc.everyone)
			}
			got, err := f.az.RoomLevel(ctx, user(u), room)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)

			own, err := f.az.RoomLevel(ctx, user(owner), room)
			require.NoError(t, err)
			require.Equal(t, dto.LevelManage, own, "the owner always manages")
		})
	}

	t.Run("missing room", func(t *testing.T) {
		got, err := f.az.RoomLevel(ctx, user(owner), store.NewID())
		require.NoError(t, err)
		require.Equal(t, None, got)
	})
}

// ACL-02: the level order is view < post < manage.
func TestLevelOrder(t *testing.T) {
	require.True(t, AtLeast(dto.LevelManage, dto.LevelPost))
	require.True(t, AtLeast(dto.LevelPost, dto.LevelView))
	require.True(t, AtLeast(dto.LevelPost, dto.LevelPost))
	require.False(t, AtLeast(dto.LevelView, dto.LevelPost))
	require.False(t, AtLeast(dto.LevelPost, dto.LevelManage))
	require.False(t, AtLeast(None, dto.LevelView))
	require.True(t, AtLeast(dto.LevelManage, dto.LevelUse))
	require.False(t, AtLeast(None, dto.LevelUse))
}

// ACL-08: instance admins get no implicit access to rooms, agents or connectors.
func TestAdminWithoutGrantHasNoAccess(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	owner := f.user(t, "owner", dto.RoleMember)
	admin := f.user(t, "admin", dto.RoleAdmin)
	room, agent, conn := f.room(t, owner), f.agent(t, owner), f.connector(t, owner)

	l, err := f.az.RoomLevel(ctx, authn.Principal{Kind: authn.KindUser, UserID: admin, Role: string(dto.RoleAdmin)}, room)
	require.NoError(t, err)
	require.Equal(t, None, l)
	l, err = f.az.AgentLevel(ctx, admin, agent)
	require.NoError(t, err)
	require.Equal(t, None, l)
	l, err = f.az.ConnectorLevel(ctx, admin, conn)
	require.NoError(t, err)
	require.Equal(t, None, l)
	ok, err := f.az.CanUseAgent(ctx, admin, agent)
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = f.az.CanUseConnector(ctx, admin, conn)
	require.NoError(t, err)
	require.False(t, ok)
	levels, err := f.az.GrantedRoomLevels(ctx, admin)
	require.NoError(t, err)
	require.Empty(t, levels)

	// A grant gives an admin exactly what it gives anyone else.
	f.grant(t, KindRoom, room, admin, dto.LevelView)
	l, err = f.az.RoomLevel(ctx, user(admin), room)
	require.NoError(t, err)
	require.Equal(t, dto.LevelView, l)
}

// AUTH-10 edge case: a grant to a disabled user is kept but resolves to no access.
func TestDisabledGranteeResolvesToNone(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	owner := f.user(t, "owner", dto.RoleMember)
	u := f.user(t, "user", dto.RoleMember)
	room, agent, conn := f.room(t, owner), f.agent(t, owner), f.connector(t, owner)
	f.grant(t, KindRoom, room, u, dto.LevelManage)
	f.grant(t, KindRoom, room, Everyone, dto.LevelPost)
	f.grant(t, KindAgent, agent, u, dto.LevelUse)
	f.grant(t, KindConnector, conn, u, dto.LevelUse)

	f.disable(t, u, true)
	l, err := f.az.RoomLevel(ctx, user(u), room)
	require.NoError(t, err)
	require.Equal(t, None, l)
	levels, err := f.az.GrantedRoomLevels(ctx, u)
	require.NoError(t, err)
	require.Empty(t, levels)
	ok, err := f.az.CanUseAgent(ctx, u, agent)
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = f.az.CanUseConnector(ctx, u, conn)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, []string{"*=post", u + "=manage"}, f.grantRows(t, KindRoom, room), "grants are kept")

	f.disable(t, owner, true)
	l, err = f.az.RoomLevel(ctx, user(owner), room)
	require.NoError(t, err)
	require.Equal(t, None, l, "a disabled owner has no access either")

	f.disable(t, u, false)
	l, err = f.az.RoomLevel(ctx, user(u), room)
	require.NoError(t, err)
	require.Equal(t, dto.LevelManage, l, "re-enabling restores the grant")
}

// Design (access.RoomLevel): an agent principal posts in rooms where it is a member, else has no access.
func TestAgentRoomLevel(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	owner := f.user(t, "owner", dto.RoleMember)
	member, other := f.agent(t, owner), f.agent(t, owner)
	room := f.room(t, owner)
	f.addAgentMember(t, room, member)

	agent := func(id string) authn.Principal {
		return authn.Principal{Kind: authn.KindAgent, UserID: owner, AgentID: id}
	}
	l, err := f.az.RoomLevel(ctx, agent(member), room)
	require.NoError(t, err)
	require.Equal(t, dto.LevelPost, l)
	l, err = f.az.RoomLevel(ctx, agent(other), room)
	require.NoError(t, err)
	require.Equal(t, None, l, "the owner's other agents get nothing from ownership")
	l, err = f.az.RoomLevel(ctx, agent(""), room)
	require.NoError(t, err)
	require.Equal(t, None, l)
}

// ACL-05, ACL-06: agents and connectors are usable by their owner (manage) and by use grantees.
func TestUseLevelsOnAgentsAndConnectors(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	owner := f.user(t, "owner", dto.RoleMember)
	grantee := f.user(t, "grantee", dto.RoleMember)
	stranger := f.user(t, "stranger", dto.RoleMember)
	agent, conn := f.agent(t, owner), f.connector(t, owner)
	publicAgent, publicConn := f.agent(t, owner), f.connector(t, owner)
	f.grant(t, KindAgent, agent, grantee, dto.LevelUse)
	f.grant(t, KindConnector, conn, grantee, dto.LevelUse)
	f.grant(t, KindAgent, publicAgent, Everyone, dto.LevelUse)
	f.grant(t, KindConnector, publicConn, Everyone, dto.LevelUse)

	cases := []struct {
		user, agent, conn string
		want              Level
	}{
		{owner, agent, conn, dto.LevelManage},
		{grantee, agent, conn, dto.LevelUse},
		{stranger, agent, conn, None},
		{stranger, publicAgent, publicConn, dto.LevelUse},
		{grantee, store.NewID(), store.NewID(), None},
	}
	for _, tc := range cases {
		l, err := f.az.AgentLevel(ctx, tc.user, tc.agent)
		require.NoError(t, err)
		require.Equal(t, tc.want, l)
		ok, err := f.az.CanUseAgent(ctx, tc.user, tc.agent)
		require.NoError(t, err)
		require.Equal(t, tc.want != None, ok)

		l, err = f.az.ConnectorLevel(ctx, tc.user, tc.conn)
		require.NoError(t, err)
		require.Equal(t, tc.want, l)
		ok, err = f.az.CanUseConnector(ctx, tc.user, tc.conn)
		require.NoError(t, err)
		require.Equal(t, tc.want != None, ok)
	}
}

// ACL-07: each visibility preset replaces the room's grants: none; the listed users at their
// level; everyone at view or post.
func TestSetVisibilityReplacesGrants(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	owner := f.user(t, "owner", dto.RoleMember)
	b := f.user(t, "bob", dto.RoleMember)
	c := f.user(t, "carol", dto.RoleMember)
	d := f.user(t, "dave", dto.RoleMember)
	room := f.room(t, owner)
	sub, cancel := f.bus.Subscribe(16)
	defer cancel()

	levelOf := func(id string) Level {
		l, err := f.az.RoomLevel(ctx, user(id), room)
		require.NoError(t, err)
		return l
	}
	set := func(v Visibility) {
		require.NoError(t, f.az.SetVisibility(ctx, owner, KindRoom, room, owner, v))
		require.Equal(t, events.GrantsChanged{ResourceKind: "room", ResourceID: room}, <-sub)
	}

	set(Visibility{Mode: dto.VisibilityShared, Users: []UserLevel{{UserID: b, Level: dto.LevelView}, {UserID: c, Level: dto.LevelPost}, {UserID: owner, Level: dto.LevelView}}})
	require.ElementsMatch(t, []string{b + "=view", c + "=post"}, f.grantRows(t, KindRoom, room), "owner entry is skipped")
	require.Equal(t, []Level{dto.LevelView, dto.LevelPost, None, dto.LevelManage}, []Level{levelOf(b), levelOf(c), levelOf(d), levelOf(owner)})
	v, err := f.az.Sharing(ctx, KindRoom, room)
	require.NoError(t, err)
	require.Equal(t, dto.VisibilityShared, v.Mode)
	require.Equal(t, []UserLevel{
		{UserID: b, Level: dto.LevelView, Username: "bob"},
		{UserID: c, Level: dto.LevelPost, Username: "carol"},
	}, v.Users)

	set(Visibility{Mode: dto.VisibilityPublic, PublicLevel: dto.LevelPost})
	require.Equal(t, []string{"*=post"}, f.grantRows(t, KindRoom, room))
	require.Equal(t, []Level{dto.LevelPost, dto.LevelPost, dto.LevelPost}, []Level{levelOf(b), levelOf(c), levelOf(d)})
	v, err = f.az.Sharing(ctx, KindRoom, room)
	require.NoError(t, err)
	require.Equal(t, Visibility{Mode: dto.VisibilityPublic, PublicLevel: dto.LevelPost, Users: []UserLevel{}}, v)

	set(Visibility{Mode: dto.VisibilityPublic, PublicLevel: dto.LevelView})
	require.Equal(t, []string{"*=view"}, f.grantRows(t, KindRoom, room))

	set(Visibility{Mode: dto.VisibilityShared, Users: []UserLevel{{UserID: d, Level: dto.LevelManage}}})
	require.Equal(t, []string{d + "=manage"}, f.grantRows(t, KindRoom, room))
	require.Equal(t, []Level{None, None, dto.LevelManage}, []Level{levelOf(b), levelOf(c), levelOf(d)})

	set(Visibility{Mode: dto.VisibilityPrivate, Users: []UserLevel{{UserID: b, Level: dto.LevelView}}})
	require.Empty(t, f.grantRows(t, KindRoom, room), "private removes every grant, even when users are sent")
	require.Equal(t, []Level{None, None, None, dto.LevelManage}, []Level{levelOf(b), levelOf(c), levelOf(d), levelOf(owner)})
	v, err = f.az.Sharing(ctx, KindRoom, room)
	require.NoError(t, err)
	require.Equal(t, Visibility{Mode: dto.VisibilityPrivate, Users: []UserLevel{}}, v)
}

// ACL-07 for agents and connectors: shared and public grant use.
func TestSetVisibilityOnAgentsAndConnectors(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	owner := f.user(t, "owner", dto.RoleMember)
	b := f.user(t, "bob", dto.RoleMember)
	agent, conn := f.agent(t, owner), f.connector(t, owner)

	require.NoError(t, f.az.SetVisibility(ctx, owner, KindAgent, agent, owner, Visibility{Mode: dto.VisibilityShared, Users: []UserLevel{{UserID: b, Level: dto.LevelUse}}}))
	require.Equal(t, []string{b + "=use"}, f.grantRows(t, KindAgent, agent))
	require.NoError(t, f.az.SetVisibility(ctx, owner, KindConnector, conn, owner, Visibility{Mode: dto.VisibilityPublic}))
	require.Equal(t, []string{"*=use"}, f.grantRows(t, KindConnector, conn), "public defaults to use")
}

// ACL-07: invalid presets are rejected with field errors and leave the grants unchanged.
func TestSetVisibilityValidation(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	owner := f.user(t, "owner", dto.RoleMember)
	b := f.user(t, "bob", dto.RoleMember)
	room, agent := f.room(t, owner), f.agent(t, owner)
	require.NoError(t, f.az.SetVisibility(ctx, owner, KindRoom, room, owner, Visibility{Mode: dto.VisibilityShared, Users: []UserLevel{{UserID: b, Level: dto.LevelView}}}))

	cases := []struct {
		name  string
		kind  Kind
		id    string
		v     Visibility
		field string
	}{
		{"unknown mode", KindRoom, room, Visibility{Mode: "secret"}, "mode"},
		{"public manage", KindRoom, room, Visibility{Mode: dto.VisibilityPublic, PublicLevel: dto.LevelManage}, "public_level"},
		{"public without level", KindRoom, room, Visibility{Mode: dto.VisibilityPublic}, "public_level"},
		{"public use on room", KindRoom, room, Visibility{Mode: dto.VisibilityPublic, PublicLevel: dto.LevelUse}, "public_level"},
		{"shared without users", KindRoom, room, Visibility{Mode: dto.VisibilityShared}, "users"},
		{"shared unknown user", KindRoom, room, Visibility{Mode: dto.VisibilityShared, Users: []UserLevel{{UserID: store.NewID(), Level: dto.LevelView}}}, "users"},
		{"shared bad level", KindRoom, room, Visibility{Mode: dto.VisibilityShared, Users: []UserLevel{{UserID: b, Level: "admin"}}}, "users"},
		{"shared duplicate", KindRoom, room, Visibility{Mode: dto.VisibilityShared, Users: []UserLevel{{UserID: b, Level: dto.LevelView}, {UserID: b, Level: dto.LevelPost}}}, "users"},
		{"agent view level", KindAgent, agent, Visibility{Mode: dto.VisibilityShared, Users: []UserLevel{{UserID: b, Level: dto.LevelView}}}, "users"},
		{"agent public post", KindAgent, agent, Visibility{Mode: dto.VisibilityPublic, PublicLevel: dto.LevelPost}, "public_level"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := f.grantRows(t, tc.kind, tc.id)
			err := f.az.SetVisibility(ctx, owner, tc.kind, tc.id, owner, tc.v)
			var verr *validation.Error
			require.ErrorAs(t, err, &verr)
			require.Contains(t, verr.Fields, tc.field)
			require.Equal(t, before, f.grantRows(t, tc.kind, tc.id))
		})
	}
}

// intelligence gives an existing user or agent its intelligence row, which a DM's participant
// columns reference.
func (f *fixture) intelligence(t *testing.T, id string, kind string) {
	t.Helper()
	f.exec(t, func(q *db.Queries) error {
		return q.CreateIntelligence(context.Background(), db.CreateIntelligenceParams{
			ID: id, Kind: kind, DisplayName: "n-" + id[len(id)-6:], Handle: "h-" + id[len(id)-8:],
			CreatedAt: t0.UnixMilli(), UpdatedAt: t0.UnixMilli(),
		})
	})
}

// human is a user with an intelligence; ai an agent with one.
func (f *fixture) human(t *testing.T, name string, role dto.Role) string {
	t.Helper()
	id := f.user(t, name, role)
	f.intelligence(t, id, "human")
	return id
}

func (f *fixture) ai(t *testing.T, ownerID string) string {
	t.Helper()
	id := f.agent(t, ownerID)
	f.intelligence(t, id, "ai")
	return id
}

// dm stores a direct message between the intelligences a and b, created by ownerID, with the
// participants as members (as opening a DM does).
func (f *fixture) dm(t *testing.T, ownerID, a, b string) string {
	t.Helper()
	id := f.dmWithoutMembers(t, ownerID, a, b)
	for _, p := range []string{a, b} {
		kind := dto.MemberKindUser
		if _, err := f.st.R().GetAgent(context.Background(), p); err == nil {
			kind = dto.MemberKindAgent
		}
		f.exec(t, func(q *db.Queries) error {
			return q.CreateRoomMember(context.Background(), db.CreateRoomMemberParams{
				ID: store.NewID(), RoomID: id, Kind: string(kind), MemberID: p, CreatedAt: t0.UnixMilli(),
			})
		})
	}
	return id
}

// dmWithoutMembers stores only the room row, whose participant columns are all a DM's access
// rule may read.
func (f *fixture) dmWithoutMembers(t *testing.T, ownerID, a, b string) string {
	t.Helper()
	if a > b {
		a, b = b, a
	}
	id := store.NewID()
	f.exec(t, func(q *db.Queries) error {
		return q.CreateDMRoom(context.Background(), db.CreateDMRoomParams{
			ID: id, OwnerID: ownerID, DmA: sql.NullString{String: a, Valid: true}, DmB: sql.NullString{String: b, Valid: true},
			CreatedAt: t0.UnixMilli(), UpdatedAt: t0.UnixMilli(),
		})
	})
	return id
}

func (f *fixture) addUserMember(t *testing.T, roomID, userID string) {
	t.Helper()
	f.exec(t, func(q *db.Queries) error {
		return q.CreateRoomMember(context.Background(), db.CreateRoomMemberParams{
			ID: store.NewID(), RoomID: roomID, Kind: string(dto.MemberKindUser), MemberID: userID, CreatedAt: t0.UnixMilli(),
		})
	})
}

func (f *fixture) levelOf(t *testing.T, userID, roomID string) Level {
	t.Helper()
	l, err := f.az.RoomLevel(context.Background(), user(userID), roomID)
	require.NoError(t, err)
	return l
}

// DM-08, AD-011: a DM's access comes from its two participants: a participant posts; the owner
// of an AI participant and users granted use on it only view; everybody else, instance admins and
// holders of a room grant included, has none.
func TestDMRoomLevelMatrix(t *testing.T) {
	f := newFixture(t)
	alice := f.human(t, "alice", dto.RoleMember) // the participant
	bob := f.human(t, "bob", dto.RoleMember)     // owns the AI
	dave := f.human(t, "dave", dto.RoleMember)   // granted use on the AI
	carol := f.human(t, "carol", dto.RoleMember) // stranger
	admin := f.human(t, "admin", dto.RoleAdmin)
	atlas := f.ai(t, bob)
	f.grant(t, KindAgent, atlas, dave, dto.LevelUse)
	room := f.dm(t, alice, alice, atlas)

	for _, tc := range []struct {
		who  string
		id   string
		want Level
	}{
		{"participant", alice, dto.LevelPost},
		{"owner of the AI", bob, dto.LevelView},
		{"use grantee of the AI", dave, dto.LevelView},
		{"stranger", carol, None},
		{"admin without a grant", admin, None},
	} {
		require.Equal(t, tc.want, f.levelOf(t, tc.id, room), tc.who)
	}
	require.Equal(t, None, f.levelOf(t, alice, store.NewID()), "a missing room")

	// The AI is a member of its DM, so its tools post there (AgentRoomLevel).
	l, err := f.az.RoomLevel(context.Background(), authn.Principal{Kind: authn.KindAgent, UserID: bob, AgentID: atlas}, room)
	require.NoError(t, err)
	require.Equal(t, dto.LevelPost, l)
}

// DM-08: grants never give access to a DM, and neither does the owner column of the room row.
func TestDMRoomLevelIgnoresGrantsAndOwnerColumn(t *testing.T) {
	f := newFixture(t)
	alice := f.human(t, "alice", dto.RoleMember)
	bob := f.human(t, "bob", dto.RoleMember)
	carol := f.human(t, "carol", dto.RoleMember)
	atlas := f.ai(t, bob)
	room := f.dm(t, carol, alice, atlas) // carol created the row but is no participant

	// A grant row on a DM can only exist by writing around the API, which refuses them.
	f.grant(t, KindRoom, room, Everyone, dto.LevelManage)
	f.grant(t, KindRoom, room, carol, dto.LevelManage)
	f.grant(t, KindRoom, room, bob, dto.LevelManage)
	require.Equal(t, None, f.levelOf(t, carol, room), "neither the owner column nor a manage grant")
	require.Equal(t, dto.LevelView, f.levelOf(t, bob, room), "a grant does not raise the AI owner's view")
	require.Equal(t, dto.LevelPost, f.levelOf(t, alice, room), "a grant does not raise the participant's post")
	stranger := f.human(t, "stranger", dto.RoleMember)
	require.Equal(t, None, f.levelOf(t, stranger, room), "an everyone grant on a DM gives nothing")

	// A user member row of someone who is no participant gives no access either.
	f.addUserMember(t, room, stranger)
	require.Equal(t, None, f.levelOf(t, stranger, room))
}

// DM-08: the participants' columns alone decide; member rows are not read.
func TestDMRoomLevelReadsOnlyTheParticipantColumns(t *testing.T) {
	f := newFixture(t)
	alice := f.human(t, "alice", dto.RoleMember)
	bob := f.human(t, "bob", dto.RoleMember)
	room := f.dmWithoutMembers(t, alice, alice, bob)
	require.Equal(t, dto.LevelPost, f.levelOf(t, alice, room))
	require.Equal(t, dto.LevelPost, f.levelOf(t, bob, room))
}

// DM-08: public use on an AI does not expose its DMs; only the owner and users granted use by name
// view them.
func TestDMOfAPublicAIIsNotVisibleToEveryone(t *testing.T) {
	f := newFixture(t)
	owner := f.human(t, "owner", dto.RoleMember)
	alice := f.human(t, "alice", dto.RoleMember)
	bob := f.human(t, "bob", dto.RoleMember)
	atlas := f.ai(t, owner)
	f.grant(t, KindAgent, atlas, Everyone, dto.LevelUse)
	room := f.dm(t, alice, alice, atlas)

	require.Equal(t, dto.LevelPost, f.levelOf(t, alice, room))
	require.Equal(t, dto.LevelView, f.levelOf(t, owner, room))
	require.Equal(t, None, f.levelOf(t, bob, room), "bob may use the AI, he does not read alice's DM with it")

	f.grant(t, KindAgent, atlas, bob, dto.LevelUse) // by name now
	require.Equal(t, dto.LevelView, f.levelOf(t, bob, room))
}

// DM-08: a DM of two AIs is viewed by both owners (and their grantees) and posted in by nobody
// through the web; two users post.
func TestDMBetweenAIsAndBetweenUsers(t *testing.T) {
	f := newFixture(t)
	alice := f.human(t, "alice", dto.RoleMember)
	bob := f.human(t, "bob", dto.RoleMember)
	carol := f.human(t, "carol", dto.RoleMember)
	atlas, orion := f.ai(t, alice), f.ai(t, bob)
	f.grant(t, KindAgent, orion, carol, dto.LevelUse)

	ais := f.dm(t, alice, atlas, orion) // created by atlas: the row's owner is atlas's owner
	require.Equal(t, dto.LevelView, f.levelOf(t, alice, ais))
	require.Equal(t, dto.LevelView, f.levelOf(t, bob, ais))
	require.Equal(t, dto.LevelView, f.levelOf(t, carol, ais), "a grantee of either AI")

	users := f.dm(t, alice, alice, bob)
	require.Equal(t, dto.LevelPost, f.levelOf(t, alice, users))
	require.Equal(t, dto.LevelPost, f.levelOf(t, bob, users))
	require.Equal(t, None, f.levelOf(t, carol, users))

	// Alice also owns atlas: she posts as the participant and does not need a lower view.
	own := f.dm(t, alice, alice, atlas)
	require.Equal(t, dto.LevelPost, f.levelOf(t, alice, own))
}

// AUTH-10 for DMs: a disabled user has no access to a DM, as participant or as the AI's owner.
func TestDMRoomLevelDisabledUsers(t *testing.T) {
	f := newFixture(t)
	alice := f.human(t, "alice", dto.RoleMember)
	bob := f.human(t, "bob", dto.RoleMember)
	atlas := f.ai(t, bob)
	room := f.dm(t, alice, alice, atlas)

	f.disable(t, alice, true)
	require.Equal(t, None, f.levelOf(t, alice, room))
	require.Equal(t, dto.LevelView, f.levelOf(t, bob, room), "the other users are not affected")
	f.disable(t, bob, true)
	require.Equal(t, None, f.levelOf(t, bob, room))
	f.disable(t, alice, false)
	f.disable(t, bob, false)
	require.Equal(t, dto.LevelPost, f.levelOf(t, alice, room))
	require.Equal(t, dto.LevelView, f.levelOf(t, bob, room))
}

// AD-011: a user member of a group posts in it, next to ownership and grants (the highest wins).
func TestGroupUserMemberPosts(t *testing.T) {
	f := newFixture(t)
	owner := f.human(t, "owner", dto.RoleMember)
	member := f.human(t, "member", dto.RoleMember)
	viewer := f.human(t, "viewer", dto.RoleMember)
	manager := f.human(t, "manager", dto.RoleMember)
	outsider := f.human(t, "outsider", dto.RoleMember)
	group, other := f.room(t, owner), f.room(t, owner)
	for _, id := range []string{member, viewer, manager} {
		f.addUserMember(t, group, id)
	}
	f.grant(t, KindRoom, group, viewer, dto.LevelView)
	f.grant(t, KindRoom, group, manager, dto.LevelManage)
	f.addUserMember(t, other, outsider)

	require.Equal(t, dto.LevelPost, f.levelOf(t, member, group), "a member posts")
	require.Equal(t, dto.LevelPost, f.levelOf(t, viewer, group), "member beats a view grant")
	require.Equal(t, dto.LevelManage, f.levelOf(t, manager, group), "a manage grant beats membership")
	require.Equal(t, dto.LevelManage, f.levelOf(t, owner, group))
	require.Equal(t, None, f.levelOf(t, outsider, group), "a member of another group")
	require.Equal(t, dto.LevelPost, f.levelOf(t, outsider, other))
	admin := f.human(t, "admin", dto.RoleAdmin)
	require.Equal(t, None, f.levelOf(t, admin, group), "admins stay out (AD-005)")
}

// AD-011: only user members count; an agent member's owner gets nothing from it, and a removed or
// disabled member loses the access.
func TestGroupMembershipAccessEnds(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	owner := f.human(t, "owner", dto.RoleMember)
	member := f.human(t, "member", dto.RoleMember)
	agentOwner := f.human(t, "agentowner", dto.RoleMember)
	group := f.room(t, owner)
	atlas := f.ai(t, agentOwner)
	f.addAgentMember(t, group, atlas)
	f.addUserMember(t, group, member)

	require.Equal(t, None, f.levelOf(t, agentOwner, group), "the owner of a member AI is no member")
	require.Equal(t, dto.LevelPost, f.levelOf(t, member, group))

	f.disable(t, member, true)
	require.Equal(t, None, f.levelOf(t, member, group), "a disabled member")
	f.disable(t, member, false)
	require.Equal(t, dto.LevelPost, f.levelOf(t, member, group))

	f.exec(t, func(q *db.Queries) error {
		row, err := q.GetRoomMemberByMember(ctx, db.GetRoomMemberByMemberParams{RoomID: group, Kind: string(dto.MemberKindUser), MemberID: member})
		require.NoError(t, err)
		return q.DeleteRoomMember(ctx, row.ID)
	})
	require.Equal(t, None, f.levelOf(t, member, group), "removed")
}

// DM-06 source: GrantedRoomLevels lists the groups the user is a member of and the DMs the user
// takes part in next to the grants; the DMs a user only views through an AI are not listed, and
// grants on DMs are ignored.
func TestGrantedRoomLevelsIncludeMembershipAndDMs(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	owner := f.human(t, "owner", dto.RoleMember)
	alice := f.human(t, "alice", dto.RoleMember)
	bob := f.human(t, "bob", dto.RoleMember)
	atlas := f.ai(t, alice)

	granted, memberOf, both, owned, notMine := f.room(t, owner), f.room(t, owner), f.room(t, owner), f.room(t, alice), f.room(t, owner)
	f.grant(t, KindRoom, granted, alice, dto.LevelView)
	f.addUserMember(t, memberOf, alice)
	f.addUserMember(t, both, alice)
	f.grant(t, KindRoom, both, alice, dto.LevelManage)
	f.addUserMember(t, notMine, bob)
	withBob := f.dm(t, alice, alice, bob)
	withAtlas := f.dm(t, bob, bob, atlas) // alice owns atlas: she views this one, it is not hers
	strayGrant := f.dm(t, bob, bob, f.human(t, "erin", dto.RoleMember))
	f.grant(t, KindRoom, strayGrant, alice, dto.LevelManage) // written around the API

	got, err := f.az.GrantedRoomLevels(ctx, alice)
	require.NoError(t, err)
	require.Equal(t, map[string]Level{
		granted: dto.LevelView, memberOf: dto.LevelPost, both: dto.LevelManage, withBob: dto.LevelPost,
	}, got, "owned rooms are not included; neither are DMs viewed through an AI or granted by a stray row")
	require.NotContains(t, got, owned)
	require.NotContains(t, got, withAtlas)

	everyone, err := f.az.GrantedRoomLevels(ctx, bob)
	require.NoError(t, err)
	require.Equal(t, dto.LevelPost, everyone[withBob])
	require.Equal(t, dto.LevelPost, everyone[withAtlas])
	require.Equal(t, dto.LevelPost, everyone[notMine])
	require.Equal(t, dto.LevelPost, everyone[strayGrant])

	f.disable(t, alice, true)
	got, err = f.az.GrantedRoomLevels(ctx, alice)
	require.NoError(t, err)
	require.Empty(t, got, "a disabled user")
}

// DM-08: sharing a DM is refused whatever the preset, with nothing written: no grant, no audit
// entry, no event.
func TestSetVisibilityRefusesDM(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.human(t, "alice", dto.RoleMember)
	bob := f.human(t, "bob", dto.RoleMember)
	carol := f.human(t, "carol", dto.RoleMember)
	atlas := f.ai(t, bob)
	room := f.dm(t, alice, alice, atlas)
	sub, cancel := f.bus.Subscribe(8)
	defer cancel()

	for name, v := range map[string]Visibility{
		"private": {Mode: dto.VisibilityPrivate},
		"shared":  {Mode: dto.VisibilityShared, Users: []UserLevel{{UserID: carol, Level: dto.LevelView}}},
		"public":  {Mode: dto.VisibilityPublic, PublicLevel: dto.LevelPost},
		"invalid": {Mode: "secret"},
	} {
		t.Run(name, func(t *testing.T) {
			err := f.az.SetVisibility(ctx, alice, KindRoom, room, alice, v)
			require.ErrorIs(t, err, ErrDMNoSharing)
		})
	}
	require.Empty(t, f.grantRows(t, KindRoom, room), "no grant was written")
	require.Equal(t, None, f.levelOf(t, carol, room))
	entries, _, err := audit.New(f.st, nil).List(ctx, "")
	require.NoError(t, err)
	require.Empty(t, entries, "no audit entry")
	select {
	case e := <-sub:
		t.Fatalf("unexpected event %#v", e)
	default:
	}

	// The same call on a group keeps working.
	group := f.room(t, alice)
	require.NoError(t, f.az.SetVisibility(ctx, alice, KindRoom, group, alice, Visibility{Mode: dto.VisibilityShared, Users: []UserLevel{{UserID: carol, Level: dto.LevelView}}}))
	require.Equal(t, []string{carol + "=view"}, f.grantRows(t, KindRoom, group))
}
