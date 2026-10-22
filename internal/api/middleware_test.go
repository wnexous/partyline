package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/users"
)

const publicOrigin = "https://partyline.example.test"

type echo struct {
	Kind      string `json:"kind"`
	UserID    string `json:"user_id"`
	AgentName string `json:"agent_name"`
	IP        string `json:"ip"`
	RequestID string `json:"request_id"`
}

type mwFixture struct {
	srv      *httptest.Server
	session  string // admin session cookie value
	user     string // admin user token
	agent    string // admin agent token
	adminID  string
	usersSvc *users.Service
}

func newMWFixture(t *testing.T, trusted []netip.Prefix, maxUpload int64) *mwFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "partyline.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(ctx))
	svc, err := users.NewService(st, users.Options{})
	require.NoError(t, err)
	admin, err := svc.Setup(ctx, svc.SetupToken(), users.AccountInput{Username: "admin", Password: "admin-password-1"})
	require.NoError(t, err)
	_, sess, err := svc.Login(ctx, users.LoginInput{Username: "admin", Password: "admin-password-1", IP: "192.0.2.1"})
	require.NoError(t, err)
	_, userTok, err := svc.CreateToken(ctx, admin.ID, users.TokenInput{Kind: dto.TokenKindUser, Name: "user"})
	require.NoError(t, err)
	_, agentTok, err := svc.CreateToken(ctx, admin.ID, users.TokenInput{Kind: dto.TokenKindAgent, Name: "agent"})
	require.NoError(t, err)

	echoHandler := func(w http.ResponseWriter, r *http.Request) {
		e := echo{Kind: "anonymous", IP: httpx.ClientIP(r.Context()).String(), RequestID: httpx.RequestID(r.Context())}
		if p, ok := authn.PrincipalFrom(r.Context()); ok {
			e.Kind, e.UserID, e.AgentName = string(p.Kind), p.UserID, p.AgentName
		}
		httpx.WriteJSON(w, http.StatusOK, e)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/rooms", echoHandler)
	mux.HandleFunc("/api/v1/agent/rooms", echoHandler)
	mux.HandleFunc("/panic", func(http.ResponseWriter, *http.Request) { panic("boom") })
	mux.HandleFunc("POST /upload", func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			var mbe *http.MaxBytesError
			require.True(t, errors.As(err, &mbe))
			httpx.WriteError(w, httpx.NewError(http.StatusRequestEntityTooLarge, dto.ErrorCodeTooLarge, "too large"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	pub, err := url.Parse(publicOrigin)
	require.NoError(t, err)
	h := Middleware(MiddlewareConfig{
		PublicURL:      pub,
		TrustedProxies: trusted,
		MaxUploadBytes: maxUpload,
		Auth:           svc,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, mux)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &mwFixture{srv: srv, session: sess.Secret, user: userTok, agent: agentTok, adminID: admin.ID, usersSvc: svc}
}

type reqOpt func(*http.Request)

func withCookie(v string) reqOpt {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: SessionCookie, Value: v}) }
}

func withBearer(v string) reqOpt {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+v) }
}

func withHeader(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }

func (f *mwFixture) do(t *testing.T, method, path string, body io.Reader, opts ...reqOpt) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, f.srv.URL+path, body)
	require.NoError(t, err)
	for _, o := range opts {
		o(req)
	}
	resp, err := f.srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func readEcho(t *testing.T, resp *http.Response) echo {
	t.Helper()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var e echo
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&e))
	return e
}

func requireErrorCode(t *testing.T, resp *http.Response, status int, code dto.ErrorCode) {
	t.Helper()
	require.Equal(t, status, resp.StatusCode)
	var body dto.ErrorBody
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, code, body.Error.Code)
}

// SEC-02: cookie-authenticated writes need the instance's own Origin or Sec-Fetch-Site.
func TestCSRFCookieWrites(t *testing.T) {
	f := newMWFixture(t, nil, 1<<20)

	requireErrorCode(t, f.do(t, "POST", "/api/v1/rooms", nil, withCookie(f.session), withHeader("Origin", "https://evil.example.test")),
		http.StatusForbidden, dto.ErrorCodeForbidden)
	requireErrorCode(t, f.do(t, "POST", "/api/v1/rooms", nil, withCookie(f.session), withHeader("Sec-Fetch-Site", "cross-site")),
		http.StatusForbidden, dto.ErrorCodeForbidden)
	requireErrorCode(t, f.do(t, "POST", "/api/v1/rooms", nil, withCookie(f.session), withHeader("Sec-Fetch-Site", "same-site")),
		http.StatusForbidden, dto.ErrorCodeForbidden)
	requireErrorCode(t, f.do(t, "DELETE", "/api/v1/rooms", nil, withCookie(f.session)),
		http.StatusForbidden, dto.ErrorCodeForbidden)
	requireErrorCode(t, f.do(t, "POST", "/api/v1/rooms", nil, withCookie(f.session),
		withHeader("Sec-Fetch-Site", "same-origin"), withHeader("Origin", "https://evil.example.test")),
		http.StatusForbidden, dto.ErrorCodeForbidden)

	e := readEcho(t, f.do(t, "POST", "/api/v1/rooms", nil, withCookie(f.session), withHeader("Origin", publicOrigin)))
	require.Equal(t, "user", e.Kind)
	require.Equal(t, f.adminID, e.UserID)
	e = readEcho(t, f.do(t, "PATCH", "/api/v1/rooms", nil, withCookie(f.session), withHeader("Sec-Fetch-Site", "same-origin")))
	require.Equal(t, "user", e.Kind)

	// Safe methods are not state-changing.
	e = readEcho(t, f.do(t, "GET", "/api/v1/rooms", nil, withCookie(f.session), withHeader("Origin", "https://evil.example.test")))
	require.Equal(t, "user", e.Kind)
}

