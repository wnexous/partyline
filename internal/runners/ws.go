package runners

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/runners/spec"
	"github.com/wnexous/partyline/internal/store/db"
)

// ServeHTTP serves GET /api/v1/runner/ws: a runner authenticated by its runner token sends a
// hello, then executes commands and reports states (CA-02, CA-09). A new connection replaces the
// runner's old one.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := authn.PrincipalFrom(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.NewError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized, "authentication required"))
		return
	}
	if p.Kind != authn.KindRunner || p.TokenID == "" {
		httpx.WriteError(w, httpx.NewError(http.StatusForbidden, dto.ErrorCodeForbidden, "runner token required"))
		return
	}
	row, err := h.runnerByToken(r.Context(), p.TokenID)
	switch {
	case errors.Is(err, ErrNotRegistered):
		httpx.WriteError(w, httpx.NewError(http.StatusForbidden, dto.ErrorCodeForbidden, "runner token is not registered: call POST /api/v1/runner/register first"))
		return
	case err != nil:
		h.d.Logger.ErrorContext(r.Context(), "runner: read the runner of a connection failed", "err", err, "request_id", httpx.RequestID(r.Context()))
		httpx.WriteError(w, err)
		return
	}
	if !h.track() {
		httpx.WriteError(w, httpx.NewError(http.StatusServiceUnavailable, dto.ErrorCodeUnavailable, "shutting down"))
		return
	}
	defer h.wg.Done()
	// Subscribe before the upgrade, so a revocation from here on is seen; a lagged bus closes the
	// connection with 1013 and the runner authenticates again.
	revocations, unsubscribe := h.revocations()
	defer unsubscribe()

	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already wrote the response
	}
	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	stopReq := context.AfterFunc(r.Context(), cancel)
	defer stopReq()

	hello, err := h.readHello(ctx, ws)
	if err != nil {
		_ = ws.CloseNow()
		return
	}
	if err := h.applyHello(ctx, row.ID, hello); err != nil {
		if ctx.Err() == nil {
			h.d.Logger.ErrorContext(ctx, "runner: hello failed", "runner_id", row.ID, "err", err)
		}
		_ = ws.Close(websocket.StatusInternalError, "hello failed")
		return
	}
	c := newConn(row.ID, ws, cancel)
	if old, attached := h.attach(c); attached {
		if old != nil {
			old.closeWith(websocket.StatusServiceRestart, "replaced by a new connection")
		}
	} else {
		_ = ws.Close(websocket.StatusGoingAway, "shutting down")
		return
	}
	defer h.detach(c)

	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		h.readLoop(ctx, c)
	}()
	h.writeLoop(ctx, c, p, revocations)
	c.closeWith(websocket.StatusNormalClosure, "connection ended")
	readers.Wait()
}

// runnerByToken returns the runner a runner token registered.
func (h *Hub) runnerByToken(ctx context.Context, tokenID string) (db.Runner, error) {
	row, err := h.d.Store.R().GetRunnerByToken(ctx, tokenID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Runner{}, ErrNotRegistered
	}
	return row, err
}

// readHello reads the first frame of a connection, which must be a valid hello.
func (h *Hub) readHello(ctx context.Context, ws *websocket.Conn) (spec.Hello, error) {
	hctx, cancel := context.WithTimeout(ctx, h.d.HelloTimeout)
	defer cancel()
	_, data, err := ws.Read(hctx)
	if err != nil {
		return spec.Hello{}, err
	}
	var hello spec.Hello
	if err := json.Unmarshal(data, &hello); err != nil {
		return spec.Hello{}, err
	}
	if err := spec.ValidateHello(hello); err != nil {
		return spec.Hello{}, err
	}
	return hello, nil
}

