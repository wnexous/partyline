package api_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

func createInvite(t *testing.T, env *testkit.Env, admin *testkit.Client, role dto.Role) (dto.CreatedInvite, string) {
	t.Helper()
	created := testkit.Decode[dto.CreatedInvite](admin.Post("/api/v1/admin/invites", dto.CreateInviteRequest{Role: role}).
		RequireStatus(http.StatusCreated))
	prefix := env.URL + "/invite/"
	require.True(t, strings.HasPrefix(created.URL, prefix), created.URL)
	token := strings.TrimPrefix(created.URL, prefix)
	require.NotEmpty(t, token)
	return created, token
}

// Admin routes: members get 403, anonymous callers 401.
func TestAdminRoutesRequireAdmin(t *testing.T) {
	env := testkit.New(t)
	env.SetupAdmin()
	bob := env.NewUser("bob")
	anon := env.Anon()

	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/invites"},
		{http.MethodPost, "/api/v1/admin/invites"},
		{http.MethodDelete, "/api/v1/admin/invites/some-id"},
		{http.MethodGet, "/api/v1/admin/users"},
		{http.MethodPatch, "/api/v1/admin/users/" + bob.User.ID},
	}
	for _, rt := range routes {
		bob.Do(rt.method, rt.path, map[string]any{}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		anon.Do(rt.method, rt.path, map[string]any{}).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	}
}

// AUTH-06, AUTH-07: an invite link creates one account (201) and is gone afterwards (410).
func TestInviteAcceptOnce(t *testing.T) {
	env := testkit.New(t)
	admin := env.SetupAdmin()
	created, token := createInvite(t, env, admin, dto.RoleMember)
	require.Equal(t, dto.InviteStatusPending, created.Invite.Status)
	require.Equal(t, dto.RoleMember, created.Invite.Role)
	require.Equal(t, "admin", created.Invite.CreatedBy.Username)
	require.WithinDuration(t, created.Invite.CreatedAt.Add(7*24*time.Hour), created.Invite.ExpiresAt, time.Millisecond)

	anon := env.Anon()
	info := testkit.Decode[dto.InviteInfo](anon.Get("/api/v1/invites/" + token).RequireStatus(http.StatusOK))
	require.Equal(t, dto.RoleMember, info.Role)
	require.True(t, info.ExpiresAt.Equal(created.Invite.ExpiresAt))

	resp := anon.Post("/api/v1/invites/"+token+"/accept", dto.AcceptInviteRequest{Username: "bob", Password: testkit.Password, DisplayName: "Bob"}).
		RequireStatus(http.StatusCreated)
	me := testkit.Decode[dto.Me](resp)
	require.Equal(t, "bob", me.Username)
	require.Equal(t, dto.RoleMember, me.Role)
	requireSessionCookie(t, resp, false)
	require.Equal(t, me.ID, testkit.Decode[dto.Me](anon.Get("/api/v1/me").RequireStatus(http.StatusOK)).ID)

	env.Anon().Post("/api/v1/invites/"+token+"/accept", dto.AcceptInviteRequest{Username: "carol", Password: testkit.Password}).
		RequireError(http.StatusGone, dto.ErrorCodeGone)
	env.Anon().Get("/api/v1/invites/"+token).RequireError(http.StatusGone, dto.ErrorCodeGone)

	list := testkit.Decode[dto.List[dto.Invite]](admin.Get("/api/v1/admin/invites").RequireStatus(http.StatusOK))
	require.Len(t, list.Items, 1)
	require.Equal(t, dto.InviteStatusUsed, list.Items[0].Status)
	require.NotNil(t, list.Items[0].UsedBy)
	require.Equal(t, "bob", list.Items[0].UsedBy.Username)
	require.NotNil(t, list.Items[0].UsedAt)
}

// AUTH-07: invalid username or short password are 422 naming the field; the invite stays usable.
func TestInviteAcceptValidation(t *testing.T) {
	env := testkit.New(t)
	admin := env.SetupAdmin()
	_, token := createInvite(t, env, admin, dto.RoleMember)
	anon := env.Anon()

	e := anon.Post("/api/v1/invites/"+token+"/accept", dto.AcceptInviteRequest{Username: "x", Password: testkit.Password}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "username")
	e = anon.Post("/api/v1/invites/"+token+"/accept", dto.AcceptInviteRequest{Username: "bob", Password: "123456789"}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "password")
	anon.Post("/api/v1/invites/"+token+"/accept", dto.AcceptInviteRequest{Username: "admin", Password: testkit.Password}).
		RequireError(http.StatusConflict, dto.ErrorCodeConflict)

	anon.Get("/api/v1/invites/" + token).RequireStatus(http.StatusOK)
}

