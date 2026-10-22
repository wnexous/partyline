package delivery

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// MaxAttempts is the number of send attempts before an outbox entry is marked failed (DLV-07).
const MaxAttempts = 5

// Backoff returns the wait after the given failed attempt: 2 s · 2^(attempt-1) (DLV-07).
func Backoff(attempt int) time.Duration {
	return 2 * time.Second << max(attempt-1, 0)
}

// Delivery is one outbox entry handed to a Sender. The sender loads the message, its
// attachments and the chat itself.
type Delivery struct {
	OutboxID  string
	MessageID string
	RoomID    string
	ChatID    string
	// Purpose is why the message goes to the chat: OutboxPurposeNotify for the copy of an AI's DM
	// sent to the user's chosen app (NOTI-02), else a delivery to a chat member of the room.
	Purpose dto.OutboxPurpose
	// Attempt is 1 for the first try.
	Attempt int
}

// permanentError is a send error that retrying cannot fix.
type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

// Permanent marks err as one that retrying cannot fix: the worker marks the entry failed at
// once, with err's text, instead of retrying it.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err}
}

// IsPermanent reports whether err, or an error it wraps, was marked by Permanent.
func IsPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

// Sender delivers a message to an external chat and returns the platform message id.
type Sender interface {
	Send(ctx context.Context, d Delivery) (externalID string, err error)
}

// OutboxOptions configure an OutboxWorker; Store, Bus and Sender are required.
type OutboxOptions struct {
	Store  *store.Store
	Bus    *events.Bus
	Sender Sender
	// Now defaults to time.Now.
	Now func() time.Time
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// PollInterval is how often due retries are checked; defaults to 1 s.
	PollInterval time.Duration
	// SendTimeout bounds one send; defaults to 60 s.
	SendTimeout time.Duration
}

const (
	outboxBatch     = 50
	maxLastErrorLen = 500
)

// OutboxWorker sends queued outbox entries to their chats, retrying failures with
// exponential backoff (DLV-07). Sends run one at a time, oldest due first.
type OutboxWorker struct {
	o       OutboxOptions
	running atomic.Bool
}

// NewOutboxWorker returns a worker; call Run to start it.
func NewOutboxWorker(o OutboxOptions) *OutboxWorker {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.PollInterval <= 0 {
		o.PollInterval = time.Second
	}
	if o.SendTimeout <= 0 {
		o.SendTimeout = 60 * time.Second
	}
	return &OutboxWorker{o: o}
}

// Running reports whether Run has finished its start-up pass and is waiting for work.
func (w *OutboxWorker) Running() bool { return w.running.Load() }

// Run requeues entries a previous process left in `sending`, sends everything due and then
// keeps sending, woken by new messages or the poll interval, until ctx is done.
func (w *OutboxWorker) Run(ctx context.Context) error {
	wake, cancel := w.o.Bus.Subscribe(64)
	defer cancel()
	defer w.running.Store(false)

	var ids []string
	err := w.o.Store.WithTx(ctx, func(q *db.Queries) (err error) {
		ids, err = q.RequeueSendingOutbox(ctx, w.o.Now().UnixMilli())
		return err
	})
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		w.o.Logger.Info("outbox: requeued interrupted sends", "count", len(ids))
	}
	w.flushLogged(ctx)
	w.running.Store(true)

	ticker := time.NewTicker(w.o.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		case e := <-wake:
			switch e.(type) {
			case events.MessageCreated, events.Lagged:
			default:
				continue
			}
		}
		w.flushLogged(ctx)
	}
}

func (w *OutboxWorker) flushLogged(ctx context.Context) {
	if err := w.Flush(ctx); err != nil && ctx.Err() == nil {
		w.o.Logger.Error("outbox: flush failed", "err", err)
	}
}

// Flush sends every entry that is due now and returns when none is left.
func (w *OutboxWorker) Flush(ctx context.Context) error {
	for {
		due, err := w.o.Store.R().ListDueOutbox(ctx, db.ListDueOutboxParams{Now: w.o.Now().UnixMilli(), Limit: outboxBatch})
		if err != nil {
			return err
		}
		if len(due) == 0 {
			return nil
		}
		for _, row := range due {
			if err := w.send(ctx, row); err != nil {
				return err
			}
		}
		if len(due) < outboxBatch {
			return nil
		}
	}
}

// send claims one entry, sends it and records the outcome.
func (w *OutboxWorker) send(ctx context.Context, row db.ListDueOutboxRow) error {
	var claimed int64
	err := w.o.Store.WithTx(ctx, func(q *db.Queries) (err error) {
		claimed, err = q.MarkOutboxSending(ctx, db.MarkOutboxSendingParams{UpdatedAt: w.o.Now().UnixMilli(), ID: row.ID})
		return err
	})
	if err != nil || claimed == 0 {
		return err
	}
	w.publish(row, dto.OutboxStatusSending)

	attempt := int(row.Attempts) + 1
	sendCtx, cancel := context.WithTimeout(ctx, w.o.SendTimeout)
	externalID, sendErr := w.o.Sender.Send(sendCtx, Delivery{
		OutboxID: row.ID, MessageID: row.MessageID, RoomID: row.RoomID, ChatID: row.ChatID,
		Purpose: dto.OutboxPurpose(row.Purpose), Attempt: attempt,
	})
	cancel()

	// Record the outcome even when ctx was cancelled mid-send, so the entry is not stuck.
	rec := context.WithoutCancel(ctx)
	now := w.o.Now()
	if sendErr == nil {
		if err := w.o.Store.WithTx(rec, func(q *db.Queries) error {
			return q.MarkOutboxSent(rec, db.MarkOutboxSentParams{ExternalID: externalID, UpdatedAt: now.UnixMilli(), ID: row.ID})
		}); err != nil {
			return err
		}
		w.publish(row, dto.OutboxStatusSent)
		return nil
	}

	status := dto.OutboxStatusPending
	if attempt >= MaxAttempts || IsPermanent(sendErr) {
		status = dto.OutboxStatusFailed
	}
	// The error text comes from the connector, which redacts its secrets; it is stored for
	// the UI but never logged (SEC-05).
	w.o.Logger.Warn("outbox: send failed", "outbox_id", row.ID, "chat_id", row.ChatID, "attempt", attempt, "status", status)
	if err := w.o.Store.WithTx(rec, func(q *db.Queries) error {
		return q.MarkOutboxAttemptFailed(rec, db.MarkOutboxAttemptFailedParams{
			Status:        string(status),
			Attempts:      int64(attempt),
			NextAttemptAt: now.Add(Backoff(attempt)).UnixMilli(),
			LastError:     truncate(sendErr.Error(), maxLastErrorLen),
			UpdatedAt:     now.UnixMilli(),
			ID:            row.ID,
		})
	}); err != nil {
		return err
	}
	w.publish(row, status)
	return ctx.Err()
}

func (w *OutboxWorker) publish(row db.ListDueOutboxRow, status dto.OutboxStatus) {
	w.o.Bus.Publish(events.OutboxChanged{
		OutboxID: row.ID, MessageID: row.MessageID, RoomID: row.RoomID, ChatID: row.ChatID, Status: string(status),
	})
}

// truncate cuts s to at most n bytes without splitting a UTF-8 sequence.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
