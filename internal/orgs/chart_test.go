package orgs

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// The positions of the spec's example chart (design: "Org chart"):
//
//	admin ─ director ─ manager ─ lead ─ worktrees
//	                └─ designer
//	sentinel
//
// Two roots: admin and sentinel.
func examplePositions() []Position {
	return []Position{
		{ID: "admin", Name: "Admin"},
		{ID: "director", Name: "Director", ParentID: "admin"},
		{ID: "manager", Name: "Manager", ParentID: "director"},
		{ID: "lead", Name: "Lead developer", ParentID: "manager"},
		{ID: "worktrees", Name: "Worktrees", ParentID: "lead"},
		{ID: "designer", Name: "Designer", ParentID: "director"},
		{ID: "sentinel", Name: "Sentinel"},
	}
}

// chartOf builds a chart of the example positions with the members.
func chartOf(members ...Member) *Chart {
	c := &Chart{OrgID: "org", Positions: map[string]Position{}, Members: map[string]Member{}}
	for _, p := range examplePositions() {
		c.Positions[p.ID] = p
	}
	for _, m := range members {
		c.Members[m.IID] = m
	}
	return c
}

func user(iid, position string) Member {
	return Member{IID: iid, Permission: PermissionMember, PositionID: position}
}
func admin(iid, position string) Member {
	return Member{IID: iid, Permission: PermissionAdmin, PositionID: position}
}

// created is a member that someone created (an AI); creator "" for none.
func created(iid, position, creator string) Member {
	return Member{IID: iid, Permission: PermissionMember, PositionID: position, CreatorIID: creator}
}

// guest is a guest that someone invited.
func guest(iid, position, inviter string) Member {
	return Member{IID: iid, Permission: PermissionGuest, PositionID: position, InviterIID: inviter}
}

// POS-03: A manages B exactly when A holds a position strictly above B's. Each case flips one
// condition of the rule and leaves the others as they would manage.
func TestManagesByPosition(t *testing.T) {
	c := chartOf(
		user("director", "director"), user("manager", "manager"), user("manager2", "manager"),
		user("lead", "lead"), user("lead2", "lead"), user("worker", "worktrees"),
		user("designer", "designer"), user("root", "admin"), user("watch", "sentinel"),
		user("plain", ""),
	)
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"the parent position manages a holder of its child", "manager", "lead", true},
		{"two levels above", "manager", "worker", true},
		{"three levels above", "director", "worker", true},
		{"a root holder manages every holder below its tree", "root", "worker", true},
		{"the same position: peers", "lead", "lead2", false},
		{"the same position, the other way", "lead2", "lead", false},
		{"a holder below does not manage the one above", "lead", "manager", false},
		{"a holder two levels below does not manage the one above", "worker", "director", false},
		{"another branch: a sibling position", "designer", "manager", false},
		{"another branch, the other way", "manager", "designer", false},
		{"a position of another tree", "watch", "worker", false},
		{"the root of another tree does not manage", "root", "watch", false},
		{"A holds no position", "plain", "worker", false},
		{"B holds no position and has no creator", "root", "plain", false},
		{"A is not a member", "ghost", "worker", false},
		{"B is not a member", "manager", "ghost", false},
		{"nobody manages themselves", "manager", "manager", false},
		{"a root holder has no manager", "director", "root", false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, c.Manages(tc.a, tc.b), tc.name)
	}
}

// POS-03: the admin permission is independent of positions and gives no management.
func TestAdminPermissionDoesNotImplyManagement(t *testing.T) {
	c := chartOf(admin("boss", ""), admin("lowadmin", "worktrees"), user("lead", "lead"), user("worker", "worktrees"))
	require.False(t, c.Manages("boss", "lead"), "an admin with no position manages nobody")
	require.False(t, c.Manages("boss", "worker"))
	require.False(t, c.Manages("lowadmin", "lead"), "an admin below does not manage the one above")
	require.True(t, c.Manages("lead", "lowadmin"), "an admin is managed like anyone else")
}

