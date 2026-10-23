package connectors

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors/safehttp"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/secrets"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// Errors of running connectors.
var (
	// ErrNotRunning means the connector is disabled, deleted or failed to start (CON-05).
	ErrNotRunning = errors.New("connector is not running")
	// ErrReactionsUnsupported means the connector's platform cannot send reactions (REACT-01).
	ErrReactionsUnsupported = errors.New("reactions not supported")
	// ErrCannotListChats means the connector's platform cannot list chats (CON-09).
	ErrCannotListChats = errors.New("connector cannot list chats")
	// ErrPlatform wraps a failure reported by the platform itself (CON-09: 502); its
	// detail is for logs only, never for API answers.
	ErrPlatform = errors.New("the platform request failed")
)

// NoOperator is the status detail of a connection nobody operates (CID-02).
const NoOperator = dto.ConnectorNoOperator

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
	// OwnerID is the connector's owner.
	OwnerID string
	// LinkOwnerOnly is the kind's Kind.LinkOwnerOnly.
	LinkOwnerOnly bool
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
	// AllowPrivateNetworks and PrivateAllowlist are passed to connectors (CON-13).
	AllowPrivateNetworks bool
	PrivateAllowlist     *safehttp.Allowlist
	// MaxMediaBytes is passed to connectors (the upload limit, S8).
	MaxMediaBytes int64
	// Replays is passed to connectors (HOOK-05); nil leaves it unset.
	Replays Replays
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
	mu      sync.Mutex // guards base, running and unwatch
	base    context.Context
	running map[string]*supervisor
	// unwatch ends the manager's listening for operators that went away (watchOperators).
	unwatch func()
}

type supervisor struct {
	id        string
	owner     string
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
	m.watchOperators(ctx)
	ids, err := m.d.Store.R().ListEnabledConnectorIDs(ctx)
	if err != nil {
		m.d.Logger.Error("connectors: list enabled", "err", err)
		return
	}
	for _, id := range ids {
		m.Apply(id)
	}
}

// operatorEventBuffer is how many bus events the manager's subscription holds while Apply is busy
// stopping a connector; a longer burst makes the bus say it lagged (watchOperators).
const operatorEventBuffer = 64

// watchOperators applies the connections that lose their operator, so that each stops and shows
// what to do (CID-02, CID-24): the agent registry publishes ConnectorsOrphaned when it deletes an
// AI that operated some. A bus that lagged may have dropped such an event, so the manager then
// looks at what it runs. It listens until ctx ends or Close.
func (m *Manager) watchOperators(ctx context.Context) {
	if m.d.Bus == nil {
		return
	}
	ch, cancel := m.d.Bus.Subscribe(operatorEventBuffer)
	m.mu.Lock()
	previous := m.unwatch
	m.unwatch = cancel
	m.mu.Unlock()
	if previous != nil {
		previous()
	}
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-ch:
				if !ok {
					return
				}
				switch e := e.(type) {
				case events.ConnectorsOrphaned:
					for _, id := range e.IDs {
						m.Apply(id)
					}
				case events.Lagged:
					m.reconcile(ctx)
				}
			}
		}
	}()
}

// reconcile applies the running connections whose operator is gone.
func (m *Manager) reconcile(ctx context.Context) {
	m.mu.Lock()
	ids := make([]string, 0, len(m.running))
	for id := range m.running {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		if c, err := m.d.Store.R().GetConnector(ctx, id); err == nil && !c.OperatorIid.Valid {
			m.Apply(id)
		}
	}
}

// Close stops every connector without changing their stored status.
func (m *Manager) Close() {
	m.mu.Lock()
	unwatch := m.unwatch
	m.unwatch = nil
	m.mu.Unlock()
	if unwatch != nil {
		unwatch()
	}
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

// Apply (re)starts the connector with its stored config when it is enabled (CON-04). A connector
// nobody operates is not started: it reports the error status NoOperator (CID-02).
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
	if !c.OperatorIid.Valid { // CID-02: what arrives would have no session to go to
		m.persist(id, StatusError, NoOperator)
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
		AllowPrivateNetworks: m.d.AllowPrivateNetworks, PrivateAllowlist: m.d.PrivateAllowlist,
		MaxMediaBytes: m.d.MaxMediaBytes, Replays: m.d.Replays,
	})
	if err != nil {
		m.persist(id, StatusError, Redact(err.Error(), sec))
		return
	}
	ctx, cancel := context.WithCancel(base)
	s := &supervisor{id: id, owner: c.OwnerID, kind: k, conn: conn, secrets: sec, cancel: cancel, done: make(chan struct{})}
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

// React sends a reaction through the running connector (REACT-01): ErrNotRunning when it is
// not running, ErrReactionsUnsupported when its platform has no reactions. Errors are
// redacted like Send's.
func (m *Manager) React(ctx context.Context, connectorID string, r Reaction) error {
	s := m.current(connectorID)
	if s == nil {
		return ErrNotRunning
	}
	reactor, ok := s.conn.(Reactor)
	if !ok {
		return ErrReactionsUnsupported
	}
	if err := reactor.React(ctx, r); err != nil {
		return errors.New(Redact(err.Error(), s.secrets))
	}
	return nil
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

// ListChats lists the chats of the running connector (CON-09). A platform failure is
// logged (secrets redacted) and returned wrapping ErrPlatform.
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
		detail := Redact(err.Error(), s.secrets)
		m.d.Logger.Warn("connectors: list chats failed", "connector_id", connectorID, "err", detail)
		return nil, fmt.Errorf("%w: %s", ErrPlatform, detail)
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
	h.m.d.Inbound(ctx, h.source(), msg)
}

func (h handler) MigrateChat(ctx context.Context, oldID, newID string) {
	if h.m.d.MigrateChat == nil || h.m.current(h.s.id) != h.s {
		return
	}
	h.m.d.MigrateChat(ctx, h.source(), oldID, newID)
}

// Account stores how the platform names the connector's account (ACC-07): trimmed, without
// control characters, at most MaxAccountLabel code points.
func (h handler) Account(label string) {
	if h.m.current(h.s.id) != h.s {
		return
	}
	label = cleanAccountLabel(label)
	err := h.m.d.Store.WithTx(context.Background(), func(q *db.Queries) error {
		return q.SetConnectorAccountLabel(context.Background(), db.SetConnectorAccountLabelParams{AccountLabel: label, ID: h.s.id})
	})
	if err != nil {
		h.m.d.Logger.Error("connectors: store account label", "connector_id", h.s.id, "err", err)
	}
}

// MaxAccountLabel is the longest account label in code points.
const MaxAccountLabel = 100

func cleanAccountLabel(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == utf8.RuneError || unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
	if r := []rune(s); len(r) > MaxAccountLabel {
		s = strings.TrimSpace(string(r[:MaxAccountLabel]))
	}
	return s
}

func (h handler) source() Source {
	return Source{ConnectorID: h.s.id, Kind: h.s.kind.Name, OwnerID: h.s.owner, LinkOwnerOnly: h.s.kind.LinkOwnerOnly}
}

func (h handler) Status(st Status, detail string) {
	if st == StatusConnected {
		h.s.connected.Store(true)
	}
	h.m.report(h.s, st, detail)
}
