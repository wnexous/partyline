package testkit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// Frame is one live WebSocket frame with its data left raw.
type Frame struct {
	Type dto.LiveEventType `json:"type"`
	Data json.RawMessage   `json:"data"`
}

// LiveConn is an open live WebSocket (GET /api/v1/live). A background reader queues incoming
// frames, so waiting for a frame never closes the connection.
type LiveConn struct {
	T      testing.TB
	Conn   *websocket.Conn
	frames chan Frame
	done   chan struct{}
	err    error // set before done closes
}

// DialLive opens the live WebSocket as the client, sending origin as the Origin header
// (Env.Origin when empty). It returns the HTTP response of a refused upgrade as an error-free
// *Response with a nil conn.
func (c *Client) DialLive(origin string) (*LiveConn, *Response) {
	c.T.Helper()
	if origin == "" {
		origin = c.env.Origin
	}
	h := http.Header{"Origin": {origin}}
	if c.bearer != "" {
		h.Set("Authorization", "Bearer "+c.bearer)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(c.env.URL, "http") + "/api/v1/live"
	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: c.http, HTTPHeader: h})
	if err != nil {
		require.NotNil(c.T, resp, "dial: %v", err)
		return nil, &Response{T: c.T, Status: resp.StatusCode, Header: resp.Header}
	}
	conn.SetReadLimit(1 << 20)
	lc := &LiveConn{T: c.T, Conn: conn, frames: make(chan Frame, 256), done: make(chan struct{})}
	go lc.readLoop()
	c.T.Cleanup(func() { _ = conn.CloseNow() })
	return lc, &Response{T: c.T, Status: resp.StatusCode, Header: resp.Header}
}

// Next returns the next frame, failing the test when none arrives within timeout.
func (l *LiveConn) Next(timeout time.Duration) Frame {
	l.T.Helper()
	f, err := l.read(timeout)
	require.NoError(l.T, err, "no live frame within %s", timeout)
	return f
}

// Expect skips frames until one of type typ arrives and decodes its data into v (when not nil).
func (l *LiveConn) Expect(typ dto.LiveEventType, v any, timeout time.Duration) Frame {
	l.T.Helper()
	deadline := time.Now().Add(timeout)
	for {
		f, err := l.read(time.Until(deadline))
		require.NoError(l.T, err, "no %q frame within %s", typ, timeout)
		if f.Type != typ {
			continue
		}
		if v != nil {
			require.NoError(l.T, json.Unmarshal(f.Data, v))
		}
		return f
	}
}

// ExpectNone fails the test when a frame other than the listed ignored types arrives within d.
func (l *LiveConn) ExpectNone(d time.Duration, ignore ...dto.LiveEventType) {
	l.T.Helper()
	deadline := time.Now().Add(d)
	for {
		f, err := l.read(time.Until(deadline))
		if err != nil {
			require.True(l.T, errors.Is(err, context.DeadlineExceeded), "read: %v", err)
			return
		}
		ignored := false
		for _, typ := range ignore {
			ignored = ignored || f.Type == typ
		}
		require.True(l.T, ignored, "unexpected live frame %s: %s", f.Type, f.Data)
	}
}

func (l *LiveConn) readLoop() {
	defer close(l.done)
	for {
		_, data, err := l.Conn.Read(context.Background())
		if err != nil {
			l.err = err
			return
		}
		var f Frame
		if err := json.Unmarshal(data, &f); err != nil {
			l.err = err
			return
		}
		l.frames <- f
	}
}

// Closed waits up to timeout for the server to close the connection and returns the close
// error (nil when still open).
func (l *LiveConn) Closed(timeout time.Duration) error {
	for {
		select {
		case <-l.frames:
		case <-l.done:
			return l.err
		case <-time.After(timeout):
			return nil
		}
	}
}

func (l *LiveConn) read(timeout time.Duration) (Frame, error) {
	if timeout <= 0 {
		return Frame{}, context.DeadlineExceeded
	}
	select {
	case f := <-l.frames:
		return f, nil
	case <-l.done:
		select {
		case f := <-l.frames:
			return f, nil
		default:
			return Frame{}, l.err
		}
	case <-time.After(timeout):
		return Frame{}, context.DeadlineExceeded
	}
}
