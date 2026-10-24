package orgs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// iids lists the identity ids of rows.
func iids(rows []MemberRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.IID
	}
	return out
}

// positionsOf lists the names of the chart's positions in the order the view gives them.
func positionNames(v ChartView) []string {
	out := make([]string, len(v.Positions))
	for i, p := range v.Positions {
		out[i] = p.Name
	}
	return out
}

func (v ChartView) holders(position string) []string {
	for _, p := range v.Positions {
		if p.ID == position {
			return iids(p.Holders)
		}
	}
	return nil
}

// CHART-01: the positions come as a tree in order - a parent before its children, siblings by name ignoring
// case and then id - whatever order they were made in; a position whose parent is gone is a root and one a
// corrupt loop leaves out of every tree comes last, so none is dropped.
func TestChartViewOrdersThePositionsAsATree(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.bareOrg() // Admin > Director > Manager > Lead > Worktrees, and Designer under Director
	f.position(a.org, "zulu", a.admin)
	f.position(a.org, "Alpha", a.admin)
	f.position(a.org, "alphabet", f.position(a.org, "Beta", a.admin))
	// Names that sort the other way round as ids, and in capitals against lower case.
	f.exec(`INSERT INTO positions (id, org_id, name, parent_id, created_at) VALUES ('p-1', ?, 'Banana', ?, 1), ('p-9', ?, 'apple', ?, 1)`,
		a.org, a.designer, a.org, a.designer)

	view, err := f.svc.ChartView(ctx, a.alice, a.org)
	require.NoError(t, err)
	require.Equal(t, []string{
		"Admin",
		"Alpha", "Beta", "alphabet", // Beta's child follows Beta, not the "alphabet" that sorts before it among all names
		"Director", "Designer", "apple", "Banana", "Manager", "Lead", "Worktrees",
		"zulu",
	}, positionNames(view))
	for i, p := range view.Positions {
		if p.ParentID != "" {
			parent := -1
			for j, q := range view.Positions {
				if q.ID == p.ParentID {
					parent = j
				}
			}
			require.Less(t, parent, i, "%s follows its parent", p.Name)
		}
	}

	// A position whose parent is not in the chart is a root; a loop is listed last.
	f.exec(`PRAGMA foreign_keys = OFF`)
	f.exec(`UPDATE positions SET parent_id = 'p-nothing' WHERE id = ?`, a.designer)
	f.exec(`PRAGMA foreign_keys = ON`)
	loopA, loopB := f.position(a.org, "Loop A", ""), f.position(a.org, "Loop B", "")
	f.exec(`UPDATE positions SET parent_id = ? WHERE id = ?`, loopB, loopA)
	f.exec(`UPDATE positions SET parent_id = ? WHERE id = ?`, loopA, loopB)
	view, err = f.svc.ChartView(ctx, a.alice, a.org)
	require.NoError(t, err)
	names := positionNames(view)
	require.Len(t, names, 14, "no position is dropped")
	require.Equal(t, []string{"Loop A", "Loop B"}, names[len(names)-2:], "a loop comes last")
	require.Equal(t, []string{"Designer", "apple", "Banana"}, names[len(names)-5:len(names)-2],
		"Designer is a root now, with its children: after the tree of Admin, which sorts before it")
}

