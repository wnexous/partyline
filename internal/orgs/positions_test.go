package orgs

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/events"
)

// holderOf returns the position an identity holds in the organization ("" for none).
func (f *fixture) holderOf(org, iid string) string {
	f.t.Helper()
	return f.str(`SELECT position_id FROM org_members WHERE org_id = ? AND iid = ?`, org, iid)
}

// parentOf returns the parent of a position ("" for a root).
func (f *fixture) parentOf(position string) string {
	f.t.Helper()
	return f.str(`SELECT parent_id FROM positions WHERE id = ?`, position)
}

// POS-01: an admin adds a position with a name of 1 to 60 characters, counted in code points, and an
// optional parent of the same organization.
func TestCreatePositionAddsItToTheChart(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	next := f.events()

	root, err := f.svc.CreatePosition(ctx, a.alice, a.org, "Sentinel", "")
	require.NoError(t, err)
	require.NotEmpty(t, root.ID)
	require.Equal(t, Position{ID: root.ID, Name: "Sentinel"}, root, "a root")
	child, err := f.svc.CreatePosition(ctx, a.alice, a.org, "  Night   shift  ", root.ID)
	require.NoError(t, err)
	require.Equal(t, Position{ID: child.ID, Name: "Night   shift", ParentID: root.ID}, child, "trimmed, and under the parent")

	c, err := Load(ctx, f.st.R(), a.org)
	require.NoError(t, err)
	require.Equal(t, child, c.Positions[child.ID])
	require.Len(t, c.Positions, 8)
	require.Equal(t, []events.Event{events.OrgChanged{OrgID: a.org}, events.OrgChanged{OrgID: a.org}}, next(), "one announcement per change")
	require.Equal(t, []string{"org.position_create " + a.org, "org.position_create " + a.org}, f.audited())
}

func TestCreatePositionNameBounds(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.bareOrg()
	accepted := 0
	for name, ok := range map[string]bool{
		"":                            false,
		"  \t ":                       false,
		"\x00":                        false,
		"A":                           true,
		strings.Repeat("a", 60):       true,
		strings.Repeat("a", 61):       false,
		strings.Repeat("é", 60):       true, // 120 bytes, 60 characters
		strings.Repeat("é", 61):       false,
		strings.Repeat("李", 60):       true, // 180 bytes
		strings.Repeat("李", 61):       false,
		" " + strings.Repeat("b", 60): true, // trimmed first
	} {
		_, err := f.svc.CreatePosition(ctx, a.alice, a.org, name, "")
		if ok {
			require.NoError(t, err, "%d characters", len([]rune(name)))
			accepted++
			continue
		}
		invalid(t, err, "name")
	}
	require.Equal(t, 5, accepted, "fixture sanity")
	require.Equal(t, 6+accepted, f.count(`SELECT COUNT(*) FROM positions WHERE org_id = ?`, a.org), "a refused name adds nothing")
}

// POS-01: a position name is unique within the organization, ignoring case, so --position <name> is
// unambiguous; another organization may reuse it.
func TestPositionNamesAreUniquePerOrganization(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	other := f.org("globex")
	boss := f.adminOf(other)

	_, err := f.svc.CreatePosition(ctx, a.alice, a.org, "Lead", "")
	require.ErrorIs(t, err, ErrPositionNameTaken)
	_, err = f.svc.CreatePosition(ctx, a.alice, a.org, "lEAD", a.admin)
	require.ErrorIs(t, err, ErrPositionNameTaken, "ignoring case, under any parent")
	_, err = f.svc.CreatePosition(ctx, boss, other, "Lead", "")
	require.NoError(t, err, "another organization")

	// A rename to a name another position has is refused; to the same name in other letters it is the
	// position's own name.
	_, err = f.svc.UpdatePosition(ctx, a.alice, a.org, a.designer, PositionUpdate{Name: ptr("manager")})
	require.ErrorIs(t, err, ErrPositionNameTaken)
	got, err := f.svc.UpdatePosition(ctx, a.alice, a.org, a.designer, PositionUpdate{Name: ptr("DESIGNER")})
	require.NoError(t, err)
	require.Equal(t, "DESIGNER", got.Name)
}

