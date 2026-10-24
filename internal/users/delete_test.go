package users

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// removal is a fixture whose service reports to a bus and to a Kick that records the AIs it was asked to
// disconnect, as the app wires it.
type removal struct {
	*fixture
	bus  *events.Bus
	mu   sync.Mutex
	kick []string
	root db.User // the first admin: the oldest active instance admin
}

func newRemoval(t *testing.T) *removal {
	t.Helper()
	f := newFixture(t)
	r := &removal{fixture: f, bus: events.NewBus()}
	svc, err := NewService(f.store, Options{Now: f.clock.Now, Bus: r.bus, Kick: func(_ context.Context, agentID string) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.kick = append(r.kick, agentID)
	}})
	require.NoError(t, err)
	f.svc = svc
	f.setupAdmin(t)
	r.root = f.user(t, "admin")
	return r
}

func (f *fixture) user(t *testing.T, username string) db.User {
	t.Helper()
	u, err := f.store.R().GetUserByUsername(context.Background(), username)
	require.NoError(t, err)
	return u
}

// account creates a member (or admin) account a little later than the previous one, so "the oldest" is clear.
func (r *removal) account(t *testing.T, username string, role dto.Role) db.User {
	t.Helper()
	r.clock.Advance(time.Hour)
	u, err := r.svc.CreateUser(context.Background(), AccountInput{Username: username, Password: memberPassword, DisplayName: username}, role)
	require.NoError(t, err)
	return u
}

func (r *removal) kicked() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.kick...)
}

func (f *fixture) execArgs(t *testing.T, query string, args ...any) {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+f.path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	_, err = raw.Exec(query, args...)
	require.NoError(t, err, query)
}

func (f *fixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+f.path+"?mode=ro&_pragma=busy_timeout(5000)")
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	var n int
	require.NoError(t, raw.QueryRow(query, args...).Scan(&n), query)
	return n
}

// agentRow reads the owner, folder and v0.3 name of an AI.
func (f *fixture) agentRow(t *testing.T, id string) (owner, folder, name string) {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+f.path+"?mode=ro&_pragma=busy_timeout(5000)")
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	require.NoError(t, raw.QueryRow(`SELECT owner_id, folder, name FROM agents WHERE id = ?`, id).Scan(&owner, &folder, &name))
	return owner, folder, name
}

func (f *fixture) personal(t *testing.T, u db.User) string {
	t.Helper()
	o, err := f.store.R().GetPersonalOrg(context.Background(), u.ID)
	require.NoError(t, err)
	return o.ID
}

func (f *fixture) newOrg(t *testing.T, creator db.User, name string) string {
	t.Helper()
	m, err := orgs.NewService(orgs.Deps{Store: f.store, Now: f.clock.Now}).Create(context.Background(), creator.ID, name)
	require.NoError(t, err)
	return m.ID
}

func (f *fixture) join(t *testing.T, org, iid string, perm orgs.Permission, position, inviter string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, f.store.WithTx(ctx, func(q *db.Queries) error {
		return orgs.AddMember(ctx, q, orgs.AddMemberInput{OrgID: org, IID: iid, Permission: perm, PositionID: position, InviterIID: inviter, Now: f.clock.Now()})
	}))
}

func (f *fixture) position(t *testing.T, org, name string) string {
	t.Helper()
	id := store.NewID()
	f.execArgs(t, `INSERT INTO positions (id, org_id, name, created_at) VALUES (?, ?, ?, 1)`, id, org, name)
	return id
}

// ai hosts an AI of the user: folder AIs have their id as their v0.3 name.
func (f *fixture) ai(t *testing.T, host db.User, folder, handle, org string) string {
	t.Helper()
	id := store.NewID()
	f.execArgs(t, `INSERT INTO agents (id, owner_id, name, created_at, harness, machine, folder, org_id, creator_iid) VALUES (?, ?, ?, 1, 'claude', 'devbox', ?, ?, ?)`,
		id, host.ID, id, folder, org, host.ID)
	f.execArgs(t, `INSERT INTO intelligences (id, kind, display_name, handle, created_at, updated_at) VALUES (?, 'ai', ?, ?, 1, 1)`, id, handle, handle)
	f.join(t, org, id, orgs.PermissionMember, "", "")
	return id
}