// POS-03: B without a position is managed by its creator and by whoever manages the creator,
// following the creator chain upward.
func TestManagesAPositionlessMemberThroughItsCreator(t *testing.T) {
	cases := []struct {
		name    string
		members []Member
		a, b    string
		want    bool
	}{
		{"A is the creator", []Member{user("lead", "lead"), created("w", "", "lead")}, "lead", "w", true},
		{"A is the creator and holds no position", []Member{user("alice", ""), created("w", "", "alice")}, "alice", "w", true},
		{"A is above the creator", []Member{user("lead", "lead"), user("manager", "manager"), created("w", "", "lead")}, "manager", "w", true},
		{"A is two above the creator", []Member{user("lead", "lead"), user("director", "director"), created("w", "", "lead")}, "director", "w", true},
		{"A is a peer of the creator", []Member{user("lead", "lead"), user("lead2", "lead"), created("w", "", "lead")}, "lead2", "w", false},
		{"A is below the creator", []Member{user("lead", "lead"), user("worker", "worktrees"), created("w", "", "lead")}, "worker", "w", false},
		{"A is in another branch from the creator", []Member{user("lead", "lead"), user("designer", "designer"), created("w", "", "lead")}, "designer", "w", false},
		{"A holds no position and is not the creator", []Member{user("lead", "lead"), user("plain", ""), created("w", "", "lead")}, "plain", "w", false},
		{"the creator is not a member", []Member{user("manager", "manager"), created("w", "", "gone")}, "manager", "w", false},
		{"A is the creator but left", []Member{created("w", "", "gone")}, "gone", "w", false},
		{"the creator chain of two positionless creators: A is the first creator",
			[]Member{user("lead", "lead"), created("mid", "", "lead"), created("w", "", "mid")}, "mid", "w", true},
		{"the creator chain of two positionless creators: A created the creator",
			[]Member{user("lead", "lead"), created("mid", "", "lead"), created("w", "", "mid")}, "lead", "w", true},
		{"the creator chain of two positionless creators: A is above the last creator",
			[]Member{user("lead", "lead"), user("manager", "manager"), created("mid", "", "lead"), created("w", "", "mid")}, "manager", "w", true},
		{"the chain breaks at a creator who is not a member",
			[]Member{user("lead", "lead"), created("mid", "", "gone"), created("w", "", "mid")}, "lead", "w", false},
		{"B holds a position, so the creator rule does not apply",
			[]Member{user("designer", "designer"), created("w", "worktrees", "designer")}, "designer", "w", false},
		{"B holds a position and its creator is above it",
			[]Member{user("lead", "lead"), created("w", "worktrees", "lead")}, "lead", "w", true},
		{"B is created by itself", []Member{created("w", "", "w"), user("lead", "lead")}, "lead", "w", false},
		{"a creator cycle terminates: A is outside it",
			[]Member{created("x", "", "y"), created("y", "", "x"), user("lead", "lead")}, "lead", "x", false},
		{"a creator cycle terminates: A is inside it",
			[]Member{created("x", "", "y"), created("y", "", "x"), user("lead", "lead")}, "y", "x", true},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, chartOf(tc.members...).Manages(tc.a, tc.b), tc.name)
	}
}

