package orgs

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store/db"
)

// addMember joins iid to the organization through the transaction helper.
func (f *fixture) addMember(in AddMemberInput) error {
	f.t.Helper()
	ctx := context.Background()
	in.Now = t0
	return f.st.WithTx(ctx, func(q *db.Queries) error { return AddMember(ctx, q, in) })
}

func memberOfOrg(t *testing.T, f *fixture, org, iid string) Member {
	t.Helper()
	m, ok, err := f.z.Member(context.Background(), org, iid)
	require.NoError(t, err)
	require.True(t, ok, "%s is a member", iid)
	return m
}

// OMEM-03: an identity is a member of several organizations, with its own permission and position in
// each.
func TestOneIdentityInTwoOrganizationsKeepsEachMembership(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	globex := f.org("globex")
	boss := f.position(globex, "Boss", "")

	require.NoError(t, f.addMember(AddMemberInput{OrgID: globex, IID: a.walt, Permission: PermissionAdmin, PositionID: boss}))

	require.Equal(t, Member{IID: a.walt, Permission: PermissionMember, PositionID: a.trees}, memberOfOrg(t, f, a.org, a.walt))
	require.Equal(t, Member{IID: a.walt, Permission: PermissionAdmin, PositionID: boss}, memberOfOrg(t, f, globex, a.walt))
	mine, err := f.svc.ListMine(ctx, a.walt)
	require.NoError(t, err)
	require.Len(t, mine, 2)
	// Changing one membership leaves the other alone.
	_, err = f.svc.SetPosition(ctx, a.alice, a.org, a.walt, "")
	require.NoError(t, err)
	require.Equal(t, boss, memberOfOrg(t, f, globex, a.walt).PositionID)
}

// AddMember is the transaction helper invitations and placement use: one membership per identity and
// organization, a valid permission, a position of the organization, an inviter only for guests.
func TestAddMember(t *testing.T) {
	f := newFixture(t)
	a := f.acme()
	zed := f.user("zed")
	globex := f.org("globex")
	foreign := f.position(globex, "Boss", "")

	require.NoError(t, f.addMember(AddMemberInput{OrgID: a.org, IID: zed, Permission: PermissionGuest, InviterIID: a.bob}))
	require.Equal(t, Member{IID: zed, Permission: PermissionGuest, InviterIID: a.bob}, memberOfOrg(t, f, a.org, zed), "a guest carries its inviter")
	require.Equal(t, int(t0.UnixMilli()), f.count(`SELECT joined_at FROM org_members WHERE org_id = ? AND iid = ?`, a.org, zed), "joined now")

	require.ErrorIs(t, f.addMember(AddMemberInput{OrgID: a.org, IID: zed, Permission: PermissionMember}), ErrAlreadyMember)
	require.ErrorIs(t, f.addMember(AddMemberInput{OrgID: a.org, IID: a.walt, Permission: PermissionGuest}), ErrAlreadyMember, "any permission")

	yan := f.user("yan")
	require.NoError(t, f.addMember(AddMemberInput{OrgID: a.org, IID: yan, Permission: PermissionMember, PositionID: a.designer, InviterIID: a.bob}))
	require.Equal(t, Member{IID: yan, Permission: PermissionMember, PositionID: a.designer}, memberOfOrg(t, f, a.org, yan), "a position; an inviter only for guests")

	xi := f.user("xi")
	for _, in := range []AddMemberInput{
		{OrgID: a.org, IID: xi, Permission: "owner"}, // an unknown permission
		{OrgID: a.org, IID: xi},                      // none
	} {
		invalid(t, f.addMember(in), "permission")
	}
	invalid(t, f.addMember(AddMemberInput{OrgID: a.org, IID: xi, Permission: PermissionMember, PositionID: foreign}), "position_id")
	invalid(t, f.addMember(AddMemberInput{OrgID: a.org, IID: xi, Permission: PermissionMember, PositionID: "p-nothing"}), "position_id")
	require.Zero(t, f.count(`SELECT COUNT(*) FROM org_members WHERE iid = ?`, xi), "a refused join adds nothing")
}

// OMEM-04: only an admin changes permissions; the identity must be a member.
func TestSetPermissionIsForAdmins(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	out := f.user("out")
	for who, want := range map[string]error{out: ErrNotFound, a.bob: ErrForbidden, a.gus: ErrForbidden, a.lee: ErrForbidden, a.notes: ErrForbidden} {
		_, err := f.svc.SetPermission(ctx, who, a.org, a.walt, PermissionAdmin)
		require.ErrorIs(t, err, want, who)
	}
	_, err := f.svc.SetPermission(ctx, a.alice, a.org, out, PermissionGuest)
	require.ErrorIs(t, err, ErrNotFound, "the target is not a member")
	_, err = f.svc.SetPermission(ctx, a.alice, "o-nothing", a.walt, PermissionGuest)
	require.ErrorIs(t, err, ErrNotFound)
	require.Equal(t, PermissionMember, memberOfOrg(t, f, a.org, a.walt).Permission)
	require.Empty(t, f.audited())
	// A bad permission is told to the admin only.
	_, err = f.svc.SetPermission(ctx, a.bob, a.org, a.walt, "owner")
	require.ErrorIs(t, err, ErrForbidden)
	_, err = f.svc.SetPermission(ctx, a.alice, a.org, a.walt, "owner")
	invalid(t, err, "permission")
}

