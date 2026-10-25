package webpush_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
	"github.com/wnexous/partyline/internal/webpush"
)

// fakeSender answers every request with one status and one error, without contacting anything.
type fakeSender struct {
	status int
	err    error
	// payloads records the payload of every call, in order.
	payloads [][]byte
}

func (f *fakeSender) Send(_ context.Context, _ webpush.Subscription, payload []byte) (int, error) {
	f.payloads = append(f.payloads, payload)
	return f.status, f.err
}

// logBuffer collects the log lines of a test's logger.
type logBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// testLogger returns a logger writing to sink; nil discards the lines.
func testLogger(sink io.Writer) *slog.Logger {
	if sink == nil {
		sink = io.Discard
	}
	return slog.New(slog.NewTextHandler(sink, nil))
}

// asSubscription renders a stored row as the notifier hands it to the deliverer.
func asSubscription(row db.PushSubscription) webpush.Subscription {
	return webpush.Subscription{ID: row.ID, Endpoint: row.Endpoint, P256dh: row.P256dh, Auth: row.Auth}
}

// subscriptionsOf lists the stored push subscriptions of the user.
func subscriptionsOf(t *testing.T, env *testkit.Env, userID string) []db.PushSubscription {
	t.Helper()
	rows, err := env.App.Store.R().ListPushSubscriptionsByUser(context.Background(), userID)
	require.NoError(t, err)
	return rows
}

// newDeliverer builds the real deliverer over the app's store with a fake sender and a fixed clock.
func newDeliverer(env *testkit.Env, sender webpush.Sender, now func() time.Time, sink io.Writer) *webpush.Deliverer {
	return webpush.NewDeliverer(webpush.DelivererDeps{
		Store: env.App.Store, Sender: sender, Now: now, Logger: testLogger(sink),
	})
}

// PUSH-12: a 2xx from the push service records when the device last answered, and keeps the row.
func TestDelivererMarksLastOKOn2xx(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNoContent} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			env := testkit.New(t, testkit.WithoutPush())
			bob := account(t, env, "bob", "Bob")
			row := subscribe(t, env, bob.User.ID, "https://push.example.com/send/bob")
			okAt := time.UnixMilli(1_700_000_123_456)
			sender := &fakeSender{status: status}
			d := newDeliverer(env, sender, func() time.Time { return okAt }, nil)

			d.Deliver(context.Background(), asSubscription(row), []byte(`{"v":1,"tag":"test"}`))

			require.Len(t, sender.payloads, 1)
			rows := subscriptionsOf(t, env, bob.User.ID)
			require.Len(t, rows, 1, "a delivered notification keeps its subscription")
			require.Equal(t, sql.NullInt64{Int64: okAt.UnixMilli(), Valid: true}, rows[0].LastOkAt,
				"the response time is written in milliseconds")
		})
	}
}

// PUSH-12: 404 and 410 mean the browser's subscription is gone for good, so the row is deleted. A
// deleted row has no last_ok_at to write.
func TestDelivererDeletesOn404And410(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusGone} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			env := testkit.New(t, testkit.WithoutPush())
			bob := account(t, env, "bob", "Bob")
			row := subscribe(t, env, bob.User.ID, "https://push.example.com/send/bob")
			d := newDeliverer(env, &fakeSender{status: status}, time.Now, nil)

			d.Deliver(context.Background(), asSubscription(row), []byte(`{"v":1,"tag":"test"}`))

			require.Empty(t, subscriptionsOf(t, env, bob.User.ID), "the expired subscription is deleted")
		})
	}
}

// PUSH-12: every other outcome keeps the subscription and records no delivery time.
func TestDelivererKeepsOnOtherFailures(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		err     error
		wantLog string
	}{
		{name: "rate limited", status: http.StatusTooManyRequests, wantLog: "429"},
		{name: "server error", status: http.StatusInternalServerError, wantLog: "500"},
		{
			name: "network",
			err: &url.Error{
				Op:  "Post",
				URL: "https://push.example.com/send/bob",
				Err: errors.New("dial tcp: connection refused"),
			},
			wantLog: "network",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := testkit.New(t, testkit.WithoutPush())
			bob := account(t, env, "bob", "Bob")
			row := subscribe(t, env, bob.User.ID, "https://push.example.com/send/bob")
			var logs logBuffer
			d := newDeliverer(env, &fakeSender{status: tc.status, err: tc.err}, time.Now, &logs)

			d.Deliver(context.Background(), asSubscription(row), []byte(`{"v":1,"tag":"test"}`))

			rows := subscriptionsOf(t, env, bob.User.ID)
			require.Len(t, rows, 1, "a failure keeps the subscription")
			require.False(t, rows[0].LastOkAt.Valid, "only a 2xx records a delivery")
			require.Contains(t, logs.String(), tc.wantLog)
			require.NotContains(t, logs.String(), "https://", "a log never carries the endpoint")
		})
	}
}