// Browser writes without a session (login, setup) are still refused from a foreign origin.
func TestCSRFAnonymousForeignOrigin(t *testing.T) {
	f := newMWFixture(t, nil, 1<<20)
	requireErrorCode(t, f.do(t, "POST", "/api/v1/rooms", nil, withHeader("Origin", "https://evil.example.test")),
		http.StatusForbidden, dto.ErrorCodeForbidden)
	e := readEcho(t, f.do(t, "POST", "/api/v1/rooms", nil))
	require.Equal(t, "anonymous", e.Kind)
}

// SEC-02: bearer requests skip the origin check.
func TestBearerWritesSkipCSRF(t *testing.T) {
	f := newMWFixture(t, nil, 1<<20)
	e := readEcho(t, f.do(t, "POST", "/api/v1/rooms", nil, withBearer(f.user)))
	require.Equal(t, "user", e.Kind)
	require.Equal(t, f.adminID, e.UserID)
	e = readEcho(t, f.do(t, "POST", "/api/v1/rooms", nil, withBearer(f.user), withHeader("Origin", "https://other.example.test")))
	require.Equal(t, "user", e.Kind)
}

// TOK-03: agent tokens reach only /api/v1/agent/ endpoints.
func TestAgentTokenRestrictedToAgentRoutes(t *testing.T) {
	f := newMWFixture(t, nil, 1<<20)
	requireErrorCode(t, f.do(t, "GET", "/api/v1/rooms", nil, withBearer(f.agent)), http.StatusForbidden, dto.ErrorCodeForbidden)
	requireErrorCode(t, f.do(t, "POST", "/api/v1/rooms", nil, withBearer(f.agent)), http.StatusForbidden, dto.ErrorCodeForbidden)

	e := readEcho(t, f.do(t, "GET", "/api/v1/agent/rooms", nil, withBearer(f.agent), withHeader(dto.HeaderAgent, "builder")))
	require.Equal(t, "agent", e.Kind)
	require.Equal(t, f.adminID, e.UserID)
	require.Equal(t, "builder", e.AgentName)
	e = readEcho(t, f.do(t, "POST", "/api/v1/agent/rooms", nil, withBearer(f.agent)))
	require.Equal(t, "agent", e.Kind)
}

// TOK-02: invalid bearer credentials are 401; an invalid cookie is just anonymous.
func TestInvalidCredentials(t *testing.T) {
	f := newMWFixture(t, nil, 1<<20)
	requireErrorCode(t, f.do(t, "GET", "/api/v1/rooms", nil, withBearer("pl_user_unknown")), http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	requireErrorCode(t, f.do(t, "GET", "/api/v1/rooms", nil, withHeader("Authorization", "Basic YWRtaW46eA==")), http.StatusUnauthorized, dto.ErrorCodeUnauthorized)

	e := readEcho(t, f.do(t, "GET", "/api/v1/rooms", nil, withCookie("stale-session")))
	require.Equal(t, "anonymous", e.Kind)
}

// Recover: a panic becomes a 500 envelope; request id and security headers are present.
func TestPanicBecomesInternalError(t *testing.T) {
	f := newMWFixture(t, nil, 1<<20)
	resp := f.do(t, "GET", "/panic", nil)
	require.NotEmpty(t, resp.Header.Get("X-Request-Id"))
	require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	requireErrorCode(t, resp, http.StatusInternalServerError, dto.ErrorCodeInternal)
}

// SEC-01 headers are present on responses.
func TestSecurityHeaders(t *testing.T) {
	f := newMWFixture(t, nil, 1<<20)
	resp := f.do(t, "GET", "/api/v1/rooms", nil)
	require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	require.Equal(t, "same-origin", resp.Header.Get("Referrer-Policy"))
	require.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))
	require.Contains(t, resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'")
	e := readEcho(t, resp)
	require.Equal(t, resp.Header.Get("X-Request-Id"), e.RequestID)
	require.NotEmpty(t, e.RequestID)
}