// OMEM-04: an admin promotes, demotes and turns a member into a guest; a guest that becomes a member
// is nobody's guest any more.
func TestSetPermissionChangesTheMembership(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	next := f.events()

	got, err := f.svc.SetPermission(ctx, a.alice, a.org, a.walt, PermissionAdmin)
	require.NoError(t, err)
	require.Equal(t, Member{IID: a.walt, Permission: PermissionAdmin, PositionID: a.trees}, got, "the position stays")

	got, err = f.svc.SetPermission(ctx, a.alice, a.org, a.bob, PermissionGuest)
	require.NoError(t, err)
	require.Equal(t, PermissionGuest, got.Permission, "a member can be made a guest")
	require.Empty(t, got.InviterIID)

	got, err = f.svc.SetPermission(ctx, a.alice, a.org, a.gus, PermissionMember)
	require.NoError(t, err)
	require.Equal(t, Member{IID: a.gus, Permission: PermissionMember}, got, "a promoted guest has no inviter")
	require.Empty(t, f.str(`SELECT inviter_iid FROM org_members WHERE org_id = ? AND iid = ?`, a.org, a.gus))

	got, err = f.svc.SetPermission(ctx, a.alice, a.org, a.w1, PermissionAdmin)
	require.NoError(t, err)
	require.Equal(t, Member{IID: a.w1, Permission: PermissionAdmin, PositionID: a.trees, CreatorIID: a.lee}, got, "an AI too")

	require.Equal(t, []events.Event{
		events.OrgChanged{OrgID: a.org}, events.OrgChanged{OrgID: a.org}, events.OrgChanged{OrgID: a.org}, events.OrgChanged{OrgID: a.org},
	}, next())
	audited := f.audited()
	require.Len(t, audited, 4)
	require.Equal(t, "org.member_permission "+a.org, audited[0])
	rows, _, err := f.audit.List(ctx, "")
	require.NoError(t, err)
	var detail map[string]any
	require.NoError(t, json.Unmarshal([]byte(rows[3].DetailJson), &detail))
	require.Equal(t, map[string]any{"iid": a.walt, "from": "member", "to": "admin"}, detail)

	// The same permission again is nothing.
	next()
	got, err = f.svc.SetPermission(ctx, a.alice, a.org, a.walt, PermissionAdmin)
	require.NoError(t, err)
	require.Equal(t, PermissionAdmin, got.Permission)
	require.Empty(t, next())
	require.Len(t, f.audited(), 4)
}

// OMEM-05: a change that would leave the organization without an admin is refused, whichever way it
// happens - demoting, removing or leaving - and each works when a second admin exists.
func TestTheLastAdminCannotBeDemotedRemovedOrLeave(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		last    func(f *fixture, a acmeChart) error
		second  func(f *fixture, a acmeChart) error
		afterOK func(t *testing.T, f *fixture, a acmeChart)
	}{
		"demoted": {
			last: func(f *fixture, a acmeChart) error {
				_, err := f.svc.SetPermission(ctx, a.alice, a.org, a.alice, PermissionMember)
				return err
			},
			second: func(f *fixture, a acmeChart) error {
				_, err := f.svc.SetPermission(ctx, a.alice, a.org, a.alice, PermissionMember)
				return err
			},
			afterOK: func(t *testing.T, f *fixture, a acmeChart) {
				require.Equal(t, PermissionMember, memberOfOrg(t, f, a.org, a.alice).Permission)
			},
		},
		"demoted to guest": {
			last: func(f *fixture, a acmeChart) error {
				_, err := f.svc.SetPermission(ctx, a.alice, a.org, a.alice, PermissionGuest)
				return err
			},
			second: func(f *fixture, a acmeChart) error {
				_, err := f.svc.SetPermission(ctx, a.alice, a.org, a.alice, PermissionGuest)
				return err
			},
			afterOK: func(t *testing.T, f *fixture, a acmeChart) {
				require.Equal(t, PermissionGuest, memberOfOrg(t, f, a.org, a.alice).Permission)
			},
		},
		"left": {
			last:   func(f *fixture, a acmeChart) error { return f.svc.Leave(ctx, a.alice, a.org) },
			second: func(f *fixture, a acmeChart) error { return f.svc.Leave(ctx, a.alice, a.org) },
			afterOK: func(t *testing.T, f *fixture, a acmeChart) {
				require.ErrorIs(t, f.z.RequireMember(ctx, a.org, a.alice), ErrNotFound)
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			a := f.bareOrg() // alice is the only admin
			next := f.events()
			require.ErrorIs(t, tc.last(f, a), ErrLastAdmin)
			require.Equal(t, PermissionAdmin, memberOfOrg(t, f, a.org, a.alice).Permission, "nothing changed")
			require.Equal(t, a.admin, memberOfOrg(t, f, a.org, a.alice).PositionID)
			require.Empty(t, next())
			require.Empty(t, f.audited())

			f.join(a.org, f.user("zoe"), PermissionAdmin, "", "") // a second admin
			require.NoError(t, tc.second(f, a))
			tc.afterOK(t, f, a)
		})
	}

	// Only an admin can remove an admin, so the last one can only be removed by itself, which is
	// leaving; Remove says so.
	t.Run("removed", func(t *testing.T) {
		f := newFixture(t)
		a := f.bareOrg()
		zoe := f.user("zoe")
		f.join(a.org, zoe, PermissionAdmin, "", "")
		require.NoError(t, f.svc.Remove(ctx, a.alice, a.org, zoe), "with two admins one removes the other")
		require.ErrorIs(t, f.svc.Remove(ctx, a.alice, a.org, a.alice), ErrLastAdmin, "removing yourself is leaving")
		require.ErrorIs(t, f.svc.Remove(ctx, a.bob, a.org, a.alice), ErrForbidden, "and a member cannot remove an admin")
		require.Equal(t, PermissionAdmin, memberOfOrg(t, f, a.org, a.alice).Permission)
	})
}

