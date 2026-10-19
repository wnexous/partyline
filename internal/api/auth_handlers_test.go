package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

const thirtyDays = 30 * 24 * 60 * 60

func requireSessionCookie(t *testing.T, r *testkit.Response, secure bool) *http.Cookie {
	t.Helper()
	c := r.Cookie(api.SessionCookie)
	require.NotNil(t, c, "no session cookie set")
	require.NotEmpty(t, c.Value)
	require.True(t, c.HttpOnly)
	require.Equal(t, http.SameSiteLaxMode, c.SameSite)
	require.Equal(t, thirtyDays, c.MaxAge)
	require.Equal(t, "/", c.Path)
	require.Equal(t, secure, c.Secure)
	return c
}

// AUTH-01, AUTH-02: setup with the setup token creates the admin once (201, then 409);
// a wrong token is 401.
func TestSetupEndpoint(t *testing.T) {
	env := testkit.New(t)
	anon := env.Anon()

	status := testkit.Decode[dto.SetupStatus](anon.Get("/api/v1/setup/status").RequireStatus(http.StatusOK))
	require.True(t, status.NeedsSetup)

	anon.Post("/api/v1/setup", dto.SetupRequest{Token: "wrong", Username: "admin", Password: testkit.Password}).
		RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	e := anon.Post("/api/v1/setup", dto.SetupRequest{Token: env.App.SetupToken(), Username: "A B", Password: "short"}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "username")
	require.Contains(t, e.Fields, "password")

	resp := anon.Post("/api/v1/setup", dto.SetupRequest{Token: env.App.SetupToken(), Username: "admin", Password: testkit.Password, DisplayName: "Admin"}).
		RequireStatus(http.StatusCreated)
	me := testkit.Decode[dto.Me](resp)
	require.Equal(t, "admin", me.Username)
	require.Equal(t, dto.RoleAdmin, me.Role)
	require.Equal(t, "Admin", me.DisplayName)
	requireSessionCookie(t, resp, false)

	// The setup response logged the browser in.
	require.Equal(t, me.ID, testkit.Decode[dto.Me](anon.Get("/api/v1/me").RequireStatus(http.StatusOK)).ID)

	status = testkit.Decode[dto.SetupStatus](env.Anon().Get("/api/v1/setup/status").RequireStatus(http.StatusOK))
	require.False(t, status.NeedsSetup)
	env.Anon().Post("/api/v1/setup", dto.SetupRequest{Token: env.App.SetupToken(), Username: "second", Password: testkit.Password}).
		RequireError(http.StatusConflict, dto.ErrorCodeConflict)
}

// AUTH-03: login sets an HttpOnly, SameSite=Lax cookie valid for 30 days.
func TestLoginSetsSessionCookie(t *testing.T) {
	env := testkit.New(t)
	env.SetupAdmin()
	anon := env.Anon()

	resp := anon.Post("/api/v1/auth/login", dto.LoginRequest{Username: "admin", Password: testkit.Password}).RequireStatus(http.StatusOK)
	me := testkit.Decode[dto.Me](resp)
	require.Equal(t, "admin", me.Username)
	requireSessionCookie(t, resp, false)

	got := testkit.Decode[dto.Me](anon.Get("/api/v1/me").RequireStatus(http.StatusOK))
	require.Equal(t, me.ID, got.ID)
	require.Equal(t, dto.RoleAdmin, got.Role)
}

// AUTH-03: the cookie is Secure when the public URL is https.
func TestLoginCookieSecureBehindHTTPS(t *testing.T) {
	env := testkit.New(t, testkit.WithEnv(map[string]string{"PARTYLINE_PUBLIC_URL": "https://partyline.example.test"}))
	env.SetupAdmin()
	resp := env.Anon().Post("/api/v1/auth/login", dto.LoginRequest{Username: "admin", Password: testkit.Password}).RequireStatus(http.StatusOK)
	requireSessionCookie(t, resp, true)
}

// AUTH-04: unknown user and wrong password get the same 401 body.
func TestLoginFailuresShareBody(t *testing.T) {
	env := testkit.New(t)
	env.SetupAdmin()

	unknown := env.Anon().Post("/api/v1/auth/login", dto.LoginRequest{Username: "nobody", Password: testkit.Password})
	wrong := env.Anon().Post("/api/v1/auth/login", dto.LoginRequest{Username: "admin", Password: "wrong-password-1"})
	unknown.RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	wrong.RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	require.Equal(t, unknown.Body, wrong.Body)
	require.Nil(t, unknown.Cookie(api.SessionCookie))
}

// AUTH-05: the 6th attempt after 5 failures in a minute from one IP is 429.
func TestLoginRateLimited(t *testing.T) {
	env := testkit.New(t)
	env.SetupAdmin()
	anon := env.Anon()

	for range 5 {
		anon.Post("/api/v1/auth/login", dto.LoginRequest{Username: "admin", Password: "wrong-password-1"}).
			RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	}
	anon.Post("/api/v1/auth/login", dto.LoginRequest{Username: "admin", Password: testkit.Password}).
		RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
}