// POS-03, Guests: a guest is the responsibility of its inviter; the inviter's inviter, and whoever
// manages any of them, manage it too.
func TestManagesAGuestThroughItsInviterChain(t *testing.T) {
	cases := []struct {
		name    string
		members []Member
		a, b    string
		want    bool
	}{
		{"the inviter manages its guest", []Member{user("bob", ""), guest("carol", "", "bob")}, "bob", "carol", true},
		{"the inviter's inviter manages a guest of a guest",
			[]Member{user("bob", ""), guest("carol", "", "bob"), guest("dave", "", "carol")}, "bob", "dave", true},
		{"a guest invited by a guest: the first inviter manages it",
			[]Member{user("bob", ""), guest("carol", "", "bob"), guest("dave", "", "carol")}, "carol", "dave", true},
		{"three levels of guests", []Member{user("bob", ""), guest("c", "", "bob"), guest("d", "", "c"), guest("e", "", "d")}, "bob", "e", true},
		{"the position manager of the inviter manages the guest",
			[]Member{user("lead", "lead"), user("manager", "manager"), guest("carol", "", "lead")}, "manager", "carol", true},
		{"the position manager of an inviter further up the chain",
			[]Member{user("lead", "lead"), user("manager", "manager"), guest("carol", "", "lead"), guest("dave", "", "carol")}, "manager", "dave", true},
		{"a peer of the inviter does not manage the guest",
			[]Member{user("lead", "lead"), user("lead2", "lead"), guest("carol", "", "lead")}, "lead2", "carol", false},
		{"a member of no relation does not manage the guest",
			[]Member{user("bob", ""), user("eve", ""), guest("carol", "", "bob")}, "eve", "carol", false},
		{"someone below the inviter does not manage the guest",
			[]Member{user("lead", "lead"), user("worker", "worktrees"), guest("carol", "", "lead")}, "worker", "carol", false},
		{"the guest does not manage its inviter", []Member{user("bob", ""), guest("carol", "", "bob")}, "carol", "bob", false},
		{"the guest of an inviter who left has no manager in the chart",
			[]Member{user("bob", ""), guest("dave", "", "carol")}, "bob", "dave", false},
		{"the manager of a position does not reach the guest of an inviter who left",
			[]Member{user("manager", "manager"), guest("dave", "", "carol")}, "manager", "dave", false},
		{"an inviter who left no longer manages (A must be a member)", []Member{guest("dave", "", "carol")}, "carol", "dave", false},
		{"the chain breaks at the inviter who left",
			[]Member{user("bob", ""), guest("dave", "", "carol"), guest("erin", "", "dave")}, "bob", "erin", false},
		{"an inviter cycle terminates: A is outside it",
			[]Member{guest("x", "", "y"), guest("y", "", "x"), user("lead", "lead")}, "lead", "x", false},
		{"an inviter cycle terminates: A is inside it",
			[]Member{guest("x", "", "y"), guest("y", "", "x")}, "y", "x", true},
		{"a guest that holds a position is managed through the position too",
			[]Member{user("manager", "manager"), guest("g", "worktrees", "")}, "manager", "g", true},
		{"a guest that holds a position is still managed by its inviter",
			[]Member{user("designer", "designer"), guest("g", "worktrees", "designer")}, "designer", "g", true},
		{"the inviter rule is for guests: a member with a stale inviter is not managed by it",
			[]Member{user("bob", ""), {IID: "carol", Permission: PermissionMember, InviterIID: "bob"}}, "bob", "carol", false},
		{"the inviter rule is for guests: an admin with a stale inviter",
			[]Member{user("bob", ""), {IID: "carol", Permission: PermissionAdmin, InviterIID: "bob"}}, "bob", "carol", false},
		{"a guest AI started with --parent: the parent is its creator and inviter",
			[]Member{user("lead", "lead"), {IID: "w", Permission: PermissionGuest, CreatorIID: "lead", InviterIID: "lead"}}, "lead", "w", true},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, chartOf(tc.members...).Manages(tc.a, tc.b), tc.name)
	}
}

// POS-05: A deletes B exactly when A is an admin, or B is a guest and A manages it (its inviter, an
// identity up the inviter chain, or whoever manages one of them).
func TestCanDelete(t *testing.T) {
	cases := []struct {
		name    string
		members []Member
		a, b    string
		want    bool
	}{
		{"an admin deletes a member it does not manage", []Member{admin("boss", ""), user("worker", "worktrees")}, "boss", "worker", true},
		{"an admin deletes a member above it", []Member{admin("boss", "worktrees"), user("director", "director")}, "boss", "director", true},
		{"an admin deletes another admin", []Member{admin("boss", ""), admin("boss2", "")}, "boss", "boss2", true},
		{"an admin deletes a guest", []Member{admin("boss", ""), guest("g", "", "")}, "boss", "g", true},
		{"an inviter deletes its guest", []Member{user("bob", ""), guest("carol", "", "bob")}, "bob", "carol", true},
		{"the inviter's inviter deletes a guest of a guest",
			[]Member{user("bob", ""), guest("carol", "", "bob"), guest("dave", "", "carol")}, "bob", "dave", true},
		{"the manager of an inviter deletes its guest",
			[]Member{user("lead", "lead"), user("manager", "manager"), guest("carol", "", "lead")}, "manager", "carol", true},
		{"a manager deletes a guest positioned below it", []Member{user("lead", "lead"), guest("g", "worktrees", "")}, "lead", "g", true},
		{"a manager cannot delete a member below", []Member{user("lead", "lead"), user("worker", "worktrees")}, "lead", "worker", false},
		{"a manager cannot delete an AI member it created", []Member{user("lead", "lead"), created("ai", "", "lead")}, "lead", "ai", false},
		{"a manager cannot delete an admin below", []Member{user("lead", "lead"), admin("boss", "worktrees")}, "lead", "boss", false},
		{"a peer cannot delete a guest it did not invite",
			[]Member{user("lead", "lead"), user("lead2", "lead"), guest("carol", "", "lead")}, "lead2", "carol", false},
		{"a member of no relation cannot delete a guest", []Member{user("bob", ""), user("eve", ""), guest("carol", "", "bob")}, "eve", "carol", false},
		{"a guest cannot delete the one who invited it", []Member{user("bob", ""), guest("carol", "", "bob")}, "carol", "bob", false},
		{"nobody deletes itself", []Member{admin("boss", ""), user("bob", ""), guest("carol", "", "bob")}, "boss", "boss", false},
		{"a guest does not delete itself", []Member{guest("carol", "", "carol")}, "carol", "carol", false},
		{"the actor is not a member", []Member{guest("carol", "", "bob")}, "bob", "carol", false},
		{"the target is not a member", []Member{admin("boss", "")}, "boss", "ghost", false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, chartOf(tc.members...).CanDelete(tc.a, tc.b), tc.name)
	}
}