// OMEM-05: the check is made in the transaction that changes the organization, so two admins who both
// leave at once cannot both succeed.
func TestTwoAdminsLeavingAtOnceLeaveOne(t *testing.T) {
	ctx := context.Background()
	for round := range 8 {
		f := newFixture(t)
		a := f.bareOrg()
		zoe := f.user("zoe")
		f.join(a.org, zoe, PermissionAdmin, "", "")
		start := make(chan struct{})
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i, who := range []string{a.alice, zoe} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs[i] = f.svc.Leave(ctx, who, a.org)
			}()
		}
		close(start)
		wg.Wait()
		succeeded := 0
		for _, err := range errs {
			if err == nil {
				succeeded++
			} else {
				require.ErrorIs(t, err, ErrLastAdmin)
			}
		}
		require.Equal(t, 1, succeeded, "round %d: %v", round, errs)
		require.Equal(t, 1, f.count(`SELECT COUNT(*) FROM org_members WHERE org_id = ? AND permission = 'admin'`, a.org))
	}
}

// POS-04: an admin gives anyone any position of the organization, or none; a position that is not the
// organization's is told to the admin as a validation error.
func TestAdminSetsAnyPosition(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	other := f.position(f.org("globex"), "Boss", "")
	next := f.events()

	got, err := f.svc.SetPosition(ctx, a.alice, a.org, a.bob, a.manager)
	require.NoError(t, err)
	require.Equal(t, Member{IID: a.bob, Permission: PermissionMember, PositionID: a.manager}, got)
	got, err = f.svc.SetPosition(ctx, a.alice, a.org, a.mia, a.admin)
	require.NoError(t, err)
	require.Equal(t, a.admin, got.PositionID, "an admin is not bound by positions")
	got, err = f.svc.SetPosition(ctx, a.alice, a.org, a.lee, "")
	require.NoError(t, err)
	require.Empty(t, got.PositionID, "none")
	got, err = f.svc.SetPosition(ctx, a.alice, a.org, a.alice, a.trees)
	require.NoError(t, err)
	require.Equal(t, a.trees, got.PositionID, "an admin moves itself too")
	_, err = f.svc.SetPosition(ctx, a.alice, a.org, a.bob, other)
	invalid(t, err, "position_id")
	_, err = f.svc.SetPosition(ctx, a.alice, a.org, a.bob, "p-nothing")
	invalid(t, err, "position_id")
	require.Equal(t, a.manager, memberOfOrg(t, f, a.org, a.bob).PositionID, "a refused position changes nothing")
	require.Len(t, next(), 4)
	require.Len(t, f.audited(), 4)
	rows, _, err := f.audit.List(ctx, "")
	require.NoError(t, err)
	var detail map[string]any
	require.NoError(t, json.Unmarshal([]byte(rows[3].DetailJson), &detail))
	require.Equal(t, map[string]any{"iid": a.bob, "from": "", "to": a.manager}, detail)
}

