package orgs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/events"
)

// invite is a shorthand for an admin or member inviting by handle.
func (f *fixture) invite(actor, org, handle string, perm Permission, position string) (Invitation, error) {
	f.t.Helper()
	return f.svc.Invite(context.Background(), actor, org, handle, perm, position)
}

// handleOf is the @handle of a fixture identity: its name.
func handleOf(iid string) string { return iid[2:] }

// pending counts the rows of org_invitations.
func (f *fixture) pending() int { return f.count(`SELECT COUNT(*) FROM org_invitations`) }

// OMEM-01, OMEM-02: an admin invites by @handle with any permission and a position; the invitation is
// pending until the invitee accepts, and then the invitee is a member with that permission and position.
func TestAdminInvitesAndTheInviteeJoinsWithThatPermissionAndPosition(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	next := f.events()

	var invitee string
	for _, perm := range []Permission{PermissionAdmin, PermissionMember, PermissionGuest} {
		invitee = f.user("new-" + string(perm))
		inv, err := f.invite(a.alice, a.org, "@"+handleOf(invitee), perm, a.designer)
		require.NoError(t, err, perm)
		require.NotEmpty(t, inv.ID)
		require.Equal(t, a.org, inv.OrgID)
		require.Equal(t, "acme", inv.OrgSlug)
		require.Equal(t, invitee, inv.InviteeIID)
		require.Equal(t, handleOf(invitee), inv.InviteeHandle)
		require.Equal(t, "user", inv.InviteeKind)
		require.Equal(t, perm, inv.Permission)
		require.Equal(t, a.designer, inv.PositionID)
		require.Equal(t, "Designer", inv.PositionName)
		require.Equal(t, a.alice, inv.InvitedByIID)
		require.Equal(t, "alice", inv.InvitedByHandle)
		require.Equal(t, t0, inv.CreatedAt.UTC())
		require.Equal(t, t0.Add(14*24*time.Hour), inv.ExpiresAt.UTC(), "valid for 14 days")
		require.ErrorIs(t, f.z.RequireMember(ctx, a.org, invitee), ErrNotFound, "not a member until it accepts")

		m, err := f.svc.Accept(ctx, invitee, inv.ID)
		require.NoError(t, err)
		require.Equal(t, a.org, m.ID)
		require.Equal(t, perm, m.Permission)
		require.Equal(t, a.designer, m.PositionID)
		joined := memberOfOrg(t, f, a.org, invitee)
		require.Equal(t, perm, joined.Permission, "OMEM-02: the invitation's permission")
		require.Equal(t, a.designer, joined.PositionID, "and its position")
		if perm == PermissionGuest {
			require.Equal(t, a.alice, joined.InviterIID, "an admin's guest is the admin's responsibility")
		} else {
			require.Empty(t, joined.InviterIID)
		}
	}
	require.Zero(t, f.pending(), "answered invitations are deleted")
	got := next()
	require.Contains(t, got, events.OrgChanged{OrgID: a.org})
	var received []events.Event
	for _, e := range got {
		if _, ok := e.(events.InvitationReceived); ok {
			received = append(received, e)
		}
	}
	require.Len(t, received, 3, "the invitee is told of each invitation")

	audited := f.audited()
	require.Equal(t, []string{
		"org.invite " + a.org, "org.invite_accept " + a.org, "org.member_join " + a.org,
		"org.invite " + a.org, "org.invite_accept " + a.org, "org.member_join " + a.org,
		"org.invite " + a.org, "org.invite_accept " + a.org, "org.member_join " + a.org,
	}, audited)
	rows, _, err := f.audit.List(ctx, "")
	require.NoError(t, err)
	var detail map[string]any
	require.NoError(t, json.Unmarshal([]byte(rows[0].DetailJson), &detail))
	require.Equal(t, invitee, rows[0].ActorID, "the join is the invitee's act")
	require.Equal(t, "invitation", detail["by"])
	require.Equal(t, "guest", detail["permission"])
}

