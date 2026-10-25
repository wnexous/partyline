package connectors_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/events"
)

// leak is text that stands for a message: it must never reach a log or a status, even when a panic
// carries it.
const leak = "this is the text of a message and must never be logged"

// logBuffer collects log output a test can read while the manager still writes to it.
type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
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

// withLogs makes the manager log into b.
func withLogs(b *logBuffer) func(*connectors.ManagerDeps) {
	return func(d *connectors.ManagerDeps) { d.Logger = slog.New(slog.NewTextHandler(b, nil)) }
}

// testClock is a clock a test moves by hand.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock { return &testClock{t: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)} }

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// withClock gives the manager c as its clock.
func withClock(c *testClock) func(*connectors.ManagerDeps) {
	return func(d *connectors.ManagerDeps) { d.Now = c.Now }
}

// nextError returns the next error status event of the connector: the persisted row is already
// "connecting" again when the backoff is not held, so a test reads what was published.
func (f *managerFixture) nextError(t *testing.T, id string) events.ConnectorStatus {
	t.Helper()
	for {
		if st := f.nextStatus(t, id); st.Status == "error" {
			return st
		}
	}
}

// crash makes the connector's Run panic and waits for the supervisor to start the next one.
func crash(t *testing.T, inst *connectortest.Instance) {
	t.Helper()
	inst.Panic(leak)
	inst.WaitRun(t)
}

// CON-15: a connector whose Run panics does not end the process. The panic becomes the connector's
// error without any text of it, is logged without that text, and the connector starts again after a
// backoff that keeps growing even when it connects in between: a connector that connects and panics
// in a loop must not reconnect every two seconds.
func TestManagerSurvivesAPanickingRun(t *testing.T) {
	logs := &logBuffer{}
	f := newManagerFixture(t, withLogs(logs))
	f.m.Start(context.Background())
	id := f.insert(t, true)
	f.m.Apply(id)
	inst := f.fake.Instance(t, id)
	inst.WaitRun(t)
	// Hold the third backoff so the persisted status is read while the manager waits.
	f.waits.mu.Lock()
	f.waits.holdAt = 3
	f.waits.mu.Unlock()
	t.Cleanup(func() { close(f.waits.release) })

	crash(t, inst)
	inst.Connected()
	crash(t, inst) // it had connected, and crashed soon after: not a fresh start
	inst.Panic(leak)

	require.Equal(t, []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}, f.waits.waitFor(t, 3))
	row := f.row(t, id)
	require.Equal(t, []string{"error", "the connector crashed in run: panic of type string"}, []string{row.Status, row.LastError})
	require.Equal(t, 3, strings.Count(logs.String(), "a connector panicked"), "every panic is logged")
	require.Contains(t, logs.String(), "stack=", "with the stack that says where it came from")
	require.NotContains(t, logs.String(), leak)
}

// CON-15: a panic of the fake's Run is a failure like any other in the status events: error, then
// connecting again once the backoff is over.
func TestManagerReportsAPanicAsAnErrorStatusAndRestarts(t *testing.T) {
	f := newManagerFixture(t)
	f.m.Start(context.Background())
	id := f.insert(t, true)
	f.m.Apply(id)
	inst := f.fake.Instance(t, id)
	inst.WaitRun(t)
	require.Equal(t, "connecting", f.nextStatus(t, id).Status)

	inst.Panic(errors.New(leak))
	st := f.nextStatus(t, id)
	require.Equal(t, "error", st.Status)
	require.Equal(t, "the connector crashed in run: panic of type *errors.errorString", st.Detail)
	inst.WaitRun(t)
	require.Equal(t, "connecting", f.nextStatus(t, id).Status)
	require.Empty(t, f.row(t, id).LastError)
}

