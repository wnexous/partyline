package webpush

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// The connection alerter's constants.
const (
	// alertBuffer is the bus backlog of the alerter's subscription: one event per status write,
	// and a consumer that falls behind only misses an ephemeral alert (ALERT-14).
	alertBuffer = 256
	// alertQueueSize is how many alert deliveries wait for a send goroutine; AlertDeps.QueueSize
	// overrides it in tests (ALERT-15).
	alertQueueSize = 64
	// alertWorkers is how many alerts are delivered at once, so the message notifier's queue is
	// never shared with an alert burst.
	alertWorkers = 2
)

// AlertDeps configure an Alerter; Store, Bus and Delivery are required.
type AlertDeps struct {
	Store *store.Store
	Bus   *events.Bus
	// Delivery sends each queued job; tests inject a recorder.
	Delivery Delivery
	// PublicURL builds the payload's absolute connection URL.
	PublicURL *url.URL
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// Now returns the current time for the cooldown; nil means time.Now.
	Now func() time.Time
	// QueueSize is the bounded queue length; zero means 64 and tests lower it (ALERT-15).
	QueueSize int
}

// Alerter turns a connection's error status into one web push per device of the people
// responsible for it (ALERT-01, ALERT-02). It subscribes to the bus in the constructor, before Run,
// so no status published while the app starts is missed; the outage state is memory, seeded from
// the stored statuses (ALERT-10).
type Alerter struct {
	store     *store.Store
	delivery  Delivery
	publicURL *url.URL
	logger    *slog.Logger
	now       func() time.Time
	worker    *worker

	mu    sync.Mutex
	rules map[string]*alertRule

	events   <-chan events.Event
	cancel   func() // the bus subscription
	closed   chan struct{}
	closeOne sync.Once
}