// PUSH-12, PUSH-22: the log line of a failed send carries the endpoint's host and the status, and
// neither the endpoint's path (the capability token) nor the payload.
func TestDelivererLogsOnlyStatusAndHost(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	bob := account(t, env, "bob", "Bob")
	row := subscribe(t, env, bob.User.ID, "https://push.example.test/send/very-secret-token")
	var logs logBuffer
	d := newDeliverer(env, &fakeSender{status: http.StatusGone}, time.Now, &logs)

	d.Deliver(context.Background(), asSubscription(row), []byte("a secret message"))

	out := logs.String()
	require.Contains(t, out, "push.example.test", "the log names the endpoint host")
	require.Contains(t, out, "410", "the log records the status")
	require.NotContains(t, out, "very-secret-token", "the endpoint path is a capability and never goes to a log")
	require.NotContains(t, out, "a secret message", "the payload never goes to a log")
	require.NotContains(t, out, "https://")
	require.Empty(t, subscriptionsOf(t, env, bob.User.ID), "the 410 deleted the row")
}

// PUSH-14, PUSH-27: app.Start runs the app's notifier, so a stored subscription receives the job;
// DisablePush builds it but leaves it stopped, with the queue size Start would have used.
func TestAppStartsThePushNotifier(t *testing.T) {
	// No subscription: a message has nowhere to go and no send is attempted.
	var idle logBuffer
	quiet := testkit.New(t, testkit.WithLogger(testLogger(&idle)))
	require.NotNil(t, quiet.App.Push)
	alice := account(t, quiet, "alice", "Alice")
	bob := account(t, quiet, "bob", "Bob")
	dm := openDM(t, alice, bob.User.ID)
	postMessage(t, alice, dm.ID, "no device to notify")
	require.Never(t, func() bool { return strings.Contains(idle.String(), "webpush") },
		300*time.Millisecond, 10*time.Millisecond, "a message with no subscription sends nothing")
	require.Empty(t, subscriptionsOf(t, quiet, bob.User.ID))

	// A subscription: Start's notifier hands the job to the deliverer, which resolves it and fails
	// before any network I/O because the row's endpoint is not a URL.
	var running logBuffer
	env := testkit.New(t, testkit.WithLogger(testLogger(&running)))
	require.NotNil(t, env.App.Push)
	alice = account(t, env, "alice", "Alice")
	bob = account(t, env, "bob", "Bob")
	dm = openDM(t, alice, bob.User.ID)
	subscribe(t, env, bob.User.ID, "not a push endpoint")
	postMessage(t, alice, dm.ID, "hello")
	require.Eventually(t, func() bool { return strings.Contains(running.String(), "endpoint_host=invalid") },
		notifierWait, 10*time.Millisecond, "the running notifier delivered the job")

	// WithoutPush: constructed, not running, so a post delivers nothing.
	var stopped logBuffer
	off := testkit.New(t, testkit.WithoutPush(), testkit.WithPushQueueSize(1),
		testkit.WithLogger(testLogger(&stopped)))
	require.NotNil(t, off.App.Push)
	alice = account(t, off, "alice", "Alice")
	bob = account(t, off, "bob", "Bob")
	dm = openDM(t, alice, bob.User.ID)
	row := subscribe(t, off, bob.User.ID, "not a push endpoint")
	postMessage(t, alice, dm.ID, "nobody delivers this")
	require.Never(t, func() bool { return strings.Contains(stopped.String(), "webpush") },
		500*time.Millisecond, 10*time.Millisecond, "a disabled notifier sends nothing")
	rows := subscriptionsOf(t, off, bob.User.ID)
	require.Len(t, rows, 1)
	require.False(t, rows[0].LastOkAt.Valid)

	// The queue size reached the notifier: one job fits and, since nothing consumes it, the next
	// does not (PUSH-27).
	require.Equal(t, 1, off.App.Push.EnqueueTest(context.Background(), []db.PushSubscription{row}))
	require.Zero(t, off.App.Push.EnqueueTest(context.Background(), []db.PushSubscription{row}))
}
