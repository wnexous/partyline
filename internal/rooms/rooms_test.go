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
	"github.com/wnexous/partyline/internal/store"
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
	az := access.New(st, bus, access.Options{Now: c.Now})
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

// user creates an account and returns its principal.
func (f *fixture) user(t *testing.T, name string, role dto.Role) authn.Principal {
	t.Helper()
	id := store.NewID()
	f.exec(t, "INSERT INTO users (id, username, display_name, password_hash, role, created_at) VALUES (?, ?, ?, 'x', ?, ?)",
		id, name, strings.ToUpper(name[:1])+name[1:], string(role), t0.UnixMilli())
	return authn.Principal{Kind: authn.KindUser, UserID: id, Role: string(role)}
}

func (f *fixture) agent(t *testing.T, owner authn.Principal, name string) string {
	t.Helper()
	id := store.NewID()
	f.exec(t, "INSERT INTO agents (id, owner_id, name, created_at) VALUES (?, ?, ?, ?)", id, owner.UserID, name, t0.UnixMilli())
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

func (f *fixture) grant(t *testing.T, kind access.Kind, id, subject string, level dto.Level) {
	t.Helper()
	f.exec(t, "INSERT INTO grants (resource_kind, resource_id, subject, level, created_at) VALUES (?, ?, ?, ?, ?)",
		string(kind), id, subject, string(level), t0.UnixMilli())
}

func (f *fixture) room(t *testing.T, owner authn.Principal, name string) Room {
	t.Helper()
	r, err := f.svc.Create(context.Background(), owner, CreateInput{Name: name})
	require.NoError(t, err)
	return r
}

func ptr[T any](v T) *T { return &v }

// ACL-01: the creator owns the room with manage.
func TestCreateRoomMakesCallerOwner(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	sub, cancel := f.bus.Subscribe(8)
	defer cancel()

	r, err := f.svc.Create(ctx, alice, CreateInput{Name: "  Ops  ", Description: "on call", BridgeChats: true})
	require.NoError(t, err)
	require.Equal(t, alice.UserID, r.OwnerID)
	require.Equal(t, dto.LevelManage, r.Level)
	require.Equal(t, "Ops", r.Name)
	require.Equal(t, "on call", r.Description)
	require.True(t, r.BridgeChats)
	require.Equal(t, int64(DefaultRetentionDays), r.RetentionDays)
	require.Equal(t, dto.VisibilityPrivate, r.Visibility)
	require.Equal(t, "alice", r.OwnerUsername)
	require.Equal(t, int64(0), r.LastSeq())
	require.Equal(t, events.RoomChanged{RoomID: r.ID}, <-sub)

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

// ACL-03, ACL-08: without view (strangers, admins without grants, missing rooms) every
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
		_, err = f.svc.Sharing(ctx, p, r.ID)
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

// ACL-02/ACL-04 in the service: a view or post grantee reads but cannot manage.
func TestGranteeWithoutManageIsForbidden(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	_, chat := f.chat(t, alice, "group")
	r := f.room(t, alice, "shared")
	_, err := f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: dto.MemberKindChat, MemberID: chat})
	require.NoError(t, err)

	for _, level := range []dto.Level{dto.LevelView, dto.LevelPost} {
		g := f.user(t, "g"+string(level), dto.RoleMember)
		f.grant(t, access.KindRoom, r.ID, g.UserID, level)
		got, err := f.svc.Get(ctx, g, r.ID)
		require.NoError(t, err)
		require.Equal(t, level, got.Level)
		members, err := f.svc.Members(ctx, g, r.ID)
		require.NoError(t, err)
		require.Len(t, members, 1)

		_, err = f.svc.Update(ctx, g, r.ID, UpdateInput{Name: ptr("x")})
		require.ErrorIs(t, err, access.ErrForbidden)
		require.ErrorIs(t, f.svc.Delete(ctx, g, r.ID), access.ErrForbidden)
		_, err = f.svc.SetSharing(ctx, g, r.ID, access.Visibility{Mode: dto.VisibilityPrivate})
		require.ErrorIs(t, err, access.ErrForbidden)
		_, err = f.svc.AddMember(ctx, g, r.ID, MemberInput{Kind: dto.MemberKindUser, MemberID: g.UserID})
		require.ErrorIs(t, err, access.ErrForbidden)
		_, err = f.svc.UpdateMember(ctx, g, r.ID, members[0].ID, MemberUpdate{Outbound: ptr(false)})
		require.ErrorIs(t, err, access.ErrForbidden)
		require.ErrorIs(t, f.svc.RemoveMember(ctx, g, r.ID, members[0].ID), access.ErrForbidden)
	}
}

