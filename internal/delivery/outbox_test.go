package delivery_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
)

var t0 = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Set(t time.Time) {
	c.mu.Lock()
	c.now = t
	c.mu.Unlock()
}

// workerDeadline bounds waits for the background worker; generous because a loaded machine
// can delay it by many seconds, while a healthy run ends in milliseconds.
const workerDeadline = time.Minute

// fakeSender records deliveries and fails the first `fail` calls.
type fakeSender struct {
	mu    sync.Mutex
	fail  int
	calls []delivery.Delivery
}

func (s *fakeSender) Send(_ context.Context, d delivery.Delivery) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, d)
	if len(s.calls) <= s.fail {
		return "", errors.New("platform unavailable")
	}
	return "ext-" + d.OutboxID, nil
}

func (s *fakeSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

type fixture struct {
	path string
	st   *store.Store
	raw  *sql.DB
	bus  *events.Bus
	room string
	chat string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "partyline.db")
	f := &fixture{path: path, bus: events.NewBus()}
	f.st = f.open(t)
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	f.raw = raw

	user, conn := store.NewID(), store.NewID()
	f.room, f.chat = store.NewID(), store.NewID()
	ms := t0.UnixMilli()
	f.exec(t, "INSERT INTO users (id, username, password_hash, role, created_at) VALUES (?, 'alice', 'x', 'member', ?)", user, ms)
	f.exec(t, "INSERT INTO rooms (id, owner_id, name, created_at, updated_at) VALUES (?, ?, 'ops', ?, ?)", f.room, user, ms, ms)
	f.exec(t, "INSERT INTO connectors (id, owner_id, kind, name, created_at, updated_at) VALUES (?, ?, 'telegram', 'tg', ?, ?)", conn, user, ms, ms)
	f.exec(t, "INSERT INTO chats (id, connector_id, external_id, title, kind, created_at) VALUES (?, ?, 'ext-chat', 'group', 'group', ?)", f.chat, conn, ms)
	f.exec(t, "INSERT INTO room_members (id, room_id, kind, member_id, created_at) VALUES (?, ?, 'chat', ?, ?)", store.NewID(), f.room, f.chat, ms)
	return f
}

func (f *fixture) open(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(f.path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(context.Background()))
	return st
}

func (f *fixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	_, err := f.raw.ExecContext(context.Background(), query, args...)
	require.NoError(t, err)
}

// queue inserts a message and one outbox row for the fixture chat, due at `due`, in `status`.
func (f *fixture) queue(t *testing.T, seq int, status dto.OutboxStatus, due time.Time) (messageID, outboxID string) {
	t.Helper()
	messageID, outboxID = store.NewID(), store.NewID()
	f.exec(t, "INSERT INTO messages (id, room_id, seq, author_kind, author_id, kind, text, created_at) VALUES (?, ?, ?, 'user', 'u', 'text', 'hi', ?)",
		messageID, f.room, seq, t0.UnixMilli())
	f.exec(t, "INSERT INTO outbox (id, message_id, chat_id, status, attempts, next_attempt_at, updated_at) VALUES (?, ?, ?, ?, 0, ?, ?)",
		outboxID, messageID, f.chat, string(status), due.UnixMilli(), t0.UnixMilli())
	return messageID, outboxID
}

type row struct {
	Status        string
	Attempts      int
	NextAttemptAt int64
	LastError     string
	ExternalID    string
}

func (f *fixture) row(t *testing.T, id string) row {
	t.Helper()
	var r row
	require.NoError(t, f.raw.QueryRowContext(context.Background(),
		"SELECT status, attempts, next_attempt_at, last_error, external_id FROM outbox WHERE id = ?", id).
		Scan(&r.Status, &r.Attempts, &r.NextAttemptAt, &r.LastError, &r.ExternalID))
	return r
}

