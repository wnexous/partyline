package connectors_test

import (
	"bytes"
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

	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/handles"
	"github.com/wnexous/partyline/internal/intelligences"
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
	// path is the database file, for tests that count rows with a connection of their own.
	path    string
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

// newManagerFixture builds a manager over a migrated store and the fake kind; opts change its deps.
func newManagerFixture(t *testing.T, opts ...func(*connectors.ManagerDeps)) *managerFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "partyline.db")
	st, err := store.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(context.Background()))
	box, err := secrets.NewBox(bytes.Repeat([]byte{3}, secrets.KeySize))
	require.NoError(t, err)
	f := &managerFixture{path: path, st: st, bus: events.NewBus(), box: box, fake: connectortest.New(), waits: newWaits(), owner: store.NewID()}
	f.kind = f.fake.Kind()
	ch, cancel := f.bus.Subscribe(256)
	t.Cleanup(cancel)
	f.statusC = ch
	require.NoError(t, st.WithTx(context.Background(), func(q *db.Queries) error {
		return q.CreateUser(context.Background(), db.CreateUserParams{ID: f.owner, Username: "alice", PasswordHash: "x", Role: "member"})
	}))
	deps := connectors.ManagerDeps{
		Store: st, Bus: f.bus, Box: box, Kinds: connectors.NewRegistry(f.kind),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Wait: f.waits.Wait,
	}
	for _, opt := range opts {
		opt(&deps)
	}
	f.m = connectors.NewManager(deps)
	t.Cleanup(f.m.Close)
	return f
}

// ai creates an AI of the owner and returns its id.
func (f *managerFixture) ai(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	id := store.NewID()
	require.NoError(t, f.st.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateAgent(ctx, db.CreateAgentParams{ID: id, OwnerID: f.owner, Name: id}); err != nil {
			return err
		}
		_, err := intelligences.Create(ctx, q, intelligences.NewParams{ID: id, Kind: handles.AI, DisplayName: "Atlas", HandleBase: "atlas", Now: time.Now()})
		return err
	}))
	return id
}

// setOperator stores the AI (NULL for "") as the connector's operator.
func (f *managerFixture) setOperator(t *testing.T, id, ai string) {
	t.Helper()
	require.NoError(t, f.st.WithTx(context.Background(), func(q *db.Queries) error {
		return q.SetConnectorOperator(context.Background(), db.SetConnectorOperatorParams{OperatorIid: sql.NullString{String: ai, Valid: ai != ""}, ID: id})
	}))
}

// insert stores a fake connector with the synthetic token, operated by a new AI of the owner (a
// connection nobody operates does not run, CID-02).
func (f *managerFixture) insert(t *testing.T, enabled bool) string {
	t.Helper()
	id := f.insertWithoutOperator(t, enabled)
	f.setOperator(t, id, f.ai(t))
	return id
}