// OMEM-01, OMEM-04: any member or guest invites guests, with no position; every other permission, or a
// position, is forbidden. The inviter becomes the guest's inviter, so a guest invited by a guest
// records the chain.
func TestMembersAndGuestsInviteGuestsOnly(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	carol, dave := f.user("carol"), f.user("dave")

	invCarol, err := f.invite(a.bob, a.org, "carol", PermissionGuest, "")
	require.NoError(t, err, "a member invites a guest")
	_, err = f.svc.Accept(ctx, carol, invCarol.ID)
	require.NoError(t, err)
	require.Equal(t, Member{IID: carol, Permission: PermissionGuest, InviterIID: a.bob}, memberOfOrg(t, f, a.org, carol), "bob is carol's inviter")

	invDave, err := f.invite(carol, a.org, "dave", PermissionGuest, "")
	require.NoError(t, err, "a guest invites a guest")
	_, err = f.svc.Accept(ctx, dave, invDave.ID)
	require.NoError(t, err)
	require.Equal(t, Member{IID: dave, Permission: PermissionGuest, InviterIID: carol}, memberOfOrg(t, f, a.org, dave), "carol is dave's inviter")

	c, err := f.z.Chart(ctx, a.org)
	require.NoError(t, err)
	require.True(t, c.Manages(carol, dave), "the chain: carol is responsible for dave")
	require.True(t, c.Manages(a.bob, dave), "and bob, who invited carol, for dave too")
	require.True(t, c.CanDelete(a.bob, dave))
	require.False(t, c.Manages(a.dee, dave))

	// An AI that is a member invites like any member.
	f.user("yan")
	_, err = f.invite(a.notes, a.org, "yan", PermissionGuest, "")
	require.NoError(t, err)

	// Everything else is forbidden: one case alone each (L-018).
	zed := f.user("zed")
	for _, tc := range []struct {
		actor    string
		perm     Permission
		position string
		what     string
	}{
		{a.bob, PermissionMember, "", "a member inviting as member"},
		{a.bob, PermissionAdmin, "", "a member inviting as admin"},
		{a.bob, PermissionGuest, a.trees, "a member inviting a guest with a position"},
		{a.lee, PermissionMember, "", "a manager inviting as member"},
		{a.lee, PermissionGuest, a.trees, "a manager inviting a guest into a position below its own"},
		{a.gus, PermissionGuest, a.trees, "a guest inviting with a position"},
		{a.gus, PermissionMember, "", "a guest inviting as member"},
		{a.gus, PermissionAdmin, "", "a guest inviting as admin"},
		{a.bob, "owner", "", "a member inviting with a permission that does not exist"},
		{a.bob, "", "", "a member inviting with no permission"},
	} {
		_, err := f.invite(tc.actor, a.org, "zed", tc.perm, tc.position)
		require.ErrorIs(t, err, ErrForbidden, tc.what)
	}
	_, err = f.invite(f.user("out"), a.org, "zed", PermissionGuest, "")
	require.ErrorIs(t, err, ErrNotFound, "an outsider does not see the organization")
	require.Zero(t, f.count(`SELECT COUNT(*) FROM org_invitations WHERE invitee_iid = ?`, zed), "nothing was invited")

	// An admin is told when the permission or the position is not valid.
	other := f.position(f.org("globex"), "Boss", "")
	_, err = f.invite(a.alice, a.org, "zed", "owner", "")
	invalid(t, err, "permission")
	_, err = f.invite(a.alice, a.org, "zed", PermissionMember, other)
	invalid(t, err, "position_id")
	_, err = f.invite(a.alice, a.org, "zed", PermissionMember, "p-nothing")
	invalid(t, err, "position_id")
}

// OMEM-01: the invitee is a user or an AI looked up by @handle, case-insensitively; a person (an
// external account), an unknown handle and a disabled account are not found.
func TestInviteResolvesUsersAndAIsByHandle(t *testing.T) {
	f := newFixture(t)
	a := f.acme()
	xi := f.user("xi")
	bot := f.ai("visitor", xi)
	gone := f.user("gone")
	f.exec(`UPDATE users SET disabled_at = 1 WHERE id = ?`, gone)
	connector := "k-person"
	f.connector(connector, xi, "")
	f.exec(`INSERT INTO people (id, connector_id, external_id, created_at) VALUES ('p-maria', ?, '100', 1)`, connector)
	f.exec(`INSERT INTO intelligences (id, kind, display_name, handle, created_at, updated_at) VALUES ('p-maria', 'human', 'Maria', 'maria', 1, 1)`)

	for _, ref := range []string{"@visitor", "visitor", "@Visitor", "VISITOR"} {
		inv, err := f.invite(a.alice, a.org, ref, PermissionMember, "")
		require.NoError(t, err, ref)
		require.Equal(t, bot, inv.InviteeIID)
		require.Equal(t, "ai", inv.InviteeKind)
	}
	require.Equal(t, 1, f.pending(), "inviting again is the same invitation")
	for _, ref := range []string{"@maria", "maria", "@nobody", "", "@", "gone", "@ gone"} {
		_, err := f.invite(a.alice, a.org, ref, PermissionMember, "")
		require.ErrorIs(t, err, ErrContactNotFound, "%q", ref)
	}
	require.Equal(t, 1, f.pending())
}