// POS-01: the parent is a position of the same organization.
func TestParentMustBelongToTheSameOrganization(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	other := f.org("globex")
	foreign := f.position(other, "Boss", "")

	_, err := f.svc.CreatePosition(ctx, a.alice, a.org, "Sentinel", foreign)
	invalid(t, err, "parent_id")
	_, err = f.svc.CreatePosition(ctx, a.alice, a.org, "Sentinel", "p-nothing")
	invalid(t, err, "parent_id")
	_, err = f.svc.UpdatePosition(ctx, a.alice, a.org, a.lead, PositionUpdate{ParentID: ptr(foreign)})
	invalid(t, err, "parent_id")
	require.Equal(t, a.manager, f.parentOf(a.lead), "nothing changed")
	require.Equal(t, 6, f.count(`SELECT COUNT(*) FROM positions WHERE org_id = ?`, a.org))
	require.Empty(t, f.audited())
}

// OMEM-04: only an admin edits the chart.
func TestPositionsAreEditedByAdminsOnly(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	out := f.user("out")
	for who, want := range map[string]error{out: ErrNotFound, a.bob: ErrForbidden, a.gus: ErrForbidden, a.lee: ErrForbidden, a.notes: ErrForbidden} {
		_, err := f.svc.CreatePosition(ctx, who, a.org, "Sentinel", "")
		require.ErrorIs(t, err, want, "create by %s", who)
		_, err = f.svc.UpdatePosition(ctx, who, a.org, a.lead, PositionUpdate{Name: ptr("Chief")})
		require.ErrorIs(t, err, want, "update by %s", who)
		require.ErrorIs(t, f.svc.DeletePosition(ctx, who, a.org, a.trees), want, "delete by %s", who)
	}
	_, err := f.svc.CreatePosition(ctx, a.alice, "o-nothing", "Sentinel", "")
	require.ErrorIs(t, err, ErrNotFound, "no such organization")
	// Authorization comes before validation.
	_, err = f.svc.CreatePosition(ctx, a.bob, a.org, "", "p-nothing")
	require.ErrorIs(t, err, ErrForbidden)
	require.Equal(t, 6, f.count(`SELECT COUNT(*) FROM positions WHERE org_id = ?`, a.org))
	require.Equal(t, a.manager, f.parentOf(a.lead))
	require.Equal(t, 1, f.count(`SELECT COUNT(*) FROM positions WHERE id = ?`, a.trees))
	require.Empty(t, f.audited())
}

// A position of another organization, or one that does not exist, is not found, whoever asks.
func TestUpdateAndDeleteOfAPositionThatIsNotInTheOrganization(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	other := f.org("globex")
	foreign := f.position(other, "Boss", "")
	for _, id := range []string{foreign, "p-nothing", ""} {
		_, err := f.svc.UpdatePosition(ctx, a.alice, a.org, id, PositionUpdate{Name: ptr("Chief")})
		require.ErrorIs(t, err, ErrNotFound, "update %q", id)
		require.ErrorIs(t, f.svc.DeletePosition(ctx, a.alice, a.org, id), ErrNotFound, "delete %q", id)
	}
	require.Equal(t, 1, f.count(`SELECT COUNT(*) FROM positions WHERE id = ?`, foreign))
	require.Empty(t, f.audited())
}

// POS-02: a parent change that would make a position its own ancestor is refused in the transaction
// that makes it, and nothing changes.
func TestUpdatePositionRefusesACycle(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.bareOrg()
	// A → B → C under a fresh root, as in the spec.
	pa, err := f.svc.CreatePosition(ctx, a.alice, a.org, "A", "")
	require.NoError(t, err)
	pb, err := f.svc.CreatePosition(ctx, a.alice, a.org, "B", pa.ID)
	require.NoError(t, err)
	pc, err := f.svc.CreatePosition(ctx, a.alice, a.org, "C", pb.ID)
	require.NoError(t, err)
	next := f.events()
	audited := len(f.audited())

	for name, tc := range map[string]struct{ position, parent string }{
		"A under C, its grandchild": {pa.ID, pc.ID},
		"A under B, its child":      {pa.ID, pb.ID},
		"A under itself":            {pa.ID, pa.ID},
		"C under itself":            {pc.ID, pc.ID},
		"B under C":                 {pb.ID, pc.ID},
	} {
		_, err := f.svc.UpdatePosition(ctx, a.alice, a.org, tc.position, PositionUpdate{ParentID: ptr(tc.parent)})
		require.ErrorIs(t, err, ErrCycle, name)
	}
	require.Equal(t, "", f.parentOf(pa.ID), "nothing changed")
	require.Equal(t, pa.ID, f.parentOf(pb.ID))
	require.Equal(t, pb.ID, f.parentOf(pc.ID))
	require.Empty(t, next())
	require.Len(t, f.audited(), audited)

	// Valid moves: C to the root, B under the Lead position of the example chart, A under C (now free).
	got, err := f.svc.UpdatePosition(ctx, a.alice, a.org, pc.ID, PositionUpdate{ParentID: ptr("")})
	require.NoError(t, err)
	require.Equal(t, Position{ID: pc.ID, Name: "C"}, got, "an empty parent makes a root")
	got, err = f.svc.UpdatePosition(ctx, a.alice, a.org, pa.ID, PositionUpdate{ParentID: ptr(pc.ID)})
	require.NoError(t, err)
	require.Equal(t, Position{ID: pa.ID, Name: "A", ParentID: pc.ID}, got, "A may go under C once C is no longer below it")
	got, err = f.svc.UpdatePosition(ctx, a.alice, a.org, pb.ID, PositionUpdate{ParentID: ptr(a.lead)})
	require.NoError(t, err)
	require.Equal(t, a.lead, got.ParentID)
	require.Len(t, next(), 3)
}

