package orgs

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
)

// acme builds one organization with its people and two AIs:
//
//	alice: admin; dora, mia, lee, walt, dee, bob, gus: members
//	notes: an AI lee hosts, created by lee, home acme
//	w1:    an AI lee hosts, created by notes (--parent), home acme; instructed by notes and by mia
type acmeChart struct {
	org                                   string
	alice, dora, mia, lee, walt, dee, bob string
	gus, notes, w1                        string
}

func (f *fixture) acme() acmeChart { return f.chart(true) }

// bareOrg is acme without the two AIs: only people, so the organization owns nothing.
func (f *fixture) bareOrg() acmeChart { return f.chart(false) }

func (f *fixture) chart(withAIs bool) acmeChart {
	f.t.Helper()
	a := acmeChart{org: f.org("acme")}
	a.alice, a.dora, a.mia, a.lee = f.user("alice"), f.user("dora"), f.user("mia"), f.user("lee")
	a.walt, a.dee, a.bob, a.gus = f.user("walt"), f.user("dee"), f.user("bob"), f.user("gus")
	f.join(a.org, a.alice, PermissionAdmin)
	for _, m := range []string{a.dora, a.mia, a.lee, a.walt, a.dee, a.bob, a.gus} {
		f.join(a.org, m, PermissionMember)
	}
	if !withAIs {
		return a
	}
	a.notes = f.ai("notes", a.lee)
	f.home(a.notes, a.org, a.lee, PermissionMember)
	a.w1 = f.ai("w1", a.lee)
	f.home(a.w1, a.org, a.notes, PermissionMember)
	f.instruct(a.w1, a.notes)
	f.instruct(a.w1, a.mia)
	return a
}

// Load reads one organization's members with their permission (AD-017).
func TestLoadReadsTheMembersOfOneOrganization(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	other := f.org("globex")
	f.join(other, a.alice, PermissionMember) // alice is in another organization too

	r, err := Load(ctx, f.st.R(), a.org)
	require.NoError(t, err)
	require.Equal(t, a.org, r.OrgID)
	require.Len(t, r.Members, 10, "alice, dora, mia, lee, walt, dee, bob, gus and the two AIs")
	require.Equal(t, Member{IID: a.alice, Permission: PermissionAdmin}, r.Members[a.alice])
	require.Equal(t, Member{IID: a.w1, Permission: PermissionMember}, r.Members[a.w1])

	r2, err := Load(ctx, f.st.R(), other)
	require.NoError(t, err)
	require.Len(t, r2.Members, 1, "another organization's members are not mixed in")

	empty, err := Load(ctx, f.st.R(), "o-nothing")
	require.NoError(t, err)
	require.Empty(t, empty.Members)
}

// Load takes the queries of a transaction, so a rule is checked on the state the transaction sees (OMEM-05).
func TestLoadReadsTheStateOfTheCallersTransaction(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	zed := f.user("zed")
	err := f.st.WithTx(ctx, func(q *db.Queries) error {
		require.NoError(t, q.CreateOrgMember(ctx, db.CreateOrgMemberParams{OrgID: a.org, Iid: zed, Permission: "member", JoinedAt: 2}))
		r, err := Load(ctx, q, a.org)
		require.NoError(t, err)
		require.Contains(t, r.Members, zed, "the uncommitted member is in the roster of its own transaction")
		r2, err := Load(ctx, f.st.R(), a.org)
		require.NoError(t, err)
		require.NotContains(t, r2.Members, zed, "and not in the committed state")
		return context.Canceled // roll back
	})
	require.ErrorIs(t, err, context.Canceled)
}

// An outsider does not see the organization (not found); a member who cannot act is forbidden.
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
	require.ErrorIs(t, f.z.RequireAdmin(ctx, a.org, a.lee), ErrForbidden, "a host is not an admin")
	require.ErrorIs(t, f.z.RequireAdmin(ctx, a.org, a.notes), ErrForbidden, "an AI member")
	require.NoError(t, f.z.RequireAdmin(ctx, a.org, a.alice))
	f.join(f.org("globex"), out, PermissionAdmin)
	require.ErrorIs(t, f.z.RequireAdmin(ctx, a.org, out), ErrNotFound, "an admin of another organization is an outsider here")
}

