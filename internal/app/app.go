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
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/discord"
	"github.com/wnexous/partyline/internal/connectors/telegram"
	"github.com/wnexous/partyline/internal/connectors/webhook"
	"github.com/wnexous/partyline/internal/connectors/whatsapp"
	"github.com/wnexous/partyline/internal/conversations"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/devices"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/identities"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/live"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/secrets"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/users"
	"github.com/wnexous/partyline/internal/webpush"
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
	// LiveSessionCheck is how often live sockets re-check their session; defaults to
	// live.DefaultSessionCheckInterval. Tests shorten it.
	LiveSessionCheck time.Duration
	// DisableOutbox keeps the worker that retries the sends of AIs through their connections
	// from running, so a send that failed stays pending (tests that inspect them).
	DisableOutbox bool
	// ConnectorKinds are the supported connector kinds; nil means BuiltinKinds().
	ConnectorKinds []connectors.Kind
	// Version is the build version, sent by connectors as User-Agent "partyline/<version>".
	Version string
	// InboundWait sleeps while the inbound pipeline holds an own-account message back until a
	// send to its chat finishes (ACC-05); nil sleeps for real. Tests advance their clock with it.
	InboundWait func(ctx context.Context, d time.Duration) error
	// ConnectorWait sleeps while the connectors' supervisor backs off before it restarts a failed
	// connector (CON-06); nil sleeps for real. Tests that make a connector crash pass a wait that
	// does not take seconds.
	ConnectorWait func(ctx context.Context, d time.Duration) error
}

// BuiltinKinds returns the connector kinds shipped with Partyline. A new platform adds its
// registration line here (AD-007).
func BuiltinKinds() []connectors.Kind {
	return []connectors.Kind{
		whatsapp.Kind(),
		telegram.Kind(),
		discord.Kind(),
		webhook.Kind(),
	}
}

