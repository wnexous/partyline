package orgs

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
)

// acme builds the spec's example chart in one organization and returns it with its people:
//
//	Admin (alice) ─ Director (dora) ─ Manager (mia) ─ Lead developer (lee) ─ Worktrees (walt)
//	                                └─ Designer (dee)
//
// plus bob, a member with no position, and gus, a guest invited by bob. notes is an AI created by
// lee with no position; w1 is an AI created by lee and held in Worktrees.
type acmeChart struct {
	org                                   string
	alice, dora, mia, lee, walt, dee, bob string
	gus, notes, w1                        string
	admin, director, manager, lead, trees string
	designer                              string
}

func (f *fixture) acme() acmeChart {
	f.t.Helper()
	a := acmeChart{org: f.org("acme")}
	a.admin = f.position(a.org, "Admin", "")
	a.director = f.position(a.org, "Director", a.admin)
	a.manager = f.position(a.org, "Manager", a.director)
	a.lead = f.position(a.org, "Lead", a.manager)
	a.trees = f.position(a.org, "Worktrees", a.lead)
	a.designer = f.position(a.org, "Designer", a.director)
	a.alice, a.dora, a.mia, a.lee = f.user("alice"), f.user("dora"), f.user("mia"), f.user("lee")
	a.walt, a.dee, a.bob, a.gus = f.user("walt"), f.user("dee"), f.user("bob"), f.user("gus")
	f.join(a.org, a.alice, PermissionAdmin, a.admin, "")
	f.join(a.org, a.dora, PermissionMember, a.director, "")
	f.join(a.org, a.mia, PermissionMember, a.manager, "")
	f.join(a.org, a.lee, PermissionMember, a.lead, "")
	f.join(a.org, a.walt, PermissionMember, a.trees, "")
	f.join(a.org, a.dee, PermissionMember, a.designer, "")
	f.join(a.org, a.bob, PermissionMember, "", "")
	f.join(a.org, a.gus, PermissionGuest, "", a.bob)
	a.notes = f.ai("notes", a.lee)
	f.home(a.notes, a.org, a.lee, PermissionMember, "")
	a.w1 = f.ai("w1", a.lee)
	f.home(a.w1, a.org, a.lee, PermissionGuest, a.trees)
	return a
}

// Load reads one organization's chart with two queries: positions with their parents, and members with
// permission, position, a guest's inviter and an AI's creator (AD-017).
func TestLoadBuildsTheChartOfOneOrganization(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	other := f.org("globex")
	f.join(other, a.alice, PermissionMember, "", "") // alice is in another organization too

	c, err := Load(ctx, f.st.R(), a.org)
	require.NoError(t, err)

	require.Equal(t, a.org, c.OrgID)
	require.Len(t, c.Positions, 6)
	require.Equal(t, Position{ID: a.lead, Name: "Lead", ParentID: a.manager}, c.Positions[a.lead])
	require.Equal(t, Position{ID: a.admin, Name: "Admin"}, c.Positions[a.admin], "a root has an empty parent")
	require.Len(t, c.Members, 10, "alice, dora, mia, lee, walt, dee, bob, gus and the two AIs")
	require.Equal(t, Member{IID: a.alice, Permission: PermissionAdmin, PositionID: a.admin}, c.Members[a.alice])
	require.Equal(t, Member{IID: a.gus, Permission: PermissionGuest, InviterIID: a.bob}, c.Members[a.gus], "a guest carries its inviter")
	require.Equal(t, Member{IID: a.notes, Permission: PermissionMember, CreatorIID: a.lee}, c.Members[a.notes], "an AI carries its creator")
	require.Equal(t, Member{IID: a.w1, Permission: PermissionGuest, PositionID: a.trees, CreatorIID: a.lee}, c.Members[a.w1])
	require.Equal(t, Member{IID: a.bob, Permission: PermissionMember}, c.Members[a.bob], "a user has no creator")

	// The chart answers the rules.
	require.True(t, c.Manages(a.lee, a.notes), "lee created notes")
	require.True(t, c.Manages(a.mia, a.notes), "the manager of the creator")
	require.False(t, c.Manages(a.dee, a.notes))
	require.True(t, c.Manages(a.bob, a.gus), "the inviter manages its guest")

	other2, err := Load(ctx, f.st.R(), other)
	require.NoError(t, err)
	require.Len(t, other2.Members, 1, "another organization's members are not mixed in")
	require.Empty(t, other2.Positions)
}