// OMEM-01: an identity that already is a member, with any permission, cannot be invited.
func TestInvitingAMemberIsRefused(t *testing.T) {
	f := newFixture(t)
	a := f.acme()
	for _, iid := range []string{a.alice, a.bob, a.gus, a.notes, a.w1} {
		_, err := f.invite(a.alice, a.org, handleOf(iid), PermissionMember, "")
		require.ErrorIs(t, err, ErrAlreadyMember, iid)
	}
	require.Zero(t, f.pending())
	require.Empty(t, f.audited())
}

// OMEM-01: inviting again updates the pending invitation - its permission, position and inviter, and its
// 14 days start over - and does not add a second one. Someone who may invite only guests cannot replace a
// pending invitation that is not for a guest.
func TestInvitingAgainUpdatesThePendingInvitation(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	zed := f.user("zed")

	first, err := f.invite(a.alice, a.org, "zed", PermissionMember, a.designer)
	require.NoError(t, err)
	f.clock.Advance(3 * 24 * time.Hour)
	second, err := f.invite(a.alice, a.org, "zed", PermissionAdmin, "")
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID, "the same invitation")
	require.Equal(t, PermissionAdmin, second.Permission)
	require.Empty(t, second.PositionID)
	require.Equal(t, t0.Add(3*24*time.Hour), second.CreatedAt.UTC())
	require.Equal(t, t0.Add(17*24*time.Hour), second.ExpiresAt.UTC(), "14 days from the new invitation")
	require.Equal(t, 1, f.pending())

	// A member who may only invite guests cannot downgrade the admin's invitation.
	_, err = f.invite(a.bob, a.org, "zed", PermissionGuest, "")
	require.ErrorIs(t, err, ErrForbidden)
	got, err := f.svc.ListInvitations(ctx, a.alice, a.org)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, PermissionAdmin, got[0].Permission, "unchanged")
	require.Equal(t, a.alice, got[0].InvitedByIID)

	// An invitation for a guest can be replaced by another inviter: the latest inviter is responsible.
	require.NoError(t, f.svc.Revoke(ctx, a.alice, a.org, second.ID))
	_, err = f.invite(a.bob, a.org, "zed", PermissionGuest, "")
	require.NoError(t, err)
	third, err := f.invite(a.lee, a.org, "zed", PermissionGuest, "")
	require.NoError(t, err)
	require.Equal(t, a.lee, third.InvitedByIID)
	m, err := f.svc.Accept(ctx, zed, third.ID)
	require.NoError(t, err)
	require.Equal(t, PermissionGuest, m.Permission)
	require.Equal(t, a.lee, memberOfOrg(t, f, a.org, zed).InviterIID, "the latest inviter")
}