func (f *fixture) group(t *testing.T, owner db.User, org, name string, members ...string) string {
	t.Helper()
	id := store.NewID()
	f.execArgs(t, `INSERT INTO rooms (id, owner_id, name, created_at, updated_at, kind, org_id, creator_iid) VALUES (?, ?, ?, 1, 1, 'group', ?, ?)`,
		id, owner.ID, name, org, owner.ID)
	for _, m := range members {
		kind := "user"
		if f.count(t, `SELECT COUNT(*) FROM agents WHERE id = ?`, m) > 0 {
			kind = "agent"
		}
		f.execArgs(t, `INSERT INTO room_members (id, room_id, kind, member_id, added_by, created_at) VALUES (?, ?, ?, ?, ?, 1)`, store.NewID(), id, kind, m, owner.ID)
	}
	return id
}

func (f *fixture) dm(t *testing.T, a, b db.User) string {
	t.Helper()
	id := store.NewID()
	lo, hi := a.ID, b.ID
	if lo > hi {
		lo, hi = hi, lo
	}
	f.execArgs(t, `INSERT INTO rooms (id, owner_id, name, created_at, updated_at, kind, dm_a, dm_b) VALUES (?, ?, '', 1, 1, 'dm', ?, ?)`, id, a.ID, lo, hi)
	for _, u := range []db.User{a, b} {
		f.execArgs(t, `INSERT INTO room_members (id, room_id, kind, member_id, added_by, created_at) VALUES (?, ?, 'user', ?, ?, 1)`, store.NewID(), id, u.ID, a.ID)
	}
	return id
}

func (f *fixture) connector(t *testing.T, owner db.User, org, name string) string {
	t.Helper()
	id := store.NewID()
	f.execArgs(t, `INSERT INTO connectors (id, owner_id, kind, name, created_at, updated_at, org_id) VALUES (?, ?, 'webhook', ?, 1, 1, ?)`, id, owner.ID, name, org)
	return id
}

func (f *fixture) memberOf(t *testing.T, org, iid string) (orgs.Member, bool) {
	t.Helper()
	m, ok, err := orgs.NewAuthz(f.store).Member(context.Background(), org, iid)
	require.NoError(t, err)
	return m, ok
}

func (f *fixture) auditActions(t *testing.T) map[string][]map[string]any {
	t.Helper()
	rows, _, err := f.svc.audit.List(context.Background(), "")
	require.NoError(t, err)
	out := map[string][]map[string]any{}
	for _, e := range rows {
		var detail map[string]any
		require.NoError(t, json.Unmarshal([]byte(e.DetailJson), &detail))
		detail["_actor"], detail["_target"] = e.ActorID, e.TargetID
		out[e.Action] = append(out[e.Action], detail)
	}
	return out
}

func requireValidationOn(t *testing.T, err error, field string) {
	t.Helper()
	var verr *ValidationError
	require.ErrorAs(t, err, &verr, "got %v", err)
	require.Contains(t, verr.Fields, field)
}

// POS-06: the password comes first. A wrong password is 422 on the field password - whoever asks, the last
// instance admin included - and changes nothing; with the right one the last instance admin is refused with
// ErrLastInstanceAdmin, and changes nothing either. Each refusal is the only thing wrong (L-018).
func TestDeleteAccountChecksThePasswordBeforeTheLastAdminRule(t *testing.T) {
	ctx := context.Background()
	r := newRemoval(t)
	bob := r.account(t, "bob", dto.RoleMember)

	for name, c := range map[string]struct {
		user     db.User
		password string
	}{
		"a member with a wrong password":          {bob, "not-the-password"},
		"a member with an empty password":         {bob, ""},
		"the last instance admin, wrong password": {r.root, "not-the-password"},
		"the last instance admin, empty password": {r.root, ""},
	} {
		t.Run(name, func(t *testing.T) {
			err := r.svc.DeleteAccount(ctx, c.user.ID, c.password)
			requireValidationOn(t, err, "password")
			require.NotErrorIs(t, err, ErrLastInstanceAdmin)
			require.Equal(t, 1, r.count(t, `SELECT COUNT(*) FROM users WHERE id = ?`, c.user.ID))
		})
	}

	// the last instance admin, right password: refused for being the last, and nothing changes
	err := r.svc.DeleteAccount(ctx, r.root.ID, adminPassword)
	require.ErrorIs(t, err, ErrLastInstanceAdmin)
	require.Equal(t, 1, r.count(t, `SELECT COUNT(*) FROM users WHERE id = ?`, r.root.ID))
	require.Equal(t, 1, r.count(t, `SELECT COUNT(*) FROM org_members WHERE iid = ?`, r.root.ID))
	require.Empty(t, r.auditActions(t)["user.delete"], "a refused deletion is not audited as a deletion")

	// a second admin lifts the refusal for the first; then the remaining one is the last
	second := r.account(t, "second", dto.RoleAdmin)
	require.NoError(t, r.svc.DeleteAccount(ctx, r.root.ID, adminPassword))
	require.ErrorIs(t, r.svc.DeleteAccount(ctx, second.ID, memberPassword), ErrLastInstanceAdmin)
}

