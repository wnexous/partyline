package access

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

var t0 = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

type fixture struct {
	st *store.Store
	az *Authorizer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "partyline.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(context.Background()))
	return &fixture{st: st, az: New(st, Options{})}
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

// room stores a group in the shape rows had before organizations: no organization, no creator.
func (f *fixture) room(t *testing.T, ownerID string) string {
	t.Helper()
	id := store.NewID()
	f.exec(t, func(q *db.Queries) error {
		return q.CreateRoom(context.Background(), db.CreateRoomParams{ID: id, OwnerID: ownerID, Name: "room", RetentionDays: 90, CreatedAt: t0.UnixMilli(), UpdatedAt: t0.UnixMilli()})
	})
	return id
}

func nullable(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// group stores a group of the organization (ORG-02) created by the identity creator ("" for nobody); the
// row's owner column, which gives no access, names ownerID.
func (f *fixture) group(t *testing.T, orgID, creator, ownerID string) string {
	t.Helper()
	id := store.NewID()
	f.exec(t, func(q *db.Queries) error {
		return q.CreateRoom(context.Background(), db.CreateRoomParams{
			ID: id, OwnerID: ownerID, Name: "group", RetentionDays: 90, CreatedAt: t0.UnixMilli(), UpdatedAt: t0.UnixMilli(),
			OrgID: nullable(orgID), CreatorIid: nullable(creator),
		})
	})
	return id
}

// org stores an organization and returns its id.
func (f *fixture) org(t *testing.T, slug string) string {
	t.Helper()
	id := "o-" + slug
	f.exec(t, func(q *db.Queries) error {
		return q.CreateOrganization(context.Background(), db.CreateOrganizationParams{ID: id, Name: slug, Slug: slug, CreatedAt: t0.UnixMilli(), UpdatedAt: t0.UnixMilli()})
	})
	return id
}

// position stores a position of the organization under parent ("" for a root) and returns its id.
func (f *fixture) position(t *testing.T, orgID, name, parent string) string {
	t.Helper()
	id := "p-" + orgID[2:] + "-" + name
	f.exec(t, func(q *db.Queries) error {
		return q.CreatePosition(context.Background(), db.CreatePositionParams{ID: id, OrgID: orgID, Name: name, ParentID: nullable(parent), CreatedAt: t0.UnixMilli()})
	})
	return id
}

// join makes the identity a member of the organization with the permission and position ("" for none).
func (f *fixture) join(t *testing.T, orgID, iid string, perm orgs.Permission, position string) {
	t.Helper()
	f.exec(t, func(q *db.Queries) error {
		return q.CreateOrgMember(context.Background(), db.CreateOrgMemberParams{
			OrgID: orgID, Iid: iid, Permission: string(perm), PositionID: nullable(position), JoinedAt: t0.UnixMilli(),
		})
	})
}

// leave takes the identity out of the organization.
func (f *fixture) leave(t *testing.T, orgID, iid string) {
	t.Helper()
	f.exec(t, func(q *db.Queries) error {
		return q.DeleteOrgMember(context.Background(), db.DeleteOrgMemberParams{OrgID: orgID, Iid: iid})
	})
}

// home makes orgID the home organization of the AI, created by creator ("" for nobody), and a member of
// it with the permission and position.
func (f *fixture) home(t *testing.T, aiID, orgID, creator string, perm orgs.Permission, position string) {
	t.Helper()
	f.exec(t, func(q *db.Queries) error {
		if err := q.SetAgentHome(context.Background(), db.SetAgentHomeParams{OrgID: nullable(orgID), ID: aiID}); err != nil {
			return err
		}
		return q.SetAgentCreator(context.Background(), db.SetAgentCreatorParams{CreatorIid: nullable(creator), ID: aiID})
	})
	f.join(t, orgID, aiID, perm, position)
}

func (f *fixture) agent(t *testing.T, ownerID string) string {
	t.Helper()
	id := store.NewID()
	f.exec(t, func(q *db.Queries) error {
		return q.CreateAgent(context.Background(), db.CreateAgentParams{ID: id, OwnerID: ownerID, Name: "agent-" + id[len(id)-6:], CreatedAt: t0.UnixMilli()})
	})
	return id
}

// connector stores a connection of the organization ("" for none, the shape of a row written before
// organizations); ownerID is the creator, a dead anchor that gives no access (AD-019).
func (f *fixture) connector(t *testing.T, orgID, ownerID string) string {
	t.Helper()
	id := store.NewID()
	f.exec(t, func(q *db.Queries) error {
		return q.CreateConnector(context.Background(), db.CreateConnectorParams{
			ID: id, OwnerID: ownerID, OrgID: nullable(orgID), Kind: "webhook", Name: "hook", ConfigJson: "{}", Enabled: true,
			CreatedAt: t0.UnixMilli(), UpdatedAt: t0.UnixMilli(),
		})
	})
	return id
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

// intelligence gives an existing user or agent its intelligence row, which a DM's participant
// columns and an organization's membership reference.
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

// AD-016, ORG-02: a group is posted to by its room members, guests included, and managed by its creator
// and by the admins of its organization. Nobody else has any access: not a member of the organization
// whatever their position, not the user the room row names as its owner, not an admin of another
// organization, not an admin of the instance. A disabled user has none either.
func TestGroupRoomLevel(t *testing.T) {
	f := newFixture(t)
	acme, globex := f.org(t, "acme"), f.org(t, "globex")
	boss := f.position(t, acme, "Admin", "")
	creator := f.human(t, "creator", dto.RoleMember)
	f.join(t, acme, creator, orgs.PermissionMember, "")

	// uniq makes usernames unique across the cases, which share one database.
	seq := 0
	uniq := func(name string) string {
		seq++
		return fmt.Sprintf("%s-%d", name, seq)
	}
	// member makes a user of acme with the permission and position.
	member := func(t *testing.T, name string, perm orgs.Permission, position string) string {
		t.Helper()
		u := f.human(t, uniq(name), dto.RoleMember)
		f.join(t, acme, u, perm, position)
		return u
	}
	// groupOf is a group of acme the creator made.
	groupOf := func(t *testing.T) string { return f.group(t, acme, creator, creator) }

	cases := []struct {
		name  string
		want  Level
		setup func(t *testing.T) (group, who string)
	}{
		{"the creator manages without being in the room", dto.LevelManage, func(t *testing.T) (string, string) {
			return groupOf(t), creator
		}},
		{"the creator who is also in the room manages", dto.LevelManage, func(t *testing.T) (string, string) {
			g := groupOf(t)
			f.addUserMember(t, g, creator)
			return g, creator
		}},
		{"a room member posts", dto.LevelPost, func(t *testing.T) (string, string) {
			g, u := groupOf(t), member(t, "member", orgs.PermissionMember, "")
			f.addUserMember(t, g, u)
			return g, u
		}},
		{"a guest of the organization in the room posts", dto.LevelPost, func(t *testing.T) (string, string) {
			g, u := groupOf(t), member(t, "guest", orgs.PermissionGuest, "")
			f.addUserMember(t, g, u)
			return g, u
		}},
		{"an admin of the organization who is not in the room manages", dto.LevelManage, func(t *testing.T) (string, string) {
			return groupOf(t), member(t, "admin", orgs.PermissionAdmin, "")
		}},
		{"an admin who is also in the room manages", dto.LevelManage, func(t *testing.T) (string, string) {
			g, u := groupOf(t), member(t, "admin", orgs.PermissionAdmin, "")
			f.addUserMember(t, g, u)
			return g, u
		}},
		{"a member of the organization who is not in the room has none", None, func(t *testing.T) (string, string) {
			return groupOf(t), member(t, "plain", orgs.PermissionMember, "")
		}},
		{"a guest of the organization who is not in the room has none", None, func(t *testing.T) (string, string) {
			return groupOf(t), member(t, "guest", orgs.PermissionGuest, "")
		}},
		{"the root position gives no access to a group", None, func(t *testing.T) (string, string) {
			return groupOf(t), member(t, "boss", orgs.PermissionMember, boss)
		}},
		{"the user the room row names as its owner has none", None, func(t *testing.T) (string, string) {
			u := member(t, "rowowner", orgs.PermissionMember, "")
			return f.group(t, acme, creator, u), u
		}},
		{"an admin of another organization has none", None, func(t *testing.T) (string, string) {
			u := f.human(t, uniq("stranger"), dto.RoleMember)
			f.join(t, globex, u, orgs.PermissionAdmin, "")
			return groupOf(t), u
		}},
		{"an admin of the instance who belongs to no organization has none", None, func(t *testing.T) (string, string) {
			return groupOf(t), f.human(t, uniq("root"), dto.RoleAdmin)
		}},
		{"an admin of the instance who is an admin of the organization manages", dto.LevelManage, func(t *testing.T) (string, string) {
			u := f.human(t, uniq("root"), dto.RoleAdmin)
			f.join(t, acme, u, orgs.PermissionAdmin, "")
			return groupOf(t), u
		}},
		{"a user of no organization has none", None, func(t *testing.T) (string, string) {
			return groupOf(t), f.human(t, uniq("outsider"), dto.RoleMember)
		}},
		{"a creator who left the organization has none", None, func(t *testing.T) (string, string) {
			u := member(t, "leaver", orgs.PermissionMember, "")
			g := f.group(t, acme, u, u)
			f.leave(t, acme, u)
			return g, u
		}},
		{"a creator who is now a guest has none", None, func(t *testing.T) (string, string) {
			u := member(t, "demoted", orgs.PermissionGuest, "")
			return f.group(t, acme, u, u), u
		}},
		{"a creator who is now a guest and is in the room posts", dto.LevelPost, func(t *testing.T) (string, string) {
			u := member(t, "demoted", orgs.PermissionGuest, "")
			g := f.group(t, acme, u, u)
			f.addUserMember(t, g, u)
			return g, u
		}},
		{"a disabled admin has none", None, func(t *testing.T) (string, string) {
			g, u := groupOf(t), member(t, "admin", orgs.PermissionAdmin, "")
			f.disable(t, u, true)
			return g, u
		}},
		{"a disabled creator has none", None, func(t *testing.T) (string, string) {
			u := member(t, "creator2", orgs.PermissionMember, "")
			g := f.group(t, acme, u, u)
			f.disable(t, u, true)
			return g, u
		}},
		{"a disabled room member has none", None, func(t *testing.T) (string, string) {
			g, u := groupOf(t), member(t, "member", orgs.PermissionMember, "")
			f.addUserMember(t, g, u)
			f.disable(t, u, true)
			return g, u
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, who := tc.setup(t)
			require.Equal(t, tc.want, f.levelOf(t, who, g))
		})
	}

	t.Run("a missing room", func(t *testing.T) {
		require.Equal(t, None, f.levelOf(t, creator, store.NewID()))
	})
	t.Run("a group of the legacy shape gives its room members post and nobody manage", func(t *testing.T) {
		g := f.room(t, creator) // no organization and no creator, as before the upgrade
		admin, m := f.human(t, uniq("lonely-admin"), dto.RoleMember), f.human(t, uniq("lonely-member"), dto.RoleMember)
		f.join(t, acme, admin, orgs.PermissionAdmin, "")
		f.addUserMember(t, g, m)
		require.Equal(t, dto.LevelPost, f.levelOf(t, m, g))
		require.Equal(t, None, f.levelOf(t, admin, g))
		require.Equal(t, None, f.levelOf(t, creator, g), "the owner column is no creator")
	})
	t.Run("a demotion takes effect at once", func(t *testing.T) {
		g, u := groupOf(t), member(t, "briefly-admin", orgs.PermissionAdmin, "")
		require.Equal(t, dto.LevelManage, f.levelOf(t, u, g))
		f.exec(t, func(q *db.Queries) error {
			return q.UpdateOrgMemberPermission(context.Background(), db.UpdateOrgMemberPermissionParams{Permission: string(orgs.PermissionMember), OrgID: acme, Iid: u})
		})
		require.Equal(t, None, f.levelOf(t, u, g), "nothing is cached across requests")
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

// ACL-08, AD-016: instance admins get no implicit access to rooms, AIs or connections; what an
// organization gives them it gives like anyone else.
func TestAdminWithoutMembershipHasNoAccess(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	owner := f.human(t, "owner", dto.RoleMember)
	admin := f.human(t, "admin", dto.RoleAdmin)
	acme := f.org(t, "acme")
	f.join(t, acme, owner, orgs.PermissionAdmin, "")
	room, agent, conn := f.group(t, acme, owner, owner), f.ai(t, owner), f.connector(t, acme, owner)
	f.home(t, agent, acme, owner, orgs.PermissionMember, "")
	principal := authn.Principal{Kind: authn.KindUser, UserID: admin, Role: string(dto.RoleAdmin)}

	l, err := f.az.RoomLevel(ctx, principal, room)
	require.NoError(t, err)
	require.Equal(t, None, l)
	ok, err := f.az.CanSeeAI(ctx, principal, agent)
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = f.az.CanSeeConnection(ctx, principal, conn)
	require.NoError(t, err)
	require.False(t, ok)
	levels, err := f.az.RoomLevels(ctx, admin)
	require.NoError(t, err)
	require.Empty(t, levels)

	// Membership of the organization gives what it gives anyone: the group to its admins, the AI and the
	// connection to its members.
	f.join(t, acme, admin, orgs.PermissionAdmin, "")
	require.Equal(t, dto.LevelManage, f.levelOf(t, admin, room))
	ok, err = f.az.CanSeeAI(ctx, principal, agent)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = f.az.CanSeeConnection(ctx, principal, conn)
	require.NoError(t, err)
	require.True(t, ok)
}

// AUTH-10: a disabled user has no access, whatever they hold; enabling restores it.
func TestDisabledUserResolvesToNone(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	acme := f.org(t, "acme")
	owner := f.human(t, "owner", dto.RoleMember)
	u := f.human(t, "user", dto.RoleMember)
	f.join(t, acme, owner, orgs.PermissionAdmin, "")
	f.join(t, acme, u, orgs.PermissionAdmin, "")
	room, agent, conn := f.group(t, acme, owner, owner), f.ai(t, owner), f.connector(t, acme, owner)
	f.home(t, agent, acme, owner, orgs.PermissionMember, "")
	sees := func(who string) (ai, connection bool) {
		t.Helper()
		p := user(who)
		ai, err := f.az.CanSeeAI(ctx, p, agent)
		require.NoError(t, err)
		connection, err = f.az.CanSeeConnection(ctx, p, conn)
		require.NoError(t, err)
		return ai, connection
	}
	require.Equal(t, dto.LevelManage, f.levelOf(t, u, room))
	ai, connection := sees(u)
	require.True(t, ai && connection)

	f.disable(t, u, true)
	require.Equal(t, None, f.levelOf(t, u, room), "an admin of the organization")
	levels, err := f.az.RoomLevels(ctx, u)
	require.NoError(t, err)
	require.Empty(t, levels)
	ai, connection = sees(u)
	require.False(t, ai, "a disabled member sees no AI of the organization")
	require.False(t, connection, "and no connection of it")

	f.disable(t, owner, true)
	require.Equal(t, None, f.levelOf(t, owner, room), "a disabled creator has no access either")
	ai, _ = sees(owner)
	require.False(t, ai, "a disabled host sees no AI")

	f.disable(t, u, false)
	require.Equal(t, dto.LevelManage, f.levelOf(t, u, room), "enabling restores the access")
	ai, connection = sees(u)
	require.True(t, ai && connection)
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
	require.Equal(t, None, l, "the host's other agents get nothing from the host")
	l, err = f.az.RoomLevel(ctx, agent(""), room)
	require.NoError(t, err)
	require.Equal(t, None, l)
}

// ACL-05, AD-016: an AI is seen by the user who hosts it and by the members of any organization it belongs
// to, with any permission, guests included; an AI sees itself and the AIs it shares an organization with,
// and never reaches further through its host. Nobody else sees it: not an outsider, not another
// organization's admin, not an admin of the instance, not the creator of the row who left, not a
// disabled user; and an AI that does not exist is seen by nobody.
func TestCanSeeAI(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	acme, globex := f.org(t, "acme"), f.org(t, "globex")
	host := f.human(t, "host", dto.RoleMember)
	f.join(t, globex, host, orgs.PermissionMember, "") // the host belongs to globex, and not to acme
	atlas := f.ai(t, host)
	f.home(t, atlas, acme, host, orgs.PermissionMember, "")
	sibling := f.ai(t, host) // hosted by the same user, in globex
	f.home(t, sibling, globex, host, orgs.PermissionMember, "")
	colleague := f.ai(t, f.human(t, "other-host", dto.RoleMember))
	f.home(t, colleague, acme, "", orgs.PermissionMember, "")

	member := func(name string, perm orgs.Permission) string {
		u := f.human(t, name, dto.RoleMember)
		f.join(t, acme, u, perm, "")
		return u
	}
	outsider := f.human(t, "outsider", dto.RoleMember)
	root := f.human(t, "root", dto.RoleAdmin)
	foreignAdmin := f.human(t, "foreign-admin", dto.RoleMember)
	f.join(t, globex, foreignAdmin, orgs.PermissionAdmin, "")
	disabled := member("disabled", orgs.PermissionMember)
	f.disable(t, disabled, true)

	for _, tc := range []struct {
		name string
		who  string
		want bool
	}{
		{"the host", host, true},
		{"a member of the organization", member("member", orgs.PermissionMember), true},
		{"an admin of the organization", member("admin", orgs.PermissionAdmin), true},
		{"a guest of the organization", member("guest", orgs.PermissionGuest), true},
		{"a user of no organization", outsider, false},
		{"an admin of another organization", foreignAdmin, false},
		{"an admin of the instance", root, false},
		{"a disabled member", disabled, false},
	} {
		ok, err := f.az.CanSeeAI(ctx, user(tc.who), atlas)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.want, ok, tc.name)
	}

	ai := func(id string) authn.Principal {
		return authn.Principal{Kind: authn.KindAgent, UserID: host, AgentID: id}
	}
	for _, tc := range []struct {
		name   string
		who    authn.Principal
		target string
		want   bool
	}{
		{"an AI sees itself", ai(atlas), atlas, true},
		{"an AI sees an AI of its organization", ai(atlas), colleague, true},
		{"an AI does not see an AI of its host's organization, which shares none with it", ai(atlas), sibling, false},
		{"an agent token that names no AI sees nothing", ai(""), atlas, false},
	} {
		ok, err := f.az.CanSeeAI(ctx, tc.who, tc.target)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.want, ok, tc.name)
	}

	ok, err := f.az.CanSeeAI(ctx, user(host), store.NewID())
	require.NoError(t, err)
	require.False(t, ok, "an AI that does not exist")
	ok, err = f.az.CanSeeAI(ctx, user(host), "")
	require.NoError(t, err)
	require.False(t, ok, "no AI named")

	// Leaving an organization ends the sight it gave, and hosting never depends on one.
	leaver := member("leaver", orgs.PermissionMember)
	ok, err = f.az.CanSeeAI(ctx, user(leaver), atlas)
	require.NoError(t, err)
	require.True(t, ok)
	f.leave(t, acme, leaver)
	ok, err = f.az.CanSeeAI(ctx, user(leaver), atlas)
	require.NoError(t, err)
	require.False(t, ok, "a member who left")
	f.leave(t, acme, atlas) // the AI is in no organization any more
	ok, err = f.az.CanSeeAI(ctx, user(host), atlas)
	require.NoError(t, err)
	require.True(t, ok, "a host sees the AI it runs whatever its organizations")
}

// ACL-06, AD-016: a connection is seen by the members of its organization, with any permission, and by nobody
// else: an outsider, an admin of the instance, the user the row names as its owner (a dead anchor), a
// disabled member. An AI is a member like anyone and sees as itself, never through its host. A connection
// without an organization (a row written before them) and one that does not exist are seen by nobody.
func TestCanSeeConnection(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	acme := f.org(t, "acme")
	creator := f.human(t, "creator", dto.RoleMember) // named as the owner by the row, and not a member
	conn := f.connector(t, acme, creator)
	legacy := f.connector(t, "", creator)

	member := func(name string, perm orgs.Permission) string {
		u := f.human(t, name, dto.RoleMember)
		f.join(t, acme, u, perm, "")
		return u
	}
	root := f.human(t, "root", dto.RoleAdmin)
	disabled := member("disabled", orgs.PermissionMember)
	f.disable(t, disabled, true)
	for _, tc := range []struct {
		name string
		who  string
		want bool
	}{
		{"a member", member("member", orgs.PermissionMember), true},
		{"an admin", member("admin", orgs.PermissionAdmin), true},
		{"a guest", member("guest", orgs.PermissionGuest), true},
		{"the user the row names as its owner, who is no member", creator, false},
		{"an admin of the instance who is no member", root, false},
		{"a disabled member", disabled, false},
	} {
		ok, err := f.az.CanSeeConnection(ctx, user(tc.who), conn)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.want, ok, tc.name)
	}

	host := member("host", orgs.PermissionMember) // hosts two AIs: one in acme and one elsewhere
	atlas, elsewhere := f.ai(t, host), f.ai(t, host)
	f.home(t, atlas, acme, host, orgs.PermissionMember, "")
	f.home(t, elsewhere, f.org(t, "globex"), host, orgs.PermissionMember, "")
	ai := func(id string) authn.Principal {
		return authn.Principal{Kind: authn.KindAgent, UserID: host, AgentID: id}
	}
	ok, err := f.az.CanSeeConnection(ctx, ai(atlas), conn)
	require.NoError(t, err)
	require.True(t, ok, "an AI of the organization")
	ok, err = f.az.CanSeeConnection(ctx, ai(elsewhere), conn)
	require.NoError(t, err)
	require.False(t, ok, "an AI of another organization, though its host is a member of this one")
	ok, err = f.az.CanSeeConnection(ctx, ai(""), conn)
	require.NoError(t, err)
	require.False(t, ok, "an agent token that names no AI acts as nobody")

	ok, err = f.az.CanSeeConnection(ctx, user(host), legacy)
	require.NoError(t, err)
	require.False(t, ok, "a connection with no organization")
	ok, err = f.az.CanSeeConnection(ctx, user(host), store.NewID())
	require.NoError(t, err)
	require.False(t, ok, "a connection that does not exist")
}

// dmCast is the people and AIs of the DM tests: acme's chart is Admin > Lead > Worker; alice writes to
// the AIs; atlas is positioned (Worker) and bob created it; orion has no position and cara created it; vega
// is orion's kind but also belongs to globex, where gxBoss manages it; nomad has no home organization.
type dmCast struct {
	acme, globex                                            string
	alice, bob, cara, host, boss, lead, peer, admin, gxBoss string
	instanceAdmin, plain                                    string
	atlas, orion, vega, nomad                               string
}

func newDMCast(t *testing.T, f *fixture) dmCast {
	t.Helper()
	c := dmCast{acme: f.org(t, "acme"), globex: f.org(t, "globex")}
	root := f.position(t, c.acme, "Admin", "")
	lead := f.position(t, c.acme, "Lead", root)
	worker := f.position(t, c.acme, "Worker", lead)
	gxRoot := f.position(t, c.globex, "Boss", "")
	gxHand := f.position(t, c.globex, "Hand", gxRoot)
	in := func(name string, perm orgs.Permission, position string) string {
		u := f.human(t, name, dto.RoleMember)
		f.join(t, c.acme, u, perm, position)
		return u
	}
	c.alice, c.bob, c.cara, c.host = in("alice", orgs.PermissionMember, ""), in("bob", orgs.PermissionMember, ""), in("cara", orgs.PermissionMember, ""), in("host", orgs.PermissionMember, "")
	c.boss, c.lead, c.peer = in("boss", orgs.PermissionMember, root), in("lead", orgs.PermissionMember, lead), in("peer", orgs.PermissionMember, worker)
	c.admin = in("admin", orgs.PermissionAdmin, "") // admin permission, no position: manages nobody
	c.gxBoss = f.human(t, "gxboss", dto.RoleMember)
	f.join(t, c.globex, c.gxBoss, orgs.PermissionMember, gxRoot)
	c.instanceAdmin = f.human(t, "root", dto.RoleAdmin)
	c.plain = in("dave", orgs.PermissionMember, "") // a member with no position and no relation to any AI

	c.atlas = f.ai(t, c.host)
	f.home(t, c.atlas, c.acme, c.bob, orgs.PermissionMember, worker)
	c.orion = f.ai(t, c.host)
	f.home(t, c.orion, c.acme, c.cara, orgs.PermissionMember, "")
	c.vega = f.ai(t, c.host)
	f.home(t, c.vega, c.acme, c.cara, orgs.PermissionMember, "")
	f.join(t, c.globex, c.vega, orgs.PermissionMember, gxHand)
	c.nomad = f.ai(t, c.host) // no home organization, no creator
	return c
}

// DM-08, AD-016, POS-03: a DM is posted to by its two participants and viewed by whoever manages one of
// its AI participants in that AI's home organization. Everybody else has none: not the AI's host, not a
// peer, not an admin without a position, not a manager in an organization that is not the AI's home, not a
// plain member of the organization, not an admin of the instance. A creator manages the AI it created even
// when it holds a position (AD-029). Nobody manages a DM.
func TestDMRoomLevel(t *testing.T) {
	f := newFixture(t)
	c := newDMCast(t, f)
	withAtlas := f.dm(t, c.alice, c.alice, c.atlas)
	withOrion := f.dm(t, c.alice, c.alice, c.orion)
	withVega := f.dm(t, c.alice, c.alice, c.vega)
	withNomad := f.dm(t, c.alice, c.alice, c.nomad)
	aiToAI := f.dm(t, c.host, c.atlas, c.orion)
	humans := f.dm(t, c.alice, c.alice, c.bob)

	for _, tc := range []struct {
		room, who string
		name      string
		want      Level
	}{
		{withAtlas, c.alice, "the participant", dto.LevelPost},
		{withAtlas, c.lead, "the holder of the position above the AI's", dto.LevelView},
		{withAtlas, c.boss, "the holder of a position two levels above", dto.LevelView},
		{withAtlas, c.peer, "a holder of the AI's own position", None},
		{withAtlas, c.bob, "the creator of an AI that holds a position", dto.LevelView},
		{withAtlas, c.host, "the user that hosts the AI", None},
		{withAtlas, c.admin, "an admin of the organization without a position", None},
		{withAtlas, c.plain, "a member of the organization with no relation to the AI", None},
		{withAtlas, c.gxBoss, "a user of another organization", None},
		{withAtlas, c.instanceAdmin, "an admin of the instance", None},
		{withOrion, c.alice, "the participant of the DM with a positionless AI", dto.LevelPost},
		{withOrion, c.cara, "the creator of an AI without a position", dto.LevelView},
		{withOrion, c.peer, "a holder of a position, not the creator", None},
		{withOrion, c.host, "the host of a positionless AI", None},
		{withVega, c.cara, "the creator of an AI, in its home organization", dto.LevelView},
		{withVega, c.gxBoss, "a manager in an organization that is not the AI's home", None},
		{withNomad, c.alice, "the participant of a DM with an AI that has no home", dto.LevelPost},
		{withNomad, c.host, "the host of an AI that has no home", None},
		{withNomad, c.cara, "anyone, for an AI that has no home", None},
		{aiToAI, c.lead, "the manager of the first AI of a DM between two AIs", dto.LevelView},
		{aiToAI, c.cara, "the manager of the second AI of a DM between two AIs", dto.LevelView},
		{aiToAI, c.alice, "a user that manages neither AI", None},
		{aiToAI, c.host, "the host of both AIs", None},
		{humans, c.alice, "the first user of a DM between users", dto.LevelPost},
		{humans, c.bob, "the second user of a DM between users", dto.LevelPost},
		{humans, c.boss, "the top of the chart, for a DM between users", None},
		{humans, c.admin, "an admin of the organization, for a DM between users", None},
		{humans, c.instanceAdmin, "an admin of the instance, for a DM between users", None},
	} {
		require.Equal(t, tc.want, f.levelOf(t, tc.who, tc.room), tc.name)
	}
	require.Equal(t, None, f.levelOf(t, c.alice, store.NewID()), "a missing room")

	// The AI is a member of its DM, so its tools post there (AgentRoomLevel).
	l, err := f.az.RoomLevel(context.Background(), authn.Principal{Kind: authn.KindAgent, UserID: c.host, AgentID: c.atlas}, withAtlas)
	require.NoError(t, err)
	require.Equal(t, dto.LevelPost, l)
}

// DLV-09, DM-08, POS-03: a group is resumed by whoever manages it. Nobody manages a DM, so a DM is resumed by
// the people who can see it: its human participants and whoever manages one of its AI participants in that AI's
// home organization - its creator, whatever position either holds, and the holders of the positions above it.
// Whoever cannot see the DM does not, the AI's host and the admins of the organization and of the instance
// included, and neither does a member of a group who only posts.
func TestCanResume(t *testing.T) {
	f := newFixture(t)
	c := newDMCast(t, f)
	withAtlas := f.dm(t, c.alice, c.alice, c.atlas)
	withOrion := f.dm(t, c.alice, c.alice, c.orion)
	withNomad := f.dm(t, c.alice, c.alice, c.nomad)
	aiToAI := f.dm(t, c.host, c.atlas, c.orion)
	humans := f.dm(t, c.alice, c.alice, c.bob)
	group := f.group(t, c.acme, c.cara, c.cara)
	f.addUserMember(t, group, c.alice)

	for _, tc := range []struct {
		room, who string
		name      string
		want      bool
	}{
		{withAtlas, c.alice, "the participant of a DM with an AI", true},
		{withAtlas, c.lead, "the holder of the position above the AI's", true},
		{withAtlas, c.boss, "the holder of a position two levels above", true},
		{withAtlas, c.peer, "a holder of the AI's own position", false},
		{withAtlas, c.bob, "the creator of an AI that holds a position", true},
		{withAtlas, c.host, "the user that hosts the AI", false},
		{withAtlas, c.admin, "an admin of the organization without a position", false},
		{withAtlas, c.plain, "a member of the organization with no relation to the AI", false},
		{withAtlas, c.gxBoss, "a user of another organization", false},
		{withAtlas, c.instanceAdmin, "an admin of the instance", false},
		{withOrion, c.cara, "the creator of an AI without a position", true},
		{withOrion, c.peer, "a holder of a position, not the creator", false},
		{withNomad, c.alice, "the participant of a DM with an AI that has no home", true},
		{withNomad, c.cara, "anyone, for an AI that has no home", false},
		{aiToAI, c.lead, "the manager of the first AI of a DM between two AIs", true},
		{aiToAI, c.cara, "the manager of the second AI of a DM between two AIs", true},
		{aiToAI, c.alice, "a user that manages neither AI", false},
		{aiToAI, c.host, "the host of both AIs", false},
		{aiToAI, c.admin, "an admin of the organization that manages neither AI", false},
		{humans, c.alice, "the first user of a DM between users", true},
		{humans, c.bob, "the second user of a DM between users", true},
		{humans, c.boss, "the top of the chart, for a DM between users", false},
		{group, c.cara, "the creator of a group", true},
		{group, c.admin, "an admin of the group's organization", true},
		{group, c.alice, "a member of the group who only posts", false},
		{group, c.bob, "a member of the organization outside the group", false},
		{group, c.instanceAdmin, "an admin of the instance", false},
	} {
		kind := f.kindOf(t, tc.room)
		require.Equal(t, tc.want, CanResume(kind, user(tc.who), f.levelOf(t, tc.who, tc.room)), tc.name)
	}

	// The AI is a member of the rooms it talks in and posts there, and it still never resumes them: the
	// guard stops the AIs from answering each other.
	for _, room := range []string{withAtlas, aiToAI} {
		ai := authn.Principal{Kind: authn.KindAgent, UserID: c.host, AgentID: c.atlas}
		l, err := f.az.RoomLevel(context.Background(), ai, room)
		require.NoError(t, err)
		require.Equal(t, dto.LevelPost, l, "a participant of the DM")
		require.False(t, CanResume(f.kindOf(t, room), ai, l), "an AI never resumes")
	}
	f.addAgentMember(t, group, c.atlas)
	ai := authn.Principal{Kind: authn.KindAgent, UserID: c.host, AgentID: c.atlas}
	l, err := f.az.RoomLevel(context.Background(), ai, group)
	require.NoError(t, err)
	require.False(t, CanResume(f.kindOf(t, group), ai, l), "nor in a group it is a member of")

	// Without any level there is nothing to resume, whatever the room.
	require.False(t, CanResume(f.kindOf(t, withAtlas), user(c.peer), None))
	require.False(t, CanResume(f.kindOf(t, group), user(c.cara), None))
}

// kindOf is the kind of the stored room.
func (f *fixture) kindOf(t *testing.T, roomID string) string {
	t.Helper()
	r, err := f.st.R().GetRoom(context.Background(), roomID)
	require.NoError(t, err)
	return r.Kind
}

// DM-08, AD-016: viewing follows the chart as it is now: a manager who is demoted, moved or disabled
// stops viewing at once, and a position that is gained starts it.
func TestDMViewFollowsTheChart(t *testing.T) {
	f := newFixture(t)
	c := newDMCast(t, f)
	room := f.dm(t, c.alice, c.alice, c.atlas)
	require.Equal(t, dto.LevelView, f.levelOf(t, c.lead, room))

	f.exec(t, func(q *db.Queries) error {
		return q.UpdateOrgMemberPosition(context.Background(), db.UpdateOrgMemberPositionParams{PositionID: nullable(""), OrgID: c.acme, Iid: c.lead})
	})
	require.Equal(t, None, f.levelOf(t, c.lead, room), "no position, no management")

	f.exec(t, func(q *db.Queries) error {
		return q.UpdateOrgMemberPosition(context.Background(), db.UpdateOrgMemberPositionParams{PositionID: nullable("p-acme-Lead"), OrgID: c.acme, Iid: c.peer})
	})
	require.Equal(t, dto.LevelView, f.levelOf(t, c.peer, room), "a position above the AI's")

	f.disable(t, c.peer, true)
	require.Equal(t, None, f.levelOf(t, c.peer, room), "a disabled manager")
	f.disable(t, c.peer, false)
	f.leave(t, c.acme, c.peer)
	require.Equal(t, None, f.levelOf(t, c.peer, room), "a manager who left the organization")
}

// DM-08: neither the owner column of the room row nor a member row of someone who is no participant gives
// access to a DM.
func TestDMRoomLevelIgnoresTheOwnerColumnAndStrayMembers(t *testing.T) {
	f := newFixture(t)
	c := newDMCast(t, f)
	stranger := f.human(t, "stranger", dto.RoleMember)
	room := f.dm(t, stranger, c.alice, c.atlas) // stranger created the row but is no participant

	require.Equal(t, None, f.levelOf(t, stranger, room), "the owner column gives nothing")
	require.Equal(t, None, f.levelOf(t, c.host, room), "the AI's host is no viewer")
	require.Equal(t, dto.LevelView, f.levelOf(t, c.lead, room), "the manager's view")
	require.Equal(t, dto.LevelPost, f.levelOf(t, c.alice, room), "the participant's post")

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

// AUTH-10 for DMs: a disabled user has no access to a DM, as participant or as the AI's manager.
func TestDMRoomLevelDisabledUsers(t *testing.T) {
	f := newFixture(t)
	c := newDMCast(t, f)
	room := f.dm(t, c.alice, c.alice, c.atlas)

	f.disable(t, c.alice, true)
	require.Equal(t, None, f.levelOf(t, c.alice, room))
	require.Equal(t, dto.LevelView, f.levelOf(t, c.lead, room), "the other users are not affected")
	f.disable(t, c.lead, true)
	require.Equal(t, None, f.levelOf(t, c.lead, room))
	f.disable(t, c.alice, false)
	f.disable(t, c.lead, false)
	require.Equal(t, dto.LevelPost, f.levelOf(t, c.alice, room))
	require.Equal(t, dto.LevelView, f.levelOf(t, c.lead, room))
}

// AD-016: only user members count for a group's access; the host of a member AI gets nothing from it,
// and a removed or disabled member loses the access.
func TestGroupMembershipAccessEnds(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	acme := f.org(t, "acme")
	creator := f.human(t, "creator", dto.RoleMember)
	member := f.human(t, "member", dto.RoleMember)
	host := f.human(t, "host", dto.RoleMember)
	for _, u := range []string{creator, member, host} {
		f.join(t, acme, u, orgs.PermissionMember, "")
	}
	group := f.group(t, acme, creator, creator)
	atlas := f.ai(t, host)
	f.home(t, atlas, acme, host, orgs.PermissionMember, "")
	f.addAgentMember(t, group, atlas)
	f.addUserMember(t, group, member)

	require.Equal(t, None, f.levelOf(t, host, group), "the host of a member AI is no member")
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

// DM-06 source: RoomLevels lists the groups the user is a member of (post) or manages as their
// creator or as an admin of their organization (manage), and the DMs the user takes part in (post); the
// DMs a user only views through an AI are not listed.
func TestRoomLevels(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	c := newDMCast(t, f)
	creator := f.human(t, "creator", dto.RoleMember)
	f.join(t, c.acme, creator, orgs.PermissionMember, "")

	inRoom, made, both, administered := f.group(t, c.acme, creator, creator), f.group(t, c.acme, c.alice, c.alice), f.group(t, c.acme, c.alice, c.alice), f.group(t, c.acme, creator, creator)
	foreign, notMine := f.group(t, c.globex, creator, creator), f.group(t, c.acme, creator, creator)
	f.addUserMember(t, inRoom, c.alice)
	f.addUserMember(t, both, c.alice)
	f.addUserMember(t, foreign, c.alice) // a stale membership of a group of an organization alice is not in
	f.addUserMember(t, notMine, c.bob)
	withBob := f.dm(t, c.alice, c.alice, c.bob)
	viaAI := f.dm(t, c.alice, c.alice, c.atlas)
	viewed := f.dm(t, c.bob, c.bob, c.atlas) // lead manages atlas: a DM lead only views
	stray := f.dm(t, c.bob, c.bob, c.cara)   // a DM alice has nothing to do with

	got, err := f.az.RoomLevels(ctx, c.alice)
	require.NoError(t, err)
	require.Equal(t, map[string]Level{
		inRoom: dto.LevelPost, made: dto.LevelManage, both: dto.LevelManage, foreign: dto.LevelPost,
		withBob: dto.LevelPost, viaAI: dto.LevelPost,
	}, got, "a creator manages, a member posts, a participant posts")
	require.NotContains(t, got, administered)
	require.NotContains(t, got, notMine)
	require.NotContains(t, got, stray)

	// An admin of the organization manages every group of it, and lists none of its DMs.
	ada := f.human(t, "ada", dto.RoleMember)
	f.join(t, c.acme, ada, orgs.PermissionAdmin, "")
	adminLevels, err := f.az.RoomLevels(ctx, ada)
	require.NoError(t, err)
	require.Equal(t, map[string]Level{
		inRoom: dto.LevelManage, made: dto.LevelManage, both: dto.LevelManage, administered: dto.LevelManage,
		notMine: dto.LevelManage,
	}, adminLevels, "the groups of acme, and not globex's, nor any DM")

	// A manager of an AI lists the DMs he takes part in and not the ones he views.
	leadLevels, err := f.az.RoomLevels(ctx, c.lead)
	require.NoError(t, err)
	require.Empty(t, leadLevels)
	require.Equal(t, dto.LevelView, f.levelOf(t, c.lead, viewed))
	require.Equal(t, dto.LevelPost, f.levelOf(t, c.bob, viewed))

	f.disable(t, c.alice, true)
	got, err = f.az.RoomLevels(ctx, c.alice)
	require.NoError(t, err)
	require.Empty(t, got, "a disabled user")
}

// The organization checks and the room checks report the same errors: the sentinels of package orgs
// are the ones this package exports, so the API maps both to 404 and 403 alike.
func TestAccessErrorsAreTheOrganizationErrors(t *testing.T) {
	require.Same(t, orgs.ErrNotFound, ErrNotFound)
	require.Same(t, orgs.ErrForbidden, ErrForbidden)
	require.ErrorIs(t, fmt.Errorf("wrapped: %w", orgs.ErrNotFound), ErrNotFound)
	require.NotErrorIs(t, ErrNotFound, ErrForbidden)
}
