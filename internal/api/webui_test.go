package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
	"github.com/wnexous/partyline/internal/webui"
)

// SEC-01: client routes return the web app's index.html with the strict CSP and the other
// security headers; unknown API paths stay JSON 404s.
func TestWebUIServedWithStrictHeaders(t *testing.T) {
	env := testkit.New(t)
	anon := env.Anon()

	resp := anon.Get("/rooms/x").RequireStatus(http.StatusOK)
	require.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
	require.True(t, strings.HasPrefix(strings.ToLower(resp.String()), "<!doctype html>"))
	require.Equal(t, webui.CSP(env.Origin), resp.Header.Get("Content-Security-Policy"))
	require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	require.Equal(t, "same-origin", resp.Header.Get("Referrer-Policy"))
	require.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))

	anon.Get("/api/v1/nope").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	anon.Post("/api/v1/nope", nil).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	env.SetupAdmin().Get("/api/v1/nope").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}