// OUPG-02: an organization whose only admin deletes their account gets the heir - the oldest active instance
// admin other than the user - as its admin; an organization with another admin is left alone. Disabled admins
// are not heirs however old, and an heir who already belongs becomes an admin keeping their position.
func TestTheHeirBecomesAdminOfEachOrganizationTheUserWasTheLastAdminOf(t *testing.T) {
	ctx := context.Background()
	r := newRemoval(t)
	retired := r.account(t, "retired", dto.RoleAdmin)
	heir := r.account(t, "heir", dto.RoleAdmin)
	younger := r.account(t, "younger", dto.RoleAdmin)
	bob := r.account(t, "bob", dto.RoleMember)
	carol := r.account(t, "carol", dto.RoleMember)
	// the first two admins are older than the heir but no longer active
	for _, old := range []db.User{r.root, retired} {
		_, err := r.svc.UpdateUser(ctx, old.ID, UserUpdate{Disabled: ptr(true)})
		require.NoError(t, err)
	}

	personal := r.personal(t, bob)
	solo := r.newOrg(t, bob, "Solo")                    // bob is its only admin; carol is a member
	pair := r.newOrg(t, bob, "Pair")                    // bob and carol are admins
	guestHeir := r.newOrg(t, bob, "Guest heir")         // the heir is a guest of it, invited by bob
	keepsPosition := r.newOrg(t, bob, "Keeps position") // the heir is a member holding a position
	notAdmin := r.newOrg(t, carol, "Carols")            // bob is only a member of it
	r.join(t, solo, carol.ID, orgs.PermissionMember, "", "")
	r.join(t, pair, carol.ID, orgs.PermissionAdmin, "", "")
	r.join(t, guestHeir, heir.ID, orgs.PermissionGuest, "", bob.ID)
	lead := r.position(t, keepsPosition, "Lead")
	r.join(t, keepsPosition, heir.ID, orgs.PermissionMember, lead, "")
	r.join(t, notAdmin, bob.ID, orgs.PermissionMember, "", "")

	require.NoError(t, r.svc.DeleteAccount(ctx, bob.ID, memberPassword))

	for name, org := range map[string]string{"bob's personal organization": personal, "an organization he alone administered": solo} {
		m, ok := r.memberOf(t, org, heir.ID)
		require.True(t, ok, name)
		require.Equal(t, orgs.Member{IID: heir.ID, Permission: orgs.PermissionAdmin}, m, "%s: the heir is an admin with no position", name)
	}
	m, _ := r.memberOf(t, guestHeir, heir.ID)
	require.Equal(t, orgs.PermissionAdmin, m.Permission, "a guest heir becomes an admin")
	require.Empty(t, m.InviterIID, "and is nobody's guest any more")
	m, _ = r.memberOf(t, keepsPosition, heir.ID)
	require.Equal(t, orgs.PermissionAdmin, m.Permission)
	require.Equal(t, lead, m.PositionID, "an heir who held a position keeps it")

	_, ok := r.memberOf(t, pair, heir.ID)
	require.False(t, ok, "an organization with another admin gets no heir")
	_, ok = r.memberOf(t, notAdmin, heir.ID)
	require.False(t, ok, "nor one he was only a member of")
	for name, other := range map[string]db.User{"a disabled admin": r.root, "a younger admin": younger} {
		_, ok = r.memberOf(t, solo, other.ID)
		require.False(t, ok, "%s is not the heir", name)
	}

	inherited := r.auditActions(t)["org.admin_inherited"]
	require.Len(t, inherited, 4, "one entry per organization that got an heir")
	targets := []string{}
	for _, e := range inherited {
		require.Equal(t, bob.ID, e["_actor"], "the user who left")
		require.Equal(t, heir.ID, e["heir"])
		targets = append(targets, e["_target"].(string))
	}
	require.ElementsMatch(t, []string{personal, solo, guestHeir, keepsPosition}, targets)
}