// CON-15: a panic in what a connector calls in the server (here the inbound pipeline, met from a
// goroutine of the connector) is recovered as well. The connector's call returns as if nothing had
// happened; its run ends, shows the error and starts again after a backoff. Nothing of the message
// is logged.
func TestManagerRestartsAConnectorWhoseHandlerCallPanics(t *testing.T) {
	logs := &logBuffer{}
	f := newManagerFixture(t, withLogs(logs), func(d *connectors.ManagerDeps) {
		d.Inbound = func(_ context.Context, _ connectors.Source, m connectors.Inbound) { panic("pipeline bug on " + m.Text) }
	})
	f.m.Start(context.Background())
	id := f.insert(t, true)
	f.m.Apply(id)
	inst := f.fake.Instance(t, id)
	inst.WaitRun(t)

	inst.Emit(context.Background(), connectors.Inbound{ChatID: "chat-1", MessageID: "m1", Text: leak}) // returns normally

	inst.WaitStop(t)
	require.Equal(t, "the connector crashed in inbound: panic of type string", f.nextError(t, id).Detail)
	require.Equal(t, []time.Duration{2 * time.Second}, f.waits.waitFor(t, 1))
	inst.WaitRun(t) // and it starts again
	require.NotContains(t, logs.String(), leak)
	require.Contains(t, logs.String(), "a connector panicked")
}

// CON-15: a chat migration that panics in the server is recovered the same way.
func TestManagerRestartsAConnectorWhoseChatMigrationPanics(t *testing.T) {
	f := newManagerFixture(t, func(d *connectors.ManagerDeps) {
		d.MigrateChat = func(context.Context, connectors.Source, string, string) { panic("migration bug") }
	})
	f.m.Start(context.Background())
	id := f.insert(t, true)
	f.m.Apply(id)
	inst := f.fake.Instance(t, id)
	inst.WaitRun(t)

	inst.MigrateChat(context.Background(), "-100", "-1001")

	inst.WaitStop(t)
	require.Equal(t, "the connector crashed in migrate chat: panic of type string", f.nextError(t, id).Detail)
	inst.WaitRun(t)
}

// CON-15: a panic of a send, a reaction or a chat listing fails that call alone: the connector keeps
// its connection (no stop, no backoff), the error names where it panicked without the panic's text,
// and the next call works.
func TestManagerFailsOnlyTheCallWhoseConnectorPanicked(t *testing.T) {
	logs := &logBuffer{}
	f := newManagerFixture(t, withLogs(logs))
	f.m.Start(context.Background())
	id := f.insert(t, true)
	f.m.Apply(id)
	inst := f.fake.Instance(t, id)
	inst.WaitRun(t)
	inst.Connected()
	ctx := context.Background()

	inst.SetSendPanic(leak)
	_, err := f.m.Send(ctx, id, connectors.Outbound{ChatID: "chat-1", Text: leak})
	require.EqualError(t, err, "the connector crashed in send: panic of type string")
	inst.SetSendPanic(nil)

	inst.SetReactPanic(errors.New(leak))
	err = f.m.React(ctx, id, connectors.Reaction{ChatID: "chat-1", TargetID: "m1", Emoji: "👍"})
	require.EqualError(t, err, "the connector crashed in react: panic of type *errors.errorString")
	inst.SetReactPanic(nil)

	inst.SetListPanic(leak)
	_, err = f.m.ListChats(ctx, id)
	require.ErrorIs(t, err, connectors.ErrPanic)
	require.NotErrorIs(t, err, connectors.ErrPlatform, "a bug of the connector is not a failure of the platform")
	inst.SetListPanic(nil)

	require.Equal(t, 3, strings.Count(logs.String(), "a connector panicked"))
	require.NotContains(t, logs.String(), leak)
	require.Zero(t, f.waits.count(), "the connector was not restarted")
	require.Equal(t, "connected", f.row(t, id).Status)
	require.Same(t, inst, f.fake.Lookup(id))

	_, err = f.m.Send(ctx, id, connectors.Outbound{ChatID: "chat-1", Text: "hello"})
	require.NoError(t, err, "the connector still sends")
	require.NoError(t, f.m.React(ctx, id, connectors.Reaction{ChatID: "chat-1", TargetID: "m1", Emoji: "👍"}))
	_, err = f.m.ListChats(ctx, id)
	require.NoError(t, err)
}

