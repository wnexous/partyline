package users

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
)

const memberPassword = "member-password-1"

func (f *fixture) adminID(t *testing.T) string {
	t.Helper()
	u, err := f.store.R().GetUserByUsername(context.Background(), "admin")
	require.NoError(t, err)
	return u.ID
}

func (f *fixture) invite(t *testing.T, role dto.Role) (string, string) {
	t.Helper()
	inv, secret, err := f.svc.CreateInvite(context.Background(), f.adminID(t), role)
	require.NoError(t, err)
	return inv.ID, secret
}

func (f *fixture) inviteStatus(t *testing.T, id string) dto.InviteStatus {
	t.Helper()
	list, err := f.svc.ListInvites(context.Background())
	require.NoError(t, err)
	for _, inv := range list {
		if inv.Invite.ID == id {
			return inv.Status
		}
	}
	t.Fatalf("invite %s not listed", id)
	return ""
}

// AUTH-06: an invite is single-use and expires after 7 days.
func TestInviteExpiresAfterSevenDays(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)
	id, secret := f.invite(t, dto.RoleMember)

	info, err := f.svc.GetInvite(ctx, secret)
	require.NoError(t, err)
	require.Equal(t, "member", info.Role)
	require.Equal(t, f.clock.Now().Add(7*24*time.Hour).UnixMilli(), info.ExpiresAt)
	require.Equal(t, dto.InviteStatusPending, f.inviteStatus(t, id))

	f.clock.Advance(7*24*time.Hour - time.Millisecond)
	_, err = f.svc.GetInvite(ctx, secret)
	require.NoError(t, err)

	f.clock.Advance(time.Millisecond)
	_, err = f.svc.GetInvite(ctx, secret)
	require.ErrorIs(t, err, ErrInviteGone)
	_, err = f.svc.AcceptInvite(ctx, secret, AccountInput{Username: "bob", Password: memberPassword})
	require.ErrorIs(t, err, ErrInviteGone)
	require.Equal(t, dto.InviteStatusExpired, f.inviteStatus(t, id))
	require.EqualValues(t, 1, f.userCount(t))
}

// AUTH-07: accepting creates the account with the invite's role and marks the invite used;
// the invite cannot be used again (AUTH-06 single use).
func TestAcceptInviteCreatesAccountOnce(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)
	id, secret := f.invite(t, dto.RoleMember)

	u, err := f.svc.AcceptInvite(ctx, secret, AccountInput{Username: "bob", Password: memberPassword, DisplayName: "Bob"})
	require.NoError(t, err)
	require.Equal(t, "bob", u.Username)
	require.Equal(t, "member", u.Role)
	require.Equal(t, "Bob", f.displayName(t, u.ID))

	_, _, err = f.svc.Login(ctx, LoginInput{Username: "bob", Password: memberPassword, IP: "192.0.2.1"})
	require.NoError(t, err)

	list, err := f.svc.ListInvites(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, dto.InviteStatusUsed, list[0].Status)
	require.Equal(t, u.ID, list[0].Invite.UsedBy.String)
	require.Equal(t, "bob", list[0].UsedByUsername)
	require.Equal(t, "admin", list[0].CreatorUsername)
	require.Equal(t, f.clock.Now().UnixMilli(), list[0].Invite.UsedAt.Int64)
	require.Equal(t, id, list[0].Invite.ID)

	_, err = f.svc.AcceptInvite(ctx, secret, AccountInput{Username: "carol", Password: memberPassword})
	require.ErrorIs(t, err, ErrInviteGone)
	_, err = f.svc.GetInvite(ctx, secret)
	require.ErrorIs(t, err, ErrInviteGone)
	require.EqualValues(t, 2, f.userCount(t))
}

func TestAcceptInviteUsesInviteRole(t *testing.T) {
	f := newFixture(t)
	f.setupAdmin(t)
	_, secret := f.invite(t, dto.RoleAdmin)

	u, err := f.svc.AcceptInvite(context.Background(), secret, AccountInput{Username: "root2", Password: memberPassword})
	require.NoError(t, err)
	require.Equal(t, "admin", u.Role)
}

func TestRevokedInviteIsGone(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)
	id, secret := f.invite(t, dto.RoleMember)

	require.NoError(t, f.svc.RevokeInvite(ctx, id))

	_, err := f.svc.GetInvite(ctx, secret)
	require.ErrorIs(t, err, ErrInviteGone)
	_, err = f.svc.AcceptInvite(ctx, secret, AccountInput{Username: "bob", Password: memberPassword})
	require.ErrorIs(t, err, ErrInviteGone)
	require.Equal(t, dto.InviteStatusRevoked, f.inviteStatus(t, id))

	require.ErrorIs(t, f.svc.RevokeInvite(ctx, "missing"), ErrNotFound)
}