// POS-06, AD-019: nothing an organization owns goes with the account. The groups and connections the user created
// stay, owned by the heir; the AIs hosted on the user's computers stay in their organizations with their positions,
// as unlinked identities owned by the heir, offline - and the heir's sessions never adopt them (MIG-02); what was
// the user's alone goes: their group memberships and their DMs, with the rest of the account.
func TestWhatTheUserOwnedForAnOrganizationStays(t *testing.T) {
	ctx := context.Background()
	r := newRemoval(t)
	bob := r.account(t, "bob", dto.RoleMember)
	carol := r.account(t, "carol", dto.RoleMember)
	team := r.newOrg(t, bob, "Team")
	r.join(t, team, carol.ID, orgs.PermissionMember, "", "")
	dev := r.position(t, team, "Dev")

	atlas := r.ai(t, bob, "/work/atlas", "atlas", team)
	notes := r.ai(t, bob, "/work/notes", "notes", team)
	_, err := orgs.NewService(orgs.Deps{Store: r.store, Now: r.clock.Now}).SetPosition(ctx, bob.ID, team, notes, dev)
	require.NoError(t, err)
	support := r.group(t, bob, team, "Support", bob.ID, carol.ID, atlas)
	other := r.group(t, carol, team, "Carols", carol.ID, bob.ID)
	dm := r.dm(t, bob, carol)
	conn := r.connector(t, bob, team, "Support line")
	r.execArgs(t, `UPDATE connectors SET operator_iid = ? WHERE id = ?`, atlas, conn)

	require.NoError(t, r.svc.DeleteAccount(ctx, bob.ID, memberPassword))

	// the account is gone, and so is what was only the user's
	require.Zero(t, r.count(t, `SELECT COUNT(*) FROM users WHERE id = ?`, bob.ID))
	require.Zero(t, r.count(t, `SELECT COUNT(*) FROM intelligences WHERE id = ?`, bob.ID))
	require.Zero(t, r.count(t, `SELECT COUNT(*) FROM rooms WHERE id = ?`, dm), "the user's DMs go with them")
	require.Zero(t, r.count(t, `SELECT COUNT(*) FROM room_members WHERE kind = 'user' AND member_id = ?`, bob.ID), "and their group memberships")
	require.Equal(t, 2, r.count(t, `SELECT COUNT(*) FROM room_members WHERE room_id = ?`, support), "the group keeps its other members")
	require.Equal(t, 1, r.count(t, `SELECT COUNT(*) FROM room_members WHERE room_id = ? AND member_id = ?`, other, carol.ID))

	// the groups and the connection stay, owned by the heir, in their organization
	for _, room := range []string{support, other} {
		require.Equal(t, 1, r.count(t, `SELECT COUNT(*) FROM rooms WHERE id = ? AND org_id = ?`, room, team), "the group stays in its organization")
	}
	require.Equal(t, 1, r.count(t, `SELECT COUNT(*) FROM rooms WHERE id = ? AND owner_id = ?`, support, r.root.ID), "owned by the heir")
	require.Equal(t, 1, r.count(t, `SELECT COUNT(*) FROM rooms WHERE id = ? AND owner_id = ?`, other, carol.ID), "another owner's group is not touched")
	require.Zero(t, r.count(t, `SELECT COUNT(*) FROM rooms WHERE id = ? AND creator_iid IS NOT NULL`, support), "nobody is its creator any more")
	require.Equal(t, 1, r.count(t, `SELECT COUNT(*) FROM connectors WHERE id = ? AND org_id = ? AND owner_id = ? AND operator_iid = ?`, conn, team, r.root.ID, atlas), "the connection and its operator stay")

	// the hosted AIs: owned by the heir, unlinked, still members of their organization with their position
	for _, ai := range []string{atlas, notes} {
		owner, folder, name := r.agentRow(t, ai)
		require.Equal(t, r.root.ID, owner, "owned by the heir")
		require.Empty(t, folder, "unlinked")
		require.Equal(t, ai, name, "named by its id, which no one else uses")
		m, ok := r.memberOf(t, team, ai)
		require.True(t, ok, "still a member of its organization")
		require.Empty(t, m.CreatorIID, "its creator is gone")
	}
	m, _ := r.memberOf(t, team, notes)
	require.Equal(t, dev, m.PositionID, "with its position")
	require.ElementsMatch(t, []string{atlas, notes}, r.kicked(), "their live sessions are ended")
}

