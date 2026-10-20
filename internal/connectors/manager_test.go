package connectors_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/secrets"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// fakeToken is a synthetic secret; status texts must never show it.
const fakeToken = "777:TEST-token-not-real"

// waits records backoff waits instead of sleeping (fake clock). With holdAt > 0 the
// holdAt-th wait blocks until release is closed, so a test can inspect the state the
// manager is in while it backs off.
type waits struct {
	mu      sync.Mutex
	ds      []time.Duration
	ch      chan struct{}
	holdAt  int
	release chan struct{}
}

func newWaits() *waits {
	return &waits{ch: make(chan struct{}, 256), release: make(chan struct{})}
}

func (w *waits) Wait(ctx context.Context, d time.Duration) error {
	w.mu.Lock()
	w.ds = append(w.ds, d)
	hold := w.holdAt > 0 && len(w.ds) == w.holdAt
	w.mu.Unlock()
	w.ch <- struct{}{}
	if hold {
		select {
		case <-w.release:
		case <-ctx.Done():
		}
	}
	return ctx.Err()
}

func (w *waits) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.ds)
}

// waitFor blocks until n waits were recorded and returns them.
func (w *waits) waitFor(t *testing.T, n int) []time.Duration {
	t.Helper()
	for {
		w.mu.Lock()
		if len(w.ds) >= n {
			out := append([]time.Duration(nil), w.ds[:n]...)
			w.mu.Unlock()
			return out
		}
		w.mu.Unlock()
		select {
		case <-w.ch:
		case <-time.After(2 * connectortest.Timeout): // per step; the host may be loaded
			t.Fatalf("only %d of %d backoff waits happened", len(w.ds), n)
		}
	}
}

type managerFixture struct {
	st      *store.Store
	bus     *events.Bus
	box     *secrets.Box
	fake    *connectortest.Fake
	kind    connectors.Kind
	waits   *waits
	m       *connectors.Manager
	owner   string
	statusC <-chan events.Event
}

func newManagerFixture(t *testing.T) *managerFixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "partyline.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(context.Background()))
	box, err := secrets.NewBox(bytes.Repeat([]byte{3}, secrets.KeySize))
	require.NoError(t, err)
	f := &managerFixture{st: st, bus: events.NewBus(), box: box, fake: connectortest.New(), waits: newWaits(), owner: store.NewID()}
	f.kind = f.fake.Kind()
	ch, cancel := f.bus.Subscribe(256)
	t.Cleanup(cancel)
	f.statusC = ch
	require.NoError(t, st.WithTx(context.Background(), func(q *db.Queries) error {
		return q.CreateUser(context.Background(), db.CreateUserParams{ID: f.owner, Username: "alice", PasswordHash: "x", Role: "member"})
	}))
	f.m = connectors.NewManager(connectors.ManagerDeps{
		Store: st, Bus: f.bus, Box: box, Kinds: connectors.NewRegistry(f.kind),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Wait: f.waits.Wait,
	})
	t.Cleanup(f.m.Close)
	return f
}

// insert stores a fake connector with the synthetic token.
func (f *managerFixture) insert(t *testing.T, enabled bool) string {
	t.Helper()
	id := store.NewID()
	public, sealed, err := f.kind.Encode(f.box, connectors.Config{"token": fakeToken})
	require.NoError(t, err)
	require.NoError(t, f.st.WithTx(context.Background(), func(q *db.Queries) error {
		return q.CreateConnector(context.Background(), db.CreateConnectorParams{
			ID: id, OwnerID: f.owner, Kind: connectortest.KindName, Name: "fake", ConfigJson: public, SecretsEnc: sealed, Enabled: enabled,
		})
	}))
	return id
}

func (f *managerFixture) row(t *testing.T, id string) db.Connector {
	t.Helper()
	c, err := f.st.R().GetConnector(context.Background(), id)
	require.NoError(t, err)
	return c
}

// nextStatus returns the next ConnectorStatus event for id.
func (f *managerFixture) nextStatus(t *testing.T, id string) events.ConnectorStatus {
	t.Helper()
	deadline := time.After(connectortest.Timeout)
	for {
		select {
		case e := <-f.statusC:
			if s, ok := e.(events.ConnectorStatus); ok && s.ConnectorID == id {
				return s
			}
		case <-deadline:
			t.Fatalf("no status event for %s", id)
		}
	}
}

// CON-04: Start runs enabled connectors only; status changes are persisted and published.
func TestManagerStartsEnabledConnectorsAndReportsStatus(t *testing.T) {
	f := newManagerFixture(t)
	on := f.insert(t, true)
	off := f.insert(t, false)
	f.m.Start(context.Background())

	inst := f.fake.Instance(t, on)
	inst.WaitRun(t)
	require.Equal(t, fakeToken, inst.Config.String("token"), "the connector gets the decrypted config")
	require.Equal(t, events.ConnectorStatus{ConnectorID: on, Status: "connecting"}, f.nextStatus(t, on))
	inst.Connected()
	require.Equal(t, events.ConnectorStatus{ConnectorID: on, Status: "connected"}, f.nextStatus(t, on))
	require.Equal(t, "connected", f.row(t, on).Status)
	require.Equal(t, "stopped", f.row(t, off).Status, "disabled connectors do not run")
}