// OMEM-07: an invitation is valid for 14 days: accepting one millisecond before works, at 14 days it is
// gone (410). Declining an expired one is gone too, and the lists no longer show it.
func TestAnInvitationExpiresAfterFourteenDays(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	early, late := f.user("early"), f.user("late")
	invEarly, err := f.invite(a.alice, a.org, "early", PermissionMember, "")
	require.NoError(t, err)
	invLate, err := f.invite(a.alice, a.org, "late", PermissionMember, "")
	require.NoError(t, err)

	f.clock.Advance(14*24*time.Hour - time.Millisecond)
	mine, err := f.svc.MyInvitations(ctx, early)
	require.NoError(t, err)
	require.Len(t, mine, 1, "still pending one millisecond before")
	m, err := f.svc.Accept(ctx, early, invEarly.ID)
	require.NoError(t, err, "14 days minus 1 ms")
	require.Equal(t, PermissionMember, m.Permission)

	f.clock.Advance(time.Millisecond)
	_, err = f.svc.Accept(ctx, late, invLate.ID)
	require.ErrorIs(t, err, ErrGone, "14 days")
	require.ErrorIs(t, f.svc.Decline(ctx, late, invLate.ID), ErrGone)
	require.ErrorIs(t, f.z.RequireMember(ctx, a.org, late), ErrNotFound, "not a member")
	mine, err = f.svc.MyInvitations(ctx, late)
	require.NoError(t, err)
	require.Empty(t, mine, "an expired invitation is not shown")
	forOrg, err := f.svc.ListInvitations(ctx, a.alice, a.org)
	require.NoError(t, err)
	require.Empty(t, forOrg)
	require.Equal(t, 1, f.pending(), "the row waits for the retention job")

	// Inviting again after the expiry starts a new invitation.
	fresh, err := f.invite(a.alice, a.org, "late", PermissionGuest, "")
	require.NoError(t, err)
	require.Equal(t, t0.Add(28*24*time.Hour), fresh.ExpiresAt.UTC())
	_, err = f.svc.Accept(ctx, late, fresh.ID)
	require.NoError(t, err)
}

// An invitation that expired no longer protects its permission: a member may invite the same identity as
// a guest, which it could not while the admin's invitation was pending.
func TestAnExpiredInvitationCanBeReplacedByAGuestInvitation(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	zed := f.user("zed")
	_, err := f.invite(a.alice, a.org, "zed", PermissionAdmin, "")
	require.NoError(t, err)
	_, err = f.invite(a.bob, a.org, "zed", PermissionGuest, "")
	require.ErrorIs(t, err, ErrForbidden, "while the admin's invitation is pending")

	f.clock.Advance(InvitationTTL - time.Millisecond)
	_, err = f.invite(a.bob, a.org, "zed", PermissionGuest, "")
	require.ErrorIs(t, err, ErrForbidden, "one millisecond before it expires")

	f.clock.Advance(time.Millisecond)
	inv, err := f.invite(a.bob, a.org, "zed", PermissionGuest, "")
	require.NoError(t, err, "once it expired")
	require.Equal(t, PermissionGuest, inv.Permission)
	require.Equal(t, a.bob, inv.InvitedByIID)
	m, err := f.svc.Accept(ctx, zed, inv.ID)
	require.NoError(t, err)
	require.Equal(t, PermissionGuest, m.Permission)
}