func TestUnknownInviteIsNotFound(t *testing.T) {
	f := newFixture(t)
	f.setupAdmin(t)
	_, err := f.svc.GetInvite(context.Background(), "unknown-token")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = f.svc.AcceptInvite(context.Background(), "unknown-token", AccountInput{Username: "bob", Password: memberPassword})
	require.ErrorIs(t, err, ErrNotFound)
}

// AUTH-07: username outside the pattern or password under 10 characters names the field;
// the invite stays usable.
func TestAcceptInviteValidatesInput(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)
	id, secret := f.invite(t, dto.RoleMember)

	cases := []struct {
		name  string
		in    AccountInput
		field string
	}{
		{"short username", AccountInput{Username: "ab", Password: memberPassword}, "username"},
		{"long username", AccountInput{Username: "a23456789012345678901234567890123", Password: memberPassword}, "username"},
		{"uppercase username", AccountInput{Username: "Bob", Password: memberPassword}, "username"},
		{"bad character", AccountInput{Username: "bob.smith", Password: memberPassword}, "username"},
		{"short password", AccountInput{Username: "bob", Password: "123456789"}, "password"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.svc.AcceptInvite(ctx, secret, tc.in)
			var verr *ValidationError
			require.ErrorAs(t, err, &verr)
			require.Contains(t, verr.Fields, tc.field)
			require.Len(t, verr.Fields, 1)
		})
	}

	// Boundaries are accepted: 3-char username, 10-character password.
	_, err := f.svc.AcceptInvite(ctx, secret, AccountInput{Username: "b_-", Password: "1234567890"})
	require.NoError(t, err)
	require.Equal(t, dto.InviteStatusUsed, f.inviteStatus(t, id))
}

func TestAcceptInviteRejectsTakenUsername(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)
	id, secret := f.invite(t, dto.RoleMember)

	_, err := f.svc.AcceptInvite(ctx, secret, AccountInput{Username: "admin", Password: memberPassword})
	require.ErrorIs(t, err, ErrUsernameTaken)
	require.Equal(t, dto.InviteStatusPending, f.inviteStatus(t, id))
}

func TestCreateInviteRejectsUnknownRole(t *testing.T) {
	f := newFixture(t)
	f.setupAdmin(t)
	_, _, err := f.svc.CreateInvite(context.Background(), f.adminID(t), dto.Role("owner"))
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	require.Contains(t, verr.Fields, "role")
}

// AUTH-10: disabling a user revokes its sessions and blocks login; enabling restores login
// but not the revoked sessions.
func TestDisableUserRejectsSessions(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)
	_, secret := f.invite(t, dto.RoleMember)
	bob, err := f.svc.AcceptInvite(ctx, secret, AccountInput{Username: "bob", Password: memberPassword})
	require.NoError(t, err)
	_, sess, err := f.svc.Login(ctx, LoginInput{Username: "bob", Password: memberPassword, IP: "192.0.2.1"})
	require.NoError(t, err)

	disabled := true
	u, err := f.svc.UpdateUser(ctx, bob.ID, UserUpdate{Disabled: &disabled})
	require.NoError(t, err)
	require.True(t, u.DisabledAt.Valid)
	require.Equal(t, f.clock.Now().UnixMilli(), u.DisabledAt.Int64)

	_, err = f.svc.ValidateSession(ctx, sess.Secret)
	require.ErrorIs(t, err, ErrUnauthenticated)
	_, _, err = f.svc.Login(ctx, LoginInput{Username: "bob", Password: memberPassword, IP: "192.0.2.2"})
	require.ErrorIs(t, err, ErrInvalidCredentials)

	enabled := false
	u, err = f.svc.UpdateUser(ctx, bob.ID, UserUpdate{Disabled: &enabled})
	require.NoError(t, err)
	require.False(t, u.DisabledAt.Valid)
	_, err = f.svc.ValidateSession(ctx, sess.Secret)
	require.ErrorIs(t, err, ErrUnauthenticated)
	_, newSess, err := f.svc.Login(ctx, LoginInput{Username: "bob", Password: memberPassword, IP: "192.0.2.3"})
	require.NoError(t, err)
	p, err := f.svc.ValidateSession(ctx, newSess.Secret)
	require.NoError(t, err)
	require.Equal(t, authn.KindUser, p.Kind)
}

