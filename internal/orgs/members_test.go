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

// OMEM-03: an identity is a member of several organizations, with its own permission in each.
func TestOneIdentityInTwoOrganizationsKeepsEachMembership(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	globex := f.org("globex")

	require.NoError(t, f.addMember(AddMemberInput{OrgID: globex, IID: a.walt, Permission: PermissionAdmin}))

	require.Equal(t, Member{IID: a.walt, Permission: PermissionMember}, memberOfOrg(t, f, a.org, a.walt))
	require.Equal(t, Member{IID: a.walt, Permission: PermissionAdmin}, memberOfOrg(t, f, globex, a.walt))
	mine, err := f.svc.ListMine(ctx, a.walt)
	require.NoError(t, err)
	require.Len(t, mine, 2)
	// Changing one membership leaves the other alone.
	_, err = f.svc.SetPermission(ctx, a.alice, a.org, a.walt, PermissionAdmin)
	require.NoError(t, err)
	_, err = f.svc.SetPermission(ctx, a.alice, a.org, a.walt, PermissionMember)
	require.NoError(t, err)
	require.Equal(t, PermissionAdmin, memberOfOrg(t, f, globex, a.walt).Permission)
}

// AddMember is the transaction helper invitations and placement use: one membership per identity and
// organization, and a permission that is admin or member (SR2-01).
func TestAddMember(t *testing.T) {
	f := newFixture(t)
	a := f.acme()
	zed := f.user("zed")

	require.NoError(t, f.addMember(AddMemberInput{OrgID: a.org, IID: zed, Permission: PermissionMember}))
	require.Equal(t, Member{IID: zed, Permission: PermissionMember}, memberOfOrg(t, f, a.org, zed))
	require.Equal(t, int(t0.UnixMilli()), f.count(`SELECT joined_at FROM org_members WHERE org_id = ? AND iid = ?`, a.org, zed), "joined now")

	require.ErrorIs(t, f.addMember(AddMemberInput{OrgID: a.org, IID: zed, Permission: PermissionMember}), ErrAlreadyMember)
	require.ErrorIs(t, f.addMember(AddMemberInput{OrgID: a.org, IID: a.walt, Permission: PermissionAdmin}), ErrAlreadyMember, "any permission")

	xi := f.user("xi")
	for _, in := range []AddMemberInput{
		{OrgID: a.org, IID: xi, Permission: "guest"}, // SR2-01: no guests any more
		{OrgID: a.org, IID: xi, Permission: "owner"}, // an unknown permission
		{OrgID: a.org, IID: xi},                      // none
	} {
		invalid(t, f.addMember(in), "permission")
	}
	require.Zero(t, f.count(`SELECT COUNT(*) FROM org_members WHERE iid = ?`, xi), "a refused join adds nothing")
	// The database refuses a guest too: the CHECK of migration 0040.
	_, err := f.raw.Exec(`INSERT INTO org_members (org_id, iid, permission, joined_at) VALUES (?, ?, 'guest', 1)`, a.org, xi)
	require.Error(t, err)
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
	_, err := f.svc.SetPermission(ctx, a.alice, a.org, out, PermissionAdmin)
	require.ErrorIs(t, err, ErrNotFound, "the target is not a member")
	_, err = f.svc.SetPermission(ctx, a.alice, "o-nothing", a.walt, PermissionAdmin)
	require.ErrorIs(t, err, ErrNotFound)
	require.Equal(t, PermissionMember, memberOfOrg(t, f, a.org, a.walt).Permission)
	require.Empty(t, f.audited())
	// A bad permission is told to the admin only.
	_, err = f.svc.SetPermission(ctx, a.bob, a.org, a.walt, "owner")
	require.ErrorIs(t, err, ErrForbidden)
	_, err = f.svc.SetPermission(ctx, a.alice, a.org, a.walt, "owner")
	invalid(t, err, "permission")
	_, err = f.svc.SetPermission(ctx, a.alice, a.org, a.walt, "guest")
	invalid(t, err, "permission")
}

