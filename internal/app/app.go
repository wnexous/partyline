// Package app wires the store, services, event bus and HTTP API into one application.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api"
	"github.com/wnexous/partyline/internal/attachments"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/config"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/live"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/secrets"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/users"
	"github.com/wnexous/partyline/internal/webui"
)

// DatabaseFile is the SQLite file name inside the data directory.
const DatabaseFile = "partyline.db"

// BlobsDir is the attachment blob directory inside the data directory.
const BlobsDir = "blobs"

// Options tune an App; zero values use the defaults.
type Options struct {
	// Logger receives the application logs; defaults to slog.Default().
	Logger *slog.Logger
	// Now returns the current time; defaults to time.Now. Tests inject a fake clock.
	Now func() time.Time
	// Sender delivers outbox entries to external chats. When nil the outbox worker does not
	// run and entries stay pending.
	Sender delivery.Sender
}

// App is one running Partyline instance.
type App struct {
	Config *config.Config
	Logger *slog.Logger
	Store  *store.Store
	Box    *secrets.Box
	Bus    *events.Bus
	Users  *users.Service
	Access *access.Authorizer
	Rooms  *rooms.Service
	Blobs  *attachments.Store
	// Presence tracks connected agents; Agents is the agent registry.
	Presence *agents.Presence
	Agents   *agents.Registry
	Live     *live.Hub

	handler http.Handler
	ready   atomic.Bool
	sender  delivery.Sender
	now     func() time.Time

	stop context.CancelFunc
	bg   sync.WaitGroup
}

// New builds the application: data directory, secret key, store, services and HTTP handler.
// It does not migrate the database; call Start for that.
func New(cfg *config.Config, opts Options) (*App, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	key, err := secrets.LoadOrCreateKey(cfg)
	if err != nil {
		return nil, fmt.Errorf("secret key: %w", err)
	}
	box, err := secrets.NewBox(key)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, DatabaseFile))
	if err != nil {
		return nil, err
	}
	usersSvc, err := users.NewService(st, users.Options{Now: opts.Now})
	if err != nil {
		_ = st.Close()
		return nil, err
	}

	blobs, err := attachments.Open(filepath.Join(cfg.DataDir, BlobsDir))
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	bus := events.NewBus()
	az := access.New(st, bus, access.Options{Now: opts.Now})
	a := &App{
		Config: cfg,
		Logger: opts.Logger,
		Store:  st,
		Box:    box,
		Bus:    bus,
		Users:  usersSvc,
		Access: az,
		Rooms:  rooms.New(rooms.Deps{Store: st, Access: az, Bus: bus, Now: opts.Now, Blobs: blobs}),
		Blobs:  blobs,
		sender: opts.Sender,
		now:    opts.Now,
	}
	a.Presence = agents.NewPresence(st, bus, opts.Now)
	a.Agents = agents.NewRegistry(agents.Deps{Store: st, Access: az, Bus: bus, Presence: a.Presence, Now: opts.Now})
	a.Live = live.New(live.Deps{
		Bus: bus, Access: az, Rooms: a.Rooms, Store: st, PublicURL: cfg.PublicURL, Logger: opts.Logger, Now: opts.Now,
	})
	a.handler = api.New(api.Deps{
		Config: cfg,
		Users:  usersSvc,
		Rooms:  a.Rooms,
		Agents: a.Agents,
		Blobs:  blobs,
		Audit:  audit.New(st, opts.Now),
		Live:   a.Live,
		WebUI:  webui.Handler(cfg.PublicURL),
		Logger: opts.Logger,
		Ready:  a.ready.Load,
	})
	return a, nil
}

// Handler returns the HTTP handler serving the API, health checks and (later) the web app.
func (a *App) Handler() http.Handler { return a.handler }

// SetupToken returns the one-time setup token; it is only useful while NeedsSetup is true.
func (a *App) SetupToken() string { return a.Users.SetupToken() }

// Start applies migrations, starts the background workers and marks the app ready
// (/readyz turns 200).
func (a *App) Start(ctx context.Context) error {
	if err := a.Store.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	bg, stop := context.WithCancel(context.WithoutCancel(ctx))
	a.stop = stop
	if a.sender != nil {
		w := delivery.NewOutboxWorker(delivery.OutboxOptions{
			Store: a.Store, Bus: a.Bus, Sender: a.sender, Now: a.now, Logger: a.Logger,
		})
		a.background(bg, "outbox", w.Run)
	}
	a.background(bg, "retention", func(ctx context.Context) error {
		return a.Rooms.RunRetention(ctx, rooms.RetentionInterval, a.Logger)
	})
	a.ready.Store(true)
	return nil
}

// background runs fn until the app shuts down.
func (a *App) background(ctx context.Context, name string, fn func(context.Context) error) {
	a.bg.Add(1)
	go func() {
		defer a.bg.Done()
		if err := fn(ctx); err != nil && ctx.Err() == nil {
			a.Logger.Error("background worker stopped", "worker", name, "err", err)
		}
	}()
}

// Shutdown marks the app not ready, stops the background workers and releases its resources.
func (a *App) Shutdown(_ context.Context) error {
	a.ready.Store(false)
	if a.stop != nil {
		a.stop()
	}
	a.Live.Close()
	a.bg.Wait()
	return a.Store.Close()
}