// applyHello records what a runner reported about itself (CA-02): Docker, when it was last seen,
// and the state of its containers.
func (h *Hub) applyHello(ctx context.Context, runnerID string, hello spec.Hello) error {
	now := h.d.Now().UnixMilli()
	version := sql.NullString{}
	dockerErr := sql.NullString{}
	if hello.DockerVersion != "" {
		version = sql.NullString{String: hello.DockerVersion, Valid: true}
	} else {
		dockerErr = sql.NullString{String: hello.DockerError, Valid: true}
	}
	err := h.d.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.SetRunnerDocker(ctx, db.SetRunnerDockerParams{
			DockerVersion: version, DockerError: dockerErr, UpdatedAt: now, ID: runnerID,
		}); err != nil {
			return err
		}
		return q.SetRunnerSeen(ctx, db.SetRunnerSeenParams{
			LastSeenAt: sql.NullInt64{Int64: now, Valid: true}, UpdatedAt: now, ID: runnerID,
		})
	})
	if err != nil {
		return err
	}
	return h.applyStates(ctx, runnerID, hello.Containers)
}

// readLoop reads results and state events until the connection ends.
func (h *Hub) readLoop(ctx context.Context, c *conn) {
	defer c.closeWith(websocket.StatusNormalClosure, "connection ended")
	for {
		_, data, err := c.ws.Read(ctx)
		if err != nil {
			return
		}
		var head struct {
			Type spec.FrameType `json:"type"`
			ID   string         `json:"id"`
		}
		if json.Unmarshal(data, &head) != nil {
			continue
		}
		switch head.Type {
		case spec.FrameResult:
			var res spec.Result
			if json.Unmarshal(data, &res) != nil {
				continue
			}
			if err := spec.ValidateResult(res); err != nil {
				h.d.Logger.Debug("runner: ignoring an invalid result", "runner_id", c.runnerID, "command_id", head.ID, "err", err)
				continue
			}
			c.complete(res)
		case spec.FrameState:
			var ev spec.StateEvent
			if json.Unmarshal(data, &ev) != nil {
				continue
			}
			if err := spec.ValidateStateEvent(ev); err != nil {
				h.d.Logger.Debug("runner: ignoring an invalid state event", "runner_id", c.runnerID, "err", err)
				continue
			}
			if err := h.applyStates(ctx, c.runnerID, ev.Containers); err != nil && ctx.Err() == nil {
				h.d.Logger.Error("runner: state update failed", "runner_id", c.runnerID, "err", err)
			}
		case spec.FrameHello:
			var hello spec.Hello
			if json.Unmarshal(data, &hello) != nil || spec.ValidateHello(hello) != nil {
				continue
			}
			if err := h.applyHello(ctx, c.runnerID, hello); err != nil && ctx.Err() == nil {
				h.d.Logger.Error("runner: hello update failed", "runner_id", c.runnerID, "err", err)
			}
		}
	}
}

// writeLoop writes the queued commands and pings; it returns when the connection ends or its
// credential is revoked.
func (h *Hub) writeLoop(ctx context.Context, c *conn, p authn.Principal, revocations <-chan events.Event) {
	ping := time.NewTicker(h.d.PingInterval)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case e, ok := <-revocations:
			if !ok {
				return
			}
			switch e := e.(type) {
			case events.SessionsRevoked:
				if e.Revokes(p.UserID, "", p.TokenID) {
					c.closeWith(websocket.StatusPolicyViolation, "token revoked")
					return
				}
			case events.Lagged:
				// The bus may have dropped the revocation: the runner reconnects and authenticates.
				c.closeWith(websocket.StatusTryAgainLater, "token check failed")
				return
			}
		case cmd := <-c.out:
			if err := h.writeJSON(ctx, c, cmd); err != nil {
				c.closeWith(websocket.StatusInternalError, "write failed")
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, h.d.PingTimeout)
			err := c.ws.Ping(pctx)
			cancel()
			if err != nil {
				c.closeWith(websocket.StatusGoingAway, "ping timeout")
				return
			}
		}
	}
}
