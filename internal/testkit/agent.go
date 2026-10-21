package testkit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// AgentFrame is one frame of the agent WebSocket; Raw holds the whole frame.
type AgentFrame struct {
	Type dto.AgentFrameType
	Raw  json.RawMessage
}

// UnmarshalJSON keeps the raw frame and reads its type.
func (f *AgentFrame) UnmarshalJSON(data []byte) error {
	var head struct {
		Type dto.AgentFrameType `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return err
	}
	f.Type, f.Raw = head.Type, append(json.RawMessage(nil), data...)
	return nil
}

// AgentConn is an open agent WebSocket (GET /api/v1/agent/ws) read in the background.
type AgentConn struct {
	T    testing.TB
	Conn *websocket.Conn
	s    *frameStream[AgentFrame]
}

// DialAgent opens the agent WebSocket with the client's bearer token, naming the agent in
// X-Partyline-Agent (omitted when empty). A refused upgrade returns a nil conn and the response.
func (c *Client) DialAgent(name string) (*AgentConn, *Response) {
	c.T.Helper()
	h := http.Header{}
	if name != "" {
		h.Set(dto.HeaderAgent, name)
	}
	conn, resp := c.dialWS("/api/v1/agent/ws", h)
	if conn == nil {
		return nil, resp
	}
	return &AgentConn{T: c.T, Conn: conn, s: newFrameStream[AgentFrame](conn)}, resp
}

// Next returns the next frame, failing the test when none arrives within timeout.
func (a *AgentConn) Next(timeout time.Duration) AgentFrame {
	a.T.Helper()
	f, err := a.s.read(timeout)
	require.NoError(a.T, err, "no agent frame within %s", timeout)
	return f
}

// Welcome reads the next frame, which must be the welcome frame.
func (a *AgentConn) Welcome(timeout time.Duration) dto.AgentWelcomeFrame {
	a.T.Helper()
	var w dto.AgentWelcomeFrame
	a.nextOf(dto.AgentFrameTypeWelcome, &w, timeout)
	return w
}

// Message reads the next frame, which must be a message frame.
func (a *AgentConn) Message(timeout time.Duration) dto.AgentMessageFrame {
	a.T.Helper()
	var m dto.AgentMessageFrame
	a.nextOf(dto.AgentFrameTypeMessage, &m, timeout)
	return m
}

// Notice reads the next frame, which must be a notice frame.
func (a *AgentConn) Notice(timeout time.Duration) dto.AgentNoticeFrame {
	a.T.Helper()
	var n dto.AgentNoticeFrame
	a.nextOf(dto.AgentFrameTypeNotice, &n, timeout)
	return n
}

func (a *AgentConn) nextOf(typ dto.AgentFrameType, v any, timeout time.Duration) {
	a.T.Helper()
	f := a.Next(timeout)
	require.Equal(a.T, typ, f.Type, "frame: %s", f.Raw)
	require.NoError(a.T, json.Unmarshal(f.Raw, v))
}

// ExpectNone fails the test when any frame arrives within d.
func (a *AgentConn) ExpectNone(d time.Duration) {
	a.T.Helper()
	f, err := a.s.read(d)
	if err == nil {
		require.Failf(a.T, "unexpected agent frame", "%s", f.Raw)
	}
	require.True(a.T, errors.Is(err, context.DeadlineExceeded), "read: %v", err)
}

// Ack confirms a message, as the channel does after handing it to Claude Code.
func (a *AgentConn) Ack(messageID string) {
	a.T.Helper()
	data, err := json.Marshal(dto.AgentAckFrame{Type: dto.AgentFrameTypeAck, MessageID: messageID})
	require.NoError(a.T, err)
	ctx, cancel := context.WithTimeout(context.Background(), IOTimeout)
	defer cancel()
	require.NoError(a.T, a.Conn.Write(ctx, websocket.MessageText, data))
}

// Closed waits up to timeout for the server to close the connection and returns the close
// error (nil when still open). Use websocket.CloseStatus or errors.As with
// websocket.CloseError to read the code and reason.
func (a *AgentConn) Closed(timeout time.Duration) error { return a.s.closed(timeout) }

// Close closes the connection normally from the agent side.
func (a *AgentConn) Close() { _ = a.Conn.Close(websocket.StatusNormalClosure, "bye") }

// DialAgentUnique opens the agent WebSocket in unique mode (X-Partyline-Agent-Mode: unique),
// the way the channel connects under a derived name.
func (c *Client) DialAgentUnique(name string) (*AgentConn, *Response) {
	c.T.Helper()
	h := http.Header{}
	h.Set(dto.HeaderAgent, name)
	h.Set(dto.HeaderAgentMode, dto.AgentModeUnique)
	conn, resp := c.dialWS("/api/v1/agent/ws", h)
	if conn == nil {
		return nil, resp
	}
	return &AgentConn{T: c.T, Conn: conn, s: newFrameStream[AgentFrame](conn)}, resp
}

// DialAgentHeaders opens the agent WebSocket with the client's bearer token and the given
// identity headers (X-Partyline-Agent, -Agent-Mode, -Project, -Harness, -Machine).
func (c *Client) DialAgentHeaders(h http.Header) (*AgentConn, *Response) {
	c.T.Helper()
	conn, resp := c.dialWS("/api/v1/agent/ws", h)
	if conn == nil {
		return nil, resp
	}
	return &AgentConn{T: c.T, Conn: conn, s: newFrameStream[AgentFrame](conn)}, resp
}