// CON-04, CON-05: Apply starts a connector within 2 s; Stop ends it and its sends.
func TestManagerApplyAndStop(t *testing.T) {
	f := newManagerFixture(t)
	f.m.Start(context.Background())
	id := f.insert(t, true)

	// Started at once (well within 2 s): the instance exists when Apply returns and the
	// first Run comes before any backoff wait. Wall time is not asserted (loaded hosts).
	f.m.Apply(id)
	inst := f.fake.Lookup(id)
	require.NotNil(t, inst, "Apply creates the connector synchronously")
	inst.WaitRun(t)
	require.Zero(t, f.waits.count(), "no backoff before the first run")

	ext, err := f.m.Send(context.Background(), id, connectors.Outbound{ChatID: "chat-1", Text: "hello"})
	require.NoError(t, err)
	require.Equal(t, "ext-1", ext)

	f.m.Stop(id)
	inst.WaitStop(t)
	require.Equal(t, "stopped", f.row(t, id).Status)
	_, err = f.m.Send(context.Background(), id, connectors.Outbound{ChatID: "chat-1", Text: "again"})
	require.ErrorIs(t, err, connectors.ErrNotRunning)
	require.Len(t, inst.Sent(), 1, "nothing is sent after stop")
}

// CON-06: a failing transport shows a redacted error and retries with growing backoff.
func TestManagerRedactsErrorsAndRetriesWithBackoff(t *testing.T) {
	f := newManagerFixture(t)
	f.m.Start(context.Background())
	id := f.insert(t, true)
	f.m.Apply(id)
	inst := f.fake.Instance(t, id)
	inst.WaitRun(t)
	// Hold the 8th backoff so the persisted status is read while the manager backs off,
	// not after it already restarted the connector ("connecting").
	f.waits.mu.Lock()
	f.waits.holdAt = 8
	f.waits.mu.Unlock()
	t.Cleanup(func() { close(f.waits.release) })

	for range 8 {
		inst.Fail(errors.New(`Get "https://api.example.test/bot` + fakeToken + `/getUpdates": unauthorized`))
	}
	got := f.waits.waitFor(t, 8)
	require.Equal(t, []time.Duration{
		2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second,
		32 * time.Second, 64 * time.Second, 120 * time.Second, 120 * time.Second,
	}, got)

	row := f.row(t, id)
	require.Equal(t, "error", row.Status)
	require.NotContains(t, row.LastError, fakeToken)
	require.Equal(t, `Get "https://api.example.test/bot[redacted]/getUpdates": unauthorized`, row.LastError)
	for {
		s := f.nextStatus(t, id)
		require.NotContains(t, s.Detail, fakeToken)
		if s.Status == "error" {
			require.Contains(t, s.Detail, "[redacted]")
			break
		}
	}
}

// CON-06: a successful connection resets the backoff.
func TestManagerResetsBackoffAfterConnecting(t *testing.T) {
	f := newManagerFixture(t)
	f.m.Start(context.Background())
	id := f.insert(t, true)
	f.m.Apply(id)
	inst := f.fake.Instance(t, id)
	inst.WaitRun(t)

	inst.Fail(errors.New("down"))
	inst.WaitRun(t)
	inst.Fail(errors.New("down"))
	inst.WaitRun(t)
	inst.Connected()
	inst.Fail(errors.New("down"))
	require.Equal(t, []time.Duration{2 * time.Second, 4 * time.Second, 2 * time.Second}, f.waits.waitFor(t, 3))
}

// Send errors are redacted before they reach the outbox (which stores them).
func TestManagerRedactsSendErrors(t *testing.T) {
	f := newManagerFixture(t)
	f.m.Start(context.Background())
	id := f.insert(t, true)
	f.m.Apply(id)
	inst := f.fake.Instance(t, id)
	inst.WaitRun(t)
	inst.SetSendError(errors.New("post /bot" + fakeToken + "/sendMessage: 400"))
	_, err := f.m.Send(context.Background(), id, connectors.Outbound{ChatID: "c", Text: "x"})
	require.EqualError(t, err, "post /bot[redacted]/sendMessage: 400")
}

func TestRedactReplacesEncodedForms(t *testing.T) {
	secret := "a b/c+d"
	got := connectors.Redact("raw=a b/c+d query=a+b%2Fc%2Bd path=a%20b%2Fc+d", []string{secret, ""})
	require.Equal(t, "raw=[redacted] query=[redacted] path=[redacted]", got)
}
