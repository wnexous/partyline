package testkit

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/runners/spec"
)

// RunnerToken creates a runner token of the client's user and returns a client that authenticates
// with it, like `partyline runner` after the device approval (CA-01).
func (c *Client) RunnerToken(name string) *Client {
	c.T.Helper()
	require.NotEmpty(c.T, c.User.ID, "RunnerToken needs a logged-in client")
	_, secret, err := c.env.App.Users.CreateRunnerToken(context.Background(), c.User.ID, name)
	require.NoError(c.T, err)
	return c.WithBearer(secret)
}

// DialRunner registers the runner of the client's runner token (POST /runner/register, once) and
// opens its WebSocket with the hello, as `partyline runner` does on start (CA-01, CA-02).
func (c *Client) DialRunner(machine string, hello spec.Hello) (*RunnerConn, dto.Runner) {
	c.T.Helper()
	resp := c.Post("/api/v1/runner/register", dto.RegisterRunnerRequest{Machine: machine})
	require.True(c.T, resp.Status == http.StatusCreated || resp.Status == http.StatusOK, "register: %d %s", resp.Status, resp.Body)
	return c.DialRunnerConn(hello), Decode[dto.Runner](resp)
}

// DialRunnerConn opens the runner WebSocket without registering, sending hello as the first frame.
func (c *Client) DialRunnerConn(hello spec.Hello) *RunnerConn {
	c.T.Helper()
	conn, resp := c.dialWS("/api/v1/runner/ws", http.Header{})
	require.NotNil(c.T, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	r := &RunnerConn{T: c.T, Conn: conn}
	if hello.Type == "" {
		hello.Type = spec.FrameHello
	}
	r.Send(hello)
	return r
}

// RunnerConn is one fake `partyline runner` connection.
type RunnerConn struct {
	T    testing.TB
	Conn *websocket.Conn

	mu sync.Mutex // one writer at a time
}

// Send writes one frame; it fails the test when it cannot.
func (r *RunnerConn) Send(v any) {
	r.T.Helper()
	data, err := json.Marshal(v)
	require.NoError(r.T, err)
	r.mu.Lock()
	defer r.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), IOTimeout)
	defer cancel()
	require.NoError(r.T, r.Conn.Write(ctx, websocket.MessageText, data))
}

// State reports container states without being asked (CA-06).
func (r *RunnerConn) State(containers ...spec.Container) {
	r.T.Helper()
	r.Send(spec.StateEvent{Type: spec.FrameState, Containers: containers})
}

// NextCommand reads the next command within timeout.
func (r *RunnerConn) NextCommand(timeout time.Duration) spec.Command {
	r.T.Helper()
	cmd, err := r.readCommand(timeout)
	require.NoError(r.T, err, "no command within %s", timeout)
	return cmd
}

// TryCommand reads the next command within timeout and reports the error instead of failing the
// test; a closed connection is how a replaced or kicked runner sees it.
func (r *RunnerConn) TryCommand(timeout time.Duration) (spec.Command, error) {
	return r.readCommand(timeout)
}

func (r *RunnerConn) readCommand(timeout time.Duration) (spec.Command, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, data, err := r.Conn.Read(ctx)
	if err != nil {
		return spec.Command{}, err
	}
	var cmd spec.Command
	if err := json.Unmarshal(data, &cmd); err != nil {
		return spec.Command{}, err
	}
	return cmd, nil
}

// Handle answers every command until the connection ends. reply returns the result for cmd (its
// ID and type are filled in); nil leaves the command unanswered, as a hung runner would.
func (r *RunnerConn) Handle(reply func(spec.Command) *spec.Result) {
	r.T.Helper()
	go func() {
		for {
			cmd, err := r.readCommand(IOTimeout)
			if err != nil {
				return
			}
			res := reply(cmd)
			if res == nil {
				continue
			}
			res.Type, res.ID = spec.FrameResult, cmd.ID
			if !res.OK && res.Error == "" {
				res.Error = "the fake runner failed the command"
			}
			r.Send(*res)
		}
	}()
}

// Close ends the connection without a close handshake.
func (r *RunnerConn) Close() { _ = r.Conn.CloseNow() }