// POS-02: a move that makes a position its own ancestor is a cycle: the new parent is the position
// itself or one of its descendants.
func TestHasCycle(t *testing.T) {
	c := chartOf()
	cases := []struct {
		name      string
		position  string
		newParent string
		wantCycle bool
	}{
		{"a position cannot be its own parent", "lead", "lead", true},
		{"a child as the parent", "manager", "lead", true},
		{"a grandchild as the parent", "manager", "worktrees", true},
		{"a root under its own descendant", "admin", "worktrees", true},
		{"a sibling branch", "lead", "designer", false},
		{"an ancestor higher up", "worktrees", "director", false},
		{"the parent it already has", "lead", "manager", false},
		{"another tree", "lead", "sentinel", false},
		{"no parent makes a root", "lead", "", false},
		{"a root under another root", "admin", "sentinel", false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.wantCycle, c.HasCycle(tc.position, tc.newParent), tc.name)
	}
}

// POS-03: a position is above another only when it is a proper ancestor.
func TestAbove(t *testing.T) {
	c := chartOf()
	cases := []struct {
		name   string
		pa, pb string
		want   bool
	}{
		{"parent", "manager", "lead", true},
		{"grandparent", "manager", "worktrees", true},
		{"the root of a deep chain", "admin", "worktrees", true},
		{"itself", "lead", "lead", false},
		{"child", "lead", "manager", false},
		{"sibling", "designer", "manager", false},
		{"another tree", "sentinel", "worktrees", false},
		{"no position above", "", "lead", false},
		{"no position below", "manager", "", false},
		{"an unknown position below", "manager", "nope", false},
		{"an unknown position above", "nope", "lead", false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, c.Above(tc.pa, tc.pb), tc.name)
	}
}

// Positions never form a cycle (POS-02), but a corrupt chart must not hang the rules.
func TestAPositionCycleTerminates(t *testing.T) {
	c := &Chart{
		Positions: map[string]Position{"a": {ID: "a", ParentID: "b"}, "b": {ID: "b", ParentID: "a"}, "c": {ID: "c", ParentID: "c"}},
		Members:   map[string]Member{"x": user("x", "a"), "y": user("y", "b"), "z": user("z", "c")},
	}
	require.NotPanics(t, func() {
		_ = c.Above("c", "a")
		_ = c.Above("a", "c")
		_ = c.Manages("x", "z")
		_ = c.HasCycle("a", "b")
		_ = c.ManagersOf("z")
	})
	require.False(t, c.Above("c", "a"))
}

func TestIsAdmin(t *testing.T) {
	c := chartOf(admin("boss", ""), user("bob", ""), guest("g", "", ""))
	require.True(t, c.IsAdmin("boss"))
	require.False(t, c.IsAdmin("bob"))
	require.False(t, c.IsAdmin("g"))
	require.False(t, c.IsAdmin("ghost"), "a stranger is not an admin")
}