func TestLoadOfAnUnknownOrganizationIsAnEmptyChart(t *testing.T) {
	f := newFixture(t)
	c, err := Load(context.Background(), f.st.R(), "o-nothing")
	require.NoError(t, err)
	require.Equal(t, "o-nothing", c.OrgID)
	require.Empty(t, c.Members)
	require.Empty(t, c.Positions)
	require.False(t, c.Manages("a", "b"))
}

// Load takes the queries of a transaction, so a rule is checked on the state the transaction sees
// (POS-02, OMEM-05).
func TestLoadReadsTheStateOfTheCallersTransaction(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	zed := f.user("zed")
	err := f.st.WithTx(ctx, func(q *db.Queries) error {
		require.NoError(t, q.CreateOrgMember(ctx, db.CreateOrgMemberParams{OrgID: a.org, Iid: zed, Permission: "member", JoinedAt: 2}))
		c, err := Load(ctx, q, a.org)
		require.NoError(t, err)
		require.Contains(t, c.Members, zed, "the uncommitted member is in the chart of its own transaction")
		c2, err := Load(ctx, f.st.R(), a.org)
		require.NoError(t, err)
		require.NotContains(t, c2.Members, zed, "and not in the committed state")
		return context.Canceled // roll back
	})
	require.ErrorIs(t, err, context.Canceled)
}

// CHART-02: an outsider does not see the organization (not found); a member who cannot act is
// forbidden. Every permission is a member.
func TestRequireMemberAndRequireAdmin(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	out := f.user("out")

	require.ErrorIs(t, f.z.RequireMember(ctx, a.org, out), ErrNotFound)
	require.ErrorIs(t, f.z.RequireMember(ctx, "o-nothing", a.alice), ErrNotFound, "no such organization")
	for _, iid := range []string{a.alice, a.bob, a.gus, a.notes} {
		require.NoError(t, f.z.RequireMember(ctx, a.org, iid), iid)
	}

	require.ErrorIs(t, f.z.RequireAdmin(ctx, a.org, out), ErrNotFound, "an outsider")
	require.ErrorIs(t, f.z.RequireAdmin(ctx, a.org, a.bob), ErrForbidden, "a member")
	require.ErrorIs(t, f.z.RequireAdmin(ctx, a.org, a.gus), ErrForbidden, "a guest")
	require.ErrorIs(t, f.z.RequireAdmin(ctx, a.org, a.lee), ErrForbidden, "a manager is not an admin")
	require.ErrorIs(t, f.z.RequireAdmin(ctx, a.org, a.notes), ErrForbidden, "an AI member")
	require.NoError(t, f.z.RequireAdmin(ctx, a.org, a.alice))
	f.join(f.org("globex"), out, PermissionAdmin, "", "")
	require.ErrorIs(t, f.z.RequireAdmin(ctx, a.org, out), ErrNotFound, "an admin of another organization is an outsider here")
}

func TestMemberReturnsTheMembership(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	m, ok, err := f.z.Member(ctx, a.org, a.gus)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, Member{IID: a.gus, Permission: PermissionGuest, InviterIID: a.bob}, m)
	m, ok, err = f.z.Member(ctx, a.org, a.w1)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, Member{IID: a.w1, Permission: PermissionGuest, PositionID: a.trees, CreatorIID: a.lee}, m)
	_, ok, err = f.z.Member(ctx, a.org, "u-nobody")
	require.NoError(t, err)
	require.False(t, ok)
}