// POS-04: a manager moves a subordinate between positions strictly below its own; to its own level,
// above it, to none, or by someone who does not manage the subordinate, it is forbidden.
func TestManagerMovesASubordinateBelowItself(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme() // mia holds Manager, lee Lead, walt Worktrees, dee Designer; notes (created by lee) has no position
	extra := f.position(a.org, "Reviewers", a.lead)

	got, err := f.svc.SetPosition(ctx, a.mia, a.org, a.walt, extra)
	require.NoError(t, err)
	require.Equal(t, extra, got.PositionID, "a subordinate moves to another position below the manager's own")
	got, err = f.svc.SetPosition(ctx, a.mia, a.org, a.walt, a.lead)
	require.NoError(t, err)
	require.Equal(t, a.lead, got.PositionID, "Lead is below Manager")

	for name, tc := range map[string]struct{ actor, target, position string }{
		"to its own level":                     {a.mia, a.walt, a.manager},
		"above its own level":                  {a.mia, a.walt, a.director},
		"to the root":                          {a.mia, a.walt, a.admin},
		"to a position of another branch":      {a.mia, a.walt, a.designer},
		"to none":                              {a.mia, a.walt, ""},
		"a peer of the target":                 {a.dee, a.walt, a.trees},
		"a subordinate of the target":          {a.walt, a.lee, a.trees},
		"the target itself":                    {a.walt, a.walt, extra},
		"the manager itself":                   {a.mia, a.mia, extra},
		"someone the manager does not manage":  {a.mia, a.dee, extra},
		"a position that does not exist":       {a.mia, a.walt, "p-nothing"},
		"an inviter without a position":        {a.bob, a.gus, a.trees},
		"a member of no relation to an AI":     {a.dee, a.notes, a.trees},
		"a member without a position on an AI": {a.bob, a.notes, a.trees},
		"a subordinate moving its manager":     {a.walt, a.mia, a.trees},
		"an AI moving its creator":             {a.notes, a.lee, a.trees},
		"a guest moving anyone":                {a.gus, a.walt, a.trees},
		"a manager moving an admin above it":   {a.mia, a.alice, extra},
		"a peer (walt now holds Lead too)":     {a.lee, a.walt, a.trees},
	} {
		_, err := f.svc.SetPosition(ctx, tc.actor, a.org, tc.target, tc.position)
		require.ErrorIs(t, err, ErrForbidden, name)
	}
	require.Equal(t, a.lead, memberOfOrg(t, f, a.org, a.walt).PositionID, "none of them changed anything")
	require.Equal(t, a.designer, memberOfOrg(t, f, a.org, a.dee).PositionID)

	// A creator manages the AI it created, which has no position; the manager of the creator too.
	got, err = f.svc.SetPosition(ctx, a.lee, a.org, a.notes, a.trees)
	require.NoError(t, err)
	require.Equal(t, a.trees, got.PositionID, "lee positions the AI it created below its own Lead position")
	got, err = f.svc.SetPosition(ctx, a.mia, a.org, a.notes, extra)
	require.NoError(t, err)
	require.Equal(t, extra, got.PositionID, "so does the manager of its creator")
	// ... and an inviter with a position positions its guest.
	g := f.user("guest2")
	f.join(a.org, g, PermissionGuest, "", a.lee)
	got, err = f.svc.SetPosition(ctx, a.lee, a.org, g, a.trees)
	require.NoError(t, err)
	require.Equal(t, a.trees, got.PositionID)
}

// POS-04: a manager cannot clear a position, an admin can.
func TestOnlyAnAdminClearsAPosition(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	_, err := f.svc.SetPosition(ctx, a.mia, a.org, a.walt, "")
	require.ErrorIs(t, err, ErrForbidden)
	require.Equal(t, a.trees, memberOfOrg(t, f, a.org, a.walt).PositionID)
	got, err := f.svc.SetPosition(ctx, a.alice, a.org, a.walt, "")
	require.NoError(t, err)
	require.Empty(t, got.PositionID)
}

func TestSetPositionNeedsMembers(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	out := f.user("out")
	_, err := f.svc.SetPosition(ctx, out, a.org, a.walt, a.trees)
	require.ErrorIs(t, err, ErrNotFound, "an outsider does not see the organization")
	_, err = f.svc.SetPosition(ctx, a.alice, a.org, out, a.trees)
	require.ErrorIs(t, err, ErrNotFound, "the target is not a member")
	_, err = f.svc.SetPosition(ctx, a.alice, "o-nothing", a.walt, a.trees)
	require.ErrorIs(t, err, ErrNotFound)
	// The same position is nothing: no audit entry, no announcement.
	next := f.events()
	got, err := f.svc.SetPosition(ctx, a.mia, a.org, a.walt, a.trees)
	require.NoError(t, err)
	require.Equal(t, a.trees, got.PositionID)
	got, err = f.svc.SetPosition(ctx, a.alice, a.org, a.bob, "")
	require.NoError(t, err)
	require.Empty(t, got.PositionID)
	require.Empty(t, next())
	require.Empty(t, f.audited())
}