// POS-02: the check runs in the transaction that moves, so two moves that are each fine alone but
// together close a loop cannot both succeed.
func TestTwoMovesThatTogetherMakeACycleCannotBothSucceed(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.bareOrg()
	for round := range 12 {
		x, err := f.svc.CreatePosition(ctx, a.alice, a.org, "X"+string(rune('a'+round)), "")
		require.NoError(t, err)
		y, err := f.svc.CreatePosition(ctx, a.alice, a.org, "Y"+string(rune('a'+round)), "")
		require.NoError(t, err)
		start := make(chan struct{})
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i, move := range []struct{ position, parent string }{{x.ID, y.ID}, {y.ID, x.ID}} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, errs[i] = f.svc.UpdatePosition(ctx, a.alice, a.org, move.position, PositionUpdate{ParentID: ptr(move.parent)})
			}()
		}
		close(start)
		wg.Wait()

		succeeded := 0
		for _, err := range errs {
			if err == nil {
				succeeded++
			} else {
				require.ErrorIs(t, err, ErrCycle)
			}
		}
		require.Equal(t, 1, succeeded, "round %d: exactly one move wins: %v", round, errs)
		c, err := Load(ctx, f.st.R(), a.org)
		require.NoError(t, err)
		require.False(t, c.Above(x.ID, y.ID) && c.Above(y.ID, x.ID), "no loop in the chart")
	}
}

// POS-01: renaming and moving are independent; asking for what already is changes nothing.
func TestUpdatePositionRenamesAndMoves(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	next := f.events()

	got, err := f.svc.UpdatePosition(ctx, a.alice, a.org, a.lead, PositionUpdate{Name: ptr("  Chief  ")})
	require.NoError(t, err)
	require.Equal(t, Position{ID: a.lead, Name: "Chief", ParentID: a.manager}, got, "renamed, still under the manager")
	got, err = f.svc.UpdatePosition(ctx, a.alice, a.org, a.lead, PositionUpdate{ParentID: ptr(a.designer)})
	require.NoError(t, err)
	require.Equal(t, Position{ID: a.lead, Name: "Chief", ParentID: a.designer}, got, "moved, still named Chief")
	got, err = f.svc.UpdatePosition(ctx, a.alice, a.org, a.lead, PositionUpdate{Name: ptr("Lead"), ParentID: ptr(a.manager)})
	require.NoError(t, err)
	require.Equal(t, Position{ID: a.lead, Name: "Lead", ParentID: a.manager}, got, "both at once")
	require.Len(t, next(), 3)

	// Nothing to change: no write, no audit entry, no announcement.
	audited := len(f.audited())
	for _, in := range []PositionUpdate{{}, {Name: ptr(" Lead ")}, {ParentID: ptr(a.manager)}, {Name: ptr("Lead"), ParentID: ptr(a.manager)}} {
		got, err = f.svc.UpdatePosition(ctx, a.alice, a.org, a.lead, in)
		require.NoError(t, err)
		require.Equal(t, a.manager, got.ParentID)
	}
	require.Empty(t, next())
	require.Len(t, f.audited(), audited)

	for _, name := range []string{"", "  ", strings.Repeat("x", 61)} {
		_, err = f.svc.UpdatePosition(ctx, a.alice, a.org, a.lead, PositionUpdate{Name: ptr(name)})
		invalid(t, err, "name")
	}
}

