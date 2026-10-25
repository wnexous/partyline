package webpush

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// Deliverer sends the notifier's jobs and records the outcome on the subscription (PUSH-12). It
// talks to the push service through a Sender, the Client in the app's wiring, and never logs what
// it sends: only the endpoint's host and the push service's status.
type Deliverer struct {
	store  *store.Store
	sender Sender
	now    func() time.Time
	logger *slog.Logger
}

// DelivererDeps configure a Deliverer; Store and Sender are required.
type DelivererDeps struct {
	Store  *store.Store
	Sender Sender
	// Now returns the current time, written to last_ok_at after a 2xx; nil means time.Now.
	Now func() time.Time
	// Logger defaults to slog.Default().
	Logger *slog.Logger
}

// NewDeliverer returns a deliverer that writes its outcomes to the store.
func NewDeliverer(deps DelivererDeps) *Deliverer {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return &Deliverer{store: deps.Store, sender: deps.Sender, now: deps.Now, logger: deps.Logger}
}

// Deliver sends one payload to one subscription and records what the push service answered: a 2xx
// sets last_ok_at, 404/410 delete the row (the browser is gone), and anything else keeps it. The
// error from the Sender is reduced to one word before it is logged, because a *url.Error carries
// the endpoint, whose path is a capability token (PUSH-12, PUSH-22).
func (d *Deliverer) Deliver(ctx context.Context, sub Subscription, payload []byte) {
	host := endpointHost(sub.Endpoint)
	status, err := d.sender.Send(ctx, sub, payload)
	if err != nil {
		d.logger.Warn("webpush: the push request failed", "endpoint_host", host, "err", sendErrorKind(err))
		return
	}
	switch {
	case status >= http.StatusOK && status < http.StatusMultipleChoices:
		d.markDelivered(ctx, sub.ID)
		d.logger.Debug("webpush: notification delivered", "endpoint_host", host, "status", status)
	case status == http.StatusNotFound || status == http.StatusGone:
		d.delete(ctx, sub.ID)
		d.logger.Info("webpush: the push service dropped the subscription", "endpoint_host", host, "status", status)
	default:
		d.logger.Warn("webpush: the push service refused the notification", "endpoint_host", host, "status", status)
	}
}

// markDelivered records the time of the 2xx; a failure to write it is a warning without content
// and does not affect the other subscriptions.
func (d *Deliverer) markDelivered(ctx context.Context, id string) {
	at := sql.NullInt64{Int64: d.now().UnixMilli(), Valid: true}
	err := d.store.WithTx(ctx, func(q *db.Queries) error {
		return q.MarkPushSubscriptionDelivered(ctx, db.MarkPushSubscriptionDeliveredParams{LastOkAt: at, ID: id})
	})
	if err != nil {
		d.logger.Warn("webpush: cannot record the delivery time")
	}
}

// delete drops the row of a subscription the push service no longer knows.
func (d *Deliverer) delete(ctx context.Context, id string) {
	err := d.store.WithTx(ctx, func(q *db.Queries) error {
		_, err := q.DeletePushSubscriptionByID(ctx, id)
		return err
	})
	if err != nil {
		d.logger.Warn("webpush: cannot delete the dropped subscription")
	}
}

// endpointHost returns the host of a stored endpoint, the only part of it that may reach a log. An
// unparsable endpoint (validation refuses one on subscribe) is reported without its value.
func endpointHost(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" {
		return "invalid"
	}
	return u.Hostname()
}

// sendErrorKind reduces a failed request to one of three words: canceled, timeout or network. The
// request itself never reaches a log, because its URL carries the subscription's capability token.
func sendErrorKind(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return "network"
	}
	if errors.As(err, &netErr) {
		return "network"
	}
	// The request could not be built from the stored row: encryption, keys or the endpoint shape.
	return "invalid"
}
