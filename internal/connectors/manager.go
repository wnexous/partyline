package connectors

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/secrets"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// Errors of running connectors.
var (
	// ErrNotRunning means the connector is disabled, deleted or failed to start (CON-05).
	ErrNotRunning = errors.New("connector is not running")
	// ErrCannotListChats means the connector's platform cannot list chats (CON-09).
	ErrCannotListChats = errors.New("connector cannot list chats")
)

// Backoff limits of the supervisor (CON-06).
const (
	MinBackoff = 2 * time.Second
	MaxBackoff = 120 * time.Second
)

// Backoff returns the wait after the n-th consecutive failure: 2 s doubling up to 120 s.
func Backoff(n int) time.Duration {
	d := MinBackoff
	for i := 1; i < n && d < MaxBackoff; i++ {
		d *= 2
	}
	return min(d, MaxBackoff)
}

// Redacted replaces secrets in texts shown to users or stored.
const Redacted = "[redacted]"

// Redact replaces every secret in s, raw or URL-encoded, with Redacted.
func Redact(s string, secrets []string) string {
	for _, sec := range secrets {
		if sec == "" {
			continue
		}
		for _, form := range []string{sec, url.QueryEscape(sec), url.PathEscape(sec)} {
			s = strings.ReplaceAll(s, form, Redacted)
		}
	}
	return s
}

