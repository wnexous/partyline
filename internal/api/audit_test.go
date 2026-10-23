package api_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

func auditPage(c *testkit.Client, before string) dto.Page[dto.AuditEntry] {
	c.T.Helper()
	path := "/api/v1/admin/audit"
	if before != "" {
		path += "?before=" + before
	}
	return testkit.Decode[dto.Page[dto.AuditEntry]](c.Get(path).RequireStatus(http.StatusOK))
}

// findAudit returns the newest entry with action, failing when there is none.
func findAudit(t *testing.T, entries []dto.AuditEntry, action string) dto.AuditEntry {
	t.Helper()
	for _, e := range entries {
		if e.Action == action {
			return e
		}
	}
	t.Fatalf("no %q audit entry in %v", action, entries)
	return dto.AuditEntry{}
}

// AUD-01: login, logout, invite, token, grant and user-state changes append entries with
// actor, action, target and time, and no secrets.
func TestAuditRecordsSecurityEvents(t *testing.T) {
	env := testkit.New(t)
	anon := env.Anon()
	anon.Post("/api/v1/setup", dto.SetupRequest{Token: env.App.SetupToken(), Username: "admin", Password: testkit.Password}).RequireStatus(http.StatusCreated)
	admin := env.LoginAs("admin", testkit.Password)

	env.Anon().Post("/api/v1/auth/login", dto.LoginRequest{Username: "admin", Password: "wrong-password-1"}).RequireStatus(http.StatusUnauthorized)
	invite := testkit.Decode[dto.CreatedInvite](admin.Post("/api/v1/admin/invites", dto.CreateInviteRequest{Role: dto.RoleMember}).RequireStatus(http.StatusCreated))
	inviteToken := invite.URL[strings.LastIndex(invite.URL, "/")+1:]
	env.Anon().Post("/api/v1/invites/"+inviteToken+"/accept", dto.AcceptInviteRequest{Username: "bob", Password: testkit.Password}).RequireStatus(http.StatusCreated)
	revoked := testkit.Decode[dto.CreatedInvite](admin.Post("/api/v1/admin/invites", dto.CreateInviteRequest{Role: dto.RoleAdmin}).RequireStatus(http.StatusCreated))
	admin.Delete("/api/v1/admin/invites/" + revoked.Invite.ID).RequireStatus(http.StatusNoContent)

	bob := env.LoginAs("bob", testkit.Password)
	tok := testkit.Decode[dto.CreatedToken](bob.Post("/api/v1/tokens", dto.CreateTokenRequest{Kind: dto.TokenKindAgent, Name: "laptop"}).RequireStatus(http.StatusCreated))
	bob.Delete("/api/v1/tokens/" + tok.Token.ID).RequireStatus(http.StatusNoContent)
	room := createRoom(bob, "ops")
	shareRoom(bob, room.ID, dto.SharingUser{UserID: admin.User.ID, Level: dto.LevelView})
	newPassword := "brand-new-password-9"
	bob.Post("/api/v1/me/password", dto.ChangePasswordRequest{CurrentPassword: testkit.Password, NewPassword: newPassword}).RequireStatus(http.StatusNoContent)
	bob.Post("/api/v1/auth/logout", nil).RequireStatus(http.StatusNoContent)
	disabled := true
	admin.Patch("/api/v1/admin/users/"+bob.User.ID, dto.UpdateUserRequest{Disabled: &disabled}).RequireStatus(http.StatusOK)

	resp := admin.Get("/api/v1/admin/audit").RequireStatus(http.StatusOK)
	page := testkit.Decode[dto.Page[dto.AuditEntry]](resp)
	entries := page.Items

	setup := findAudit(t, entries, "auth.setup")
	require.Equal(t, admin.User.ID, setup.Actor.ID)
	require.Equal(t, "admin", setup.Actor.Username)
	require.Equal(t, "user", setup.TargetKind)
	require.Equal(t, admin.User.ID, setup.TargetID)

	login := findAudit(t, entries, "auth.login")
	require.Equal(t, bob.User.ID, login.Actor.ID)
	require.Equal(t, "session", login.TargetKind)
	require.NotEmpty(t, login.IP)

	failed := findAudit(t, entries, "auth.login_failed")
	require.Nil(t, failed.Actor)
	require.NotEmpty(t, failed.IP)

	created := findAudit(t, entries, "invite.create")
	require.Equal(t, admin.User.ID, created.Actor.ID)
	require.Equal(t, "invite", created.TargetKind)
	require.Equal(t, revoked.Invite.ID, created.TargetID, "newest invite.create is the revoked one")
	require.Equal(t, "admin", created.Detail["role"])
	accepted := findAudit(t, entries, "invite.accept")
	require.Equal(t, bob.User.ID, accepted.Actor.ID)
	require.Equal(t, invite.Invite.ID, accepted.TargetID)
	rev := findAudit(t, entries, "invite.revoke")
	require.Equal(t, admin.User.ID, rev.Actor.ID)
	require.Equal(t, revoked.Invite.ID, rev.TargetID)

	tc := findAudit(t, entries, "token.create")
	require.Equal(t, bob.User.ID, tc.Actor.ID)
	require.Equal(t, "token", tc.TargetKind)
	require.Equal(t, tok.Token.ID, tc.TargetID)
	require.Equal(t, "laptop", tc.Detail["name"])
	require.Equal(t, tok.Token.Prefix, tc.Detail["prefix"])
	tr := findAudit(t, entries, "token.revoke")
	require.Equal(t, tok.Token.ID, tr.TargetID)

	grants := findAudit(t, entries, "grants.set")
	require.Equal(t, bob.User.ID, grants.Actor.ID)
	require.Equal(t, "room", grants.TargetKind)
	require.Equal(t, room.ID, grants.TargetID)
	require.Equal(t, "shared", grants.Detail["mode"])

	pw := findAudit(t, entries, "user.password_change")
	require.Equal(t, bob.User.ID, pw.Actor.ID)
	require.Equal(t, bob.User.ID, pw.TargetID)

	logout := findAudit(t, entries, "auth.logout")
	require.Equal(t, bob.User.ID, logout.Actor.ID)
	require.Equal(t, "session", logout.TargetKind)

	upd := findAudit(t, entries, "user.update")
	require.Equal(t, admin.User.ID, upd.Actor.ID)
	require.Equal(t, bob.User.ID, upd.TargetID)
	require.Equal(t, true, upd.Detail["disabled"])

	for _, e := range entries {
		require.False(t, e.CreatedAt.IsZero(), e.Action)
	}

	// No secrets: passwords, token secrets, invite tokens and session cookies never appear.
	body := string(resp.Body)
	for _, secret := range []string{testkit.Password, "wrong-password-1", newPassword, tok.Secret, inviteToken, admin.Session, bob.Session} {
		require.NotContains(t, body, secret)
	}
}

