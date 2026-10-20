package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/app"
	"github.com/wnexous/partyline/internal/config"
)

// Log messages other tools (and tests) look for.
const (
	msgListening  = "listening"
	msgSetupToken = "FIRST-RUN SETUP: open the setup page and enter this setup token" // #nosec G101 -- a log message, not a credential
	msgStopped    = "stopped"
)

// shutdownTimeout bounds the graceful shutdown.
const shutdownTimeout = 10 * time.Second

// serve runs the server until ctx is done, then shuts down gracefully.
func serve(ctx context.Context, io stdio) int {
	cfg, err := config.Load(io.getenv)
	if err != nil {
		fmt.Fprintln(io.stderr, "config:", err)
		return 1
	}
	log := newLogger(cfg, io.stderr)
	a, err := app.New(cfg, app.Options{Logger: log, Version: buildVersion()})
	if err != nil {
		log.Error("start failed", "err", err)
		return 1
	}
	if err := a.Start(ctx); err != nil {
		log.Error("start failed", "err", err)
		_ = a.Shutdown(context.Background())
		return 1
	}
	if needs, err := a.Users.NeedsSetup(ctx); err == nil && needs {
		// The token is only useful until the first account exists (AUTH-01).
		log.Warn(msgSetupToken, "setup_url", strings.TrimRight(cfg.PublicURL.String(), "/")+"/setup", "setup_token", a.SetupToken())
	}

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		log.Error("listen failed", "addr", cfg.Addr, "err", err)
		_ = a.Shutdown(context.Background())
		return 1
	}
	srv := &http.Server{
		Handler:           a.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	log.Info(msgListening, "addr", ln.Addr().String(), "public_url", cfg.PublicURL.String(), "version", buildVersion())

	code := 0
	select {
	case <-ctx.Done():
	case err := <-errc:
		log.Error("server failed", "err", err)
		code = 1
	}
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("http shutdown", "err", err)
		code = 1
	}
	if err := a.Shutdown(sctx); err != nil {
		log.Error("app shutdown", "err", err)
		code = 1
	}
	log.Info(msgStopped)
	return code
}
