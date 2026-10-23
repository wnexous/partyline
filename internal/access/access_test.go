package access

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
// whatever their position, not the user the room row names as its owner, not the holder of a grant, not
// an admin of another organization, not an admin of the instance. A disabled user has none either.
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
		{"grants of every level, to the user and to everyone, give nothing", None, func(t *testing.T) (string, string) {
			g, u := groupOf(t), member(t, "grantee", orgs.PermissionMember, "")
			f.grant(t, KindRoom, g, u, dto.LevelManage)
			f.grant(t, KindRoom, g, Everyone, dto.LevelPost)
			return g, u
		}},
		{"a grant to a user of no organization gives nothing", None, func(t *testing.T) (string, string) {
			g, u := groupOf(t), f.human(t, uniq("outsider"), dto.RoleMember)
			f.grant(t, KindRoom, g, u, dto.LevelView)
			return g, u
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

// ACL-08, AD-016: instance admins get no implicit access to rooms, agents or connectors; what an
// organization gives them it gives like anyone else.
func TestAdminWithoutMembershipHasNoAccess(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	owner := f.human(t, "owner", dto.RoleMember)
	admin := f.human(t, "admin", dto.RoleAdmin)
	acme := f.org(t, "acme")
	f.join(t, acme, owner, orgs.PermissionAdmin, "")
	room, agent, conn := f.group(t, acme, owner, owner), f.agent(t, owner), f.connector(t, owner)

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

	// A grant gives an admin nothing on a room, like anyone else.
	f.grant(t, KindRoom, room, admin, dto.LevelManage)
	require.Equal(t, None, f.levelOf(t, admin, room))
	// Membership of the organization does.
	f.join(t, acme, admin, orgs.PermissionAdmin, "")
	require.Equal(t, dto.LevelManage, f.levelOf(t, admin, room))
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
	room, agent, conn := f.group(t, acme, owner, owner), f.agent(t, owner), f.connector(t, owner)
	f.grant(t, KindAgent, agent, u, dto.LevelUse)
	f.grant(t, KindConnector, conn, u, dto.LevelUse)
	require.Equal(t, dto.LevelManage, f.levelOf(t, u, room))

	f.disable(t, u, true)
	require.Equal(t, None, f.levelOf(t, u, room), "an admin of the organization")
	levels, err := f.az.GrantedRoomLevels(ctx, u)
	require.NoError(t, err)
	require.Empty(t, levels)
	ok, err := f.az.CanUseAgent(ctx, u, agent)
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = f.az.CanUseConnector(ctx, u, conn)
	require.NoError(t, err)
	require.False(t, ok)

	f.disable(t, owner, true)
	require.Equal(t, None, f.levelOf(t, owner, room), "a disabled creator has no access either")

	f.disable(t, u, false)
	require.Equal(t, dto.LevelManage, f.levelOf(t, u, room), "enabling restores the access")
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

// ACL-07 (inert since AD-016): a visibility preset still replaces the room's grants - none; the listed
// users at their level; everyone at view or post - and still announces it, but a grant gives no access to
// a room any more, so no preset changes anyone's level.
func TestSetVisibilityReplacesGrantsButGivesNoRoomAccess(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	acme := f.org(t, "acme")
	owner := f.human(t, "owner", dto.RoleMember)
	f.join(t, acme, owner, orgs.PermissionAdmin, "")
	b := f.human(t, "bob", dto.RoleMember)
	c := f.human(t, "carol", dto.RoleMember)
	d := f.human(t, "dave", dto.RoleMember)
	for _, u := range []string{b, c, d} {
		f.join(t, acme, u, orgs.PermissionMember, "")
	}
	room := f.group(t, acme, owner, owner)
	sub, cancel := f.bus.Subscribe(16)
	defer cancel()

	noOneElse := func() {
		t.Helper()
		require.Equal(t, []Level{None, None, None, dto.LevelManage},
			[]Level{f.levelOf(t, b, room), f.levelOf(t, c, room), f.levelOf(t, d, room), f.levelOf(t, owner, room)})
	}
	set := func(v Visibility) {
		t.Helper()
		require.NoError(t, f.az.SetVisibility(ctx, owner, KindRoom, room, owner, v))
		require.Equal(t, events.GrantsChanged{ResourceKind: "room", ResourceID: room}, <-sub)
		noOneElse()
	}

	set(Visibility{Mode: dto.VisibilityShared, Users: []UserLevel{{UserID: b, Level: dto.LevelView}, {UserID: c, Level: dto.LevelPost}, {UserID: owner, Level: dto.LevelView}}})
	require.ElementsMatch(t, []string{b + "=view", c + "=post"}, f.grantRows(t, KindRoom, room), "owner entry is skipped")
	v, err := f.az.Sharing(ctx, KindRoom, room)
	require.NoError(t, err)
	require.Equal(t, dto.VisibilityShared, v.Mode)
	require.Equal(t, []UserLevel{
		{UserID: b, Level: dto.LevelView, Username: "bob", DisplayName: "n-" + b[len(b)-6:]},
		{UserID: c, Level: dto.LevelPost, Username: "carol", DisplayName: "n-" + c[len(c)-6:]},
	}, v.Users)

	set(Visibility{Mode: dto.VisibilityPublic, PublicLevel: dto.LevelPost})
	require.Equal(t, []string{"*=post"}, f.grantRows(t, KindRoom, room))
	v, err = f.az.Sharing(ctx, KindRoom, room)
	require.NoError(t, err)
	require.Equal(t, Visibility{Mode: dto.VisibilityPublic, PublicLevel: dto.LevelPost, Users: []UserLevel{}}, v)

	set(Visibility{Mode: dto.VisibilityPublic, PublicLevel: dto.LevelView})
	require.Equal(t, []string{"*=view"}, f.grantRows(t, KindRoom, room))

	set(Visibility{Mode: dto.VisibilityShared, Users: []UserLevel{{UserID: d, Level: dto.LevelManage}}})
	require.Equal(t, []string{d + "=manage"}, f.grantRows(t, KindRoom, room))

	set(Visibility{Mode: dto.VisibilityPrivate, Users: []UserLevel{{UserID: b, Level: dto.LevelView}}})
	require.Empty(t, f.grantRows(t, KindRoom, room), "private removes every grant, even when users are sent")
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

// dmCast is the people and AIs of the DM tests: acme's chart is Admin > Lead > Worker; alice writes to
// the AIs; atlas is positioned (Worker) and bob created it; orion has no position and cara created it; vega
// is orion's kind but also belongs to globex, where gxBoss manages it; nomad has no home organization.
type dmCast struct {
	acme, globex                                            string
	alice, bob, cara, host, boss, lead, peer, admin, gxBoss string
	instanceAdmin, grantee                                  string
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
	c.grantee = in("dave", orgs.PermissionMember, "")

	c.atlas = f.ai(t, c.host)
	f.home(t, c.atlas, c.acme, c.bob, orgs.PermissionMember, worker)
	c.orion = f.ai(t, c.host)
	f.home(t, c.orion, c.acme, c.cara, orgs.PermissionMember, "")
	c.vega = f.ai(t, c.host)
	f.home(t, c.vega, c.acme, c.cara, orgs.PermissionMember, "")
	f.join(t, c.globex, c.vega, orgs.PermissionMember, gxHand)
	c.nomad = f.ai(t, c.host) // no home organization, no creator
	f.grant(t, KindAgent, c.atlas, c.grantee, dto.LevelUse)
	f.grant(t, KindAgent, c.atlas, Everyone, dto.LevelUse)
	return c
}

// DM-08, AD-016, POS-03: a DM is posted to by its two participants and viewed by whoever manages one of
// its AI participants in that AI's home organization. Everybody else has none: not the AI's host, not the
// creator of an AI that holds a position, not a peer, not an admin without a position, not a manager in an
// organization that is not the AI's home, not the holder of a grant, not an admin of the instance. Nobody
// manages a DM.
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
		{withAtlas, c.bob, "the creator of an AI that holds a position", None},
		{withAtlas, c.host, "the user that hosts the AI", None},
		{withAtlas, c.admin, "an admin of the organization without a position", None},
		{withAtlas, c.grantee, "a user granted use on the AI, and everyone", None},
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

// DM-08: grants never give access to a DM, and neither does the owner column of the room row.
func TestDMRoomLevelIgnoresGrantsAndOwnerColumn(t *testing.T) {
	f := newFixture(t)
	c := newDMCast(t, f)
	stranger := f.human(t, "stranger", dto.RoleMember)
	room := f.dm(t, stranger, c.alice, c.atlas) // stranger created the row but is no participant

	// A grant row on a DM can only exist by writing around the API, which refuses them.
	f.grant(t, KindRoom, room, Everyone, dto.LevelManage)
	f.grant(t, KindRoom, room, stranger, dto.LevelManage)
	f.grant(t, KindRoom, room, c.host, dto.LevelManage)
	require.Equal(t, None, f.levelOf(t, stranger, room), "neither the owner column nor a manage grant")
	require.Equal(t, None, f.levelOf(t, c.host, room), "a grant does not make the AI's host a viewer")
	require.Equal(t, dto.LevelView, f.levelOf(t, c.lead, room), "a grant does not change the manager's view")
	require.Equal(t, dto.LevelPost, f.levelOf(t, c.alice, room), "a grant does not raise the participant's post")

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

// DM-06 source: GrantedRoomLevels lists the groups the user is a member of (post) or manages as their
// creator or as an admin of their organization (manage), and the DMs the user takes part in (post); the
// DMs a user only views through an AI are not listed, and neither are the rooms only a grant names.
func TestGrantedRoomLevels(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	c := newDMCast(t, f)
	creator := f.human(t, "creator", dto.RoleMember)
	f.join(t, c.acme, creator, orgs.PermissionMember, "")

	inRoom, made, both, administered := f.group(t, c.acme, creator, creator), f.group(t, c.acme, c.alice, c.alice), f.group(t, c.acme, c.alice, c.alice), f.group(t, c.acme, creator, creator)
	foreign, granted, notMine := f.group(t, c.globex, creator, creator), f.group(t, c.acme, creator, creator), f.group(t, c.acme, creator, creator)
	f.addUserMember(t, inRoom, c.alice)
	f.addUserMember(t, both, c.alice)
	f.addUserMember(t, foreign, c.alice) // a stale membership of a group of an organization alice is not in
	f.grant(t, KindRoom, granted, c.alice, dto.LevelManage)
	f.addUserMember(t, notMine, c.bob)
	withBob := f.dm(t, c.alice, c.alice, c.bob)
	viaAI := f.dm(t, c.alice, c.alice, c.atlas)
	viewed := f.dm(t, c.bob, c.bob, c.atlas) // lead manages atlas: a DM lead only views
	stray := f.dm(t, c.bob, c.bob, c.cara)
	f.grant(t, KindRoom, stray, c.alice, dto.LevelManage) // written around the API

	got, err := f.az.GrantedRoomLevels(ctx, c.alice)
	require.NoError(t, err)
	require.Equal(t, map[string]Level{
		inRoom: dto.LevelPost, made: dto.LevelManage, both: dto.LevelManage, foreign: dto.LevelPost,
		withBob: dto.LevelPost, viaAI: dto.LevelPost,
	}, got, "a creator manages, a member posts, a participant posts; a grant lists nothing")
	require.NotContains(t, got, administered)
	require.NotContains(t, got, granted)
	require.NotContains(t, got, stray)

	// An admin of the organization manages every group of it, and lists none of its DMs.
	ada := f.human(t, "ada", dto.RoleMember)
	f.join(t, c.acme, ada, orgs.PermissionAdmin, "")
	adminLevels, err := f.az.GrantedRoomLevels(ctx, ada)
	require.NoError(t, err)
	require.Equal(t, map[string]Level{
		inRoom: dto.LevelManage, made: dto.LevelManage, both: dto.LevelManage, administered: dto.LevelManage,
		granted: dto.LevelManage, notMine: dto.LevelManage,
	}, adminLevels, "the groups of acme, and not globex's, nor any DM")

	// A manager of an AI lists the DMs he takes part in and not the ones he views.
	leadLevels, err := f.az.GrantedRoomLevels(ctx, c.lead)
	require.NoError(t, err)
	require.Empty(t, leadLevels)
	require.Equal(t, dto.LevelView, f.levelOf(t, c.lead, viewed))
	require.Equal(t, dto.LevelPost, f.levelOf(t, c.bob, viewed))

	f.disable(t, c.alice, true)
	got, err = f.az.GrantedRoomLevels(ctx, c.alice)
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

// The organization checks and the room checks report the same errors: the sentinels of package orgs
// are the ones this package exports, so the API maps both to 404 and 403 alike.
func TestAccessErrorsAreTheOrganizationErrors(t *testing.T) {
	require.Same(t, orgs.ErrNotFound, ErrNotFound)
	require.Same(t, orgs.ErrForbidden, ErrForbidden)
	require.ErrorIs(t, fmt.Errorf("wrapped: %w", orgs.ErrNotFound), ErrNotFound)
	require.NotErrorIs(t, ErrNotFound, ErrForbidden)
}
