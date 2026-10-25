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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
)

// KindName is the name of the fake kind.
const KindName = "fake"

// Timeout bounds every wait; generous because the test host may be loaded.
const Timeout = 30 * time.Second

func afterTimeout() <-chan time.Time { return time.After(Timeout) }

// Fake creates fake connector instances and remembers them by connector id.
type Fake struct {
	mu        sync.Mutex
	instances map[string]*Instance
	created   chan struct{}
	noReact   bool
}

// WithoutReactions makes instances created from now on lack connectors.Reactor, like a
// platform without reactions.
func (f *Fake) WithoutReactions() {
	f.mu.Lock()
	f.noReact = true
	f.mu.Unlock()
}

// plain is an Instance seen without its Reactor method.
type plain struct{ inst *Instance }

func (p plain) Run(ctx context.Context, h connectors.Handler) error { return p.inst.Run(ctx, h) }
func (p plain) Send(ctx context.Context, m connectors.Outbound) (string, error) {
	return p.inst.Send(ctx, m)
}
func (p plain) ListChats(ctx context.Context) ([]connectors.ChatInfo, error) {
	return p.inst.ListChats(ctx)
}

// New returns an empty Fake.
func New() *Fake {
	return &Fake{instances: map[string]*Instance{}, created: make(chan struct{}, 1)}
}

// Kind returns the fake kind: a required secret "token", an optional "label" and chat listing.
func (f *Fake) Kind() connectors.Kind {
	return connectors.Kind{
		Name: KindName, Label: "Fake", CanListChats: true, CanReact: true, CanSendFiles: true, Address: Address,
		Fields: []connectors.Field{
			{Name: "token", Label: "Token", Type: dto.FieldTypeSecret, Required: true},
			{Name: "label", Label: "Label", Type: dto.FieldTypeString},
		},
		New: f.newInstance,
	}
}

// Address is the fake kind's Address (CID-31): a phone number of 8 to 15 digits is a direct chat
// that the platform names by the same digits; anything else is no address. Chat ids of Partyline and
// the ids the tests give their chats ("120363000000000001@g.us", "-1001234567890") are not.
func Address(_ connectors.Config, raw string) (connectors.ChatInfo, bool) {
	raw = strings.TrimSpace(raw)
	if n := len(raw); n < 8 || n > 15 || strings.Trim(raw, "0123456789") != "" {
		return connectors.ChatInfo{}, false
	}
	return connectors.ChatInfo{ID: raw, Kind: "dm"}, true
}

func (f *Fake) newInstance(cfg connectors.Config, d connectors.Deps) (connectors.Connector, error) {
	inst := &Instance{
		ID: d.ID, Config: cfg, Deps: d,
		runs: make(chan struct{}, 64), stops: make(chan struct{}, 64), fail: make(chan error, 64), crash: make(chan any, 64),
		sends: make(chan connectors.Outbound, 64), sending: make(chan struct{}, 64), reacts: make(chan connectors.Reaction, 64),
	}
	f.mu.Lock()
	f.instances[d.ID] = inst
	noReact := f.noReact
	f.mu.Unlock()
	select {
	case f.created <- struct{}{}:
	default:
	}
	if noReact {
		return plain{inst}, nil
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
	// Deps are what the manager passed to Kind.New.
	Deps connectors.Deps

	mu      sync.Mutex
	h       connectors.Handler
	sendErr error
	listErr error
	chats   []connectors.ChatInfo
	sent    []connectors.Outbound
	nextID  int
	// gate, when set, holds every Send until it is closed (HoldSends); parts is how many
	// messages beyond the first a send creates (SetSendParts).
	gate  chan struct{}
	parts int
	// quiet makes sends skip Created (QuietSends).
	quiet bool
	// failAfter, when set, makes a send create its first message and then fail with it
	// (SetSendErrorAfterFirst).
	failAfter error
	// reactErr fails reactions (SetReactError); nextReact numbers the messages they create and
	// reacted records the ones that went out.
	reactErr  error
	nextReact int
	reacted   []connectors.Reaction
	// sendPanic, reactPanic and listPanic make the call panic with them (SetSendPanic and the like).
	sendPanic, reactPanic, listPanic *panicValue

	runs    chan struct{}
	stops   chan struct{}
	fail    chan error
	crash   chan any
	sends   chan connectors.Outbound
	sending chan struct{}
	reacts  chan connectors.Reaction
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
	case v := <-i.crash:
		panic(v)
	}
}