// OMEM-06: a member who leaves or is removed is taken out of the organization's groups and of its
// position; its groups of other organizations, the AIs of the organization and its other memberships
// stay.
func TestRemovalDropsOnlyThatOrganizationsGroupsAndKeepsTheAIs(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme() // lee created notes and w1, whose home is acme
	globex := f.org("globex")
	f.join(globex, a.lee, PermissionMember, "", "")
	f.group("g-acme-1", a.alice, a.org)
	f.group("g-acme-2", a.alice, a.org)
	f.group("g-globex", a.alice, globex)
	f.exec(`INSERT INTO rooms (id, owner_id, name, created_at, updated_at, kind, dm_a, dm_b) VALUES ('dm-1', ?, '', 1, 1, 'dm', ?, ?)`,
		a.lee, a.alice, a.lee) // a DM belongs to no organization
	for _, room := range []string{"g-acme-1", "g-acme-2", "g-globex"} {
		f.roomMember(room, "user", a.lee)
	}
	f.roomMember("dm-1", "user", a.lee)
	f.roomMember("g-acme-1", "agent", a.notes)
	f.roomMember("g-acme-1", "user", a.bob)
	f.exec(`UPDATE users SET default_org_id = ? WHERE id = ?`, a.org, a.lee)
	next := f.events()

	require.NoError(t, f.svc.Remove(ctx, a.alice, a.org, a.lee))

	_, ok, err := f.z.Member(ctx, a.org, a.lee)
	require.NoError(t, err)
	require.False(t, ok, "out of the organization, and so of its position")
	require.Equal(t, []string{"dm-1", "g-globex"}, f.roomsOf(a.lee), "only the groups of acme are gone: the other organization's group and the DM stay")
	require.Equal(t, []string{"g-acme-1"}, f.roomsOf(a.bob), "other members stay")
	require.Equal(t, a.lee, f.str(`SELECT creator_iid FROM agents WHERE id = ?`, a.notes), "the AIs it created keep it as their creator")
	require.Equal(t, a.org, f.str(`SELECT org_id FROM agents WHERE id = ?`, a.notes), "the AIs of the organization stay in it")
	require.Equal(t, a.org, f.str(`SELECT org_id FROM agents WHERE id = ?`, a.w1))
	require.Equal(t, PermissionMember, memberOfOrg(t, f, a.org, a.notes).Permission)
	require.Equal(t, PermissionGuest, memberOfOrg(t, f, a.org, a.w1).Permission)
	require.Equal(t, PermissionMember, memberOfOrg(t, f, globex, a.lee).Permission, "its other organization")
	require.Empty(t, f.str(`SELECT default_org_id FROM users WHERE id = ?`, a.lee), "the default organization it left is cleared")
	c, err := Load(ctx, f.st.R(), a.org)
	require.NoError(t, err)
	require.False(t, c.Manages(a.lee, a.notes), "a creator who left manages nothing there")
	require.False(t, c.Manages(a.mia, a.notes), "and the chain through the creator ends: only an admin reaches the AI now")
	require.True(t, c.CanDelete(a.alice, a.notes))

	got := next()
	require.Contains(t, got, events.OrgChanged{OrgID: a.org})
	require.Contains(t, got, events.MembersChanged{RoomID: "g-acme-1"})
	require.Contains(t, got, events.MembersChanged{RoomID: "g-acme-2"})
	require.NotContains(t, got, events.MembersChanged{RoomID: "g-globex"})
	require.Equal(t, []string{"org.member_remove " + a.org}, f.audited())
	rows, _, err := f.audit.List(ctx, "")
	require.NoError(t, err)
	require.Equal(t, a.alice, rows[0].ActorID)
	var detail map[string]any
	require.NoError(t, json.Unmarshal([]byte(rows[0].DetailJson), &detail))
	require.Equal(t, map[string]any{"iid": a.lee, "groups": float64(2)}, detail)
}

// POS-05: removing someone follows the deletion rule - an admin removes anyone, a manager or an inviter
// only a guest below them - and an identity removes itself by leaving. Each case alone (L-018).
func TestRemoveFollowsTheDeletionRule(t *testing.T) {
	ctx := context.Background()
	// A case picks who acts and who is removed on a fresh acme chart.
	type pick func(f *fixture, a acmeChart) (actor, target string)
	guestBelow := func(f *fixture, a acmeChart) string { // a guest held in Worktrees, with no inviter
		g := f.user("guestw")
		f.join(a.org, g, PermissionGuest, a.trees, "")
		return g
	}
	guestOfLee := func(f *fixture, a acmeChart) string { // a guest lee invited
		g := f.user("guestl")
		f.join(a.org, g, PermissionGuest, "", a.lee)
		return g
	}
	for name, tc := range map[string]struct {
		pick pick
		want error // nil: removed
	}{
		"an admin removes a member with no position":        {func(_ *fixture, a acmeChart) (string, string) { return a.alice, a.bob }, nil},
		"an admin removes a member who holds a position":    {func(_ *fixture, a acmeChart) (string, string) { return a.alice, a.dee }, nil},
		"an admin removes a guest":                          {func(_ *fixture, a acmeChart) (string, string) { return a.alice, a.gus }, nil},
		"an admin removes another admin":                    {func(f *fixture, a acmeChart) (string, string) { return a.alice, f.adminOf(a.org) }, nil},
		"an inviter removes its guest":                      {func(_ *fixture, a acmeChart) (string, string) { return a.bob, a.gus }, nil},
		"a manager removes a guest positioned below it":     {func(f *fixture, a acmeChart) (string, string) { return a.mia, guestBelow(f, a) }, nil},
		"the manager of an inviter removes its guest":       {func(f *fixture, a acmeChart) (string, string) { return a.mia, guestOfLee(f, a) }, nil},
		"a manager cannot remove a member below":            {func(_ *fixture, a acmeChart) (string, string) { return a.mia, a.walt }, ErrForbidden},
		"a manager cannot remove an AI member below":        {func(_ *fixture, a acmeChart) (string, string) { return a.mia, a.notes }, ErrForbidden},
		"a peer cannot remove a guest it did not invite":    {func(_ *fixture, a acmeChart) (string, string) { return a.dee, a.gus }, ErrForbidden},
		"a manager of another branch cannot remove a guest": {func(f *fixture, a acmeChart) (string, string) { return a.dee, guestOfLee(f, a) }, ErrForbidden},
		"a member cannot remove an admin":                   {func(_ *fixture, a acmeChart) (string, string) { return a.bob, a.alice }, ErrForbidden},
		"a guest cannot remove its inviter":                 {func(_ *fixture, a acmeChart) (string, string) { return a.gus, a.bob }, ErrForbidden},
		"an AI cannot remove anyone":                        {func(_ *fixture, a acmeChart) (string, string) { return a.notes, a.bob }, ErrForbidden},
		"an outsider does not see the organization":         {func(f *fixture, a acmeChart) (string, string) { return f.user("out"), a.bob }, ErrNotFound},
		"the target is not a member":                        {func(f *fixture, a acmeChart) (string, string) { return a.alice, f.user("out") }, ErrNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			a := f.acme()
			actor, target := tc.pick(f, a)
			err := f.svc.Remove(ctx, actor, a.org, target)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				require.Empty(t, f.audited())
				return
			}
			require.NoError(t, err)
			_, ok, err := f.z.Member(ctx, a.org, target)
			require.NoError(t, err)
			require.False(t, ok, "removed")
			require.Equal(t, []string{"org.member_remove " + a.org}, f.audited())
		})
	}
	require.ErrorIs(t, newFixture(t).svc.Remove(ctx, "u-a", "o-nothing", "u-b"), ErrNotFound, "no such organization")
}

