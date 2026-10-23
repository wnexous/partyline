package orgs

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
)

// createPersonal runs CreatePersonal in a transaction.
func (f *fixture) createPersonal(user, name, username string) Membership {
	f.t.Helper()
	ctx := context.Background()
	var m Membership
	require.NoError(f.t, f.st.WithTx(ctx, func(q *db.Queries) (err error) {
		m, err = CreatePersonal(ctx, q, PersonalParams{UserID: user, Name: name, Username: username, Now: f.clock.Now()})
		return err
	}))
	return m
}

// joinAt makes iid a member joined at the given time (unix milliseconds).
func (f *fixture) joinAt(org, iid string, perm Permission, joinedAt int64) {
	f.t.Helper()
	f.exec(`INSERT INTO org_members (org_id, iid, permission, joined_at) VALUES (?, ?, ?, ?)`, org, iid, string(perm), joinedAt)
}

func (f *fixture) defaultFor(iid string) string {
	f.t.Helper()
	ctx := context.Background()
	var got string
	require.NoError(f.t, f.st.WithTx(ctx, func(q *db.Queries) (err error) {
		got, err = DefaultFor(ctx, q, iid)
		return err
	}))
	return got
}

// AD-021, ORG-04: a new account gets an organization named after it, with the account as its admin
// holding the root position Admin, and it is the account's default organization.
func TestCreatePersonalMakesTheAccountsOwnOrganization(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user("alice")

	m := f.createPersonal(alice, "Alice A.", "alice")

	require.Equal(t, "Alice A.", m.Name)
	require.Equal(t, "alice", m.Slug, "the address derives from the username")
	require.Equal(t, alice, m.CreatedBy)
	require.Equal(t, PermissionAdmin, m.Permission)
	require.Equal(t, t0.UnixMilli(), m.CreatedAt)
	c, err := Load(ctx, f.st.R(), m.ID)
	require.NoError(t, err)
	require.Equal(t, map[string]Position{m.PositionID: {ID: m.PositionID, Name: "Admin"}}, c.Positions, "one root position, Admin")
	require.Equal(t, map[string]Member{alice: {IID: alice, Permission: PermissionAdmin, PositionID: m.PositionID}}, c.Members)
	require.Equal(t, m.ID, f.str(`SELECT default_org_id FROM users WHERE id = ?`, alice), "and it is the default")
	require.Equal(t, m.ID, f.defaultFor(alice))
	require.Empty(t, f.audited(), "the caller audits (it knows the account)")
}

func TestCreatePersonalNamesAndAddresses(t *testing.T) {
	f := newFixture(t)
	bob, bob2, han := f.user("bob"), f.user("bob_"), f.user("han")
	require.Equal(t, "bob", f.createPersonal(bob, "Bob", "bob").Slug)
	require.Equal(t, "bob-2", f.createPersonal(bob2, "Bob again", "bob_").Slug, "the first free address")
	m := f.createPersonal(han, "", "han")
	require.Equal(t, "han", m.Name, "no display name: the username")
	long := f.createPersonal(f.user("lng"), strings.Repeat("é", 150), "lng")
	require.Equal(t, 100, utf8.RuneCountInString(long.Name), "cut to 100 characters")
	require.Equal(t, "org", f.createPersonal(f.user("han2"), "李雷", "李雷").Slug, "no Latin letter left in the username")
}