// Send implements connectors.Connector; it records m and returns "ext-<n>" or SetSendError's error.
// A send that SetSendParts made multi-part also creates "ext-<n>.p1", "ext-<n>.p2", ...; Created
// is told every id, as a real connector does. While HoldSends is on it waits before it creates
// anything, as a platform call that is still running does.
func (i *Instance) Send(ctx context.Context, m connectors.Outbound) (string, error) {
	i.mu.Lock()
	err := i.sendErr
	i.nextID++
	id := "ext-" + strconv.Itoa(i.nextID)
	gate, parts, quiet, failAfter, crash := i.gate, i.parts, i.quiet, i.failAfter, i.sendPanic
	i.mu.Unlock()
	if crash != nil {
		panic(crash.v)
	}
	if err != nil {
		return "", err
	}
	if failAfter != nil {
		if m.Created != nil {
			m.Created(id)
		}
		return "", failAfter
	}
	if gate != nil {
		i.sending <- struct{}{}
		select {
		case <-gate:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	i.mu.Lock()
	i.sent = append(i.sent, m)
	i.mu.Unlock()
	if m.Created != nil && !quiet {
		m.Created(id)
		for p := 1; p <= parts; p++ {
			m.Created(id + ".p" + strconv.Itoa(p))
		}
	}
	i.sends <- m
	return id, nil
}

// HoldSends makes every later send wait, before it creates anything, until ReleaseSends (see
// WaitSending): a platform call that is still running.
func (i *Instance) HoldSends() {
	i.mu.Lock()
	i.gate = make(chan struct{})
	i.mu.Unlock()
}

// ReleaseSends lets the sends held by HoldSends, and later ones, go on.
func (i *Instance) ReleaseSends() {
	i.mu.Lock()
	if i.gate != nil {
		close(i.gate)
		i.gate = nil
	}
	i.mu.Unlock()
}

// WaitSending waits until a send is held by HoldSends.
func (i *Instance) WaitSending(t testing.TB) {
	t.Helper()
	select {
	case <-i.sending:
	case <-time.After(Timeout):
		t.Fatalf("connector %s: no send is in flight", i.ID)
	}
}

// QuietSends makes later sends not tell Outbound.Created the ids they create, like a connector
// that only returns the first one: the send's row (platform_sends) is then the only record of it.
func (i *Instance) QuietSends() {
	i.mu.Lock()
	i.quiet = true
	i.mu.Unlock()
}

// SetSendParts makes later sends create n more messages besides the first, with the ids
// "ext-<n>.p1" ...: the parts of a long text, or its files.
func (i *Instance) SetSendParts(n int) {
	i.mu.Lock()
	i.parts = n
	i.mu.Unlock()
}

// React implements connectors.Reactor; it records r (see WaitReact) and tells r.Created the id of the
// message the reaction created, "react-<n>", as a platform that reports the account's own messages does.
// A reaction fails with SetReactError's error, and waits while HoldSends is on, like a send.
func (i *Instance) React(ctx context.Context, r connectors.Reaction) error {
	i.mu.Lock()
	err := i.reactErr
	i.nextReact++
	id := "react-" + strconv.Itoa(i.nextReact)
	gate, quiet, crash := i.gate, i.quiet, i.reactPanic
	i.mu.Unlock()
	if crash != nil {
		panic(crash.v)
	}
	if err != nil {
		return err
	}
	if gate != nil {
		i.sending <- struct{}{}
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if r.Created != nil && !quiet {
		r.Created(id)
	}
	i.mu.Lock()
	i.reacted = append(i.reacted, r)
	i.mu.Unlock()
	i.reacts <- r
	return nil
}

// Reactions returns every reaction that went out so far.
func (i *Instance) Reactions() []connectors.Reaction {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]connectors.Reaction(nil), i.reacted...)
}

// SetReactError makes later reactions fail with err (nil restores success).
func (i *Instance) SetReactError(err error) {
	i.mu.Lock()
	i.reactErr = err
	i.mu.Unlock()
}

// WaitReact waits for the next reaction and returns it.
func (i *Instance) WaitReact(t testing.TB) connectors.Reaction {
	t.Helper()
	select {
	case r := <-i.reacts:
		return r
	case <-time.After(Timeout):
		t.Fatalf("connector %s: no reaction was sent", i.ID)
		return connectors.Reaction{}
	}
}

// ListChats implements connectors.ChatLister with the chats given to SetChats, or fails
// with the error given to SetListError.
func (i *Instance) ListChats(context.Context) ([]connectors.ChatInfo, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.listPanic != nil {
		panic(i.listPanic.v)
	}
	if i.listErr != nil {
		return nil, i.listErr
	}
	return append([]connectors.ChatInfo(nil), i.chats...), nil
}

// SetListError makes ListChats fail with err (nil restores it).
func (i *Instance) SetListError(err error) {
	i.mu.Lock()
	i.listErr = err
	i.mu.Unlock()
}

// SetChats sets what ListChats returns.
func (i *Instance) SetChats(chats ...connectors.ChatInfo) {
	i.mu.Lock()
	i.chats = chats
	i.mu.Unlock()
}

// SetSendErrorAfterFirst makes later sends create their first message (telling Created its id, as a
// platform that took the text of a send and refused its file does) and then fail with err (nil
// restores success).
func (i *Instance) SetSendErrorAfterFirst(err error) {
	i.mu.Lock()
	i.failAfter = err
	i.mu.Unlock()
}

// SetSendError makes later sends fail with err (nil restores success).
func (i *Instance) SetSendError(err error) {
	i.mu.Lock()
	i.sendErr = err
	i.mu.Unlock()
}

// panicValue holds a value a call panics with, so that "no panic" is not a nil value.
type panicValue struct{ v any }

// SetSendPanic makes later sends panic with v, like a connector with a bug (nil restores success).
func (i *Instance) SetSendPanic(v any) {
	i.mu.Lock()
	i.sendPanic = panicOf(v)
	i.mu.Unlock()
}

// SetReactPanic makes later reactions panic with v (nil restores success).
func (i *Instance) SetReactPanic(v any) {
	i.mu.Lock()
	i.reactPanic = panicOf(v)
	i.mu.Unlock()
}

// SetListPanic makes ListChats panic with v (nil restores it).
func (i *Instance) SetListPanic(v any) {
	i.mu.Lock()
	i.listPanic = panicOf(v)
	i.mu.Unlock()
}

func panicOf(v any) *panicValue {
	if v == nil {
		return nil
	}
	return &panicValue{v}
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

// Panic makes the running (or next) Run panic with v, like a connector with a bug.
func (i *Instance) Panic(v any) { i.crash <- v }

// Connected reports StatusConnected through the handler of the current Run.
func (i *Instance) Connected() { i.handler().Status(connectors.StatusConnected, "") }

// Account reports the connector's account label through the handler of the current Run.
func (i *Instance) Account(label string) { i.handler().Account(label) }

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

// MigrateChat reports a chat migration through the handler of the current Run.
func (i *Instance) MigrateChat(ctx context.Context, oldID, newID string) {
	i.handler().MigrateChat(ctx, oldID, newID)
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