func TestUpdateUserRole(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)
	_, secret := f.invite(t, dto.RoleMember)
	bob, err := f.svc.AcceptInvite(ctx, secret, AccountInput{Username: "bob", Password: memberPassword})
	require.NoError(t, err)

	admin := dto.RoleAdmin
	u, err := f.svc.UpdateUser(ctx, bob.ID, UserUpdate{Role: &admin})
	require.NoError(t, err)
	require.Equal(t, "admin", u.Role)

	bad := dto.Role("root")
	_, err = f.svc.UpdateUser(ctx, bob.ID, UserUpdate{Role: &bad})
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	require.Contains(t, verr.Fields, "role")

	_, err = f.svc.UpdateUser(ctx, "missing", UserUpdate{Role: &admin})
	require.ErrorIs(t, err, ErrNotFound)

	list, err := f.svc.ListUsers(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, "admin", list[0].Username)
	require.Equal(t, "bob", list[1].Username)
	require.Equal(t, "admin", list[1].Role)
}

// AUTH-10 guard: the last active admin cannot be demoted or disabled (ErrLastAdmin), even
// when two admins demote each other at the same time; a member is never blocked by it.
func TestLastActiveAdminIsKept(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)
	admin := f.adminID(t)
	member, disabled := dto.RoleMember, true

	_, err := f.svc.UpdateUser(ctx, admin, UserUpdate{Role: &member})
	require.ErrorIs(t, err, ErrLastAdmin)
	_, err = f.svc.UpdateUser(ctx, admin, UserUpdate{Disabled: &disabled})
	require.ErrorIs(t, err, ErrLastAdmin)

	bob, err := f.svc.CreateUser(ctx, AccountInput{Username: "bob", Password: memberPassword}, dto.RoleMember)
	require.NoError(t, err)
	_, err = f.svc.UpdateUser(ctx, bob.ID, UserUpdate{Disabled: &disabled})
	require.NoError(t, err, "disabling a member is not guarded")

	// A disabled admin does not count as active.
	carol, err := f.svc.CreateUser(ctx, AccountInput{Username: "carol", Password: memberPassword}, dto.RoleAdmin)
	require.NoError(t, err)
	_, err = f.svc.UpdateUser(ctx, carol.ID, UserUpdate{Disabled: &disabled})
	require.NoError(t, err)
	_, err = f.svc.UpdateUser(ctx, admin, UserUpdate{Role: &member})
	require.ErrorIs(t, err, ErrLastAdmin)

	// Two active admins demoting each other concurrently: exactly one succeeds.
	dave, err := f.svc.CreateUser(ctx, AccountInput{Username: "dave", Password: memberPassword}, dto.RoleAdmin)
	require.NoError(t, err)
	errs := make(chan error, 2)
	for _, id := range []string{admin, dave.ID} {
		go func() {
			_, err := f.svc.UpdateUser(ctx, id, UserUpdate{Role: &member})
			errs <- err
		}()
	}
	e1, e2 := <-errs, <-errs
	require.True(t, (e1 == nil) != (e2 == nil), "exactly one demotion succeeds: %v, %v", e1, e2)
	require.ErrorIs(t, errors.Join(e1, e2), ErrLastAdmin)
	n, err := f.store.R().CountActiveAdmins(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
}

// AUTH-10 guard: an admin acting through the API cannot demote or disable themselves.
func TestAdminCannotChangeSelf(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)
	admin := f.adminID(t)
	_, err := f.svc.CreateUser(ctx, AccountInput{Username: "carol", Password: memberPassword}, dto.RoleAdmin)
	require.NoError(t, err)
	member, disabled := dto.RoleMember, true
	actx := authn.WithPrincipal(ctx, authn.Principal{Kind: authn.KindUser, UserID: admin, Role: string(dto.RoleAdmin)})

	_, err = f.svc.UpdateUser(actx, admin, UserUpdate{Role: &member})
	require.ErrorIs(t, err, ErrSelfAdminChange)
	_, err = f.svc.UpdateUser(actx, admin, UserUpdate{Disabled: &disabled})
	require.ErrorIs(t, err, ErrSelfAdminChange)
	admin2 := dto.RoleAdmin
	_, err = f.svc.UpdateUser(actx, admin, UserUpdate{Role: &admin2})
	require.NoError(t, err, "keeping one's own admin role is not a change")
}