// POS-06, OMEM-06: removing an AI of another organization takes it out of this one and its groups only;
// it stays in its home organization with everything else.
func TestRemovingAnAIOfAnotherOrganization(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	globex := f.org("globex")
	xi := f.user("xi")
	bot := f.ai("visitor", xi)
	f.home(bot, globex, xi, PermissionMember, "")
	f.join(a.org, bot, PermissionMember, a.designer, "")
	f.group("g-acme", a.alice, a.org)
	f.group("g-globex", xi, globex)
	f.roomMember("g-acme", "agent", bot)
	f.roomMember("g-globex", "agent", bot)

	require.NoError(t, f.svc.Remove(ctx, a.alice, a.org, bot))

	require.ErrorIs(t, f.z.RequireMember(ctx, a.org, bot), ErrNotFound)
	require.Equal(t, []string{"g-globex"}, f.roomsOf(bot))
	require.Equal(t, globex, f.str(`SELECT org_id FROM agents WHERE id = ?`, bot), "it still exists, at home")
	require.Equal(t, PermissionMember, memberOfOrg(t, f, globex, bot).Permission)
}

// POS-06: an AI whose home is the organization is deleted for good there, not removed: Remove and
// Leave refuse it (409 home_organization), whoever asks. Its other organizations are another matter.
func TestAnAIDoesNotLeaveItsHomeOrganization(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	require.ErrorIs(t, f.svc.Leave(ctx, a.notes, a.org), ErrHomeOrganization, "an AI leaving by itself")
	require.ErrorIs(t, f.svc.Remove(ctx, a.notes, a.org, a.notes), ErrHomeOrganization, "which is what Remove with itself means")
	require.ErrorIs(t, f.svc.Remove(ctx, a.alice, a.org, a.notes), ErrHomeOrganization, "an admin cannot remove it either: it is deleted")
	require.ErrorIs(t, f.svc.Remove(ctx, a.lee, a.org, a.w1), ErrHomeOrganization, "nor the manager that may delete a guest AI")
	require.Equal(t, PermissionMember, memberOfOrg(t, f, a.org, a.notes).Permission)
	require.Equal(t, PermissionGuest, memberOfOrg(t, f, a.org, a.w1).Permission)
	require.Empty(t, f.audited())

	// A user, and an AI in an organization that is not its home, leave freely.
	require.NoError(t, f.svc.Leave(ctx, a.walt, a.org))
	globex := f.org("globex")
	f.join(globex, a.notes, PermissionMember, "", "")
	require.NoError(t, f.svc.Leave(ctx, a.notes, globex))
	require.ErrorIs(t, f.z.RequireMember(ctx, globex, a.notes), ErrNotFound)
	require.NoError(t, f.z.RequireMember(ctx, a.org, a.notes), "still at home")
	require.Equal(t, []string{"org.member_leave " + a.org, "org.member_leave " + globex}, f.audited(), "each leave is audited")
}

func TestLeaveNeedsMembership(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	require.ErrorIs(t, f.svc.Leave(ctx, f.user("out"), a.org), ErrNotFound)
	require.ErrorIs(t, f.svc.Leave(ctx, a.bob, "o-nothing"), ErrNotFound)
	require.NoError(t, f.svc.Leave(ctx, a.gus, a.org))
	require.ErrorIs(t, f.svc.Leave(ctx, a.gus, a.org), ErrNotFound, "twice")
	require.Equal(t, []string{"org.member_leave " + a.org}, f.audited())
}