// CON-15: a kind whose constructor panics leaves the connection in error with that detail; the other
// connections start as usual and nothing ends the process.
func TestManagerReportsAKindThatPanicsWhenItStarts(t *testing.T) {
	f := newManagerFixture(t)
	bad := f.kind
	bad.New = func(connectors.Config, connectors.Deps) (connectors.Connector, error) { panic(leak) }
	m := connectors.NewManager(connectors.ManagerDeps{
		Store: f.st, Bus: f.bus, Box: f.box, Kinds: connectors.NewRegistry(bad), Wait: f.waits.Wait,
		Logger: slog.New(slog.NewTextHandler(&logBuffer{}, nil)),
	})
	t.Cleanup(m.Close)
	id := f.insert(t, true)

	m.Start(context.Background())

	f.settled(t, id, "error", "the connector crashed in start: panic of type string")
	require.Nil(t, f.fake.Lookup(id), "no connector was made")
	_, err := m.Send(context.Background(), id, connectors.Outbound{ChatID: "chat-1", Text: "x"})
	require.ErrorIs(t, err, connectors.ErrNotRunning)
}

// CON-15: reading a kind's secret values is a call into connector code like any other. A panic
// there leaves that one connection in error, with the type of the panic as the detail and nothing of
// its value in the status or the logs, and the other connections start as usual.
func TestManagerReportsAKindThatPanicsWhenItReadsItsSecrets(t *testing.T) {
	logs := &logBuffer{}
	f := newManagerFixture(t)
	m := connectors.NewManager(connectors.ManagerDeps{
		Store: f.st, Bus: f.bus, Box: f.box, Kinds: connectors.NewRegistry(f.kind), Wait: f.waits.Wait,
		Logger: slog.New(slog.NewTextHandler(logs, nil)),
	})
	t.Cleanup(m.Close)
	connectors.SetSecretValues(m, func(_ connectors.Kind, cfg connectors.Config) []string {
		if cfg.String("label") == "boom" {
			panic("secret values bug on " + fakeToken)
		}
		return []string{cfg.String("token")}
	})
	bad := f.insertWithConfig(t, connectors.Config{"token": fakeToken, "label": "boom"})
	good := f.insertWithConfig(t, connectors.Config{"token": fakeToken, "label": "fine"})

	m.Start(context.Background())

	f.settled(t, bad, "error", "the connector crashed in secrets: panic of type string")
	require.Nil(t, f.fake.Lookup(bad), "a connector whose secrets were not read does not start")
	f.fake.Instance(t, good).WaitRun(t)
	require.Empty(t, f.row(t, good).LastError, "the other connection starts as usual")
	require.Contains(t, logs.String(), "a connector panicked")
	require.NotContains(t, logs.String(), fakeToken, "the panic value, which carries a secret, is not logged")
	_, err := m.Send(context.Background(), bad, connectors.Outbound{ChatID: "chat-1", Text: "x"})
	require.ErrorIs(t, err, connectors.ErrNotRunning)
}

// hookConnector is a connector that receives HTTP requests and panics on them.
type hookConnector struct{}

func (hookConnector) Run(ctx context.Context, _ connectors.Handler) error {
	<-ctx.Done()
	return ctx.Err()
}

func (hookConnector) Send(context.Context, connectors.Outbound) (string, error) { return "", nil }

func (hookConnector) ReceiveHook(_ context.Context, r connectors.HookRequest) error {
	panic("hook bug on " + string(r.Body))
}