// NewAlerter returns an alerter that is already subscribed to the bus; call Seed for the stored
// outage state, Run to process the events and Close to cancel the subscription. It does no I/O, so
// the app can build it before the database is migrated.
func NewAlerter(deps AlertDeps) *Alerter {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.PublicURL == nil {
		deps.PublicURL = &url.URL{}
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	queueSize := deps.QueueSize
	if queueSize <= 0 {
		queueSize = alertQueueSize
	}
	ch, cancel := deps.Bus.Subscribe(alertBuffer)
	return &Alerter{
		store:     deps.Store,
		delivery:  deps.Delivery,
		publicURL: deps.PublicURL,
		logger:    deps.Logger,
		now:       deps.Now,
		worker:    newWorker(queueSize, alertWorkers, deps.Logger),
		rules:     map[string]*alertRule{},
		events:    ch,
		cancel:    cancel,
		closed:    make(chan struct{}),
	}
}

// Seed fills the outage state from the stored statuses, so a connection already in error sends no
// alert for the outage it was already in and one stored healthy alerts on its first error
// (ALERT-10, ALERT-11). A read failure warns and leaves the map empty: every connection is then
// unseen, so its first error only records a baseline instead of flooding on the boot burst.
func (a *Alerter) Seed(ctx context.Context) {
	rows, err := a.store.R().ListConnectorStatuses(ctx)
	if err != nil {
		a.logger.Warn("webpush: cannot seed the connection alert state", "err", err)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, row := range rows {
		failing := dto.ConnectorStatus(row.Status) == dto.ConnectorStatusError
		a.rules[row.ID] = &alertRule{down: failing, seenUp: !failing}
	}
}

// Run processes the bus events and runs the worker until ctx ends, the alerter is closed, or its
// subscription ends. It returns nil in every one of those cases and waits for the worker's
// goroutines, dropping the jobs still queued (ALERT-14).
func (a *Alerter) Run(ctx context.Context) error {
	defer a.Close()
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = a.worker.run(workerCtx)
	}()
	defer func() { <-done }()
	defer cancelWorkers()

	for {
		// An alerter closed before Run must not process the events its subscription buffered.
		select {
		case <-a.closed:
			return nil
		default:
		}
		select {
		case <-ctx.Done():
			return nil
		case <-a.closed:
			return nil
		case e, ok := <-a.events:
			if !ok {
				return nil
			}
			switch e := e.(type) {
			case events.ConnectorStatus:
				a.handle(ctx, e)
			case events.Lagged:
				// Alerts are ephemeral: a missed event only costs one alert, never a refetch.
				a.logger.Debug("webpush: alert consumer lagged; alerts may be missed")
			}
		}
	}
}

// Close cancels the bus subscription, also when Run never started (app.Options.DisablePush).
func (a *Alerter) Close() {
	a.closeOne.Do(func() {
		a.cancel()
		close(a.closed)
	})
}

// handle applies one status event. An error first reads the connection, because the event only
// carries its id and the row decides whether the message is allowed; every run of the rule emits
// at most one alert. A read failure leaves the rule untouched, so the next error retries
// (ALERT-12).
func (a *Alerter) handle(ctx context.Context, e events.ConnectorStatus) {
	var conn db.Connector
	if dto.ConnectorStatus(e.Status) == dto.ConnectorStatusError {
		c, err := a.store.R().GetConnector(ctx, e.ConnectorID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// Deleted before the event was handled: nothing to alert (ALERT-12).
			return
		case err != nil:
			a.logger.Warn("webpush: cannot read the connection of an alert",
				"connector_id", e.ConnectorID, "err", err)
			return
		case !c.Enabled:
			// Disabling a connection is deliberate, and this closes the race with a status that
			// was still an error when it was written (ALERT-06).
			return
		}
		conn = c
	}
	if !a.observe(e.ConnectorID, e.Status) {
		a.logger.Debug("webpush: connection alert suppressed", "connector_id", e.ConnectorID, "status", e.Status)
		return
	}
	a.send(ctx, conn, e)
}

// observe applies one status to the connection's rule and reports whether it should alert. A
// connection with no entry starts on the zero rule: never seen healthy, so its first error only
// records the baseline (ALERT-10, ALERT-11).
func (a *Alerter) observe(id, status string) bool {
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	rule, ok := a.rules[id]
	if !ok {
		rule = &alertRule{}
		a.rules[id] = rule
	}
	return rule.observe(status, now)
}

// send builds the fixed payload once, resolves the responsible devices and submits one job per
// device. The log lines carry the connection id and the status only, never the name, the reason,
// the platform detail, the URL or a device (ALERT-14).
func (a *Alerter) send(ctx context.Context, c db.Connector, e events.ConnectorStatus) {
	payload, err := alertPayload(a.publicURL, c.ID, c.Name, c.Kind, e.Detail)
	if err != nil {
		a.logger.Warn("webpush: cannot build a connection alert", "connector_id", c.ID)
		return
	}
	targets, err := a.store.R().ListConnectorAlertTargets(ctx, c.ID)
	if err != nil {
		a.logger.Error("webpush: cannot resolve the connection alert targets",
			"connector_id", c.ID, "err", err)
		return
	}
	if len(targets) == 0 {
		a.logger.Debug("webpush: connection alert has no recipients", "connector_id", c.ID, "status", e.Status)
		return
	}
	queued := 0
	for _, row := range targets {
		sub := Subscription{ID: row.SubscriptionID, Endpoint: row.Endpoint, P256dh: row.P256dh, Auth: row.Auth}
		if a.submit(sub, payload, e) {
			queued++
		}
	}
	if queued > 0 {
		a.logger.Info("webpush: connection alert queued", "connector_id", c.ID, "status", e.Status)
	}
}

// submit queues one delivery, logging a full queue with the connection id and the status only: a
// dropped alert never blocks the status change and never logs its content (ALERT-15).
func (a *Alerter) submit(sub Subscription, payload []byte, e events.ConnectorStatus) bool {
	ok := a.worker.submit(func(ctx context.Context) {
		a.delivery.Deliver(ctx, sub, payload)
	})
	if !ok {
		a.logger.Warn("webpush: connection alert queue full",
			"connector_id", e.ConnectorID, "status", e.Status)
	}
	return ok
}