func TestInviteRevokeAndUnknown(t *testing.T) {
	env := testkit.New(t)
	admin := env.SetupAdmin()
	created, token := createInvite(t, env, admin, dto.RoleAdmin)

	admin.Delete("/api/v1/admin/invites/" + created.Invite.ID).RequireStatus(http.StatusNoContent)
	env.Anon().Get("/api/v1/invites/"+token).RequireError(http.StatusGone, dto.ErrorCodeGone)
	env.Anon().Post("/api/v1/invites/"+token+"/accept", dto.AcceptInviteRequest{Username: "bob", Password: testkit.Password}).
		RequireError(http.StatusGone, dto.ErrorCodeGone)

	admin.Delete("/api/v1/admin/invites/missing").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	env.Anon().Get("/api/v1/invites/unknown-token").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	admin.Post("/api/v1/admin/invites", dto.CreateInviteRequest{Role: "owner"}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
}

// AUTH-10: a disabled user's cookie and tokens are rejected with 401 on the next request.
func TestDisabledUserIsLoggedOut(t *testing.T) {
	env := testkit.New(t)
	admin := env.SetupAdmin()
	bob := env.NewUser("bob")
	bobToken := bob.Token(dto.TokenKindUser, "script")
	bob.Get("/api/v1/me").RequireStatus(http.StatusOK)
	bobToken.Get("/api/v1/me").RequireStatus(http.StatusOK)

	disabled := true
	got := testkit.Decode[dto.AdminUser](admin.Patch("/api/v1/admin/users/"+bob.User.ID, dto.UpdateUserRequest{Disabled: &disabled}).
		RequireStatus(http.StatusOK))
	require.True(t, got.Disabled)
	require.NotNil(t, got.DisabledAt)

	bob.Get("/api/v1/me").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	bobToken.Get("/api/v1/me").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	env.Anon().Post("/api/v1/auth/login", dto.LoginRequest{Username: "bob", Password: testkit.Password}).
		RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)

	users := testkit.Decode[dto.List[dto.AdminUser]](admin.Get("/api/v1/admin/users").RequireStatus(http.StatusOK))
	require.Len(t, users.Items, 2)
	require.Equal(t, "bob", users.Items[1].Username)
	require.True(t, users.Items[1].Disabled)

	enabled := false
	got = testkit.Decode[dto.AdminUser](admin.Patch("/api/v1/admin/users/"+bob.User.ID, dto.UpdateUserRequest{Disabled: &enabled}).
		RequireStatus(http.StatusOK))
	require.False(t, got.Disabled)
	bobToken.Get("/api/v1/me").RequireStatus(http.StatusOK)
}

func TestAdminChangesRole(t *testing.T) {
	env := testkit.New(t)
	admin := env.SetupAdmin()
	bob := env.NewUser("bob")
	bob.Get("/api/v1/admin/users").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)

	role := dto.RoleAdmin
	got := testkit.Decode[dto.AdminUser](admin.Patch("/api/v1/admin/users/"+bob.User.ID, dto.UpdateUserRequest{Role: &role}).
		RequireStatus(http.StatusOK))
	require.Equal(t, dto.RoleAdmin, got.Role)
	bob.Get("/api/v1/admin/users").RequireStatus(http.StatusOK)

	admin.Patch("/api/v1/admin/users/missing", dto.UpdateUserRequest{Role: &role}).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// AUTH-10 guard: an admin cannot demote or disable themselves (409 conflict); another admin
// can, and the account keeps working until then.
func TestAdminCannotDemoteOrDisableSelf(t *testing.T) {
	env := testkit.New(t)
	admin := env.SetupAdmin()
	self := "/api/v1/admin/users/" + admin.User.ID
	member, disabled := dto.RoleMember, true

	admin.Patch(self, dto.UpdateUserRequest{Role: &member}).RequireError(http.StatusConflict, dto.ErrorCodeConflict)
	admin.Patch(self, dto.UpdateUserRequest{Disabled: &disabled}).RequireError(http.StatusConflict, dto.ErrorCodeConflict)
	admin.Get("/api/v1/admin/audit").RequireStatus(http.StatusOK) // still an active admin

	// A second admin may demote the first.
	carol := env.NewAdmin("carol")
	got := testkit.Decode[dto.AdminUser](carol.Patch(self, dto.UpdateUserRequest{Role: &member}).RequireStatus(http.StatusOK))
	require.Equal(t, dto.RoleMember, got.Role)
}