// POS-03 through the facade: it loads the chart of the organization named and applies the rules.
func TestAuthzManagesAndCanDeleteInAnOrganization(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	other := f.org("globex")
	f.join(other, a.lee, PermissionMember, "", "")
	f.join(other, a.walt, PermissionMember, "", "")

	for _, tc := range []struct {
		name          string
		actor, target string
		org           string
		manages, del  bool
	}{
		{"the manager of Lead manages a Worktrees holder", a.mia, a.walt, a.org, true, false},
		{"a peer does not", a.dee, a.walt, a.org, false, false},
		{"the creator manages its AI", a.lee, a.notes, a.org, true, false},
		{"and deletes a guest AI it created", a.lee, a.w1, a.org, true, true},
		{"the inviter deletes its guest", a.bob, a.gus, a.org, true, true},
		{"an admin deletes anyone but manages by position only", a.alice, a.bob, a.org, false, true},
		{"nobody manages itself", a.mia, a.mia, a.org, false, false},
		{"in the other organization lee has no position", a.lee, a.walt, other, false, false},
		{"a stranger", "u-nobody", a.walt, a.org, false, false},
		{"an organization that does not exist", a.mia, a.walt, "o-nothing", false, false},
	} {
		got, err := f.z.Manages(ctx, tc.actor, tc.target, tc.org)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.manages, got, "Manages: "+tc.name)
		got, err = f.z.CanDelete(ctx, tc.actor, tc.target, tc.org)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.del, got, "CanDelete: "+tc.name)
	}
}

// ManagesAnywhere looks through every organization both identities belong to: management in one of
// them is enough, and an organization where only one of them is a member does not count.
func TestManagesAnywhereIsTrueThroughASecondSharedOrganizationOnly(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme() // lee and walt: lee has Lead, walt has Worktrees: lee manages walt in acme
	x := f.user("xavier")
	y := f.user("yara")

	// Two peers in acme... and a manager in the second organization they share.
	f.join(a.org, x, PermissionMember, a.designer, "")
	f.join(a.org, y, PermissionMember, a.designer, "")
	ok, err := f.z.ManagesAnywhere(ctx, x, y)
	require.NoError(t, err)
	require.False(t, ok, "peers in the only organization they share")
	ok, err = f.z.ManagesAnywhere(ctx, x, "u-nobody")
	require.NoError(t, err)
	require.False(t, ok, "nothing shared")

	second := f.org("globex")
	boss := f.position(second, "Boss", "")
	crew := f.position(second, "Crew", boss)
	f.join(second, x, PermissionMember, boss, "")
	f.join(second, y, PermissionMember, crew, "")
	ok, err = f.z.Manages(ctx, x, y, a.org)
	require.NoError(t, err)
	require.False(t, ok, "not in acme")
	ok, err = f.z.ManagesAnywhere(ctx, x, y)
	require.NoError(t, err)
	require.True(t, ok, "in the second shared organization")
	ok, err = f.z.ManagesAnywhere(ctx, y, x)
	require.NoError(t, err)
	require.False(t, ok, "the other way round: nobody manages the boss")

	// Management in an organization only the actor belongs to does not count.
	z := f.user("zoe")
	third := f.org("initech")
	f.join(third, a.mia, PermissionMember, f.position(third, "Boss", ""), "")
	f.join(third, z, PermissionMember, "", "")
	ok, err = f.z.ManagesAnywhere(ctx, a.mia, a.walt)
	require.NoError(t, err)
	require.True(t, ok, "mia manages walt in acme")
	ok, err = f.z.ManagesAnywhere(ctx, a.mia, z)
	require.NoError(t, err)
	require.False(t, ok, "mia is above nobody in initech: zoe has no position there")
}