// OMEM-08: an AI that stops being a member of a connection's organization no longer operates the
// connection: the operator is cleared, the AI's queue for that connection goes, and the connection is
// told to stop. Its other connections are untouched. (Operators are home AIs, which never leave, so this
// guards against a state that should not exist.)
func TestRemovingAnOperatorClearsItsConnections(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	globex := f.org("globex")
	xi := f.user("xi")
	bot := f.ai("visitor", xi)
	f.home(bot, globex, xi, PermissionMember, "")
	f.join(a.org, bot, PermissionMember, "", "")
	f.connector("k-acme", a.alice, a.org)
	f.connector("k-acme-2", a.alice, a.org)
	f.connector("k-globex", xi, globex)
	f.exec(`UPDATE connectors SET operator_iid = ? WHERE id IN ('k-acme', 'k-globex')`, bot)
	f.queued("item-acme", bot, "k-acme")
	f.queued("item-globex", bot, "k-globex")
	next := f.events()

	require.NoError(t, f.svc.Remove(ctx, a.alice, a.org, bot))

	require.Empty(t, f.str(`SELECT operator_iid FROM connectors WHERE id = 'k-acme'`), "no operator any more")
	require.Equal(t, bot, f.str(`SELECT operator_iid FROM connectors WHERE id = 'k-globex'`), "the connection of its home organization is its own")
	require.Zero(t, f.count(`SELECT COUNT(*) FROM platform_items WHERE id = 'item-acme'`), "its queue of that connection goes")
	require.Equal(t, 1, f.count(`SELECT COUNT(*) FROM platform_items WHERE id = 'item-globex'`))
	require.Contains(t, next(), events.ConnectorsOrphaned{IDs: []string{"k-acme"}})
}

// A removal that fails leaves everything as it was: groups, operator and membership.
func TestAFailedRemovalChangesNothing(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	f.group("g1", a.alice, a.org)
	f.roomMember("g1", "agent", a.notes)
	next := f.events()
	require.ErrorIs(t, f.svc.Remove(ctx, a.alice, a.org, a.notes), ErrHomeOrganization)
	require.Equal(t, []string{"g1"}, f.roomsOf(a.notes), "its group membership stayed")
	require.Empty(t, next())
}

// OMEM-04, POS-04: UpdateMember changes the permission and the position in one transaction: both change,
// with one audit entry each and one announcement, or neither does.
func TestUpdateMemberChangesPermissionAndPositionTogether(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	next := f.events()

	got, err := f.svc.UpdateMember(ctx, a.alice, a.org, a.bob, MemberUpdate{Permission: ptr(PermissionGuest), PositionID: ptr(a.designer)})
	require.NoError(t, err)
	require.Equal(t, Member{IID: a.bob, Permission: PermissionGuest, PositionID: a.designer}, got)
	require.Equal(t, got, memberOfOrg(t, f, a.org, a.bob))
	require.Equal(t, []string{"org.member_permission " + a.org, "org.member_position " + a.org}, f.audited())
	require.Equal(t, []events.Event{events.OrgChanged{OrgID: a.org}}, next(), "one announcement for the request")

	// Only one of the two changes is a change: only that one is recorded.
	got, err = f.svc.UpdateMember(ctx, a.alice, a.org, a.bob, MemberUpdate{Permission: ptr(PermissionGuest), PositionID: ptr(a.manager)})
	require.NoError(t, err)
	require.Equal(t, a.manager, got.PositionID)
	require.Len(t, f.audited(), 3)
	require.Len(t, next(), 1)
	// Nothing changes at all: nothing is recorded or announced.
	_, err = f.svc.UpdateMember(ctx, a.alice, a.org, a.bob, MemberUpdate{Permission: ptr(PermissionGuest), PositionID: ptr(a.manager)})
	require.NoError(t, err)
	require.Len(t, f.audited(), 3)
	require.Empty(t, next())
}

// A change that is refused changes nothing, not even the half of it that would have been fine.
func TestAFailedUpdateMemberChangesNothing(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	other := f.position(f.org("globex"), "Boss", "")
	next := f.events()
	unchanged := func(iid string, want Member) {
		t.Helper()
		require.Equal(t, want, memberOfOrg(t, f, a.org, iid))
		require.Empty(t, f.audited())
		require.Empty(t, next())
	}

	// The last admin cannot step down, so it does not move either.
	alice := memberOfOrg(t, f, a.org, a.alice)
	_, err := f.svc.UpdateMember(ctx, a.alice, a.org, a.alice, MemberUpdate{Permission: ptr(PermissionMember), PositionID: ptr(a.trees)})
	require.ErrorIs(t, err, ErrLastAdmin)
	unchanged(a.alice, alice)

	// A position the organization does not have stops the permission change.
	bob := memberOfOrg(t, f, a.org, a.bob)
	_, err = f.svc.UpdateMember(ctx, a.alice, a.org, a.bob, MemberUpdate{Permission: ptr(PermissionGuest), PositionID: ptr(other)})
	invalid(t, err, "position_id")
	unchanged(a.bob, bob)

	// A manager may move a subordinate but not change its permission: the move is not made either.
	walt := memberOfOrg(t, f, a.org, a.walt)
	_, err = f.svc.UpdateMember(ctx, a.mia, a.org, a.walt, MemberUpdate{Permission: ptr(PermissionGuest), PositionID: ptr(a.lead)})
	require.ErrorIs(t, err, ErrForbidden)
	unchanged(a.walt, walt)

	// A bad permission is told to the admin, and nothing is moved.
	_, err = f.svc.UpdateMember(ctx, a.alice, a.org, a.bob, MemberUpdate{Permission: ptr(Permission("owner")), PositionID: ptr(a.designer)})
	invalid(t, err, "permission")
	unchanged(a.bob, bob)
}