func (f *fixture) worker(st *store.Store, sender delivery.Sender, now func() time.Time, poll time.Duration) *delivery.OutboxWorker {
	return delivery.NewOutboxWorker(delivery.OutboxOptions{
		Store: st, Bus: f.bus, Sender: sender, Now: now, PollInterval: poll,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// DLV-07: a successful send marks the entry sent with the platform id and announces each
// status change.
func TestOutboxSendSuccess(t *testing.T) {
	f := newFixture(t)
	c := &clock{now: t0}
	sender := &fakeSender{}
	ch, cancel := f.bus.Subscribe(16)
	defer cancel()
	msgID, id := f.queue(t, 1, dto.OutboxStatusPending, t0)

	require.NoError(t, f.worker(f.st, sender, c.Now, 0).Flush(context.Background()))

	r := f.row(t, id)
	require.Equal(t, "sent", r.Status)
	require.Equal(t, 1, r.Attempts)
	require.Equal(t, "ext-"+id, r.ExternalID)
	require.Len(t, sender.calls, 1)
	require.Equal(t, delivery.Delivery{OutboxID: id, MessageID: msgID, RoomID: f.room, ChatID: f.chat, Attempt: 1}, sender.calls[0])

	var last events.OutboxChanged
	statuses := []string{}
	for _, s := range drainEvents(ch) {
		statuses = append(statuses, s.Status)
		last = s
	}
	require.Equal(t, []string{"sending", "sent"}, statuses)
	require.Equal(t, events.OutboxChanged{OutboxID: id, MessageID: msgID, RoomID: f.room, ChatID: f.chat, Status: "sent"}, last)
}

// drainEvents returns the OutboxChanged events received so far.
func drainEvents(ch <-chan events.Event) []events.OutboxChanged {
	var out []events.OutboxChanged
	for {
		select {
		case e := <-ch:
			if oc, ok := e.(events.OutboxChanged); ok {
				out = append(out, oc)
			}
		default:
			return out
		}
	}
}

// DLV-07: failures retry with backoff 2 s, 4 s, 8 s, 16 s; the 5th failure marks it failed.
func TestOutboxRetryBackoffThenFailed(t *testing.T) {
	f := newFixture(t)
	c := &clock{now: t0}
	sender := &fakeSender{fail: 100}
	ch, cancel := f.bus.Subscribe(64)
	defer cancel()
	_, id := f.queue(t, 1, dto.OutboxStatusPending, t0)
	w := f.worker(f.st, sender, c.Now, 0)
	ctx := context.Background()

	now := t0
	for i, wait := range []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second} {
		require.NoError(t, w.Flush(ctx))
		r := f.row(t, id)
		require.Equal(t, "pending", r.Status, "attempt %d", i+1)
		require.Equal(t, i+1, r.Attempts)
		require.Equal(t, now.Add(wait).UnixMilli(), r.NextAttemptAt, "attempt %d", i+1)
		require.Equal(t, "platform unavailable", r.LastError)

		// Not due yet: nothing is sent one millisecond early.
		c.Set(now.Add(wait - time.Millisecond))
		require.NoError(t, w.Flush(ctx))
		require.Equal(t, i+1, sender.count())

		now = now.Add(wait)
		c.Set(now)
	}
	require.NoError(t, w.Flush(ctx))
	r := f.row(t, id)
	require.Equal(t, "failed", r.Status)
	require.Equal(t, 5, r.Attempts)
	require.Equal(t, 5, sender.count())
	for i, d := range sender.calls {
		require.Equal(t, i+1, d.Attempt)
	}

	// A failed entry is never retried.
	c.Set(now.Add(time.Hour))
	require.NoError(t, w.Flush(ctx))
	require.Equal(t, 5, sender.count())
	var statuses []string
	for _, e := range drainEvents(ch) {
		statuses = append(statuses, e.Status)
	}
	require.Equal(t, []string{"sending", "pending", "sending", "pending", "sending", "pending", "sending", "pending", "sending", "failed"}, statuses)
}

// DLV-07 edge case: entries queued (or left mid-send) by a previous process are delivered
// after start.
func TestOutboxResumesAfterRestart(t *testing.T) {
	f := newFixture(t)
	_, pending := f.queue(t, 1, dto.OutboxStatusPending, t0)
	_, interrupted := f.queue(t, 2, dto.OutboxStatusSending, t0)
	_, future := f.queue(t, 3, dto.OutboxStatusPending, t0.Add(time.Hour))
	require.NoError(t, f.st.Close())

	st := f.open(t) // a new process opening the same database
	sender := &fakeSender{}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.worker(st, sender, func() time.Time { return t0 }, time.Hour).Run(ctx) }()

	require.Eventually(t, func() bool {
		return f.row(t, pending).Status == "sent" && f.row(t, interrupted).Status == "sent"
	}, workerDeadline, 10*time.Millisecond)
	require.Equal(t, "pending", f.row(t, future).Status, "not due yet")
	stop()
	require.NoError(t, <-done)
}

// The worker wakes up on new messages instead of waiting for its poll interval.
func TestOutboxWakesOnMessageCreated(t *testing.T) {
	f := newFixture(t)
	sender := &fakeSender{}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	w := f.worker(f.st, sender, func() time.Time { return t0 }, time.Hour)
	go func() { done <- w.Run(ctx) }()
	require.Eventually(t, w.Running, workerDeadline, 5*time.Millisecond)

	msgID, id := f.queue(t, 1, dto.OutboxStatusPending, t0)
	f.bus.Publish(events.MessageCreated{RoomID: f.room, MessageID: msgID, Seq: 1})
	require.Eventually(t, func() bool { return f.row(t, id).Status == "sent" }, workerDeadline, 10*time.Millisecond)
	stop()
	require.NoError(t, <-done)
}

func TestBackoff(t *testing.T) {
	require.Equal(t, 2*time.Second, delivery.Backoff(1))
	require.Equal(t, 4*time.Second, delivery.Backoff(2))
	require.Equal(t, 8*time.Second, delivery.Backoff(3))
	require.Equal(t, 16*time.Second, delivery.Backoff(4))
	require.Equal(t, 5, delivery.MaxAttempts)
}

// ROOM-01 (defense in depth): the worker never sends to a chat that is no longer a member
// of the message's room, even if a pending entry survived the removal.
func TestOutboxSkipsChatsNoLongerMembers(t *testing.T) {
	f := newFixture(t)
	c := &clock{now: t0}
	sender := &fakeSender{}
	_, id := f.queue(t, 1, dto.OutboxStatusPending, t0)
	f.exec(t, "DELETE FROM room_members WHERE room_id = ? AND member_id = ?", f.room, f.chat)

	require.NoError(t, f.worker(f.st, sender, c.Now, 0).Flush(context.Background()))

	require.Zero(t, sender.count(), "a removed chat must not be sent to")
	require.NotEqual(t, "sent", f.row(t, id).Status)
}
