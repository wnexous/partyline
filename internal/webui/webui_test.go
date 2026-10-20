package webui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

var testDist = fstest.MapFS{
	"index.html":              {Data: []byte("<!doctype html><title>partyline</title>")},
	"theme-init.js":           {Data: []byte("/* theme */")},
	"favicon.svg":             {Data: []byte("<svg/>")},
	"assets/index-Abc123.js":  {Data: []byte("console.log(1)")},
	"assets/index-Abc123.css": {Data: []byte("body{}")},
}

func serve(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func newHandler(t *testing.T) http.Handler {
	t.Helper()
	u, err := url.Parse("https://partyline.example.com")
	require.NoError(t, err)
	return New(testDist, u)
}

// SEC-01: HTML responses carry the strict CSP; client routes fall back to index.html.
func TestSPAFallbackServesIndexWithCSP(t *testing.T) {
	h := newHandler(t)
	for _, path := range []string{"/", "/rooms/x", "/settings/tokens", "/index.html"} {
		rec := serve(t, h, http.MethodGet, path)
		require.Equal(t, http.StatusOK, rec.Code, path)
		require.Equal(t, "<!doctype html><title>partyline</title>", rec.Body.String(), path)
		require.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"), path)
		require.Equal(t, CSP("https://partyline.example.com"), rec.Header().Get("Content-Security-Policy"), path)
		require.Equal(t, "no-cache", rec.Header().Get("Cache-Control"), path)
	}
	csp := CSP("https://partyline.example.com")
	require.Contains(t, csp, "default-src 'self'")
	require.Contains(t, csp, "script-src 'self'")
	require.NotContains(t, csp, "'unsafe-eval'")
	require.Contains(t, csp, "connect-src 'self' wss://partyline.example.com")
	require.Contains(t, csp, "img-src 'self' data: blob:")
	require.Contains(t, csp, "style-src 'self' 'unsafe-inline'")
	require.Contains(t, csp, "frame-ancestors 'none'")
	scriptSrc := strings.SplitN(strings.SplitN(csp, "script-src", 2)[1], ";", 2)[0]
	require.Equal(t, " 'self'", scriptSrc, "scripts only from the instance: no inline scripts")
	require.Contains(t, CSP("http://localhost:8080"), "connect-src 'self' ws://localhost:8080")
}

// Static files are served as files; hashed assets are cached forever, others revalidated.
func TestStaticFilesAndCaching(t *testing.T) {
	h := newHandler(t)

	rec := serve(t, h, http.MethodGet, "/assets/index-Abc123.js")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "console.log(1)", rec.Body.String())
	require.Contains(t, rec.Header().Get("Content-Type"), "javascript")
	require.Equal(t, "public, max-age=31536000, immutable", rec.Header().Get("Cache-Control"))

	rec = serve(t, h, http.MethodGet, "/theme-init.js")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "/* theme */", rec.Body.String())
	require.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))

	// A missing asset is a real 404, not the SPA page (a stale tab must not get HTML as JS).
	rec = serve(t, h, http.MethodGet, "/assets/missing-1.js")
	require.Equal(t, http.StatusNotFound, rec.Code)

	rec = serve(t, h, http.MethodHead, "/rooms/x")
	require.Equal(t, http.StatusOK, rec.Code)
	body, _ := io.ReadAll(rec.Body)
	require.Empty(t, body)
}

// Without a built web app the binary serves the committed placeholder page.
func TestFallbackWhenNotBuilt(t *testing.T) {
	u, _ := url.Parse("http://localhost:8080")
	h := New(fstest.MapFS{".gitkeep": {}}, u)
	rec := serve(t, h, http.MethodGet, "/rooms/x")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "web app is not built")
	require.NotContains(t, rec.Body.String(), "<script")
	require.Equal(t, CSP("http://localhost:8080"), rec.Header().Get("Content-Security-Policy"))
}