// OMEM-07: the retention job deletes the invitations that have expired, exactly when they expire and
// not a moment before (L-016), and audits each.
func TestThePurgeDeletesExpiredInvitations(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	f.user("one")
	f.user("two")
	_, err := f.invite(a.alice, a.org, "one", PermissionMember, "")
	require.NoError(t, err)
	f.clock.Advance(time.Hour)
	_, err = f.invite(a.alice, a.org, "two", PermissionMember, "")
	require.NoError(t, err)
	next := f.events()

	f.clock.Advance(14*24*time.Hour - time.Hour - time.Millisecond) // one ms before the first expires
	n, err := f.svc.PurgeExpiredInvitations(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
	require.Equal(t, 2, f.pending())
	require.Empty(t, next())

	f.clock.Advance(time.Millisecond) // the first expires now
	n, err = f.svc.PurgeExpiredInvitations(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	require.Equal(t, 1, f.pending())
	require.Equal(t, 1, f.count(`SELECT COUNT(*) FROM org_invitations WHERE invitee_iid = ?`, "u-two"))
	require.Equal(t, []events.Event{events.OrgChanged{OrgID: a.org}}, next())
	require.Contains(t, f.audited(), "org.invite_expire "+a.org)

	f.clock.Advance(time.Hour)
	n, err = f.svc.PurgeExpiredInvitations(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	require.Zero(t, f.pending())
	n, err = f.svc.PurgeExpiredInvitations(ctx)
	require.NoError(t, err)
	require.Zero(t, n, "nothing left")
}

// A tool for the rate-limit tests: n new users named <prefix>NN and their handles.
func (f *fixture) users(prefix string, n int) []string {
	f.t.Helper()
	out := make([]string, n)
	for i := range n {
		out[i] = f.user(fmt.Sprintf("%s%02d", prefix, i))
	}
	return out
}

// Rate limit (spec sweep): at most 30 invitations per hour per organization, guest invitations
// included, counted from the audit log so the limit survives a restart. The window is one hour: inside
// it the 31st is refused, from its end on invitations go through again (L-016).
func TestAtMost30InvitationsPerHourPerOrganization(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	others := f.users("inv", 40)
	globex := f.org("globex")
	boss := f.adminOf(globex)

	for i := range 30 {
		who, perm := a.alice, PermissionMember
		if i%2 == 1 {
			who, perm = a.bob, PermissionGuest // guest invitations count too
		}
		_, err := f.invite(who, a.org, handleOf(others[i]), perm, "")
		require.NoError(t, err, "invitation %d", i+1)
		f.clock.Advance(time.Second)
	}
	_, err := f.invite(a.alice, a.org, handleOf(others[30]), PermissionMember, "")
	require.ErrorIs(t, err, ErrRateLimited, "the 31st")
	_, err = f.invite(a.bob, a.org, handleOf(others[30]), PermissionGuest, "")
	require.ErrorIs(t, err, ErrRateLimited, "for a guest invitation too")
	require.Equal(t, 30, f.pending())

	// Inviting again counts, and the limit is per organization.
	_, err = f.invite(a.alice, a.org, handleOf(others[0]), PermissionMember, "")
	require.ErrorIs(t, err, ErrRateLimited, "a re-invitation is an invitation")
	_, err = f.invite(boss, globex, handleOf(others[30]), PermissionMember, "")
	require.NoError(t, err, "another organization has its own count")

	// It still holds after a restart: a new service over the same database has no memory of its own.
	restarted := NewService(Deps{Store: f.st, Bus: f.bus, Audit: f.audit, Blobs: f.blobs, Now: f.clock.Now})
	_, err = restarted.Invite(ctx, a.alice, a.org, handleOf(others[31]), PermissionMember, "")
	require.ErrorIs(t, err, ErrRateLimited)

	// The first invitation was made at t0; the window ends an hour after it.
	f.clock.now = t0.Add(time.Hour - time.Millisecond)
	_, err = f.invite(a.alice, a.org, handleOf(others[31]), PermissionMember, "")
	require.ErrorIs(t, err, ErrRateLimited, "1 ms before the first invitation leaves the window")
	f.clock.now = t0.Add(time.Hour)
	_, err = f.invite(a.alice, a.org, handleOf(others[31]), PermissionMember, "")
	require.NoError(t, err, "the first one is out of the window")
	_, err = f.invite(a.alice, a.org, handleOf(others[32]), PermissionMember, "")
	require.ErrorIs(t, err, ErrRateLimited, "the window holds 30 again: 29 from the first minute and the one just made")
}

// L-002: the count and the invitation are one transaction, so a burst of parallel invitations cannot
// pass the limit.
func TestAParallelBurstOfInvitationsStopsAtTheLimit(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.bareOrg()
	others := f.users("burst", 45)
	start := make(chan struct{})
	errs := make([]error, len(others))
	var wg sync.WaitGroup
	for i, iid := range others {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = f.svc.Invite(ctx, a.alice, a.org, handleOf(iid), PermissionMember, "")
		}()
	}
	close(start)
	wg.Wait()
	ok, limited := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrRateLimited):
			limited++
		default:
			require.NoError(t, err)
		}
	}
	require.Equal(t, 30, ok)
	require.Equal(t, 15, limited)
	require.Equal(t, 30, f.pending())
}