// A hosted AI whose v0.3 name the heir also uses does not stop the deletion: the name is only a label of the
// upgrade, and the AI gets its id as name, which is unique.
func TestAHostedAIWhoseNameTheHeirAlsoUsesIsRenamedNotRefused(t *testing.T) {
	ctx := context.Background()
	r := newRemoval(t)
	bob := r.account(t, "bob", dto.RoleMember)
	bobsOrg := r.personal(t, bob)
	rootsOrg := r.personal(t, r.root)
	clash := r.ai(t, bob, "", "billing-bob", bobsOrg)
	r.execArgs(t, `UPDATE agents SET name = 'billing' WHERE id = ?`, clash)
	mine := r.ai(t, r.root, "", "billing-root", rootsOrg)
	r.execArgs(t, `UPDATE agents SET name = 'billing' WHERE id = ?`, mine)

	require.NoError(t, r.svc.DeleteAccount(ctx, bob.ID, memberPassword))

	require.Equal(t, 1, r.count(t, `SELECT COUNT(*) FROM agents WHERE id = ? AND owner_id = ? AND name = id`, clash, r.root.ID))
	require.Equal(t, 1, r.count(t, `SELECT COUNT(*) FROM agents WHERE id = ? AND name = 'billing'`, mine), "the heir's own AI keeps its name")
}

// The account is deleted in one transaction: when the last step fails nothing before it stays - not the heir's
// admin membership, not the re-pointed owners, not the AIs' new owner, not the removed memberships - and no
// event or kick goes out.
func TestADeletionThatFailsChangesNothing(t *testing.T) {
	ctx := context.Background()
	r := newRemoval(t)
	bob := r.account(t, "bob", dto.RoleMember)
	team := r.newOrg(t, bob, "Team")
	atlas := r.ai(t, bob, "/work/atlas", "atlas", team)
	support := r.group(t, bob, team, "Support", bob.ID)
	conn := r.connector(t, bob, team, "Line")
	sess, err := r.svc.CreateSession(ctx, bob.ID, "127.0.0.1", "test")
	require.NoError(t, err)
	r.execArgs(t, `CREATE TRIGGER forced_failure BEFORE DELETE ON users WHEN OLD.username = 'bob' BEGIN SELECT RAISE(ABORT, 'forced failure'); END`)
	sub, cancel := r.bus.Subscribe(64)
	defer cancel()

	require.Error(t, r.svc.DeleteAccount(ctx, bob.ID, memberPassword))

	_, ok := r.memberOf(t, team, r.root.ID)
	require.False(t, ok, "the heir did not become an admin")
	require.Equal(t, 1, r.count(t, `SELECT COUNT(*) FROM rooms WHERE id = ? AND owner_id = ?`, support, bob.ID))
	require.Equal(t, 1, r.count(t, `SELECT COUNT(*) FROM connectors WHERE id = ? AND owner_id = ?`, conn, bob.ID))
	require.Equal(t, 1, r.count(t, `SELECT COUNT(*) FROM agents WHERE id = ? AND owner_id = ? AND folder = '/work/atlas'`, atlas, bob.ID))
	require.Equal(t, 1, r.count(t, `SELECT COUNT(*) FROM room_members WHERE room_id = ? AND member_id = ?`, support, bob.ID))
	require.Equal(t, 1, r.count(t, `SELECT COUNT(*) FROM sessions WHERE id = ?`, sess.ID))
	require.Empty(t, r.auditActions(t)["user.delete"])
	require.Empty(t, r.auditActions(t)["org.admin_inherited"])
	require.Empty(t, r.kicked(), "nobody is kicked")
	select {
	case e := <-sub:
		require.Fail(t, "an event went out for a deletion that failed", "%#v", e)
	default:
	}
}