// CHART-01, CHART-02: a member with no position hangs under the guest's inviter, else the creator, each only
// while it is a member; one with nobody to hang under is unassigned; a position keeps a member out of both; and
// every member is in exactly one place.
func TestChartViewHangsMembersWithoutAPositionUnderWhoAnswersForThem(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme() // bob has no position; gus is bob's guest; notes was created by lee (no position)
	stray := f.user("stray")
	f.join(a.org, stray, PermissionMember, "", "")
	// A guest AI whose inviter and creator differ: the inviter wins (CHART-02).
	both := f.ai("both", a.lee)
	f.home(both, a.org, a.lee, PermissionGuest, "")
	f.exec(`UPDATE org_members SET inviter_iid = ? WHERE org_id = ? AND iid = ?`, a.dora, a.org, both)
	// A guest whose inviter has left the organization falls back to its creator; an AI whose creator left is unassigned.
	left := f.user("left")
	orphan := f.ai("orphan", a.lee)
	f.home(orphan, a.org, a.lee, PermissionGuest, "")
	f.exec(`UPDATE org_members SET inviter_iid = ? WHERE org_id = ? AND iid = ?`, left, a.org, orphan)
	lost := f.ai("lost", a.lee)
	f.home(lost, a.org, left, PermissionMember, "")

	view, err := f.svc.ChartView(ctx, a.alice, a.org)
	require.NoError(t, err)
	under := map[string]string{}
	for _, m := range view.Members {
		under[m.IID] = m.Under
	}
	require.Equal(t, map[string]string{
		a.gus: a.bob, a.notes: a.lee, both: a.dora, orphan: a.lee,
	}, under)
	require.ElementsMatch(t, []string{a.bob, stray, lost}, iids(view.Unassigned))
	for _, m := range view.Unassigned {
		require.Empty(t, m.Under)
	}
	require.Equal(t, []string{a.alice}, view.holders(a.admin))
	require.ElementsMatch(t, []string{a.w1, a.walt}, view.holders(a.trees), "w1 is a guest with a position: it is shown there, not under its inviter")

	seen := map[string]int{}
	for _, p := range view.Positions {
		for _, h := range p.Holders {
			seen[h.IID]++
		}
	}
	for _, m := range append(append([]MemberRow{}, view.Members...), view.Unassigned...) {
		seen[m.IID]++
	}
	members, err := f.svc.ListMembers(ctx, a.alice, a.org)
	require.NoError(t, err)
	require.Len(t, seen, len(members))
	for _, m := range members {
		require.Equal(t, 1, seen[m.IID], "%s is in the chart once", m.IID)
		require.Empty(t, m.Under, "Under belongs to the chart, not to the members table")
	}
}

// A creator or inviter loop - two AIs each created by the other - leaves nobody to hang under, so none of them
// is lost, and a member that hangs under the loop is still placed under it.
func TestChartViewBreaksALoopOfCreators(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.bareOrg()
	x, y := f.ai("x", a.alice), f.ai("y", a.alice)
	f.home(x, a.org, a.alice, PermissionMember, "")
	f.home(y, a.org, x, PermissionMember, "")
	f.exec(`UPDATE agents SET creator_iid = ? WHERE id = ?`, y, x) // x was created by y, y by x
	z := f.ai("z", a.alice)
	f.home(z, a.org, x, PermissionMember, "")

	view, err := f.svc.ChartView(ctx, a.alice, a.org)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{a.bob, x, y}, iids(view.Unassigned), "a loop hangs under nobody")
	under := map[string]string{}
	for _, m := range view.Members {
		under[m.IID] = m.Under
	}
	require.Equal(t, map[string]string{a.gus: a.bob, z: x}, under, "z hangs under x, which is itself unassigned")
}

// A member whose position is not one of the organization's (a stale row) is shown as having none instead of
// disappearing from the chart.
func TestChartViewTreatsAForeignPositionAsNone(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.bareOrg()
	foreign := f.position(f.org("globex"), "Boss", "")
	f.exec(`PRAGMA foreign_keys = OFF`)
	f.exec(`UPDATE org_members SET position_id = ? WHERE org_id = ? AND iid = ?`, foreign, a.org, a.walt)
	f.exec(`PRAGMA foreign_keys = ON`)

	view, err := f.svc.ChartView(ctx, a.alice, a.org)
	require.NoError(t, err)
	require.NotContains(t, view.holders(a.trees), a.walt)
	require.Contains(t, iids(view.Unassigned), a.walt, "walt has no creator, so he is unassigned")
}

// CHART-02: the chart is for the organization's members, guests included, and for nobody else.
func TestChartViewIsForMembers(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	for _, viewer := range []string{a.alice, a.walt, a.bob, a.gus, a.w1} {
		view, err := f.svc.ChartView(ctx, viewer, a.org)
		require.NoError(t, err, viewer)
		require.Equal(t, viewer == a.alice, view.ViewerAdmin, viewer)
	}
	_, err := f.svc.ChartView(ctx, f.user("out"), a.org)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = f.svc.ChartView(ctx, a.alice, "o-nothing")
	require.ErrorIs(t, err, ErrNotFound)
}