// CON-15: a connector that panics while it receives an HTTP request fails that request alone: the
// error says where, without the panic's text, and the connector keeps running.
func TestManagerFailsOnlyTheHookRequestWhoseConnectorPanicked(t *testing.T) {
	logs := &logBuffer{}
	f := newManagerFixture(t)
	kind := f.kind
	kind.New = func(connectors.Config, connectors.Deps) (connectors.Connector, error) { return hookConnector{}, nil }
	m := connectors.NewManager(connectors.ManagerDeps{
		Store: f.st, Bus: f.bus, Box: f.box, Kinds: connectors.NewRegistry(kind), Wait: f.waits.Wait,
		Logger: slog.New(slog.NewTextHandler(logs, nil)),
	})
	t.Cleanup(m.Close)
	id := f.insert(t, true)
	m.Start(context.Background())

	hook, ok := m.Hook(id)
	require.True(t, ok)
	err := hook.ReceiveHook(context.Background(), connectors.HookRequest{Body: []byte(leak)})
	require.ErrorIs(t, err, connectors.ErrPanic)
	require.EqualError(t, err, "the connector crashed in hook: panic of type string")
	require.Contains(t, logs.String(), "a connector panicked")
	require.NotContains(t, logs.String(), leak)
	require.Zero(t, f.waits.count(), "the connector was not restarted")
	_, ok = m.Hook(id)
	require.True(t, ok, "it still receives requests")
}

// CON-15: three runs in a row that end in a panic make a connector crash-looping, which the server
// reports as degraded. Two do not, and a run that ends in any other way ends the loop.
func TestManagerCountsTheConnectorsInACrashLoop(t *testing.T) {
	f := newManagerFixture(t)
	f.m.Start(context.Background())
	id := f.insert(t, true)
	other := f.insert(t, true)
	f.m.Apply(id)
	f.m.Apply(other)
	inst, otherInst := f.fake.Instance(t, id), f.fake.Instance(t, other)
	inst.WaitRun(t)
	otherInst.WaitRun(t)
	require.Zero(t, f.m.CrashLooping())

	for range connectors.CrashLoopThreshold - 1 {
		crash(t, inst)
	}
	require.Zero(t, f.m.CrashLooping(), "two panics are not a loop yet")
	crash(t, inst)
	require.Equal(t, 1, f.m.CrashLooping(), "the one connector that keeps panicking; the other runs fine")

	inst.Fail(errors.New("down")) // a failure that is no panic
	inst.WaitRun(t)
	require.Zero(t, f.m.CrashLooping(), "any other end of a run ends the loop")
}

// CON-15: a connector that has run StableRun since its last panic is healthy again, whatever came
// before: it is no longer reported, its next panic starts a new count, and its backoff starts over.
func TestACrashLoopEndsOnceTheConnectorRunsStably(t *testing.T) {
	clock := newTestClock()
	f := newManagerFixture(t, withClock(clock))
	f.m.Start(context.Background())
	id := f.insert(t, true)
	f.m.Apply(id)
	inst := f.fake.Instance(t, id)
	inst.WaitRun(t)
	for range connectors.CrashLoopThreshold {
		crash(t, inst)
	}
	require.Equal(t, 1, f.m.CrashLooping())

	clock.Advance(connectors.StableRun - time.Second)
	require.Equal(t, 1, f.m.CrashLooping(), "not yet stable")
	clock.Advance(time.Second)
	require.Zero(t, f.m.CrashLooping(), "the current run has lasted StableRun")

	inst.Connected()
	crash(t, inst) // an isolated panic after a long, healthy run
	require.Zero(t, f.m.CrashLooping(), "the count started over")
	require.Equal(t, []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 2 * time.Second}, f.waits.waitFor(t, 4),
		"and so did the backoff")
	for range connectors.CrashLoopThreshold - 1 {
		crash(t, inst)
	}
	require.Equal(t, 1, f.m.CrashLooping(), "it takes another CrashLoopThreshold panics in a row")
}

// CON-15: a connector stopped, or applied again, leaves the report: a stopped connector is not crash-looping.
func TestAStoppedConnectorIsNotCrashLooping(t *testing.T) {
	f := newManagerFixture(t)
	f.m.Start(context.Background())
	id := f.insert(t, true)
	f.m.Apply(id)
	inst := f.fake.Instance(t, id)
	inst.WaitRun(t)
	for range connectors.CrashLoopThreshold {
		crash(t, inst)
	}
	require.Equal(t, 1, f.m.CrashLooping())

	f.m.Stop(id)
	require.Zero(t, f.m.CrashLooping())
}