func TestUserDirectory(t *testing.T) {
	env := testkit.New(t)
	admin := env.SetupAdmin()
	bob := env.NewUser("bob")
	carol := env.NewUser("carol")
	disabled := true
	admin.Patch("/api/v1/admin/users/"+carol.User.ID, dto.UpdateUserRequest{Disabled: &disabled}).RequireStatus(http.StatusOK)

	list := testkit.Decode[dto.List[dto.User]](bob.Get("/api/v1/users").RequireStatus(http.StatusOK))
	names := []string{}
	for _, u := range list.Items {
		names = append(names, u.Username)
	}
	require.Equal(t, []string{"admin", "bob"}, names)
	env.Anon().Get("/api/v1/users").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
}

// TOK-01, TOK-02, TOK-03: the secret is returned once, revoked tokens are 401 and agent
// tokens cannot manage tokens.
func TestTokenEndpoints(t *testing.T) {
	env := testkit.New(t)
	bob := env.NewUser("bob")

	resp := bob.Post("/api/v1/tokens", dto.CreateTokenRequest{Kind: dto.TokenKindAgent, Name: "laptop"}).RequireStatus(http.StatusCreated)
	created := testkit.Decode[dto.CreatedToken](resp)
	require.True(t, strings.HasPrefix(created.Secret, "pl_agent_"))
	require.Equal(t, created.Secret[:12], created.Token.Prefix)
	require.Equal(t, dto.TokenKindAgent, created.Token.Kind)
	require.Equal(t, "laptop", created.Token.Name)

	userTok := testkit.Decode[dto.CreatedToken](bob.Post("/api/v1/tokens", dto.CreateTokenRequest{Kind: dto.TokenKindUser, Name: "script"}).
		RequireStatus(http.StatusCreated))
	require.True(t, strings.HasPrefix(userTok.Secret, "pl_user_"))

	listResp := bob.Get("/api/v1/tokens").RequireStatus(http.StatusOK)
	require.NotContains(t, string(listResp.Body), created.Secret)
	require.NotContains(t, string(listResp.Body), userTok.Secret)
	list := testkit.Decode[dto.List[dto.Token]](listResp)
	require.Len(t, list.Items, 2)
	for _, tok := range list.Items {
		require.Len(t, tok.Prefix, 12)
	}

	// TOK-03: the agent token cannot reach management endpoints.
	agent := bob.WithBearer(created.Secret)
	agent.Get("/api/v1/tokens").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	agent.Get("/api/v1/me").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)

	// A user token acts as the user.
	script := bob.WithBearer(userTok.Secret)
	require.Equal(t, bob.User.ID, testkit.Decode[dto.Me](script.Get("/api/v1/me").RequireStatus(http.StatusOK)).ID)

	// TOK-02: revoked and unknown tokens are 401.
	bob.Delete("/api/v1/tokens/" + userTok.Token.ID).RequireStatus(http.StatusNoContent)
	script.Get("/api/v1/me").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	bob.WithBearer("pl_user_0000000000000000000000000000000000000000000").Get("/api/v1/me").
		RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	list = testkit.Decode[dto.List[dto.Token]](bob.Get("/api/v1/tokens?include_revoked=true").RequireStatus(http.StatusOK))
	found := false
	for _, tok := range list.Items {
		if tok.ID == userTok.Token.ID {
			found = true
			require.NotNil(t, tok.RevokedAt)
		}
	}
	require.True(t, found, "revoked tokens are listed with include_revoked=true (TOKH-01)")

	carol := env.NewUser("carol")
	carol.Delete("/api/v1/tokens/"+created.Token.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	e := bob.Post("/api/v1/tokens", dto.CreateTokenRequest{Kind: "root", Name: ""}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "kind")
	require.Contains(t, e.Fields, "name")
	env.Anon().Get("/api/v1/tokens").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
}