// After the commit the user's credentials end at once (a session, a user token and an agent token no longer
// authenticate, and a login fails like any unknown account's), every organization the user belonged to or
// inherited is told, the groups that lost a member and the DMs that vanished are announced, and the deletion is
// audited with the user as actor and no secret in the entry.
func TestDeletingAnAccountEndsItsCredentialsAndTellsEveryone(t *testing.T) {
	ctx := context.Background()
	r := newRemoval(t)
	bob := r.account(t, "bob", dto.RoleMember)
	carol := r.account(t, "carol", dto.RoleMember)
	team := r.newOrg(t, bob, "Team")
	shared := r.newOrg(t, carol, "Shared")
	r.join(t, shared, bob.ID, orgs.PermissionMember, "", "")
	r.join(t, team, carol.ID, orgs.PermissionMember, "", "")
	personal := r.personal(t, bob)
	group := r.group(t, carol, shared, "Chat", carol.ID, bob.ID)
	dm := r.dm(t, bob, carol)
	atlas := r.ai(t, bob, "/work/atlas", "atlas", team)
	// an organization bob is not in, where an AI he hosts lives: it loses its creator, so who manages it changes
	faraway := r.newOrg(t, carol, "Faraway")
	remote := r.ai(t, bob, "/work/remote", "remote", faraway)

	sess, err := r.svc.CreateSession(ctx, bob.ID, "127.0.0.1", "test")
	require.NoError(t, err)
	_, userSecret := r.token(t, bob.ID, dto.TokenKindUser, nil)
	_, agentSecret := r.token(t, bob.ID, dto.TokenKindAgent, nil)
	bus, cancel := r.bus.Subscribe(64)
	defer cancel()

	require.NoError(t, r.svc.DeleteAccount(authn.WithPrincipal(ctx, authn.Principal{Kind: authn.KindUser, UserID: bob.ID}), bob.ID, memberPassword))

	_, err = r.svc.ValidateSession(ctx, sess.Secret)
	require.ErrorIs(t, err, ErrUnauthenticated, "the session")
	for name, secret := range map[string]string{"the user token": userSecret, "the agent token": agentSecret} {
		_, err := r.svc.AuthenticateToken(ctx, secret)
		require.ErrorIs(t, err, ErrUnauthenticated, name)
	}
	_, err = login(r.fixture, "127.0.0.1", "bob", memberPassword)
	require.ErrorIs(t, err, ErrInvalidCredentials)
	require.Zero(t, r.count(t, `SELECT COUNT(*) FROM api_tokens WHERE user_id = ?`, bob.ID))

	var got []events.Event
	for drained := false; !drained; {
		select {
		case e := <-bus:
			got = append(got, e)
		default:
			drained = true
		}
	}
	require.Contains(t, got, events.SessionsRevoked{UserID: bob.ID, SessionIDs: []string{sess.ID}, UserDisabled: true})
	for _, org := range []string{team, shared, personal} {
		require.Contains(t, got, events.OrgChanged{OrgID: org}, "every organization the user belonged to")
	}
	require.Contains(t, got, events.OrgChanged{OrgID: faraway}, "and the one a hosted AI lives in, whose creator link is gone")
	require.NotContains(t, got, events.OrgChanged{OrgID: r.personal(t, carol)}, "but not the ones that did not change")
	require.Contains(t, got, events.MembersChanged{RoomID: group}, "the group that lost a member")
	require.Contains(t, got, events.RoomChanged{RoomID: dm, Deleted: true}, "the DM that went with the account")
	require.ElementsMatch(t, []string{atlas, remote}, r.kicked())

	entries := r.auditActions(t)["user.delete"]
	require.Len(t, entries, 1)
	require.Equal(t, bob.ID, entries[0]["_actor"], "the user deleted their own account")
	require.Equal(t, bob.ID, entries[0]["_target"])
	require.NotContains(t, fmt.Sprint(entries[0]), memberPassword)
	require.NotContains(t, fmt.Sprint(entries[0]), userSecret)
}

func ptr[T any](v T) *T { return &v }
