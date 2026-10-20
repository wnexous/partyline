package testkit

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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
	T    testing.TB
	Conn *websocket.Conn
	s    *frameStream[Frame]
}

// DialLive opens the live WebSocket as the client, sending origin as the Origin header
// (Env.Origin when empty). It returns the HTTP response of a refused upgrade as an error-free
// *Response with a nil conn.
func (c *Client) DialLive(origin string) (*LiveConn, *Response) {
	c.T.Helper()
	if origin == "" {
		origin = c.env.Origin
	}
	conn, resp := c.dialWS("/api/v1/live", http.Header{"Origin": {origin}})
	if conn == nil {
		return nil, resp
	}
	return &LiveConn{T: c.T, Conn: conn, s: newFrameStream[Frame](conn)}, resp
}

// dialWS opens a WebSocket on path with the client's cookies, bearer token and header h. A
// refused upgrade returns a nil conn and the HTTP response.
func (c *Client) dialWS(path string, h http.Header) (*websocket.Conn, *Response) {
	c.T.Helper()
	if c.bearer != "" {
		h.Set("Authorization", "Bearer "+c.bearer)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(c.env.URL, "http") + path
	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: c.http, HTTPHeader: h})
	if err != nil {
		require.NotNil(c.T, resp, "dial: %v", err)
		r := &Response{T: c.T, Status: resp.StatusCode, Header: resp.Header}
		if resp.Body != nil {
			r.Body, _ = io.ReadAll(resp.Body)
		}
		return nil, r
	}
	conn.SetReadLimit(1 << 20)
	c.T.Cleanup(func() { _ = conn.CloseNow() })
	return conn, &Response{T: c.T, Status: resp.StatusCode, Header: resp.Header}
}

// Next returns the next frame, failing the test when none arrives within timeout.
func (l *LiveConn) Next(timeout time.Duration) Frame {
	l.T.Helper()
	f, err := l.s.read(timeout)
	require.NoError(l.T, err, "no live frame within %s", timeout)
	return f
}

// Expect skips frames until one of type typ arrives and decodes its data into v (when not nil).
func (l *LiveConn) Expect(typ dto.LiveEventType, v any, timeout time.Duration) Frame {
	l.T.Helper()
	deadline := time.Now().Add(timeout)
	for {
		f, err := l.s.read(time.Until(deadline))
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
		f, err := l.s.read(time.Until(deadline))
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

// Closed waits up to timeout for the server to close the connection and returns the close
// error (nil when still open).
func (l *LiveConn) Closed(timeout time.Duration) error { return l.s.closed(timeout) }

// frameStream reads JSON frames of type F from a WebSocket in the background.
type frameStream[F any] struct {
	conn   *websocket.Conn
	frames chan F
	done   chan struct{}
	err    error // set before done closes
}

func newFrameStream[F any](conn *websocket.Conn) *frameStream[F] {
	s := &frameStream[F]{conn: conn, frames: make(chan F, 256), done: make(chan struct{})}
	go s.readLoop()
	return s
}

func (s *frameStream[F]) readLoop() {
	defer close(s.done)
	for {
		_, data, err := s.conn.Read(context.Background())
		if err != nil {
			s.err = err
			return
		}
		var f F
		if err := json.Unmarshal(data, &f); err != nil {
			s.err = err
			return
		}
		s.frames <- f
	}
}

// closed drops frames until the connection ends (returning its error) or timeout passes (nil).
func (s *frameStream[F]) closed(timeout time.Duration) error {
	deadline := time.After(timeout)
	for {
		select {
		case <-s.frames:
		case <-s.done:
			return s.err
		case <-deadline:
			return nil
		}
	}
}

func (s *frameStream[F]) read(timeout time.Duration) (F, error) {
	var zero F
	if timeout <= 0 {
		return zero, context.DeadlineExceeded
	}
	select {
	case f := <-s.frames:
		return f, nil
	case <-s.done:
		select {
		case f := <-s.frames:
			return f, nil
		default:
			return zero, s.err
		}
	case <-time.After(timeout):
		return zero, context.DeadlineExceeded
	}
}