// OMEM-04: an admin promotes and demotes members, people and AIs alike.
func TestSetPermissionChangesTheMembership(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	next := f.events()

	got, err := f.svc.SetPermission(ctx, a.alice, a.org, a.walt, PermissionAdmin)
	require.NoError(t, err)
	require.Equal(t, Member{IID: a.walt, Permission: PermissionAdmin}, got)

	got, err = f.svc.SetPermission(ctx, a.alice, a.org, a.walt, PermissionMember)
	require.NoError(t, err)
	require.Equal(t, PermissionMember, got.Permission)

	got, err = f.svc.SetPermission(ctx, a.alice, a.org, a.gus, PermissionAdmin)
	require.NoError(t, err)
	require.Equal(t, PermissionAdmin, got.Permission)

	got, err = f.svc.SetPermission(ctx, a.alice, a.org, a.w1, PermissionAdmin)
	require.NoError(t, err)
	require.Equal(t, Member{IID: a.w1, Permission: PermissionAdmin}, got, "an AI too")

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
	got, err = f.svc.SetPermission(ctx, a.alice, a.org, a.w1, PermissionAdmin)
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
			require.Empty(t, next())
			require.Empty(t, f.audited())

			f.join(a.org, f.user("zoe"), PermissionAdmin) // a second admin
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
		f.join(a.org, zoe, PermissionAdmin)
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
		f.join(a.org, zoe, PermissionAdmin)
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

// OMEM-06: a member who leaves or is removed is taken out of the organization's groups and of its
// position; its groups of other organizations, the AIs of the organization and its other memberships
// stay.
func TestRemovalDropsOnlyThatOrganizationsGroupsAndKeepsTheAIs(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme() // lee created notes and w1, whose home is acme
	globex := f.org("globex")
	f.join(globex, a.lee, PermissionMember)
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
	require.False(t, ok, "out of the organization")
	require.Equal(t, []string{"dm-1", "g-globex"}, f.roomsOf(a.lee), "only the groups of acme are gone: the other organization's group and the DM stay")
	require.Equal(t, []string{"g-acme-1"}, f.roomsOf(a.bob), "other members stay")
	require.Equal(t, a.lee, f.str(`SELECT creator_iid FROM agents WHERE id = ?`, a.notes), "the AIs it created keep it as their creator")
	require.Equal(t, a.org, f.str(`SELECT org_id FROM agents WHERE id = ?`, a.notes), "the AIs of the organization stay in it")
	require.Equal(t, a.org, f.str(`SELECT org_id FROM agents WHERE id = ?`, a.w1))
	require.Equal(t, PermissionMember, memberOfOrg(t, f, a.org, a.notes).Permission)
	require.Equal(t, PermissionMember, memberOfOrg(t, f, a.org, a.w1).Permission)
	require.Equal(t, PermissionMember, memberOfOrg(t, f, globex, a.lee).Permission, "its other organization")
	require.Empty(t, f.str(`SELECT default_org_id FROM users WHERE id = ?`, a.lee), "the default organization it left is cleared")
	// SR2-07: the host who left the organization still instructs and manages the AIs it hosts there.
	ok, err = f.z.Trusts(ctx, a.lee, a.notes)
	require.NoError(t, err)
	require.True(t, ok, "hosting is not membership")
	ok, err = f.z.CanDeleteAI(ctx, a.alice, a.notes)
	require.NoError(t, err)
	require.True(t, ok, "and an admin still deletes it")

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

// SR2-12, SR2-03: an admin removes anyone else; nobody else removes anyone - not a host, an instructor or
// whoever invited the target - and an identity removes itself by leaving. Each case alone (L-018).
func TestSR212RemoveIsForAdmins(t *testing.T) {
	ctx := context.Background()
	type pick func(f *fixture, a acmeChart) (actor, target string)
	for name, tc := range map[string]struct {
		pick pick
		want error // nil: removed
	}{
		"an admin removes a member":                 {func(_ *fixture, a acmeChart) (string, string) { return a.alice, a.bob }, nil},
		"an admin removes another admin":            {func(f *fixture, a acmeChart) (string, string) { return a.alice, f.adminOf(a.org) }, nil},
		"a member cannot remove a member":           {func(_ *fixture, a acmeChart) (string, string) { return a.dee, a.gus }, ErrForbidden},
		"a member cannot remove an admin":           {func(_ *fixture, a acmeChart) (string, string) { return a.bob, a.alice }, ErrForbidden},
		"a host cannot remove a person":             {func(_ *fixture, a acmeChart) (string, string) { return a.lee, a.bob }, ErrForbidden},
		"an AI cannot remove anyone":                {func(_ *fixture, a acmeChart) (string, string) { return a.notes, a.bob }, ErrForbidden},
		"an outsider does not see the organization": {func(f *fixture, a acmeChart) (string, string) { return f.user("out"), a.bob }, ErrNotFound},
		"the target is not a member":                {func(f *fixture, a acmeChart) (string, string) { return a.alice, f.user("out") }, ErrNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			a := f.acme()
			actor, target := tc.pick(f, a)
			before := len(f.audited())
			err := f.svc.Remove(ctx, actor, a.org, target)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				require.Len(t, f.audited(), before)
				return
			}
			require.NoError(t, err)
			_, ok, err := f.z.Member(ctx, a.org, target)
			require.NoError(t, err)
			require.False(t, ok, "removed")
			require.Contains(t, f.audited(), "org.member_remove "+a.org)
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
	f.home(bot, globex, xi, PermissionMember)
	f.join(a.org, bot, PermissionMember)
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
	require.ErrorIs(t, f.svc.Remove(ctx, a.lee, a.org, a.w1), ErrForbidden, "its host deletes it, it does not remove members")
	require.Equal(t, PermissionMember, memberOfOrg(t, f, a.org, a.notes).Permission)
	require.Equal(t, PermissionMember, memberOfOrg(t, f, a.org, a.w1).Permission)
	require.Empty(t, f.audited())

	// A user, and an AI in an organization that is not its home, leave freely.
	require.NoError(t, f.svc.Leave(ctx, a.walt, a.org))
	globex := f.org("globex")
	f.join(globex, a.notes, PermissionMember)
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
	f.home(bot, globex, xi, PermissionMember)
	f.join(a.org, bot, PermissionMember)
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

// OMEM-03, SR2-12: the list names each member with its profile summary and permission, and what the viewer
// may do: an admin deletes anyone else, the host deletes the AIs it hosts whose home is the organization; every
// member sees it, and only this organization's members are in it.
func TestListMembersShowsTheProfileThePermissionAndTheRights(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	globex := f.org("globex")
	stranger := f.user("stranger")
	f.join(globex, stranger, PermissionAdmin)
	f.exec(`UPDATE intelligences SET avatar_sha256 = 'sha-bob' WHERE id = ?`, a.bob)

	list, err := f.svc.ListMembers(ctx, a.gus, a.org)
	require.NoError(t, err)
	byID := map[string]MemberRow{}
	for _, m := range list {
		byID[m.IID] = m
	}
	require.Len(t, byID, len(list))
	require.Len(t, list, 10, "eight people and two AIs of acme, and not the stranger of globex")
	require.NotContains(t, byID, stranger)
	require.Equal(t, MemberRow{IID: a.walt, Permission: "member", Kind: "human", DisplayName: "walt", Handle: "walt", JoinedAt: 1}, byID[a.walt])
	require.Equal(t, "ai", byID[a.w1].Kind)
	require.True(t, byID[a.w1].DeletedForGood, "acme is w1's home")
	require.False(t, byID[a.w1].CanDelete, "gus neither administers acme nor hosts w1")
	require.Equal(t, "sha-bob", byID[a.bob].AvatarSha256)

	names := make([]string, len(list))
	for i, m := range list {
		names[i] = m.DisplayName
	}
	require.IsIncreasing(t, names, "ordered by display name")

	rights := func(viewer string) map[string]bool {
		rows, err := f.svc.ListMembers(ctx, viewer, a.org)
		require.NoError(t, err)
		out := map[string]bool{}
		for _, r := range rows {
			out[r.IID] = r.CanDelete
		}
		return out
	}
	admin := rights(a.alice)
	require.True(t, admin[a.bob])
	require.True(t, admin[a.w1])
	require.False(t, admin[a.alice], "not itself")
	host := rights(a.lee)
	require.True(t, host[a.w1], "SR2-12: the host deletes its AI")
	require.True(t, host[a.notes])
	require.False(t, host[a.bob], "but removes no person")
	instructor := rights(a.mia)
	require.False(t, instructor[a.w1], "an instructor does not delete")

	_, err = f.svc.ListMembers(ctx, stranger, a.org)
	require.ErrorIs(t, err, ErrNotFound, "an outsider")
	_, err = f.svc.ListMembers(ctx, a.alice, "o-nothing")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestMemberViewIsOneRowOfTheList(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	out := f.user("out")

	for _, viewer := range []string{a.alice, a.lee, a.mia, a.gus} {
		list, err := f.svc.ListMembers(ctx, viewer, a.org)
		require.NoError(t, err)
		require.NotEmpty(t, list)
		for _, row := range list {
			got, err := f.svc.MemberView(ctx, viewer, a.org, row.IID)
			require.NoError(t, err)
			require.Equal(t, row, got, "%s sees %s", viewer, row.IID)
		}
	}
	_, err := f.svc.MemberView(ctx, a.alice, a.org, out)
	require.ErrorIs(t, err, ErrNotFound, "not a member")
	_, err = f.svc.MemberView(ctx, out, a.org, a.walt)
	require.ErrorIs(t, err, ErrNotFound, "an outsider")
}