// OMEM-01: an invitation to an AI is accepted by whoever manages the AI in its home organization, and by
// nobody else - not even an admin of the inviting organization.
func TestAnAIInvitationIsAcceptedByItsManagers(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	globex := f.org("globex")
	xi := f.user("xi")
	boss := f.position(globex, "Boss", "")
	crew := f.position(globex, "Crew", boss)
	f.join(globex, xi, PermissionMember, boss, "")
	bot := f.ai("visitor", xi)
	f.home(bot, globex, xi, PermissionMember, crew)
	zoe := f.user("zoe") // an admin of globex who is not above the bot
	f.join(globex, zoe, PermissionAdmin, "", "")
	peer := f.user("peer")
	f.join(globex, peer, PermissionMember, crew, "")

	inv, err := f.invite(a.alice, a.org, "visitor", PermissionMember, a.designer)
	require.NoError(t, err)
	for _, who := range []string{a.alice, zoe, peer, a.bob, f.user("out")} {
		_, err := f.svc.Accept(ctx, who, inv.ID)
		require.ErrorIs(t, err, ErrNotFound, "%s does not manage the AI", who)
		require.ErrorIs(t, f.svc.Decline(ctx, who, inv.ID), ErrNotFound)
	}
	require.ErrorIs(t, f.z.RequireMember(ctx, a.org, bot), ErrNotFound)

	mine, err := f.svc.MyInvitations(ctx, xi)
	require.NoError(t, err)
	require.Len(t, mine, 1, "the manager sees the AI's invitation")
	require.Equal(t, bot, mine[0].InviteeIID)
	require.Equal(t, "ai", mine[0].InviteeKind)
	for _, who := range []string{peer, zoe, a.alice} {
		none, err := f.svc.MyInvitations(ctx, who)
		require.NoError(t, err)
		require.Empty(t, none, "%s sees nothing", who)
	}

	m, err := f.svc.Accept(ctx, xi, inv.ID)
	require.NoError(t, err)
	require.Equal(t, a.org, m.ID)
	require.Equal(t, PermissionMember, m.Permission, "the view is the AI's membership")
	require.Equal(t, Member{IID: bot, Permission: PermissionMember, PositionID: a.designer, CreatorIID: xi}, memberOfOrg(t, f, a.org, bot))
	require.Equal(t, globex, f.str(`SELECT org_id FROM agents WHERE id = ?`, bot), "its home stays")
	rows, _, err := f.audit.List(ctx, "")
	require.NoError(t, err)
	require.Equal(t, xi, rows[0].ActorID, "audited as the manager who accepted")
}

// A user answers their own invitation, and a stranger cannot even tell it exists.
func TestOnlyTheInviteeAnswers(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	zed := f.user("zed")
	inv, err := f.invite(a.alice, a.org, "zed", PermissionMember, "")
	require.NoError(t, err)
	for _, who := range []string{a.alice, a.bob, f.user("stranger")} {
		_, err := f.svc.Accept(ctx, who, inv.ID)
		require.ErrorIs(t, err, ErrNotFound, who)
		require.ErrorIs(t, f.svc.Decline(ctx, who, inv.ID), ErrNotFound, who)
	}
	_, err = f.svc.Accept(ctx, zed, "i-nothing")
	require.ErrorIs(t, err, ErrNotFound)
	require.Equal(t, 1, f.pending())

	require.NoError(t, f.svc.Decline(ctx, zed, inv.ID))
	require.Zero(t, f.pending())
	_, err = f.svc.Accept(ctx, zed, inv.ID)
	require.ErrorIs(t, err, ErrNotFound, "an answered invitation is gone")
	require.ErrorIs(t, f.z.RequireMember(ctx, a.org, zed), ErrNotFound)
	require.Contains(t, f.audited(), "org.invite_decline "+a.org)
}

// OMEM-07, OMEM-01: the invitee was added another way meanwhile: the invitation is stale, it goes away
// and accepting says so.
func TestAcceptingAfterJoiningAnotherWayIsAlreadyAMember(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	zed := f.user("zed")
	inv, err := f.invite(a.alice, a.org, "zed", PermissionGuest, "")
	require.NoError(t, err)
	f.join(a.org, zed, PermissionMember, "", "")

	_, err = f.svc.Accept(ctx, zed, inv.ID)
	require.ErrorIs(t, err, ErrAlreadyMember)
	require.Zero(t, f.pending(), "the stale invitation is deleted")
	require.Equal(t, PermissionMember, memberOfOrg(t, f, a.org, zed).Permission, "and the membership is untouched")
}

// OMEM-01, POS-09: what happened to the inviter or the position while the invitation waited. A deleted
// position moved the invitation to its parent; an inviter who no longer exists leaves the guest with none.
func TestAcceptAfterThePositionOrTheInviterChanged(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	zed, yan := f.user("zed"), f.user("yan")
	inv, err := f.invite(a.alice, a.org, "zed", PermissionMember, a.lead)
	require.NoError(t, err)
	require.NoError(t, f.svc.DeletePosition(ctx, a.alice, a.org, a.lead))
	m, err := f.svc.Accept(ctx, zed, inv.ID)
	require.NoError(t, err)
	require.Equal(t, a.manager, m.PositionID, "the deleted position's parent")

	ghost := f.user("ghost")
	inv, err = f.invite(ghost, a.org, "yan", PermissionGuest, "")
	require.ErrorIs(t, err, ErrNotFound, "ghost is not a member yet")
	f.join(a.org, ghost, PermissionMember, "", "")
	inv, err = f.invite(ghost, a.org, "yan", PermissionGuest, "")
	require.NoError(t, err)
	f.exec(`DELETE FROM users WHERE id = ?`, ghost) // the inviter's account is gone
	m, err = f.svc.Accept(ctx, yan, inv.ID)
	require.NoError(t, err)
	require.Equal(t, PermissionGuest, m.Permission)
	require.Equal(t, Member{IID: yan, Permission: PermissionGuest}, memberOfOrg(t, f, a.org, yan), "no inviter left to record")
}

