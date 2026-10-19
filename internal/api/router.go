package api

import (
	"log/slog"
	"net/http"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/config"
	"github.com/wnexous/partyline/internal/users"
)

// Deps are the services the API serves.
type Deps struct {
	Config *config.Config
	Users  *users.Service
	Logger *slog.Logger
	// Ready reports whether the app finished starting (migrations applied).
	Ready func() bool
}

// server holds the handler dependencies.
type server struct {
	Deps
}

// New returns the full HTTP handler: routes wrapped in the middleware chain.
func New(d Deps) http.Handler {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	s := &server{Deps: d}
	mux := http.NewServeMux()
	s.routes(mux)
	return Middleware(MiddlewareConfig{
		PublicURL:      d.Config.PublicURL,
		TrustedProxies: d.Config.TrustedProxies,
		MaxUploadBytes: d.Config.MaxUploadBytes,
		Auth:           d.Users,
		Logger:         d.Logger,
	}, mux)
}

func (s *server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
}

func (s *server) healthz(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) readyz(w http.ResponseWriter, _ *http.Request) {
	if s.Ready == nil || !s.Ready() {
		httpx.WriteError(w, httpx.NewError(http.StatusServiceUnavailable, dto.ErrorCodeUnavailable, "starting"))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