// The actor's rights are the ones it had when it asked: an admin that steps down and moves in the same
// request is still an admin for the move.
func TestUpdateMemberJudgesTheActorAsItWasWhenItAsked(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	f.adminOf(a.org) // a second admin, so alice may step down

	got, err := f.svc.UpdateMember(ctx, a.alice, a.org, a.alice, MemberUpdate{Permission: ptr(PermissionMember), PositionID: ptr("")})
	require.NoError(t, err)
	require.Equal(t, Member{IID: a.alice, Permission: PermissionMember}, got, "alice stepped down and left her position")
	// Now a plain member, she can neither change a permission nor position anybody.
	_, err = f.svc.UpdateMember(ctx, a.alice, a.org, a.bob, MemberUpdate{Permission: ptr(PermissionGuest)})
	require.ErrorIs(t, err, ErrForbidden)
	_, err = f.svc.UpdateMember(ctx, a.alice, a.org, a.bob, MemberUpdate{PositionID: ptr(a.designer)})
	require.ErrorIs(t, err, ErrForbidden)
}

// A request that asks for nothing needs a member and changes nothing; it names the member as it is.
func TestUpdateMemberWithNothingToChange(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	out := f.user("out")
	next := f.events()

	got, err := f.svc.UpdateMember(ctx, a.bob, a.org, a.walt, MemberUpdate{})
	require.NoError(t, err)
	require.Equal(t, memberOfOrg(t, f, a.org, a.walt), got)
	_, err = f.svc.UpdateMember(ctx, out, a.org, a.walt, MemberUpdate{})
	require.ErrorIs(t, err, ErrNotFound)
	_, err = f.svc.UpdateMember(ctx, a.bob, a.org, out, MemberUpdate{})
	require.ErrorIs(t, err, ErrNotFound, "the target is not a member")
	require.Empty(t, next())
	require.Empty(t, f.audited())
}

// OMEM-01, OMEM-03: the list names each member with its profile summary, permission, position and, for a
// guest, its inviter; every member sees it, a guest included, and only this organization's members are in it.
func TestListMembersShowsTheProfileThePermissionThePositionAndTheInviter(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	globex := f.org("globex")
	stranger := f.user("stranger")
	f.join(globex, stranger, PermissionAdmin, "", "")
	f.exec(`UPDATE intelligences SET avatar_sha256 = 'sha-bob' WHERE id = ?`, a.bob)

	list, err := f.svc.ListMembers(ctx, a.gus, a.org) // a guest sees the members (CHART-02)
	require.NoError(t, err)
	byID := map[string]MemberRow{}
	for _, m := range list {
		byID[m.IID] = m
	}
	require.Len(t, byID, len(list))
	require.Len(t, list, 10, "eight people (one a guest) and two AIs of acme, and not the stranger of globex")
	require.NotContains(t, byID, stranger)

	require.Equal(t, MemberRow{
		IID: a.walt, Permission: "member", PositionID: a.trees, PositionName: "Worktrees",
		Kind: "human", DisplayName: "walt", Handle: "walt", JoinedAt: 1,
	}, byID[a.walt])
	require.Equal(t, MemberRow{
		IID: a.gus, Permission: "guest", Kind: "human", DisplayName: "gus", Handle: "gus", JoinedAt: 1,
		InviterIID: a.bob, InviterKind: "human", InviterDisplayName: "bob", InviterHandle: "bob", InviterAvatarSha256: "sha-bob",
	}, byID[a.gus], "a guest carries the identity that invited it")
	require.Equal(t, "ai", byID[a.w1].Kind)
	require.Equal(t, "guest", byID[a.w1].Permission)
	require.Empty(t, byID[a.bob].InviterIID)
	require.Equal(t, "sha-bob", byID[a.bob].AvatarSha256)

	names := make([]string, len(list))
	for i, m := range list {
		names[i] = m.DisplayName
	}
	require.IsIncreasing(t, names, "ordered by display name")

	_, err = f.svc.ListMembers(ctx, stranger, a.org)
	require.ErrorIs(t, err, ErrNotFound, "an outsider")
	_, err = f.svc.ListMembers(ctx, a.alice, "o-nothing")
	require.ErrorIs(t, err, ErrNotFound)
}

// MemberView is one row of the list; an outsider or a target that is not a member is not found.
func TestMemberViewIsOneRowOfTheList(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	out := f.user("out")

	list, err := f.svc.ListMembers(ctx, a.alice, a.org)
	require.NoError(t, err)
	for _, row := range list {
		got, err := f.svc.MemberView(ctx, a.gus, a.org, row.IID)
		require.NoError(t, err)
		require.Equal(t, row, got)
	}
	_, err = f.svc.MemberView(ctx, a.alice, a.org, out)
	require.ErrorIs(t, err, ErrNotFound, "not a member")
	_, err = f.svc.MemberView(ctx, out, a.org, a.walt)
	require.ErrorIs(t, err, ErrNotFound, "an outsider")
}