func TestMeRequiresLogin(t *testing.T) {
	env := testkit.New(t)
	env.Anon().Get("/api/v1/me").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	env.Anon().Post("/api/v1/auth/logout", nil).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
}

// AUTH-09: after logout the old cookie is rejected with 401.
func TestLogoutRevokesCookie(t *testing.T) {
	env := testkit.New(t)
	admin := env.SetupAdmin()

	resp := admin.Post("/api/v1/auth/logout", nil).RequireStatus(http.StatusNoContent)
	c := resp.Cookie(api.SessionCookie)
	require.NotNil(t, c)
	require.Empty(t, c.Value)
	require.Negative(t, c.MaxAge)

	env.SessionClient(admin.Session).Get("/api/v1/me").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
}

func TestUpdateMe(t *testing.T) {
	env := testkit.New(t)
	bob := env.NewUser("bob")

	name, locale := "Bob B.", "pt-BR"
	me := testkit.Decode[dto.Me](bob.Patch("/api/v1/me", dto.UpdateMeRequest{DisplayName: &name, Locale: &locale}).RequireStatus(http.StatusOK))
	require.Equal(t, "Bob B.", me.DisplayName)
	require.Equal(t, "pt-BR", me.Locale)

	// Absent fields are unchanged.
	other := "Bobby"
	me = testkit.Decode[dto.Me](bob.Patch("/api/v1/me", dto.UpdateMeRequest{DisplayName: &other}).RequireStatus(http.StatusOK))
	require.Equal(t, "Bobby", me.DisplayName)
	require.Equal(t, "pt-BR", me.Locale)

	bad := "not a locale!"
	e := bob.Patch("/api/v1/me", dto.UpdateMeRequest{Locale: &bad}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "locale")

	got := testkit.Decode[dto.Me](bob.Get("/api/v1/me").RequireStatus(http.StatusOK))
	require.Equal(t, "Bobby", got.DisplayName)
	require.Equal(t, "pt-BR", got.Locale)
}

func TestChangePassword(t *testing.T) {
	env := testkit.New(t)
	bob := env.NewUser("bob")
	const next = "brand-new-password"

	e := bob.Post("/api/v1/me/password", dto.ChangePasswordRequest{CurrentPassword: "wrong-password-1", NewPassword: next}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "current_password")
	e = bob.Post("/api/v1/me/password", dto.ChangePasswordRequest{CurrentPassword: testkit.Password, NewPassword: "short"}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "new_password")

	bob.Post("/api/v1/me/password", dto.ChangePasswordRequest{CurrentPassword: testkit.Password, NewPassword: next}).
		RequireStatus(http.StatusNoContent)

	env.Anon().Post("/api/v1/auth/login", dto.LoginRequest{Username: "bob", Password: testkit.Password}).
		RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	env.Anon().Post("/api/v1/auth/login", dto.LoginRequest{Username: "bob", Password: next}).RequireStatus(http.StatusOK)
}

// AUTH-09: a user lists its sessions and revokes one; that cookie is then rejected.
func TestSessionsListAndRevoke(t *testing.T) {
	env := testkit.New(t)
	env.NewUser("bob")
	laptop := env.LoginAs("bob", testkit.Password)
	phone := env.LoginAs("bob", testkit.Password)
	carol := env.NewUser("carol")

	list := testkit.Decode[dto.List[dto.Session]](laptop.Get("/api/v1/me/sessions").RequireStatus(http.StatusOK))
	require.Len(t, list.Items, 3)
	current := 0
	var phoneID string
	for _, s := range list.Items {
		if s.Current {
			current++
		}
		require.Equal(t, "testkit", s.UserAgent)
		require.WithinDuration(t, s.LastSeenAt.Add(30*24*time.Hour), s.ExpiresAt, time.Second)
	}
	require.Equal(t, 1, current)

	// Find the phone session: it is the one the phone client reports as current.
	for _, s := range testkit.Decode[dto.List[dto.Session]](phone.Get("/api/v1/me/sessions").RequireStatus(http.StatusOK)).Items {
		if s.Current {
			phoneID = s.ID
		}
	}
	require.NotEmpty(t, phoneID)

	carol.Delete("/api/v1/me/sessions/"+phoneID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	phone.Get("/api/v1/me").RequireStatus(http.StatusOK)

	laptop.Delete("/api/v1/me/sessions/" + phoneID).RequireStatus(http.StatusNoContent)
	phone.Get("/api/v1/me").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	laptop.Get("/api/v1/me").RequireStatus(http.StatusOK)
	laptop.Delete("/api/v1/me/sessions/"+phoneID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}