func TestMemberReturnsTheMembership(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	m, ok, err := f.z.Member(ctx, a.org, a.gus)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, Member{IID: a.gus, Permission: PermissionMember}, m)
	_, ok, err = f.z.Member(ctx, a.org, "u-nobody")
	require.NoError(t, err)
	require.False(t, ok)
}

// SR2-07: a message is trusted by an AI exactly when its author is the AI's host or one of its instructors,
// whatever organizations, permissions or creators say.
func TestSR207TrustsTheHostAndTheInstructorsOnly(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme() // w1: hosted by lee, created by notes, instructed by notes and mia

	other := f.org("globex")
	f.join(other, f.user("boris"), PermissionAdmin)
	f.join(other, a.w1, PermissionMember)

	for _, tc := range []struct {
		name   string
		author string
		agent  string
		want   bool
	}{
		{"the host", a.lee, a.w1, true},
		{"an AI instructor (its creator, by --parent)", a.notes, a.w1, true},
		{"a user instructor", a.mia, a.w1, true},
		{"an admin of its home organization", a.alice, a.w1, false},
		{"a member of its home organization", a.dee, a.w1, false},
		{"an admin of another organization it belongs to", "u-boris", a.w1, false},
		{"an outsider", f.user("out"), a.w1, false},
		{"the AI itself", a.w1, a.w1, false},
		{"no author", "", a.w1, false},
		{"an unknown agent", a.lee, "a-nobody", false},
		{"the host of another AI", a.lee, a.notes, true},
		{"an instructor of another AI", a.mia, a.notes, false},
	} {
		got, err := f.z.Trusts(ctx, tc.author, tc.agent)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.want, got, tc.name)
	}

	// An AI without a home organization still trusts its host (SR2-07 has no organization in it).
	homeless := f.ai("homeless", a.lee)
	got, err := f.z.Trusts(ctx, a.lee, homeless)
	require.NoError(t, err)
	require.True(t, got)
}

// SR2-13: the host and the instructors that are users manage an AI (edit it, end its sessions); an AI
// instructor does not, and nobody manages a user.
func TestSR213ManagesIsTheHostAndTheUserInstructors(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	for _, tc := range []struct {
		name          string
		actor, target string
		want          bool
	}{
		{"the host", a.lee, a.w1, true},
		{"a user instructor", a.mia, a.w1, true},
		{"an AI instructor", a.notes, a.w1, false},
		{"an admin", a.alice, a.w1, false},
		{"a member", a.dee, a.w1, false},
		{"nobody manages a user", a.alice, a.bob, false},
		{"nobody manages itself", a.w1, a.w1, false},
		{"a stranger", "u-nobody", a.w1, false},
	} {
		got, err := f.z.Manages(ctx, tc.actor, tc.target)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.want, got, tc.name)
	}
}

// SR2-12: an AI is deleted by its host or by an admin of its home organization; an instructor, a member or an
// admin of another organization does not delete it. SR2-03: inviting someone gives nothing.
func TestSR212CanDeleteAIIsTheHostOrAHomeAdmin(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	other := f.org("globex")
	boris := f.user("boris")
	f.join(other, boris, PermissionAdmin)
	f.join(other, a.w1, PermissionMember)
	for _, tc := range []struct {
		name  string
		actor string
		want  bool
	}{
		{"the host", a.lee, true},
		{"an admin of its home organization", a.alice, true},
		{"a user instructor", a.mia, false},
		{"an AI instructor (its creator)", a.notes, false},
		{"a member", a.bob, false},
		{"an admin of another organization it belongs to", boris, false},
		{"itself", a.w1, false},
		{"nobody", "", false},
	} {
		got, err := f.z.CanDeleteAI(ctx, tc.actor, a.w1)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.want, got, tc.name)
	}
	got, err := f.z.CanDeleteAI(ctx, a.alice, a.bob)
	require.NoError(t, err)
	require.False(t, got, "a user is no AI: it is removed, never deleted")
}