// App is one running Partyline instance.
type App struct {
	Config *config.Config
	Logger *slog.Logger
	Store  *store.Store
	Box    *secrets.Box
	// VAPID signs the Web Push requests; it is the instance key from <data dir>/vapid.key.
	VAPID  *webpush.VAPID
	Bus    *events.Bus
	Users  *users.Service
	Access *access.Authorizer
	// Authz answers the organization questions: membership, the admin permission, who manages whom
	// and who is trusted (AD-017). Every service that authorizes through organizations asks it.
	Authz *orgs.Authz
	// Orgs is the organization use cases: organizations, their org chart, members and invitations.
	Orgs  *orgs.Service
	Rooms *rooms.Service
	// Intelligences serves the profiles of users, AIs and people.
	Intelligences *intelligences.Service
	// Conversations opens direct messages and creates groups.
	Conversations *conversations.Service
	Blobs         *attachments.Store
	// Platform is the delivery queue of the messages that arrive on connections (AD-015).
	Platform *platform.Queue
	// Presence tracks connected agents; Agents is the agent registry.
	Presence *agents.Presence
	Agents   *agents.Registry
	// Connectors manages connectors (CRUD); ConnectorManager runs them.
	Connectors       *connectors.Service
	ConnectorManager *connectors.Manager
	// Sends sends what an AI answers through a connection it operates (AD-015).
	Sends *connectors.Sends
	// Identities links chat accounts to users (pairing codes).
	Identities *identities.Service
	// Devices approves new machines (partyline setup --browser).
	Devices *devices.Service
	// Gateway serves the agent WebSocket and pushes messages to connected agents.
	Gateway *agents.Gateway
	Live    *live.Hub

	// trust opens the trust of one operation: a message is trusted by an AI when its author manages
	// the AI in the AI's home organization (POS-10). Every operation reads each chart at most once.
	trust delivery.TrustSource

	handler http.Handler
	ready   atomic.Bool
	// sendWorker is whether Start runs the worker that retries the sends of Sends.
	sendWorker bool

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
	vapid, err := webpush.LoadOrCreateVAPID(cfg.DataDir, box)
	if err != nil {
		return nil, fmt.Errorf("vapid key: %w", err)
	}
	if vapid.Replaced {
		opts.Logger.Warn("webpush: replaced an invalid VAPID key file and dropped the stored subscriptions", "file", webpush.VAPIDKeyFile)
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, DatabaseFile))
	if err != nil {
		return nil, err
	}
	bus := events.NewBus()
	presence := agents.NewPresence(st, bus, opts.Now)
	usersSvc, err := users.NewService(st, users.Options{
		Now: opts.Now, Bus: bus,
		Kick: func(ctx context.Context, agentID string) {
			presence.Kick(ctx, agentID, agents.CloseDeleted, agents.ReasonDeleted)
		},
	})
	if err != nil {
		_ = st.Close()
		return nil, err
	}

	blobs, err := attachments.Open(filepath.Join(cfg.DataDir, BlobsDir))
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	authz := orgs.NewAuthz(st)
	az := access.New(st, access.Options{Authz: authz})
	trust := delivery.TrustSource(func() delivery.Trust { return authz.Session() })
	auditLog := audit.New(st, opts.Now)
	a := &App{
		Config:        cfg,
		Logger:        opts.Logger,
		Store:         st,
		Box:           box,
		VAPID:         vapid,
		Bus:           bus,
		Users:         usersSvc,
		Access:        az,
		Authz:         authz,
		Orgs:          orgs.NewService(orgs.Deps{Store: st, Authz: authz, Bus: bus, Audit: auditLog, Blobs: blobs, Now: opts.Now}),
		Rooms:         rooms.New(rooms.Deps{Store: st, Access: az, Bus: bus, Now: opts.Now, Blobs: blobs}),
		Blobs:         blobs,
		Platform:      platform.New(platform.Deps{Store: st, Bus: bus, Now: opts.Now}),
		Intelligences: intelligences.New(intelligences.Deps{Store: st, Access: az, Bus: bus, Blobs: blobs, Online: presence.Online, Now: opts.Now}),
	}
	a.Presence = presence
	a.Agents = agents.NewRegistry(agents.Deps{Store: st, Access: az, Bus: bus, Presence: a.Presence, Audit: auditLog, Logger: opts.Logger, Now: opts.Now})
	a.Conversations = conversations.New(conversations.Deps{
		Store: st, Rooms: a.Rooms, Intelligences: a.Intelligences, Bus: bus, Queue: a.Platform, Access: az, AllowCreate: a.Agents.AllowCreate,
		Online: a.Presence.Online, Now: opts.Now,
		Kick: func(ctx context.Context, agentID string) {
			a.Presence.Kick(ctx, agentID, agents.CloseDeleted, agents.ReasonDeleted)
		},
	})
	a.trust = trust
	a.Gateway = agents.NewGateway(agents.GatewayDeps{
		Store: st, Registry: a.Agents, Presence: a.Presence, Rooms: a.Rooms, Platform: a.Platform, Logger: opts.Logger,
		Bus: bus, Sessions: usersSvc, Trust: trust, Authz: authz,
	})
	kinds := opts.ConnectorKinds
	if kinds == nil {
		kinds = BuiltinKinds()
	}
	a.Identities = identities.New(identities.Deps{Store: st, Bus: bus, Now: opts.Now})
	registry := connectors.NewRegistry(kinds...)
	version := opts.Version
	if version == "" {
		version = "dev"
	}
	sentIDs := connectors.NewSentIDs(opts.Now)
	pipeline := connectors.NewPipeline(connectors.PipelineDeps{
		Store: st, Bus: bus, Blobs: blobs, Identities: a.Identities, Trust: authz, Queue: a.Platform,
		Sent: sentIDs, Wait: opts.InboundWait, MaxUploadBytes: cfg.MaxUploadBytes, Logger: opts.Logger, Now: opts.Now,
	})
	fileLinks := connectors.NewFileLinks(box, st, cfg.PublicURL, opts.Now)
	a.ConnectorManager = connectors.NewManager(connectors.ManagerDeps{
		Store: st, Bus: bus, Box: box, Kinds: registry, Logger: opts.Logger, UserAgent: "partyline/" + version,
		FileURL: fileLinks.URL, AllowPrivateNetworks: cfg.AllowPrivateNetworks,
		PrivateAllowlist: cfg.PrivateNetworkAllowlist, MaxMediaBytes: cfg.MaxUploadBytes, Replays: connectors.NewReplays(st),
		Inbound: pipeline.Handle, MigrateChat: pipeline.MigrateChat, Wait: opts.ConnectorWait,
	})
	a.Sends = connectors.NewSends(connectors.SendsDeps{
		Store: st, Transport: a.ConnectorManager, Sent: sentIDs, Now: opts.Now, Logger: opts.Logger,
		Kinds: registry, Bus: bus, AllowNewChat: a.Agents.AllowNewChat,
	})
	a.sendWorker = !opts.DisableOutbox
	a.Connectors = connectors.NewService(connectors.ServiceDeps{
		Store: st, Access: az, Bus: bus, Box: box, Kinds: registry, Queue: a.Platform, Runner: a.ConnectorManager, Now: opts.Now,
	})
	a.Live = live.New(live.Deps{
		Bus: bus, Access: az, Authz: authz, Rooms: a.Rooms, Intelligences: a.Intelligences, Orgs: a.Orgs, Store: st, PublicURL: cfg.PublicURL, Logger: opts.Logger, Now: opts.Now,
		Sessions: usersSvc, SessionCheckInterval: opts.LiveSessionCheck,
	})
	a.Devices = devices.New(devices.Deps{Store: st, Users: usersSvc, Box: box, Audit: auditLog, Now: opts.Now})
	a.handler = api.New(api.Deps{
		Config:        cfg,
		Users:         usersSvc,
		Rooms:         a.Rooms,
		Agents:        a.Agents,
		Orgs:          a.Orgs,
		Intelligences: a.Intelligences,
		Conversations: a.Conversations,
		Connectors:    a.Connectors,
		Identities:    a.Identities,
		Devices:       a.Devices,
		Hooks:         a.ConnectorManager,
		Crashes:       a.ConnectorManager,
		FileLinks:     fileLinks,
		Blobs:         blobs,
		Audit:         auditLog,
		Platform:      a.Platform,
		Sends:         a.Sends,
		Live:          a.Live,
		AgentWS:       a.Gateway,
		Inbox:         a.Gateway,
		Trust:         trust,
		WebUI:         webui.Handler(cfg.PublicURL),
		Logger:        opts.Logger,
		Ready:         a.ready.Load,
		Version:       version,
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
	if a.sendWorker {
		// Before the server takes requests: a send a request starts must not look interrupted.
		if err := a.Sends.Recover(ctx); err != nil {
			return fmt.Errorf("recover platform sends: %w", err)
		}
	}
	bg, stop := context.WithCancel(context.WithoutCancel(ctx))
	a.stop = stop
	dispatcher := delivery.NewAgentDispatcher(delivery.DispatcherOptions{
		Store: a.Store, Bus: a.Bus, Agents: a.Gateway, Load: a.Gateway.Message, Trust: a.trust, Logger: a.Logger,
	})
	a.background(bg, "agent-dispatcher", dispatcher.Run)
	if a.sendWorker {
		a.background(bg, "platform-sends", a.Sends.Run)
	}
	a.ConnectorManager.Start(bg)
	a.background(bg, "platform-purge", func(ctx context.Context) error {
		return a.Platform.Run(ctx, platform.PurgeInterval, a.Rooms.ReleaseBlobs, a.Logger)
	})
	a.background(bg, "retention", func(ctx context.Context) error {
		return a.Rooms.RunRetention(ctx, rooms.RetentionInterval, a.Logger,
			rooms.RetentionTask{Name: "revoked_tokens", Run: a.Users.PurgeRevokedTokens},
			rooms.RetentionTask{Name: "platform_sends", Run: a.Sends.Purge},
			rooms.RetentionTask{Name: "org_invitations", Run: a.Orgs.PurgeExpiredInvitations})
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
	a.ConnectorManager.Close()
	a.Gateway.Close()
	a.Live.Close()
	a.bg.Wait()
	return a.Store.Close()
}