// OMEM-01, OMEM-04: an invitation for more than a guest needs its inviter to still be an admin when it is
// accepted; otherwise it is gone (and deleted).
func TestAdminInvitationsLapseWithTheInvitersAdminRight(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	zed := f.user("zed")
	boss := f.user("boss")
	f.join(a.org, boss, PermissionAdmin, "", "")
	inv, err := f.invite(boss, a.org, "zed", PermissionMember, "")
	require.NoError(t, err)
	_, err = f.svc.SetPermission(ctx, a.alice, a.org, boss, PermissionMember)
	require.NoError(t, err)
	_, err = f.svc.Accept(ctx, zed, inv.ID)
	require.ErrorIs(t, err, ErrGone, "the inviter is no longer an admin")
	require.Zero(t, f.pending(), "the lapsed invitation is deleted")
	_, ok, err := memberOf(ctx, f.st.R(), a.org, zed)
	require.NoError(t, err)
	require.False(t, ok)

}

// OMEM-04: only an admin lists and revokes the organization's pending invitations.
func TestListAndRevokeAreForAdmins(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	f.user("zed")
	other := f.org("globex")
	boss := f.adminOf(other)
	inv, err := f.invite(a.bob, a.org, "zed", PermissionGuest, "")
	require.NoError(t, err)

	for who, want := range map[string]error{f.user("out"): ErrNotFound, a.bob: ErrForbidden, a.gus: ErrForbidden, a.lee: ErrForbidden, a.notes: ErrForbidden} {
		_, err := f.svc.ListInvitations(ctx, who, a.org)
		require.ErrorIs(t, err, want, "list by %s", who)
		require.ErrorIs(t, f.svc.Revoke(ctx, who, a.org, inv.ID), want, "revoke by %s", who)
	}
	require.ErrorIs(t, f.svc.Revoke(ctx, boss, other, inv.ID), ErrNotFound, "an invitation of another organization")
	require.ErrorIs(t, f.svc.Revoke(ctx, a.alice, a.org, "i-nothing"), ErrNotFound)
	require.Equal(t, 1, f.pending())

	got, err := f.svc.ListInvitations(ctx, a.alice, a.org)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, inv.ID, got[0].ID)
	require.Equal(t, "bob", got[0].InvitedByHandle)
	require.Equal(t, PermissionGuest, got[0].Permission)

	next := f.events()
	require.NoError(t, f.svc.Revoke(ctx, a.alice, a.org, inv.ID))
	require.Zero(t, f.pending())
	require.Equal(t, []events.Event{events.OrgChanged{OrgID: a.org}}, next())
	require.Contains(t, f.audited(), "org.invite_revoke "+a.org)
	got, err = f.svc.ListInvitations(ctx, a.alice, a.org)
	require.NoError(t, err)
	require.Empty(t, got)
}

// MyInvitations lists a user's own pending invitations with the organization, newest first.
func TestMyInvitations(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	globex := f.org("globex")
	boss := f.adminOf(globex)
	zed := f.user("zed")
	first, err := f.invite(a.alice, a.org, "zed", PermissionMember, a.designer)
	require.NoError(t, err)
	f.clock.Advance(time.Minute)
	second, err := f.invite(boss, globex, "zed", PermissionGuest, "")
	require.NoError(t, err)
	f.user("someone")
	_, err = f.invite(a.alice, a.org, "someone", PermissionMember, "")
	require.NoError(t, err)

	mine, err := f.svc.MyInvitations(ctx, zed)
	require.NoError(t, err)
	require.Len(t, mine, 2)
	require.Equal(t, second.ID, mine[0].ID, "newest first")
	require.Equal(t, first.ID, mine[1].ID)
	require.Equal(t, "globex", mine[0].OrgName)
	require.Equal(t, "acme", mine[1].OrgSlug)
	require.Equal(t, "Designer", mine[1].PositionName)
	none, err := f.svc.MyInvitations(ctx, f.user("lonely"))
	require.NoError(t, err)
	require.Empty(t, none)
	none, err = f.svc.MyInvitations(ctx, "")
	require.NoError(t, err)
	require.Empty(t, none)
}