// insertWithoutOperator stores a fake connector that nobody operates.
func (f *managerFixture) insertWithoutOperator(t *testing.T, enabled bool) string {
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

// Send errors are redacted before they reach the record of the send (which stores them).
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

// CID-02: Start and Apply do not run a connection nobody operates: it reports the error status
// that says what to do, persisted and published, and receives nothing; operated and disabled
// connections are unaffected.
func TestManagerDoesNotRunAConnectionWithoutAnOperator(t *testing.T) {
	f := newManagerFixture(t)
	idle := f.insertWithoutOperator(t, true)
	operated := f.insert(t, true)
	disabled := f.insertWithoutOperator(t, false)
	f.m.Start(context.Background())

	require.Equal(t, events.ConnectorStatus{ConnectorID: idle, Status: "error", Detail: "choose the AI that operates this connection"}, f.nextStatus(t, idle))
	row := f.row(t, idle)
	require.Equal(t, []string{"error", "choose the AI that operates this connection"}, []string{row.Status, row.LastError})
	require.Nil(t, f.fake.Lookup(idle), "no connector instance is created")
	_, err := f.m.Send(context.Background(), idle, connectors.Outbound{ChatID: "chat-1", Text: "x"})
	require.ErrorIs(t, err, connectors.ErrNotRunning)

	f.fake.Instance(t, operated).WaitRun(t)
	require.Empty(t, f.row(t, operated).LastError)
	require.Equal(t, []string{"stopped", ""}, []string{f.row(t, disabled).Status, f.row(t, disabled).LastError}, "a disabled connection is stopped, not idle")
	require.Nil(t, f.fake.Lookup(disabled))
}

// CID-02: setting the operator and applying the connection runs it, and the detail goes away.
func TestManagerRunsAConnectionOnceItHasAnOperator(t *testing.T) {
	f := newManagerFixture(t)
	f.m.Start(context.Background())
	id := f.insertWithoutOperator(t, true)

	f.m.Apply(id)
	require.Equal(t, "error", f.nextStatus(t, id).Status)
	require.Nil(t, f.fake.Lookup(id))

	f.setOperator(t, id, f.ai(t))
	f.m.Apply(id)
	f.fake.Instance(t, id).WaitRun(t)
	require.Equal(t, events.ConnectorStatus{ConnectorID: id, Status: "connecting"}, f.nextStatus(t, id), "the detail is cleared")
	row := f.row(t, id)
	require.Equal(t, []string{"connecting", ""}, []string{row.Status, row.LastError})
}

// CID-02: applying a running connection whose operator is gone (deleted AI) stops it and shows
// the same status.
func TestManagerStopsAConnectionThatLostItsOperator(t *testing.T) {
	f := newManagerFixture(t)
	f.m.Start(context.Background())
	id := f.insert(t, true)
	f.m.Apply(id)
	inst := f.fake.Instance(t, id)
	inst.WaitRun(t)

	f.setOperator(t, id, "") // what deleting the operator does
	f.m.Apply(id)

	inst.WaitStop(t)
	row := f.row(t, id)
	require.Equal(t, []string{"error", "choose the AI that operates this connection"}, []string{row.Status, row.LastError})
	_, err := f.m.Send(context.Background(), id, connectors.Outbound{ChatID: "chat-1", Text: "x"})
	require.ErrorIs(t, err, connectors.ErrNotRunning)
}

// operatorGone is the state deleting the operator AI leaves: the connection has no operator and
// nothing told the manager yet.
func (f *managerFixture) operatorGone(t *testing.T, id string) {
	t.Helper()
	f.setOperator(t, id, "")
}

// settled waits until the connection shows the status and detail.
func (f *managerFixture) settled(t *testing.T, id, status, detail string) {
	t.Helper()
	require.Eventually(t, func() bool {
		row := f.row(t, id)
		return row.Status == status && row.LastError == detail
	}, 2*connectortest.Timeout, 10*time.Millisecond, "the connection never reached %s %q: %s %q", status, detail, f.row(t, id).Status, f.row(t, id).LastError)
}

// CID-24, CID-02: when the owner deletes the operator AI, the registry tells the bus which
// connections lost their operator; the manager applies them again, so each stops and shows what to
// do, and nothing else is touched: a connection that still has its operator keeps its session.
func TestManagerStopsTheConnectionsNamedByAnOrphanedEvent(t *testing.T) {
	f := newManagerFixture(t)
	f.m.Start(context.Background())
	orphan, other, again := f.insert(t, true), f.insert(t, true), f.insert(t, true)
	for _, id := range []string{orphan, other, again} {
		f.m.Apply(id)
	}
	orphanInst, otherInst, againInst := f.fake.Instance(t, orphan), f.fake.Instance(t, other), f.fake.Instance(t, again)
	for _, inst := range []*connectortest.Instance{orphanInst, otherInst, againInst} {
		inst.WaitRun(t)
		inst.Connected()
	}
	f.operatorGone(t, orphan)
	f.operatorGone(t, again)

	f.bus.Publish(events.ConnectorsOrphaned{IDs: []string{orphan, again}})

	orphanInst.WaitStop(t)
	againInst.WaitStop(t)
	f.settled(t, orphan, "error", connectors.NoOperator)
	f.settled(t, again, "error", connectors.NoOperator)
	require.Equal(t, []string{"connected", ""}, []string{f.row(t, other).Status, f.row(t, other).LastError}, "a connection with its operator is not touched")
	require.Same(t, otherInst, f.fake.Lookup(other), "and keeps its session with the platform")
	_, err := f.m.Send(context.Background(), orphan, connectors.Outbound{ChatID: "chat-1", Text: "x"})
	require.ErrorIs(t, err, connectors.ErrNotRunning)
}

// CID-02: the owner may choose another operator right after the deletion; the connection then runs
// again with no stale detail, whichever of the two the manager handles first.
func TestManagerRunsAnOrphanedConnectionThatGotANewOperatorMeanwhile(t *testing.T) {
	f := newManagerFixture(t)
	f.m.Start(context.Background())
	id := f.insert(t, true)
	f.m.Apply(id)
	inst := f.fake.Instance(t, id)
	inst.WaitRun(t)
	f.operatorGone(t, id)
	f.setOperator(t, id, f.ai(t)) // chosen before the manager reads the event

	f.bus.Publish(events.ConnectorsOrphaned{IDs: []string{id}})

	inst.WaitStop(t) // applied: the old session is replaced by a new one
	require.Eventually(t, func() bool { return f.fake.Lookup(id) != inst }, 2*connectortest.Timeout, 10*time.Millisecond, "no new session was started")
	f.fake.Lookup(id).WaitRun(t)
	require.Empty(t, f.row(t, id).LastError)
	require.Equal(t, "connecting", f.row(t, id).Status)
}

// A bus that lagged may have dropped an orphaned event: the manager looks at what it runs and
// applies the connections whose operator is gone, and only those.
func TestManagerReconcilesAfterALaggedBus(t *testing.T) {
	f := newManagerFixture(t)
	f.m.Start(context.Background())
	orphan, kept := f.insert(t, true), f.insert(t, true)
	f.m.Apply(orphan)
	f.m.Apply(kept)
	orphanInst, keptInst := f.fake.Instance(t, orphan), f.fake.Instance(t, kept)
	orphanInst.WaitRun(t)
	keptInst.WaitRun(t)
	f.operatorGone(t, orphan)

	f.bus.Publish(events.Lagged{})

	orphanInst.WaitStop(t)
	f.settled(t, orphan, "error", connectors.NoOperator)
	require.Same(t, keptInst, f.fake.Lookup(kept), "a connection that has its operator is not restarted")
}

// A closed manager applies nothing: an orphaned event published afterwards changes nothing.
func TestManagerAppliesNothingAfterClose(t *testing.T) {
	f := newManagerFixture(t)
	f.m.Start(context.Background())
	id := f.insert(t, true)
	f.m.Apply(id)
	inst := f.fake.Instance(t, id)
	inst.WaitRun(t)
	inst.Connected()

	f.m.Close()
	f.operatorGone(t, id)
	f.bus.Publish(events.ConnectorsOrphaned{IDs: []string{id}})

	time.Sleep(200 * time.Millisecond)
	require.NotEqual(t, connectors.NoOperator, f.row(t, id).LastError, "a closed manager applies nothing")
}
