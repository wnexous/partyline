// Package connectortest provides a controllable fake connector kind for tests of the
// connector framework, the HTTP API and platform-independent delivery.
//
//	fake := connectortest.New()
//	env := testkit.New(t, testkit.WithConnectorKinds(fake.Kind()))
//	// create a "fake" connector through the API, then:
//	inst := fake.Instance(t, connectorID)
//	inst.WaitRun(t)
//	inst.Connected()
//	inst.Emit(ctx, connectors.Inbound{...})
package connectortest

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
)

// KindName is the name of the fake kind.
const KindName = "fake"

// Timeout bounds every wait; generous because the test host may be loaded.
const Timeout = 10 * time.Second

// Fake creates fake connector instances and remembers them by connector id.
type Fake struct {
	mu        sync.Mutex
	instances map[string]*Instance
	created   chan struct{}
}

// New returns an empty Fake.
func New() *Fake {
	return &Fake{instances: map[string]*Instance{}, created: make(chan struct{}, 1)}
}

// Kind returns the fake kind: a required secret "token", an optional "label" and chat listing.
func (f *Fake) Kind() connectors.Kind {
	return connectors.Kind{
		Name: KindName, Label: "Fake", CanListChats: true,
		Fields: []connectors.Field{
			{Name: "token", Label: "Token", Type: dto.FieldTypeSecret, Required: true},
			{Name: "label", Label: "Label", Type: dto.FieldTypeString},
		},
		New: f.newInstance,
	}
}

func (f *Fake) newInstance(cfg connectors.Config, d connectors.Deps) (connectors.Connector, error) {
	inst := &Instance{
		ID: d.ID, Config: cfg,
		runs: make(chan struct{}, 64), stops: make(chan struct{}, 64), fail: make(chan error, 64),
		sends: make(chan connectors.Outbound, 64),
	}
	f.mu.Lock()
	f.instances[d.ID] = inst
	f.mu.Unlock()
	select {
	case f.created <- struct{}{}:
	default:
	}
	return inst, nil
}

// Lookup returns the connector's newest instance without waiting, or nil.
func (f *Fake) Lookup(connectorID string) *Instance {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.instances[connectorID]
}

// Instance waits until the manager created the connector's newest instance and returns it.
func (f *Fake) Instance(t testing.TB, connectorID string) *Instance {
	t.Helper()
	deadline := time.After(Timeout)
	for {
		f.mu.Lock()
		inst := f.instances[connectorID]
		f.mu.Unlock()
		if inst != nil {
			return inst
		}
		select {
		case <-f.created:
		case <-deadline:
			t.Fatalf("connector %s was never started", connectorID)
		}
	}
}

// Instance is one fake connector. Run blocks until its context ends or Fail is called.
type Instance struct {
	ID     string
	Config connectors.Config

	mu      sync.Mutex
	h       connectors.Handler
	sendErr error
	chats   []connectors.ChatInfo
	sent    []connectors.Outbound
	nextID  int

	runs  chan struct{}
	stops chan struct{}
	fail  chan error
	sends chan connectors.Outbound
}

// Run implements connectors.Connector.
func (i *Instance) Run(ctx context.Context, h connectors.Handler) error {
	i.mu.Lock()
	i.h = h
	i.mu.Unlock()
	i.runs <- struct{}{}
	defer func() { i.stops <- struct{}{} }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-i.fail:
		return err
	}
}

// Send implements connectors.Connector; it records m and returns "ext-<n>" or SetSendError's error.
func (i *Instance) Send(_ context.Context, m connectors.Outbound) (string, error) {
	i.mu.Lock()
	err := i.sendErr
	i.nextID++
	id := "ext-" + strconv.Itoa(i.nextID)
	if err == nil {
		i.sent = append(i.sent, m)
	}
	i.mu.Unlock()
	if err != nil {
		return "", err
	}
	i.sends <- m
	return id, nil
}

// ListChats implements connectors.ChatLister with the chats given to SetChats.
func (i *Instance) ListChats(context.Context) ([]connectors.ChatInfo, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]connectors.ChatInfo(nil), i.chats...), nil
}

// SetChats sets what ListChats returns.
func (i *Instance) SetChats(chats ...connectors.ChatInfo) {
	i.mu.Lock()
	i.chats = chats
	i.mu.Unlock()
}

// SetSendError makes later sends fail with err (nil restores success).
func (i *Instance) SetSendError(err error) {
	i.mu.Lock()
	i.sendErr = err
	i.mu.Unlock()
}

// WaitRun waits until Run was called (once per call of WaitRun).
func (i *Instance) WaitRun(t testing.TB) {
	t.Helper()
	select {
	case <-i.runs:
	case <-time.After(Timeout):
		t.Fatalf("connector %s: Run was not called", i.ID)
	}
}

// WaitStop waits until a Run call returned.
func (i *Instance) WaitStop(t testing.TB) {
	t.Helper()
	select {
	case <-i.stops:
	case <-time.After(Timeout):
		t.Fatalf("connector %s: Run did not return", i.ID)
	}
}

// Fail makes the running (or next) Run return err.
func (i *Instance) Fail(err error) { i.fail <- err }

// Connected reports StatusConnected through the handler of the current Run.
func (i *Instance) Connected() { i.handler().Status(connectors.StatusConnected, "") }

// Emit hands a received message to the handler of the current Run.
func (i *Instance) Emit(ctx context.Context, m connectors.Inbound) { i.handler().Inbound(ctx, m) }

func (i *Instance) handler() connectors.Handler {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.h == nil {
		panic(errors.New("connectortest: Run has not been called"))
	}
	return i.h
}

// WaitSend waits for the next successful send and returns it.
func (i *Instance) WaitSend(t testing.TB) connectors.Outbound {
	t.Helper()
	select {
	case m := <-i.sends:
		return m
	case <-time.After(Timeout):
		t.Fatalf("connector %s: nothing was sent", i.ID)
		return connectors.Outbound{}
	}
}

// Sent returns every successful send so far.
func (i *Instance) Sent() []connectors.Outbound {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]connectors.Outbound(nil), i.sent...)
}