// The welcome frame names the identities that manage an AI, nearest first (POS-10).
func TestManagersOfIsNearestFirst(t *testing.T) {
	members := []Member{
		admin("alice", "admin"), user("dir", "director"), user("mgr", "manager"), user("lead", "lead"),
		guest("w", "worktrees", "lead"), // started by Lead with --position Worktrees
	}
	require.Equal(t, []string{"lead", "mgr", "dir", "alice"}, chartOf(members...).ManagersOf("w"),
		"the inviter and the Lead position first, then each position up to the root")

	c := chartOf(append(members, created("ai", "", "lead"))...)
	require.Equal(t, []string{"lead", "mgr", "dir", "alice"}, c.ManagersOf("ai"), "a positionless AI: its creator, then whoever manages the creator")

	c = chartOf(append(members, created("mid", "", "lead"), created("ai", "", "mid"))...)
	require.Equal(t, []string{"mid", "lead", "mgr", "dir", "alice"}, c.ManagersOf("ai"), "a creator chain, nearest creator first")

	c = chartOf(append(members, guest("g1", "", "w"), guest("g2", "", "g1"))...)
	require.Equal(t, []string{"g1", "w", "lead", "mgr", "dir", "alice"}, c.ManagersOf("g2"), "an inviter chain of guests")
}

func TestManagersOfListsEachManagerOnceAndBreaksTiesById(t *testing.T) {
	// "lead" is both the Lead developer position above the worker and its inviter: listed once.
	c := chartOf(user("lead", "lead"), guest("w", "worktrees", "lead"), user("m-b", "manager"), user("m-a", "manager"), user("m-c", "manager"))
	require.Equal(t, []string{"lead", "m-a", "m-b", "m-c"}, c.ManagersOf("w"), "co-holders of a position are ordered by id")

	require.Empty(t, chartOf(user("root", "admin")).ManagersOf("root"), "a root holder has no manager")
	require.Empty(t, chartOf(user("lead", "lead")).ManagersOf("ghost"), "a stranger has none")
	require.NotContains(t, chartOf(user("lead", "lead"), guest("w", "worktrees", "lead")).ManagersOf("w"), "w", "nobody manages themselves")
	require.Empty(t, chartOf(admin("boss", ""), user("w", "")).ManagersOf("w"), "an admin is not a manager")
}

func TestManagedByListsExactlyTheTargetsOfManages(t *testing.T) {
	c := chartOf(
		user("manager", "manager"), user("lead", "lead"), user("worker", "worktrees"), user("designer", "designer"),
		created("ai", "", "manager"), guest("g", "", "lead"), user("plain", ""),
	)
	require.Equal(t, map[string]bool{"lead": true, "worker": true, "ai": true, "g": true}, c.ManagedBy("manager"))
	require.Equal(t, map[string]bool{"worker": true, "g": true}, c.ManagedBy("lead"))
	require.Empty(t, c.ManagedBy("plain"))
	require.Empty(t, c.ManagedBy("ghost"), "a stranger manages nobody")
}

// Manages, ManagersOf and ManagedBy are three views of one rule: for every pair they agree.
func TestManagesManagersOfAndManagedByAgree(t *testing.T) {
	c := chartOf(
		admin("alice", "admin"), user("dir", "director"), user("mgr", "manager"), user("lead", "lead"), user("lead2", "lead"),
		user("worker", "worktrees"), user("designer", "designer"), user("watch", "sentinel"), user("plain", ""),
		created("ai1", "", "lead"), created("ai2", "worktrees", "lead"), created("ai3", "", "ai1"), created("ai4", "", "plain"),
		guest("g1", "", "plain"), guest("g2", "", "g1"), guest("g3", "designer", "worker"), guest("g4", "", "gone"),
		created("loop1", "", "loop2"), created("loop2", "", "loop1"),
	)
	for b := range c.Members {
		managers := map[string]bool{}
		for _, a := range c.ManagersOf(b) {
			require.False(t, managers[a], "%s is listed once for %s", a, b)
			managers[a] = true
		}
		for a := range c.Members {
			want := c.Manages(a, b)
			require.Equal(t, want, managers[a], fmt.Sprintf("ManagersOf(%s) lists %s exactly when Manages", b, a))
			require.Equal(t, want, c.ManagedBy(a)[b], fmt.Sprintf("ManagedBy(%s) holds %s exactly when Manages", a, b))
		}
	}
}