// POS-10: trust is management in the AI's home organization, not in any other organization the author
// shares with it.
func TestTrustsUsesTheHomeOrganizationOfTheAI(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme() // notes: home acme, created by lee

	// A second organization where mia is above notes but notes' home is acme.
	other := f.org("globex")
	boss := f.position(other, "Boss", "")
	crew := f.position(other, "Crew", boss)
	f.join(other, f.user("boris"), PermissionMember, boss, "")
	f.join(other, a.notes, PermissionMember, crew, "")
	ok, err := f.z.Manages(ctx, "u-boris", a.notes, other)
	require.NoError(t, err)
	require.True(t, ok, "fixture sanity: boris manages notes in globex")

	for _, tc := range []struct {
		name   string
		author string
		agent  string
		want   bool
	}{
		{"the creator", a.lee, a.notes, true},
		{"someone above the creator", a.mia, a.notes, true},
		{"the admin holding the root position", a.alice, a.notes, true},
		{"a peer of the creator", a.dee, a.notes, false},
		{"a subordinate", a.walt, a.notes, false},
		{"an outsider", f.user("out"), a.notes, false},
		{"a manager in another shared organization who does not manage it at home", "u-boris", a.notes, false},
		{"the AI itself", a.notes, a.notes, false},
		{"a sibling AI of the same creator", a.w1, a.notes, false},
		{"no author", "", a.notes, false},
		{"an unknown agent", a.lee, "a-nobody", false},
		{"a manager of the guest worker", a.mia, a.w1, true},
		{"a peer of the guest worker's creator", a.dee, a.w1, false},
	} {
		got, err := f.z.Trusts(ctx, tc.author, tc.agent)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.want, got, tc.name)
	}

	// An AI that has no home organization trusts nobody, not even itself.
	homeless := f.ai("homeless", a.lee)
	for _, author := range []string{a.lee, homeless} {
		got, err := f.z.Trusts(ctx, author, homeless)
		require.NoError(t, err)
		require.False(t, got, "author %s", author)
	}
}

// A session is the snapshot of one operation: what it read stays for its whole life, while a new
// session (the next request) reads the new state. The home organization of an AI is part of it.
func TestSessionKeepsWhatItReadForTheOperation(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	elsewhere := f.org("globex")
	f.join(elsewhere, a.mia, PermissionAdmin, "", "")

	s := f.z.Session()
	ok, err := s.Trusts(ctx, a.mia, a.notes)
	require.NoError(t, err)
	require.True(t, ok, "mia manages notes at its home")

	// notes moves to another home, where mia no longer manages it, and mia leaves lee's side.
	f.tx(func(ctx context.Context, q *db.Queries) error {
		return q.SetAgentHome(ctx, db.SetAgentHomeParams{OrgID: nullable(elsewhere), ID: a.notes})
	})
	ok, err = s.Trusts(ctx, a.mia, a.notes)
	require.NoError(t, err)
	require.True(t, ok, "the session still answers from the home it read")
	ok, err = f.z.Session().Trusts(ctx, a.mia, a.notes)
	require.NoError(t, err)
	require.False(t, ok, "the next operation reads the new home")
	ok, err = f.z.Trusts(ctx, a.mia, a.notes)
	require.NoError(t, err)
	require.False(t, ok, "and so does the facade, which never remembers")
}

func TestSharesOrg(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	lone := f.user("lone")
	f.join(f.org("globex"), lone, PermissionGuest, "", "")
	for _, tc := range []struct {
		name string
		x, y string
		want bool
	}{
		{"two members", a.walt, a.dee, true},
		{"a member and a guest", a.alice, a.gus, true},
		{"a user and an AI of the organization", a.bob, a.notes, true},
		{"members of different organizations", a.walt, lone, false},
		{"a stranger", a.walt, "u-nobody", false},
	} {
		for _, pair := range [][2]string{{tc.x, tc.y}, {tc.y, tc.x}} {
			got, err := f.z.SharesOrg(ctx, pair[0], pair[1])
			require.NoError(t, err)
			require.Equal(t, tc.want, got, tc.name)
		}
	}
	f.join(a.org, lone, PermissionGuest, "", a.bob)
	got, err := f.z.SharesOrg(ctx, lone, a.walt)
	require.NoError(t, err)
	require.True(t, got, "a second organization in common")
}