// ACL-02: List returns the rooms the user can view with the resolved level.
func TestListVisibleRooms(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.user(t, "bob", dto.RoleMember)
	carol := f.user(t, "carol", dto.RoleMember)
	f.room(t, alice, "a-own")
	shared := f.room(t, alice, "b-shared")
	public := f.room(t, alice, "c-public")
	f.room(t, bob, "d-bob")
	_, err := f.svc.SetSharing(ctx, alice, shared.ID, access.Visibility{Mode: dto.VisibilityShared, Users: []access.UserLevel{{UserID: bob.UserID, Level: dto.LevelPost}}})
	require.NoError(t, err)
	_, err = f.svc.SetSharing(ctx, alice, public.ID, access.Visibility{Mode: dto.VisibilityPublic, PublicLevel: dto.LevelView})
	require.NoError(t, err)

	type entry struct {
		name       string
		level      dto.Level
		visibility dto.Visibility
	}
	listOf := func(p authn.Principal) []entry {
		list, err := f.svc.List(ctx, p)
		require.NoError(t, err)
		out := []entry{}
		for _, r := range list {
			out = append(out, entry{r.Name, r.Level, r.Visibility})
		}
		return out
	}
	require.Equal(t, []entry{
		{"a-own", dto.LevelManage, dto.VisibilityPrivate},
		{"b-shared", dto.LevelManage, dto.VisibilityShared},
		{"c-public", dto.LevelManage, dto.VisibilityPublic},
	}, listOf(alice))
	require.Equal(t, []entry{
		{"b-shared", dto.LevelPost, dto.VisibilityShared},
		{"c-public", dto.LevelView, dto.VisibilityPublic},
		{"d-bob", dto.LevelManage, dto.VisibilityPrivate},
	}, listOf(bob))
	require.Equal(t, []entry{{"c-public", dto.LevelView, dto.VisibilityPublic}}, listOf(carol))
}

// ACL-07: a manage grantee (not only the owner) may change settings and sharing; the owner
// stays the owner.
func TestManagerUpdatesRoom(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.user(t, "bob", dto.RoleMember)
	r := f.room(t, alice, "ops")
	f.grant(t, access.KindRoom, r.ID, bob.UserID, dto.LevelManage)
	sub, cancel := f.bus.Subscribe(8)
	defer cancel()

	got, err := f.svc.Update(ctx, bob, r.ID, UpdateInput{Name: ptr("ops-2"), Description: ptr("d"), BridgeChats: ptr(true), RetentionDays: ptr(7)})
	require.NoError(t, err)
	require.Equal(t, "ops-2", got.Name)
	require.Equal(t, "d", got.Description)
	require.True(t, got.BridgeChats)
	require.Equal(t, int64(7), got.RetentionDays)
	require.Equal(t, alice.UserID, got.OwnerID)
	require.Equal(t, events.RoomChanged{RoomID: r.ID}, <-sub)

	_, err = f.svc.Update(ctx, bob, r.ID, UpdateInput{Name: ptr("")})
	var verr *validation.Error
	require.ErrorAs(t, err, &verr)
	require.Contains(t, verr.Fields, "name")

	v, err := f.svc.SetSharing(ctx, bob, r.ID, access.Visibility{Mode: dto.VisibilityPublic, PublicLevel: dto.LevelPost})
	require.NoError(t, err)
	require.Equal(t, dto.VisibilityPublic, v.Mode)
	require.Equal(t, dto.LevelPost, v.PublicLevel)
}

// ACL-05: adding an agent needs ownership of the agent or a use grant on it.
func TestAddAgentRequiresUse(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.user(t, "bob", dto.RoleMember)
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
	require.True(t, m.Outbound)
	require.Equal(t, events.MembersChanged{RoomID: r.ID}, <-sub)

	_, err = f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: dto.MemberKindAgent, MemberID: bobs})
	require.ErrorIs(t, err, access.ErrForbidden)
	_, err = f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: dto.MemberKindAgent, MemberID: store.NewID()})
	require.ErrorIs(t, err, access.ErrForbidden)

	f.grant(t, access.KindAgent, bobs, alice.UserID, dto.LevelUse)
	m, err = f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: dto.MemberKindAgent, MemberID: bobs})
	require.NoError(t, err)
	require.Equal(t, "bob", m.AgentOwner)

	// The check is on the caller, not on the room owner: a manager adds their own agent.
	carol := f.user(t, "carol", dto.RoleMember)
	f.grant(t, access.KindRoom, r.ID, carol.UserID, dto.LevelManage)
	_, err = f.svc.AddMember(ctx, carol, r.ID, MemberInput{Kind: dto.MemberKindAgent, MemberID: f.agent(t, carol, "c")})
	require.NoError(t, err)
}