// SecretValues returns the values of the config's secret fields.
func (k Kind) SecretValues(cfg Config) []string {
	var out []string
	for _, f := range k.Fields {
		if s := cfg.String(f.Name); f.Secret() && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Source identifies the connector an inbound message came from.
type Source struct {
	ConnectorID string
	Kind        string
}

// ManagerDeps are the collaborators of the Manager.
type ManagerDeps struct {
	Store  *store.Store
	Bus    *events.Bus
	Box    *secrets.Box
	Kinds  *Registry
	Logger *slog.Logger
	// UserAgent is passed to connectors ("partyline/<version>").
	UserAgent string
	// FileURL is passed to connectors (signed attachment links); nil leaves it unset.
	FileURL func(attachmentID string) string
	// Inbound receives every message of a running connector (the inbound pipeline); nil
	// drops them.
	Inbound func(ctx context.Context, src Source, m Inbound)
	// MigrateChat moves a chat of a running connector to a new platform id; nil ignores it.
	MigrateChat func(ctx context.Context, src Source, oldID, newID string)
	// Wait sleeps for d or until ctx is done; defaults to a timer. Tests pass a fake clock.
	Wait func(ctx context.Context, d time.Duration) error
}

// Manager runs enabled connectors, each under a supervisor that restarts Run with backoff
// and reports status changes (CON-04, CON-06).
type Manager struct {
	d ManagerDeps

	ops     sync.Mutex // serializes Apply, Stop and Close
	mu      sync.Mutex // guards base and running
	base    context.Context
	running map[string]*supervisor
}

type supervisor struct {
	id        string
	kind      Kind
	conn      Connector
	secrets   []string
	cancel    context.CancelFunc
	done      chan struct{}
	connected atomic.Bool
}

// NewManager returns a Manager; call Start to run the enabled connectors.
func NewManager(d ManagerDeps) *Manager {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Wait == nil {
		d.Wait = sleep
	}
	return &Manager{d: d, running: map[string]*supervisor{}}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Start runs every enabled connector; they stop when ctx ends or on Close. Apply before
// Start does nothing (Start picks the connector up).
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	m.base = ctx
	m.mu.Unlock()
	ids, err := m.d.Store.R().ListEnabledConnectorIDs(ctx)
	if err != nil {
		m.d.Logger.Error("connectors: list enabled", "err", err)
		return
	}
	for _, id := range ids {
		m.Apply(id)
	}
}

// Close stops every connector without changing their stored status.
func (m *Manager) Close() {
	m.ops.Lock()
	defer m.ops.Unlock()
	m.mu.Lock()
	all := m.running
	m.running = map[string]*supervisor{}
	m.base = nil
	m.mu.Unlock()
	for _, s := range all {
		s.cancel()
	}
	for _, s := range all {
		<-s.done
	}
}

// Apply (re)starts the connector with its stored config when it is enabled (CON-04).
func (m *Manager) Apply(id string) {
	m.ops.Lock()
	defer m.ops.Unlock()
	m.stop(id)
	m.mu.Lock()
	base := m.base
	m.mu.Unlock()
	if base == nil || base.Err() != nil {
		return
	}
	c, err := m.d.Store.R().GetConnector(base, id)
	if err != nil || !c.Enabled {
		return
	}
	k, ok := m.d.Kinds.Lookup(c.Kind)
	if !ok || k.New == nil {
		m.persist(id, StatusError, "unknown connector kind: "+c.Kind)
		return
	}
	cfg, err := Decode(m.d.Box, c.ConfigJson, c.SecretsEnc)
	if err != nil {
		m.persist(id, StatusError, "cannot read the connector config")
		return
	}
	sec := k.SecretValues(cfg)
	conn, err := k.New(cfg, Deps{
		ID: id, Logger: m.d.Logger.With("connector_id", id, "kind", k.Name), UserAgent: m.d.UserAgent, FileURL: m.d.FileURL,
	})
	if err != nil {
		m.persist(id, StatusError, Redact(err.Error(), sec))
		return
	}
	ctx, cancel := context.WithCancel(base)
	s := &supervisor{id: id, kind: k, conn: conn, secrets: sec, cancel: cancel, done: make(chan struct{})}
	m.mu.Lock()
	m.running[id] = s
	m.mu.Unlock()
	go m.supervise(ctx, s)
}

// Stop stops the connector and sets its status to stopped (CON-05).
func (m *Manager) Stop(id string) {
	m.ops.Lock()
	defer m.ops.Unlock()
	m.stop(id)
	m.persist(id, StatusStopped, "")
}

// stop ends the connector's supervisor and waits for its Run to return.
func (m *Manager) stop(id string) {
	m.mu.Lock()
	s := m.running[id]
	delete(m.running, id)
	m.mu.Unlock()
	if s != nil {
		s.cancel()
		<-s.done
	}
}

func (m *Manager) current(id string) *supervisor {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running[id]
}

// Send delivers m through the running connector; secrets are redacted from its error.
func (m *Manager) Send(ctx context.Context, connectorID string, msg Outbound) (string, error) {
	s := m.current(connectorID)
	if s == nil {
		return "", ErrNotRunning
	}
	ext, err := s.conn.Send(ctx, msg)
	if err != nil {
		return "", errors.New(Redact(err.Error(), s.secrets))
	}
	return ext, nil
}

// Hook returns the running connector when it receives messages over HTTP.
func (m *Manager) Hook(connectorID string) (HookReceiver, bool) {
	s := m.current(connectorID)
	if s == nil {
		return nil, false
	}
	r, ok := s.conn.(HookReceiver)
	return r, ok
}

// ListChats lists the chats of the running connector (CON-09); secrets are redacted from
// its error.
func (m *Manager) ListChats(ctx context.Context, connectorID string) ([]ChatInfo, error) {
	s := m.current(connectorID)
	if s == nil {
		return nil, ErrNotRunning
	}
	lister, ok := s.conn.(ChatLister)
	if !ok {
		return nil, ErrCannotListChats
	}
	chats, err := lister.ListChats(ctx)
	if err != nil {
		return nil, errors.New(Redact(err.Error(), s.secrets))
	}
	return chats, nil
}

// supervise calls Run until ctx ends, waiting Backoff(n) after the n-th consecutive failure;
// a connection that reached StatusConnected resets the count (CON-06).
func (m *Manager) supervise(ctx context.Context, s *supervisor) {
	defer close(s.done)
	failures := 0
	for {
		m.report(s, StatusConnecting, "")
		err := s.conn.Run(ctx, handler{m: m, s: s})
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			err = errors.New("connection closed")
		}
		if s.connected.Swap(false) {
			failures = 0
		}
		failures++
		m.report(s, StatusError, err.Error())
		if m.d.Wait(ctx, Backoff(failures)) != nil {
			return
		}
	}
}

// report persists a status of a still-current supervisor.
func (m *Manager) report(s *supervisor, st Status, detail string) {
	if m.current(s.id) != s {
		return
	}
	m.persist(s.id, st, Redact(detail, s.secrets))
}

// persist stores the status and publishes it; detail must already be redacted.
func (m *Manager) persist(id string, st Status, detail string) {
	err := m.d.Store.WithTx(context.Background(), func(q *db.Queries) error {
		return q.UpdateConnectorStatus(context.Background(), db.UpdateConnectorStatusParams{Status: string(st), LastError: detail, ID: id})
	})
	if err != nil {
		m.d.Logger.Error("connectors: store status", "connector_id", id, "err", err)
		return
	}
	m.d.Bus.Publish(events.ConnectorStatus{ConnectorID: id, Status: string(st), Detail: detail})
}

// handler is the Handler of one supervisor.
type handler struct {
	m *Manager
	s *supervisor
}

func (h handler) Inbound(ctx context.Context, msg Inbound) {
	if h.m.d.Inbound == nil || h.m.current(h.s.id) != h.s {
		return
	}
	h.m.d.Inbound(ctx, Source{ConnectorID: h.s.id, Kind: h.s.kind.Name}, msg)
}

func (h handler) MigrateChat(ctx context.Context, oldID, newID string) {
	if h.m.d.MigrateChat == nil || h.m.current(h.s.id) != h.s {
		return
	}
	h.m.d.MigrateChat(ctx, Source{ConnectorID: h.s.id, Kind: h.s.kind.Name}, oldID, newID)
}

func (h handler) Status(st Status, detail string) {
	if st == StatusConnected {
		h.s.connected.Store(true)
	}
	h.m.report(h.s, st, detail)
}
