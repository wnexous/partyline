package runners

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/runners/spec"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// Timeouts and connection keeping.
const (
	// DefaultCommandTimeout bounds every operation but logs and state (design, "Runner protocol").
	DefaultCommandTimeout = 60 * time.Second
	// LogsTimeout bounds a logs read, and StateTimeout a state read: both are fast.
	LogsTimeout  = 15 * time.Second
	StateTimeout = 15 * time.Second
	// DefaultHelloTimeout is how long a connection has to send its hello.
	DefaultHelloTimeout = 10 * time.Second
	// DefaultPingInterval keeps idle connections open through proxies, like the agent WebSocket.
	DefaultPingInterval = 25 * time.Second
	// DefaultPingTimeout bounds the wait for a pong.
	DefaultPingTimeout = 10 * time.Second
	// commandQueueSize is how many commands wait for one connection's writer.
	commandQueueSize = 16
	// revocationBuffer is the per-connection bus backlog.
	revocationBuffer = 64
)

// TimeoutFor returns the default timeout of an operation.
func TimeoutFor(op spec.Op) time.Duration {
	switch op {
	case spec.OpLogs:
		return LogsTimeout
	case spec.OpState:
		return StateTimeout
	}
	return DefaultCommandTimeout
}

// HubDeps are the collaborators of the Hub.
type HubDeps struct {
	Store  *store.Store
	Bus    *events.Bus
	Logger *slog.Logger
	Now    func() time.Time
	// Images are the extra image repositories a run may use (CA-09).
	Images []string
	// Timeouts overrides TimeoutFor per operation (tests shorten it).
	Timeouts map[spec.Op]time.Duration
	// HelloTimeout, PingInterval, PingTimeout and WriteTimeout default to the constants above.
	HelloTimeout time.Duration
	PingInterval time.Duration
	PingTimeout  time.Duration
	WriteTimeout time.Duration
}

// Hub is the set of live runner connections, at most one per runner (a new connection replaces
// the old one) and the request/response of commands over them.
type Hub struct {
	d    HubDeps
	ctx  context.Context
	stop context.CancelFunc

	mu     sync.Mutex
	conns  map[string]*conn
	closed bool
	wg     sync.WaitGroup
}

// NewHub returns a Hub.
func NewHub(d HubDeps) *Hub {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.HelloTimeout <= 0 {
		d.HelloTimeout = DefaultHelloTimeout
	}
	if d.PingInterval <= 0 {
		d.PingInterval = DefaultPingInterval
	}
	if d.PingTimeout <= 0 {
		d.PingTimeout = DefaultPingTimeout
	}
	if d.WriteTimeout <= 0 {
		d.WriteTimeout = DefaultPingTimeout
	}
	ctx, stop := context.WithCancel(context.Background())
	return &Hub{d: d, ctx: ctx, stop: stop, conns: map[string]*conn{}}
}

// Close ends every runner connection and waits for them to finish.
func (h *Hub) Close() {
	h.mu.Lock()
	h.closed = true
	conns := make([]*conn, 0, len(h.conns))
	for _, c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	h.stop()
	for _, c := range conns {
		c.closeWith(websocket.StatusGoingAway, "server shutting down")
	}
	h.wg.Wait()
}

// Online reports whether the runner has a live connection.
func (h *Hub) Online(runnerID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.conns[runnerID]
	return ok && !c.isClosed()
}

// Kick closes the live connection of a runner, if any (delete, CA-01).
func (h *Hub) Kick(runnerID, reason string) {
	h.mu.Lock()
	c, ok := h.conns[runnerID]
	h.mu.Unlock()
	if ok {
		c.closeWith(websocket.StatusPolicyViolation, reason)
	}
}

// Do sends cmd to the runner's live connection and waits for its Result, bounded by the
// operation's timeout (CA-09). ErrOffline when the runner is not connected (no queue in v1),
// ErrTimeout when it did not answer in time, and *OpError when it answered ok=false.
func (h *Hub) Do(ctx context.Context, runnerID string, cmd spec.Command) (spec.Result, error) {
	cmd.Type = spec.FrameCommand
	if cmd.ID == "" {
		id, err := newCommandID()
		if err != nil {
			return spec.Result{}, err
		}
		cmd.ID = id
	}
	if err := spec.ValidateCommand(cmd, h.d.Images); err != nil {
		return spec.Result{}, err
	}
	c := h.connection(runnerID)
	if c == nil {
		return spec.Result{}, ErrOffline
	}
	timeout := h.timeout(cmd.Op)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	results := make(chan spec.Result, 1)
	if !c.addPending(cmd.ID, results) {
		return spec.Result{}, ErrOffline
	}
	defer c.removePending(cmd.ID)

	select {
	case c.out <- cmd:
	case <-timer.C:
		return spec.Result{}, fmt.Errorf("%w (%s)", ErrTimeout, cmd.Op)
	case <-ctx.Done():
		return spec.Result{}, ctx.Err()
	case <-c.done:
		return spec.Result{}, ErrOffline
	}
	select {
	case res := <-results:
		if err := spec.ValidateResult(res); err != nil {
			return spec.Result{}, fmt.Errorf("runner %s: %w", runnerID, err)
		}
		if !res.OK {
			return res, &OpError{Op: cmd.Op, Message: res.Error}
		}
		return res, nil
	case <-timer.C:
		return spec.Result{}, fmt.Errorf("%w (%s)", ErrTimeout, cmd.Op)
	case <-ctx.Done():
		return spec.Result{}, ctx.Err()
	case <-c.done:
		return spec.Result{}, ErrOffline
	}
}