// OMEM-01: InvitationFor gives the invitation to the identity that answers it - the invited user, or for an
// invited AI whoever manages it - and ErrNotFound to everybody else, to an unknown invitation and, each
// of these alone, to one that has expired.
func TestInvitationForIsForWhoAnswersIt(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	globex := f.org("globex")
	xi := f.user("xi")
	boss := f.position(globex, "Boss", "")
	crew := f.position(globex, "Crew", boss)
	f.join(globex, xi, PermissionMember, boss, "")
	bot := f.ai("visitor", xi)
	f.home(bot, globex, xi, PermissionMember, crew)
	peer := f.user("peer")
	f.join(globex, peer, PermissionMember, crew, "")
	zed := f.user("zed")

	forUser, err := f.invite(a.alice, a.org, "zed", PermissionMember, "")
	require.NoError(t, err)
	forAI, err := f.invite(a.alice, a.org, "visitor", PermissionGuest, "")
	require.NoError(t, err)

	got, err := f.svc.InvitationFor(ctx, zed, forUser.ID)
	require.NoError(t, err)
	require.Equal(t, forUser, got, "the invited user")
	got, err = f.svc.InvitationFor(ctx, xi, forAI.ID)
	require.NoError(t, err)
	require.Equal(t, forAI, got, "whoever manages the invited AI")
	require.Equal(t, "ai", got.InviteeKind)

	for who, id := range map[string]string{
		a.alice: forUser.ID, // the inviter
		a.bob:   forUser.ID, // another member
		xi:      forUser.ID, // answers for an AI, not for another user
		peer:    forAI.ID,   // does not manage the AI
		zed:     forAI.ID,   // the other invitee
		"":      forUser.ID, // nobody
	} {
		_, err := f.svc.InvitationFor(ctx, who, id)
		require.ErrorIs(t, err, ErrNotFound, "%q / %s", who, id)
	}
	_, err = f.svc.InvitationFor(ctx, zed, "i-nothing")
	require.ErrorIs(t, err, ErrNotFound)

	f.clock.Advance(InvitationTTL - time.Millisecond)
	_, err = f.svc.InvitationFor(ctx, zed, forUser.ID)
	require.NoError(t, err, "14 days minus a millisecond")
	f.clock.Advance(time.Millisecond)
	_, err = f.svc.InvitationFor(ctx, zed, forUser.ID)
	require.ErrorIs(t, err, ErrNotFound, "an expired invitation is nobody's")
}

// The view names the invitee and the inviter with their kind and avatar, so a client can draw them.
func TestAnInvitationNamesItsInviteeAndInviterWithKindAndAvatar(t *testing.T) {
	f := newFixture(t)
	a := f.acme()
	zed := f.user("zed")
	f.exec(`UPDATE intelligences SET avatar_sha256 = 'sha-zed' WHERE id = ?`, zed)
	f.exec(`UPDATE intelligences SET avatar_sha256 = 'sha-notes' WHERE id = ?`, a.notes)

	byUser, err := f.invite(a.alice, a.org, "zed", PermissionMember, "")
	require.NoError(t, err)
	require.Equal(t, "user", byUser.InviteeKind)
	require.Equal(t, "sha-zed", byUser.InviteeAvatarSha256)
	require.Equal(t, "user", byUser.InvitedByKind)
	require.Empty(t, byUser.InvitedByAvatarSha256)

	other := f.user("amy")
	byAI, err := f.invite(a.notes, a.org, "amy", PermissionGuest, "") // an AI that is a member invites a guest
	require.NoError(t, err)
	require.Equal(t, other, byAI.InviteeIID)
	require.Equal(t, "ai", byAI.InvitedByKind)
	require.Equal(t, "sha-notes", byAI.InvitedByAvatarSha256)
	require.Equal(t, "user", byAI.InviteeKind)
}