// CHART-03: what a viewer may do to each member, from the chart - delete (POS-05) with the effect that follows
// ownership (POS-06), and move (POS-04). Each flag has a case where only its own condition decides.
func TestViewerActions(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	row := func(viewer, target string) MemberRow {
		t.Helper()
		m, err := f.svc.MemberView(ctx, viewer, a.org, target)
		require.NoError(t, err)
		return m
	}

	// An admin deletes everyone but itself and moves everyone, positions or not; an AI of acme is deleted for good.
	for _, who := range []string{a.dora, a.walt, a.bob, a.gus, a.notes, a.w1, a.alice} {
		r := row(a.alice, who)
		require.Equal(t, who != a.alice, r.CanDelete, "alice deleting %s", who)
		require.True(t, r.CanMove, who)
		require.Equal(t, who == a.notes || who == a.w1, r.DeletedForGood, who)
	}
	// An admin who holds no position manages nobody and still moves and deletes anyone: the admin permission alone.
	boss := f.adminOf(a.org)
	for _, who := range []string{a.dora, a.bob, a.w1, a.alice} {
		r := row(boss, who)
		require.True(t, r.CanDelete, who)
		require.True(t, r.CanMove, who)
	}
	require.False(t, row(boss, boss).CanDelete)

	// mia (Manager) manages everybody below: it deletes guests only, and moves any of them.
	require.Equal(t, [3]bool{false, false, true}, [3]bool{row(a.mia, a.lee).CanDelete, row(a.mia, a.lee).DeletedForGood, row(a.mia, a.lee).CanMove}, "a member below: moved, not deleted")
	require.Equal(t, [3]bool{true, true, true}, [3]bool{row(a.mia, a.w1).CanDelete, row(a.mia, a.w1).DeletedForGood, row(a.mia, a.w1).CanMove}, "a guest AI below: deleted for good")
	require.False(t, row(a.mia, a.dora).CanMove, "a superior")
	require.False(t, row(a.mia, a.dee).CanMove, "beside, in another branch")
	require.False(t, row(a.mia, a.mia).CanMove, "itself")
	require.False(t, row(a.mia, a.bob).CanMove, "no relation")

	// walt holds the leaf position Worktrees: it manages the AIs it created but has no position below its own to
	// move them to, and a guest it created can still be deleted.
	scout, helper := f.ai("scout", a.walt), f.ai("helper", a.walt)
	f.home(scout, a.org, a.walt, PermissionMember, "")
	f.home(helper, a.org, a.walt, PermissionGuest, "")
	c, err := f.z.Chart(ctx, a.org)
	require.NoError(t, err)
	require.True(t, c.Manages(a.walt, scout), "the fixture: walt manages what it created")
	require.Equal(t, [2]bool{false, false}, [2]bool{row(a.walt, scout).CanDelete, row(a.walt, scout).CanMove}, "a member: not deleted, and nowhere to move it")
	require.Equal(t, [2]bool{true, false}, [2]bool{row(a.walt, helper).CanDelete, row(a.walt, helper).CanMove}, "a guest: deleted, still nowhere to move it")
	// ... while lee, who holds a position with others below it, moves what it manages.
	require.True(t, row(a.lee, a.w1).CanMove)
	require.True(t, row(a.lee, a.notes).CanMove)

	// bob has no position: he deletes the guest he invited and moves nobody; gus (a guest) and members with no
	// relation do nothing.
	require.Equal(t, [2]bool{true, false}, [2]bool{row(a.bob, a.gus).CanDelete, row(a.bob, a.gus).CanMove})
	for _, viewer := range []string{a.gus, a.dee} {
		for _, who := range []string{a.alice, a.walt, a.bob, a.gus, a.notes} {
			r := row(viewer, who)
			require.False(t, r.CanDelete || r.CanMove, "%s on %s", viewer, who)
		}
	}
}