// ACL-06: adding a chat needs ownership of its connector or a use grant on it.
func TestAddChatRequiresConnectorUse(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.user(t, "bob", dto.RoleMember)
	_, mine := f.chat(t, alice, "family")
	bobConn, bobs := f.chat(t, bob, "team")
	r := f.room(t, alice, "ops")

	m, err := f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: dto.MemberKindChat, MemberID: mine, Outbound: ptr(false)})
	require.NoError(t, err)
	require.Equal(t, "family", m.Name)
	require.Equal(t, "telegram", m.ConnectorKind)
	require.Equal(t, "group", m.ChatKind)
	require.False(t, m.Outbound)

	_, err = f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: dto.MemberKindChat, MemberID: bobs})
	require.ErrorIs(t, err, access.ErrForbidden)
	_, err = f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: dto.MemberKindChat, MemberID: store.NewID()})
	require.ErrorIs(t, err, access.ErrForbidden)

	f.grant(t, access.KindConnector, bobConn, alice.UserID, dto.LevelUse)
	_, err = f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: dto.MemberKindChat, MemberID: bobs})
	require.NoError(t, err)
}

func TestAddMemberValidationAndDuplicates(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.user(t, "bob", dto.RoleMember)
	r := f.room(t, alice, "ops")

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
	_, chat := f.chat(t, alice, "family")
	r := f.room(t, alice, "ops")
	other := f.room(t, alice, "other")
	m, err := f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: dto.MemberKindChat, MemberID: chat})
	require.NoError(t, err)

	m, err = f.svc.UpdateMember(ctx, alice, r.ID, m.ID, MemberUpdate{Outbound: ptr(false)})
	require.NoError(t, err)
	require.False(t, m.Outbound)
	_, err = f.svc.UpdateMember(ctx, alice, other.ID, m.ID, MemberUpdate{Outbound: ptr(true)})
	require.ErrorIs(t, err, access.ErrNotFound, "member ids are scoped to their room")

	require.ErrorIs(t, f.svc.RemoveMember(ctx, alice, other.ID, m.ID), access.ErrNotFound)
	require.NoError(t, f.svc.RemoveMember(ctx, alice, r.ID, m.ID))
	members, err := f.svc.Members(ctx, alice, r.ID)
	require.NoError(t, err)
	require.Empty(t, members)
	require.ErrorIs(t, f.svc.RemoveMember(ctx, alice, r.ID, m.ID), access.ErrNotFound)
}

// ROOM-02: deleting a room deletes its messages, attachments, cursors, outbox entries,
// members and grants, and nothing of other rooms.
func TestDeleteRoomRemovesEverything(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.user(t, "bob", dto.RoleMember)
	agent := f.agent(t, alice, "builder")
	_, chat := f.chat(t, alice, "family")
	doomed := f.room(t, alice, "doomed")
	kept := f.room(t, alice, "kept")
	for _, r := range []Room{doomed, kept} {
		_, err := f.svc.AddMember(ctx, alice, r.ID, MemberInput{Kind: dto.MemberKindChat, MemberID: chat})
		require.NoError(t, err)
		_, err = f.svc.SetSharing(ctx, alice, r.ID, access.Visibility{Mode: dto.VisibilityShared, Users: []access.UserLevel{{UserID: bob.UserID, Level: dto.LevelView}}})
		require.NoError(t, err)
		msg := store.NewID()
		f.exec(t, "INSERT INTO messages (id, room_id, seq, author_kind, author_id, text, created_at) VALUES (?, ?, 1, 'user', ?, 'hi', ?)", msg, r.ID, alice.UserID, t0.UnixMilli())
		f.exec(t, "INSERT INTO attachments (id, message_id, sha256, mime, name, size, created_at) VALUES (?, ?, 'abc', 'text/plain', 'a.txt', 3, ?)", store.NewID(), msg, t0.UnixMilli())
		f.exec(t, "INSERT INTO outbox (id, message_id, chat_id, next_attempt_at, updated_at) VALUES (?, ?, ?, ?, ?)", store.NewID(), msg, chat, t0.UnixMilli(), t0.UnixMilli())
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
			"grants":      f.count(t, "SELECT COUNT(*) FROM grants WHERE resource_kind = 'room' AND resource_id = ?", roomID),
			"messages":    f.count(t, "SELECT COUNT(*) FROM messages WHERE room_id = ?", roomID),
			"attachments": f.count(t, "SELECT COUNT(*) FROM attachments a JOIN messages m ON m.id = a.message_id WHERE m.room_id = ?", roomID),
			"outbox":      f.count(t, "SELECT COUNT(*) FROM outbox o JOIN messages m ON m.id = o.message_id WHERE m.room_id = ?", roomID),
			"cursors":     f.count(t, "SELECT COUNT(*) FROM agent_cursors WHERE room_id = ?", roomID),
		}
	}
	require.Equal(t, map[string]int{"rooms": 0, "members": 0, "grants": 0, "messages": 0, "attachments": 0, "outbox": 0, "cursors": 0}, countFor(doomed.ID))
	require.Equal(t, map[string]int{"rooms": 1, "members": 1, "grants": 1, "messages": 1, "attachments": 1, "outbox": 1, "cursors": 1}, countFor(kept.ID))
	// Orphaned rows would hide behind the joins above: check the totals too.
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM attachments"))
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM outbox"))

	_, err := f.svc.Get(ctx, alice, doomed.ID)
	require.ErrorIs(t, err, access.ErrNotFound)
}