// SEC-03: bodies over 1 MiB (JSON) or the upload limit (multipart) are 413.
func TestBodyLimit(t *testing.T) {
	const upload = 4 << 10
	f := newMWFixture(t, nil, upload)

	big := bytes.Repeat([]byte("a"), httpx.MaxJSONBody+1)
	requireErrorCode(t, f.do(t, "POST", "/upload", bytes.NewReader(big), withBearer(f.user), withHeader("Content-Type", "application/json")),
		http.StatusRequestEntityTooLarge, dto.ErrorCodeTooLarge)

	// Chunked bodies (unknown length) are cut by the reader limit.
	requireErrorCode(t, f.do(t, "POST", "/upload", io.MultiReader(bytes.NewReader(big)), withBearer(f.user)),
		http.StatusRequestEntityTooLarge, dto.ErrorCodeTooLarge)

	mp := "multipart/form-data; boundary=x"
	ok := bytes.Repeat([]byte("a"), httpx.MaxJSONBody+upload/2)
	resp := f.do(t, "POST", "/upload", bytes.NewReader(ok), withBearer(f.user), withHeader("Content-Type", mp))
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	tooBig := bytes.Repeat([]byte("a"), httpx.MaxJSONBody+upload+1)
	requireErrorCode(t, f.do(t, "POST", "/upload", bytes.NewReader(tooBig), withBearer(f.user), withHeader("Content-Type", mp)),
		http.StatusRequestEntityTooLarge, dto.ErrorCodeTooLarge)
}

// Client IP: X-Forwarded-For is honored only when the peer is a trusted proxy.
func TestClientIP(t *testing.T) {
	untrusted := newMWFixture(t, nil, 1<<20)
	e := readEcho(t, untrusted.do(t, "GET", "/api/v1/rooms", nil, withHeader("X-Forwarded-For", "203.0.113.9")))
	require.Equal(t, "127.0.0.1", e.IP)

	trusted := newMWFixture(t, []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8")}, 1<<20)
	e = readEcho(t, trusted.do(t, "GET", "/api/v1/rooms", nil, withHeader("X-Forwarded-For", "198.51.100.1, 203.0.113.9, 10.0.0.2")))
	require.Equal(t, "203.0.113.9", e.IP)
	e = readEcho(t, trusted.do(t, "GET", "/api/v1/rooms", nil))
	require.Equal(t, "127.0.0.1", e.IP)
}

func TestSessionCookieIsUserPrincipal(t *testing.T) {
	f := newMWFixture(t, nil, 1<<20)
	e := readEcho(t, f.do(t, "GET", "/api/v1/rooms", nil, withCookie(f.session)))
	require.Equal(t, "user", e.Kind)
	require.Equal(t, f.adminID, e.UserID)
}

// PROF-06: an avatar upload is a raw image that may be larger than a JSON body: up to the
// instance upload limit, and never below 2 MiB + 1 byte (so a 2 MiB file is accepted and a
// larger one reaches the handler, which refuses it as an invalid avatar). Only PUT on the two
// avatar routes gets this; every other route keeps the 1 MiB JSON limit.
func TestAvatarUploadBodyLimit(t *testing.T) {
	const upload = 3 << 20
	f := newMWFixture(t, nil, upload)
	send := func(method, path string, size int) *http.Response {
		t.Helper()
		return f.do(t, method, path, bytes.NewReader(bytes.Repeat([]byte("a"), size)), withBearer(f.user))
	}
	// The routes do not exist in this fixture: a body within the limit reaches the router (404).
	for _, path := range []string{"/api/v1/intelligences/abc/avatar", "/api/v1/agent/profile/avatar"} {
		require.Equal(t, http.StatusNotFound, send(http.MethodPut, path, 2<<20+1).StatusCode, path+": over 2 MiB still reaches the handler")
		require.Equal(t, http.StatusNotFound, send(http.MethodPut, path, upload).StatusCode, path+": up to the upload limit")
		requireErrorCode(t, send(http.MethodPut, path, upload+1), http.StatusRequestEntityTooLarge, dto.ErrorCodeTooLarge)
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/intelligences/abc/avatar"}, // another method
		{http.MethodPut, "/api/v1/intelligences/abc"},         // the profile, not its avatar
		{http.MethodPut, "/api/v1/rooms/abc/avatar"},          // another resource
		{http.MethodPut, "/api/v1/agent/profile"},             // an agent route that is no upload
	} {
		requireErrorCode(t, send(c.method, c.path, httpx.MaxJSONBody+1), http.StatusRequestEntityTooLarge, dto.ErrorCodeTooLarge)
	}

	small := newMWFixture(t, nil, 1<<20) // an upload limit under 2 MiB does not shrink avatars
	big := func(size int) *http.Response {
		return small.do(t, http.MethodPut, "/api/v1/intelligences/abc/avatar", bytes.NewReader(bytes.Repeat([]byte("a"), size)), withBearer(small.user))
	}
	require.Equal(t, http.StatusNotFound, big(2<<20+1).StatusCode)
	requireErrorCode(t, big(2<<20+2), http.StatusRequestEntityTooLarge, dto.ErrorCodeTooLarge)
}
