package testkit

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
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

// Machine is the hostname of the AI sessions the kit starts.
const Machine = "testbox"

// FolderOf is the session of the AI called name in the tests: Claude Code in the folder
// /work/<name> on the machine Machine. Its display name is name with an upper-case first letter
// and its handle is name (when no other intelligence has it).
func FolderOf(name string) Folder {
	return Folder{Machine: Machine, Path: "/work/" + name, Harness: "claude"}
}

// DialAgent opens the agent WebSocket with the client's bearer token as the AI of FolderOf(name).
// A refused upgrade returns a nil conn and the response.
func (c *Client) DialAgent(name string) (*AgentConn, *Response) {
	c.T.Helper()
	return c.DialFolder(FolderOf(name))
}

// AsAgent returns a copy of the client that sends the folder headers of FolderOf(name) with
// every request: an agent token then acts as that AI.
func (c *Client) AsAgent(name string) *Client { return c.AsFolder(FolderOf(name)) }

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

// DialAgentHeaders opens the agent WebSocket with the client's bearer token and the given
// headers (the folder headers, see Folder.Headers).
func (c *Client) DialAgentHeaders(h http.Header) (*AgentConn, *Response) {
	c.T.Helper()
	conn, resp := c.dialWS("/api/v1/agent/ws", h)
	if conn == nil {
		return nil, resp
	}
	return &AgentConn{T: c.T, Conn: conn, s: newFrameStream[AgentFrame](conn)}, resp
}

// Folder is the identity an AI session sends with every connection and tool call: the machine
// and canonical folder it works in and, optionally, the home folder, the harness and the
// handle or v0.3 name of an unlinked AI to adopt (AID-07, MIG-02). Live is the id of the
// process that holds the WebSocket (DialFolder gives every dial its own when it is empty, as
// every process has its own: pass one to reconnect as the same process) and Takeover asks to
// take over the AI's live session (SES-02).
type Folder struct {
	Machine  string
	Path     string
	Home     string
	Harness  string
	Adopt    string
	Live     string
	Takeover bool
}

// Headers returns the folder headers the way the CLI sends them: the folder and the home
// folder percent-encoded, the others as they are; empty fields are left out.
func (f Folder) Headers() http.Header {
	h := http.Header{}
	for k, v := range map[string]string{
		dto.HeaderMachine: f.Machine, dto.HeaderHarness: f.Harness, dto.HeaderAdopt: f.Adopt,
		dto.HeaderFolder: escapePath(f.Path), dto.HeaderHome: escapePath(f.Home), dto.HeaderLive: f.Live,
	} {
		if v != "" {
			h.Set(k, v)
		}
	}
	if f.Takeover {
		h.Set(dto.HeaderTakeover, "1")
	}
	return h
}

func escapePath(p string) string {
	if p == "" {
		return ""
	}
	return url.PathEscape(p)
}

// AsFolder returns a copy of the client (sharing its cookies and token) that sends the folder
// headers with every request: an agent token then acts as the AI of the folder.
func (c *Client) AsFolder(f Folder) *Client {
	cp := *c
	cp.header = c.header.Clone()
	for k, v := range f.Headers() {
		cp.header[k] = v
	}
	return &cp
}

// DialFolderSilent opens the agent WebSocket for f and never reads from it, like a client whose
// network went silent: the pings it receives are never answered.
func (c *Client) DialFolderSilent(f Folder) *websocket.Conn {
	c.T.Helper()
	if f.Live == "" {
		f.Live = NewLiveID()
	}
	conn, resp := c.dialWS("/api/v1/agent/ws", f.Headers())
	require.NotNil(c.T, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	return conn
}

// DialFolder opens the agent WebSocket as the AI of the folder. A dial without a Live id is a
// process of its own.
func (c *Client) DialFolder(f Folder) (*AgentConn, *Response) {
	c.T.Helper()
	if f.Live == "" {
		f.Live = NewLiveID()
	}
	return c.DialAgentHeaders(f.Headers())
}

// NewLiveID returns a random live id, the way a process makes its own.
func NewLiveID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}
