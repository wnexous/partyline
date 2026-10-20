// Package webui serves the embedded web app: static files, the single-page-app fallback for
// client routes and a strict Content-Security-Policy on HTML (SEC-01).
//
// The build copies web/dist into internal/webui/dist (make build, Dockerfile). That copy is
// not committed; without it the binary embeds only dist/.gitkeep and serves the placeholder
// page from fallback/index.html.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

//go:embed fallback/index.html
var fallbackFS embed.FS

// immutableCache is sent for content-hashed build assets.
const immutableCache = "public, max-age=31536000, immutable"

// CSP returns the policy for HTML pages served from origin (scheme://host). Scripts load
// only from the instance (no inline scripts); the live WebSocket is allowed explicitly
// because older browsers do not match ws: against 'self'.
func CSP(origin string) string {
	ws := "ws" + strings.TrimPrefix(origin, "http")
	return "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self' " + ws + "; " +
		"object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"
}

// Handler serves the web app embedded in the binary.
func Handler(public *url.URL) http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err) // the embed pattern guarantees the directory
	}
	return New(sub, public)
}

// New serves the web app in fsys (the contents of web/dist). When fsys has no index.html
// the placeholder page is served instead.
func New(fsys fs.FS, public *url.URL) http.Handler {
	index, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		fsys, _ = fs.Sub(fallbackFS, "fallback")
		index, _ = fs.ReadFile(fsys, "index.html")
	}
	return &handler{fsys: fsys, index: index, csp: CSP(public.Scheme + "://" + public.Host)}
}

type handler struct {
	fsys  fs.FS
	index []byte
	csp   string
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name != "" && name != "index.html" && fs.ValidPath(name) {
		if info, err := fs.Stat(h.fsys, name); err == nil && !info.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", immutableCache)
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			http.ServeFileFS(w, r, h.fsys, name) // #nosec G703 -- cleaned, fs.ValidPath, read from an fs.FS rooted at the build
			return
		}
		// A missing build file is a 404, so a stale page never receives HTML as a script.
		if strings.HasPrefix(name, "assets/") || strings.Contains(path.Base(name), ".") {
			http.NotFound(w, r)
			return
		}
	}
	hdr := w.Header()
	hdr.Set("Content-Security-Policy", h.csp)
	hdr.Set("Content-Type", "text/html; charset=utf-8")
	hdr.Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(h.index)
	}
}