// A session loads each organization's chart once, however many questions it answers, while the facade
// itself reads every time (never stale, AD-017). Loads counts the facade's reads.
func TestSessionLoadsEachChartOnce(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	other := f.org("globex")
	f.join(other, a.lee, PermissionAdmin, "", "")
	third := f.ai("globexbot", a.lee)
	f.home(third, other, a.lee, PermissionMember, "")

	before := f.z.Loads()
	_, err := f.z.Manages(ctx, a.mia, a.walt, a.org)
	require.NoError(t, err)
	_, err = f.z.Manages(ctx, a.mia, a.walt, a.org)
	require.NoError(t, err)
	require.Equal(t, before+2, f.z.Loads(), "the facade reads the chart on every question")

	s := f.z.Session()
	before = f.z.Loads()
	for range 3 {
		ok, err := s.Manages(ctx, a.mia, a.walt, a.org)
		require.NoError(t, err)
		require.True(t, ok)
	}
	ok, err := s.CanDelete(ctx, a.bob, a.gus, a.org)
	require.NoError(t, err)
	require.True(t, ok)
	c, err := s.Chart(ctx, a.org)
	require.NoError(t, err)
	require.Contains(t, c.Members, a.walt)
	ok, err = s.Trusts(ctx, a.lee, a.notes) // home org: acme
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = s.Trusts(ctx, a.mia, a.w1) // home org: acme
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, before+1, f.z.Loads(), "seven questions about acme: one load")

	ok, err = s.Trusts(ctx, a.lee, third) // home org: globex, where lee created it
	require.NoError(t, err)
	require.True(t, ok)
	_, err = s.Manages(ctx, a.lee, third, other)
	require.NoError(t, err)
	require.Equal(t, before+2, f.z.Loads(), "a second organization: one more load, not two")

	_, err = s.ManagesAnywhere(ctx, a.lee, a.walt) // the only organization they share is acme: cached
	require.NoError(t, err)
	_, err = s.ManagesAnywhere(ctx, a.lee, a.walt)
	require.NoError(t, err)
	require.Equal(t, before+2, f.z.Loads(), "ManagesAnywhere reuses what the session holds")

	// A new session starts empty: nothing is cached across operations.
	s2 := f.z.Session()
	_, err = s2.Manages(ctx, a.mia, a.walt, a.org)
	require.NoError(t, err)
	require.Equal(t, before+3, f.z.Loads())
}

// A session answers what the facade answers.
func TestSessionAnswersLikeTheFacade(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	s := f.z.Session()
	people := []string{a.alice, a.dora, a.mia, a.lee, a.walt, a.dee, a.bob, a.gus, a.notes, a.w1, "u-nobody"}
	for _, x := range people {
		for _, y := range people {
			want, err := f.z.Manages(ctx, x, y, a.org)
			require.NoError(t, err)
			got, err := s.Manages(ctx, x, y, a.org)
			require.NoError(t, err)
			require.Equal(t, want, got, "%s manages %s", x, y)
			wantDel, err := f.z.CanDelete(ctx, x, y, a.org)
			require.NoError(t, err)
			gotDel, err := s.CanDelete(ctx, x, y, a.org)
			require.NoError(t, err)
			require.Equal(t, wantDel, gotDel, "%s deletes %s", x, y)
		}
	}
	require.ErrorIs(t, s.RequireAdmin(ctx, a.org, a.bob), ErrForbidden)
	require.ErrorIs(t, s.RequireMember(ctx, a.org, "u-nobody"), ErrNotFound)
	ok, err := s.SharesOrg(ctx, a.bob, a.gus)
	require.NoError(t, err)
	require.True(t, ok)
	m, found, err := s.Member(ctx, a.org, a.gus)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, PermissionGuest, m.Permission)
}

// One operation may be answered by several goroutines (a dispatch to many recipients): the session
// is safe for that and still loads a chart once.
func TestSessionIsSafeForConcurrentUse(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	s := f.z.Session()
	before := f.z.Loads()
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.Trusts(ctx, a.mia, a.notes)
			assert.NoError(t, err)
			assert.True(t, ok)
		}()
	}
	wg.Wait()
	require.Equal(t, before+1, f.z.Loads())
}