// ORG-04: the organization a new AI of a user joins when --org is not given. A user's choice wins; with
// none, the organization they administer longest, else the one they belong to longest (L-041: each
// fallback is shown where the next one would pick differently).
func TestDefaultForAUser(t *testing.T) {
	f := newFixture(t)
	u := f.user("uma")
	a, b, c, d := f.org("a"), f.org("b"), f.org("c"), f.org("d")
	f.joinAt(a, u, PermissionMember, 20) // the oldest member of the list... but not the oldest membership
	f.joinAt(b, u, PermissionAdmin, 50)  // an admin, joined late
	f.joinAt(c, u, PermissionAdmin, 30)  // an admin, joined earlier than b
	f.joinAt(d, u, PermissionGuest, 10)  // the oldest membership of all

	require.Empty(t, f.defaultFor("u-nobody"), "an unknown identity")
	f.exec(`UPDATE users SET default_org_id = ? WHERE id = ?`, b, u)
	require.Equal(t, b, f.defaultFor(u), "the chosen default, though c is the oldest admin and d the oldest membership")

	f.exec(`UPDATE users SET default_org_id = NULL WHERE id = ?`, u)
	require.Equal(t, c, f.defaultFor(u), "no default: the oldest admin membership, not the oldest membership (d) nor the oldest member one (a)")

	f.exec(`UPDATE org_members SET permission = 'member' WHERE iid = ? AND org_id IN (?, ?)`, u, b, c)
	require.Equal(t, d, f.defaultFor(u), "no admin membership: the oldest membership, guests included")

	f.exec(`DELETE FROM org_members WHERE iid = ? AND org_id = ?`, u, d)
	require.Equal(t, a, f.defaultFor(u), "and the next oldest")

	// A default the user no longer belongs to is no default.
	f.exec(`UPDATE users SET default_org_id = ? WHERE id = ?`, d, u)
	require.Equal(t, a, f.defaultFor(u), "d was left: fall back")

	f.exec(`DELETE FROM org_members WHERE iid = ?`, u)
	require.Empty(t, f.defaultFor(u), "no organization at all")
}

func TestDefaultForTiesGoByOrganizationID(t *testing.T) {
	f := newFixture(t)
	u := f.user("uma")
	later, earlier := f.org("z"), f.org("m")
	f.joinAt(later, u, PermissionAdmin, 7)
	f.joinAt(earlier, u, PermissionAdmin, 7)
	require.Equal(t, earlier, f.defaultFor(u), "joined at the same moment: the smaller id")
}

// ORG-04: the default of an AI is its home organization, whatever else it belongs to; an AI without one
// has none.
func TestDefaultForAnAIIsItsHome(t *testing.T) {
	f := newFixture(t)
	owner := f.user("uma")
	home, other := f.org("home"), f.org("other")
	bot := f.ai("bot", owner)
	f.joinAt(other, bot, PermissionAdmin, 1) // older, and an admin
	f.home(bot, home, owner, PermissionMember, "")
	require.Equal(t, home, f.defaultFor(bot))

	homeless := f.ai("homeless", owner)
	f.joinAt(other, homeless, PermissionAdmin, 1)
	require.Empty(t, f.defaultFor(homeless), "no home, no default: it is not picked from its memberships")
}

// ORG-04: a user chooses the default organization among those they belong to; any permission will do.
func TestSetDefaultRequiresMembership(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	globex := f.org("globex")
	for _, who := range []string{a.alice, a.bob, a.gus} {
		require.NoError(t, f.svc.SetDefault(ctx, who, a.org), who)
		require.Equal(t, a.org, f.str(`SELECT default_org_id FROM users WHERE id = ?`, who))
		require.Equal(t, a.org, f.defaultFor(who))
	}
	require.NoError(t, f.svc.SetDefault(ctx, a.bob, a.org), "the same again")

	for name, tc := range map[string]struct{ who, org string }{
		"a user who is not a member":          {a.bob, globex},
		"an organization that does not exist": {a.bob, "o-nothing"},
		"no organization":                     {a.bob, ""},
		"an AI, which has no default to set":  {a.notes, a.org},
		"an identity that does not exist":     {"u-nobody", a.org},
	} {
		require.ErrorIs(t, f.svc.SetDefault(ctx, tc.who, tc.org), ErrForbidden, name)
	}
	require.Equal(t, a.org, f.str(`SELECT default_org_id FROM users WHERE id = ?`, a.bob), "a refusal changes nothing")
	require.Empty(t, f.str(`SELECT default_org_id FROM users WHERE id = ?`, f.user("zed")))

	// Changing it takes effect at once.
	f.join(globex, a.bob, PermissionMember, "", "")
	require.NoError(t, f.svc.SetDefault(ctx, a.bob, globex))
	require.Equal(t, globex, f.defaultFor(a.bob))
}