// timeout returns the timeout of an operation, with the test override.
func (h *Hub) timeout(op spec.Op) time.Duration {
	if t, ok := h.d.Timeouts[op]; ok && t > 0 {
		return t
	}
	return TimeoutFor(op)
}

// connection returns the live connection of a runner.
func (h *Hub) connection(runnerID string) *conn {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.conns[runnerID]
	if !ok || c.isClosed() {
		return nil
	}
	return c
}

// attach makes c the runner's connection and returns the one it replaced, closed by the caller.
// ok is false while the hub shuts down.
func (h *Hub) attach(c *conn) (old *conn, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, false
	}
	old = h.conns[c.runnerID]
	h.conns[c.runnerID] = c
	return old, true
}

// detach removes c if it is still the runner's connection.
func (h *Hub) detach(c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conns[c.runnerID] == c {
		delete(h.conns, c.runnerID)
	}
}

// track reserves a connection for Close to wait for; false while the hub shuts down.
func (h *Hub) track() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	h.wg.Add(1)
	return true
}

// applyStates writes the states the runner reported into container_agents (CA-06). A container the
// runner does not know here is ignored: the row is the AI's, created by Partyline.
func (h *Hub) applyStates(ctx context.Context, runnerID string, containers []spec.Container) error {
	if len(containers) == 0 {
		return nil
	}
	rows, err := h.d.Store.R().ListContainerAgentsByRunner(ctx, runnerID)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	known := make(map[string]db.ContainerAgent, len(rows))
	for _, row := range rows {
		known[row.ContainerName] = row
	}
	type change struct {
		state string
		err   sql.NullString
	}
	changes := map[string]change{}
	for _, c := range containers {
		row, ok := known[c.Name]
		if !ok {
			continue
		}
		// The error travels with the state, and a running container has none (CA-06).
		var lastError sql.NullString
		if c.Error != "" {
			lastError = sql.NullString{String: c.Error, Valid: true}
		}
		if row.State == string(c.State) && row.LastError.String == lastError.String && row.LastError.Valid == lastError.Valid {
			continue
		}
		if c.Error == "" && c.State != spec.StateRunning && row.LastError.Valid {
			lastError = row.LastError // keep the last failure until a running report clears it
		}
		changes[c.Name] = change{state: string(c.State), err: lastError}
	}
	if len(changes) == 0 {
		return nil
	}
	return h.d.Store.WithTx(ctx, func(q *db.Queries) error {
		now := h.d.Now().UnixMilli()
		for name, c := range changes {
			row := known[name]
			if err := q.SetContainerAgentState(ctx, db.SetContainerAgentStateParams{
				State: c.state, UpdatedAt: now, AgentID: row.AgentID,
			}); err != nil {
				return err
			}
			if row.LastError.String == c.err.String && row.LastError.Valid == c.err.Valid {
				continue
			}
			if err := q.SetContainerAgentLastError(ctx, db.SetContainerAgentLastErrorParams{
				LastError: c.err, UpdatedAt: now, AgentID: row.AgentID,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// writeJSON writes one frame to the runner, bounded by the write timeout.
func (h *Hub) writeJSON(ctx context.Context, c *conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, h.d.WriteTimeout)
	defer cancel()
	return c.ws.Write(wctx, websocket.MessageText, data)
}

// conn is one live runner connection.
type conn struct {
	runnerID string
	ws       *websocket.Conn
	out      chan spec.Command
	done     chan struct{}
	cancel   context.CancelFunc

	closeOnce sync.Once
	mu        sync.Mutex
	closed    bool
	pending   map[string]chan spec.Result
}

func newConn(runnerID string, ws *websocket.Conn, cancel context.CancelFunc) *conn {
	return &conn{
		runnerID: runnerID, ws: ws, out: make(chan spec.Command, commandQueueSize), done: make(chan struct{}),
		cancel: cancel, pending: map[string]chan spec.Result{},
	}
}

// isClosed reports whether the connection ended.
func (c *conn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// closeWith ends the connection, once: the pending commands fail with ErrOffline and the loops
// stop. The close handshake runs in the background, so the caller never blocks on the peer.
func (c *conn) closeWith(code websocket.StatusCode, reason string) {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		close(c.done)
		go func() {
			_ = c.ws.Close(code, reason)
			c.cancel()
		}()
	})
}

// addPending records the result channel of a command; false when the connection is gone.
func (c *conn) addPending(id string, ch chan spec.Result) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	c.pending[id] = ch
	return true
}

// removePending drops the result channel of a command that ended.
func (c *conn) removePending(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pending, id)
}

// complete hands a result to the command waiting for it; unknown ids are ignored.
func (c *conn) complete(res spec.Result) {
	c.mu.Lock()
	ch, ok := c.pending[res.ID]
	delete(c.pending, res.ID)
	c.mu.Unlock()
	if ok {
		ch <- res
	}
}

// newCommandID returns a random command id.
func newCommandID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// revocations is the bus subscription a connection watches; a nil channel never fires.
func (h *Hub) revocations() (<-chan events.Event, func()) {
	if h.d.Bus == nil {
		return nil, func() {}
	}
	return h.d.Bus.Subscribe(revocationBuffer)
}