// A session is the snapshot of one operation: what it read stays for its whole life, while a new
// session (the next request) reads the new state.
func TestSessionKeepsWhatItReadForTheOperation(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()

	s := f.z.Session()
	ok, err := s.Trusts(ctx, a.mia, a.w1)
	require.NoError(t, err)
	require.True(t, ok, "mia instructs w1")

	f.tx(func(ctx context.Context, q *db.Queries) error {
		_, err := q.RemoveAgentInstructor(ctx, db.RemoveAgentInstructorParams{AgentIid: a.w1, InstructorIid: a.mia})
		return err
	})
	ok, err = s.Trusts(ctx, a.mia, a.w1)
	require.NoError(t, err)
	require.True(t, ok, "the session still answers from the links it read")
	ok, err = f.z.Session().Trusts(ctx, a.mia, a.w1)
	require.NoError(t, err)
	require.False(t, ok, "the next operation reads the new links")
	ok, err = f.z.Trusts(ctx, a.mia, a.w1)
	require.NoError(t, err)
	require.False(t, ok, "and so does the facade, which never remembers")
}

func TestSharesOrg(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	lone := f.user("lone")
	f.join(f.org("globex"), lone, PermissionMember)
	for _, tc := range []struct {
		name string
		x, y string
		want bool
	}{
		{"two members", a.walt, a.dee, true},
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
}

// A session loads each AI's links and each organization's roster once, however many questions it answers,
// while the facade itself reads every time (never stale, AD-017). Loads counts the facade's reads.
func TestSessionLoadsEachAIsLinksOnce(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()

	before := f.z.Loads()
	_, err := f.z.Trusts(ctx, a.mia, a.w1)
	require.NoError(t, err)
	_, err = f.z.Manages(ctx, a.mia, a.w1)
	require.NoError(t, err)
	require.Equal(t, before+2, f.z.Loads(), "the facade reads on every question")

	s := f.z.Session()
	before = f.z.Loads()
	for range 3 {
		ok, err := s.Trusts(ctx, a.mia, a.w1)
		require.NoError(t, err)
		require.True(t, ok)
		ok, err = s.Manages(ctx, a.lee, a.w1)
		require.NoError(t, err)
		require.True(t, ok)
	}
	require.Equal(t, before+1, f.z.Loads(), "six questions about w1: one load")
	_, err = s.Trusts(ctx, a.lee, a.notes)
	require.NoError(t, err)
	_, err = s.Roster(ctx, a.org)
	require.NoError(t, err)
	_, err = s.Roster(ctx, a.org)
	require.NoError(t, err)
	require.Equal(t, before+3, f.z.Loads(), "another AI and one roster: two more loads")
}

// A session answers what the facade answers.
func TestSessionAnswersLikeTheFacade(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	s := f.z.Session()
	people := []string{a.alice, a.mia, a.lee, a.bob, a.notes, a.w1, "u-nobody"}
	for _, x := range people {
		for _, y := range people {
			want, err := f.z.Manages(ctx, x, y)
			require.NoError(t, err)
			got, err := s.Manages(ctx, x, y)
			require.NoError(t, err)
			require.Equal(t, want, got, "%s manages %s", x, y)
			wantT, err := f.z.Trusts(ctx, x, y)
			require.NoError(t, err)
			gotT, err := s.Trusts(ctx, x, y)
			require.NoError(t, err)
			require.Equal(t, wantT, gotT, "%s instructs %s", x, y)
		}
	}
	require.ErrorIs(t, s.RequireAdmin(ctx, a.org, a.bob), ErrForbidden)
	require.ErrorIs(t, s.RequireMember(ctx, a.org, "u-nobody"), ErrNotFound)
}

// One operation may be answered by several goroutines (a dispatch to many recipients): the session
// is safe for that and still loads the links once.
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
			ok, err := s.Trusts(ctx, a.mia, a.w1)
			assert.NoError(t, err)
			assert.True(t, ok)
		}()
	}
	wg.Wait()
	require.Equal(t, before+1, f.z.Loads())
}