// POS-09: deleting a position moves its holders and its child positions to its parent.
func TestDeletingAMiddlePositionMovesHoldersAndChildrenToItsParent(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme() // Admin ─ Director ─ Manager ─ Lead ─ Worktrees; mia holds Manager, lee Lead
	extra := f.user("extra")
	f.join(a.org, extra, PermissionMember, a.lead, "") // a second holder of Lead
	f.exec(`INSERT INTO org_invitations (id, org_id, invitee_iid, permission, position_id, invited_by, created_at, expires_at)
		VALUES ('i1', ?, ?, 'member', ?, ?, 1, 2)`, a.org, f.user("zed"), a.lead, a.alice)
	next := f.events()

	require.NoError(t, f.svc.DeletePosition(ctx, a.alice, a.org, a.lead))

	require.Zero(t, f.count(`SELECT COUNT(*) FROM positions WHERE id = ?`, a.lead), "the position is gone")
	require.Equal(t, a.manager, f.parentOf(a.trees), "its child moved up to the parent")
	require.Equal(t, a.manager, f.holderOf(a.org, a.lee), "its holders moved up")
	require.Equal(t, a.manager, f.holderOf(a.org, extra))
	require.Equal(t, a.trees, f.holderOf(a.org, a.walt), "the child's holder keeps the child position")
	require.Equal(t, a.manager, f.holderOf(a.org, a.mia), "the parent's own holder is untouched")
	require.Equal(t, a.manager, f.str(`SELECT position_id FROM org_invitations WHERE id = 'i1'`), "so does a pending invitation")
	require.Equal(t, 1, f.count(`SELECT COUNT(*) FROM org_members WHERE org_id = ? AND iid = ?`, a.org, a.lee), "the holder is still a member")
	require.Equal(t, 5, f.count(`SELECT COUNT(*) FROM positions WHERE org_id = ?`, a.org))

	// The moves change who manages whom: lee now holds Manager like mia, and both are above Worktrees.
	c, err := Load(ctx, f.st.R(), a.org)
	require.NoError(t, err)
	require.True(t, c.Manages(a.lee, a.walt))
	require.True(t, c.Manages(a.mia, a.walt))
	require.False(t, c.Manages(a.mia, a.lee), "peers")
	require.True(t, c.Manages(a.dora, a.lee), "the Director is above them")

	require.Equal(t, []events.Event{events.OrgChanged{OrgID: a.org}}, next())
	require.Equal(t, []string{"org.position_delete " + a.org}, f.audited())
	rows, _, err := f.audit.List(ctx, "")
	require.NoError(t, err)
	var detail map[string]any
	require.NoError(t, json.Unmarshal([]byte(rows[0].DetailJson), &detail))
	require.Equal(t, map[string]any{"position_id": a.lead, "name": "Lead", "moved_to": a.manager, "holders": float64(2), "children": float64(1)}, detail)
}

func TestDeletingARootPositionLeavesHoldersWithoutOneAndMakesChildrenRoots(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()

	require.NoError(t, f.svc.DeletePosition(ctx, a.alice, a.org, a.admin))

	require.Zero(t, f.count(`SELECT COUNT(*) FROM positions WHERE id = ?`, a.admin))
	require.Equal(t, "", f.holderOf(a.org, a.alice), "the holder has no position, and stays an admin")
	require.Equal(t, 1, f.count(`SELECT COUNT(*) FROM org_members WHERE org_id = ? AND iid = ? AND permission = 'admin'`, a.org, a.alice))
	require.Equal(t, "", f.parentOf(a.director), "the child became a root")
	require.Equal(t, a.director, f.holderOf(a.org, a.dora), "and kept its holder")
	require.Equal(t, a.manager, f.parentOf(a.lead), "deeper levels are untouched")

	// A leaf has no children to move; its holder moves up to the parent.
	require.NoError(t, f.svc.DeletePosition(ctx, a.alice, a.org, a.trees))
	require.Equal(t, a.lead, f.holderOf(a.org, a.walt))
	require.Equal(t, 4, f.count(`SELECT COUNT(*) FROM positions WHERE org_id = ?`, a.org))
}