// AUD-02: entries come newest first, 50 per page, continued with ?before=; admins only.
func TestAuditPagination(t *testing.T) {
	env := testkit.New(t)
	admin := env.SetupAdmin() // org.create (the admin's personal organization) + auth.setup + auth.login
	for i := range 58 {
		admin.Post("/api/v1/tokens", dto.CreateTokenRequest{Kind: dto.TokenKindUser, Name: fmt.Sprintf("t%02d", i)}).RequireStatus(http.StatusCreated)
	}

	first := auditPage(admin, "")
	require.Len(t, first.Items, 50)
	require.True(t, first.HasMore)
	require.Equal(t, "t57", first.Items[0].Detail["name"], "newest first")
	require.Equal(t, "t08", first.Items[49].Detail["name"])
	for i := 1; i < len(first.Items); i++ {
		require.False(t, first.Items[i].CreatedAt.After(first.Items[i-1].CreatedAt))
	}

	second := auditPage(admin, first.Items[49].ID)
	require.Len(t, second.Items, 11)
	require.False(t, second.HasMore)
	require.Equal(t, "t07", second.Items[0].Detail["name"])
	require.Equal(t, "auth.login", second.Items[8].Action)
	require.Equal(t, "auth.setup", second.Items[9].Action)
	require.Equal(t, "org.create", second.Items[10].Action, "the oldest entry: the first account's personal organization")

	member := env.NewUser("bob")
	member.Get("/api/v1/admin/audit").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	env.Anon().Get("/api/v1/admin/audit").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
}
