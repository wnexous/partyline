// Package mcp is a minimal Model Context Protocol server over newline-delimited JSON-RPC 2.0
// on stdio, just enough for a Claude Code channel (AD-004): it answers server/discover with
// -32601 so Claude Code falls back to initialize, declares the claude/channel capability,
// serves tools and pushes notifications.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// LatestProtocolVersion is the newest protocol revision the server speaks. A client asking for
// a newer one gets this version; older ones are echoed (MCP-02, AD-004).
const LatestProtocolVersion = "2025-11-25"

// JSON-RPC error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// ErrClosed is returned by Notify once Serve has returned.
var ErrClosed = errors.New("mcp: server closed")

// ServerInfo identifies the server in the initialize answer.
type ServerInfo struct {
	Name         string
	Version      string
	Instructions string
}

// ToolDef describes a tool in tools/list.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Content is one content block of a tool result.
type Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ToolResult is the result of tools/call. IsError marks a tool failure the model should see.
type ToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError"`
}

// TextResult is a successful result with one text block.
func TextResult(text string) ToolResult {
	return ToolResult{Content: []Content{{Type: "text", Text: text}}}
}

// ErrorResult is a failed result with one text block.
func ErrorResult(text string) ToolResult {
	return ToolResult{Content: []Content{{Type: "text", Text: text}}, IsError: true}
}

// ToolFunc handles one tools/call. A returned error becomes an isError result with its text.
type ToolFunc func(ctx context.Context, args json.RawMessage) (ToolResult, error)

type tool struct {
	def ToolDef
	fn  ToolFunc
}

// Server is an MCP server bound to one input and one output stream.
type Server struct {
	in   io.Reader
	out  io.Writer
	info ServerInfo

	tools []tool

	wmu sync.Mutex // serializes writes; each message is one line

	initOnce    sync.Once
	initialized chan struct{}
	listedOnce  sync.Once
	listed      chan struct{}
	readyOnce   sync.Once
	ready       chan struct{}
	// readyDelay and readyFallback are ReadyDelay and ReadyFallback when the server was made.
	readyDelay, readyFallback time.Duration
	doneOnce                  sync.Once
	done                      chan struct{}
}

// NewServer returns a server reading requests from in and writing to out.
func NewServer(in io.Reader, out io.Writer, info ServerInfo) *Server {
	return &Server{
		in: in, out: out, info: info, initialized: make(chan struct{}), listed: make(chan struct{}),
		ready: make(chan struct{}), done: make(chan struct{}), readyDelay: ReadyDelay, readyFallback: ReadyFallback,
	}
}

// HandleTool registers a tool. Call it before Serve.
func (s *Server) HandleTool(def ToolDef, fn ToolFunc) {
	s.tools = append(s.tools, tool{def: def, fn: fn})
}

// Readiness waits (see Ready); NewServer reads them, tests shorten them.
var (
	// ReadyDelay is the wait after the first tools/list answer before notifications go out.
	ReadyDelay = 2 * time.Second
	// ReadyFallback is how long after notifications/initialized a client that never lists
	// the tools is considered ready.
	ReadyFallback = 5 * time.Second
)

// Notify sends a JSON-RPC notification. Until the client is ready for notifications (Ready)
// it blocks, so no notification reaches a client that would drop it; it returns once the
// line is written, or ErrClosed when the server stops first.
func (s *Server) Notify(method string, params any) error {
	select {
	case <-s.Ready():
	case <-s.done:
		return ErrClosed
	}
	select {
	case <-s.done:
		return ErrClosed
	default:
	}
	return s.write(notification{JSONRPC: "2.0", Method: method, Params: params})
}

// Initialized is closed once the client has sent notifications/initialized.
func (s *Server) Initialized() <-chan struct{} { return s.initialized }

// Ready is closed once the client takes notifications: ReadyDelay after the answer to its
// first tools/list (following notifications/initialized), or ReadyFallback after
// notifications/initialized when it does not list the tools. Claude Code 2.1.304 drops
// channel notifications that arrive right after it initializes, before it has set the
// channel up: the backlog a session gets on connect was acknowledged but never shown
// (agent-identity UAT; 1.5 s after tools/list was enough there).
func (s *Server) Ready() <-chan struct{} {
	s.readyOnce.Do(func() {
		go func() {
			select {
			case <-s.initialized:
			case <-s.done:
				return
			}
			select {
			case <-s.listed:
				select {
				case <-time.After(s.readyDelay):
				case <-s.done:
					return
				}
			case <-time.After(s.readyFallback):
			case <-s.done:
				return
			}
			close(s.ready)
		}()
	})
	return s.ready
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

func (s *Server) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err = s.out.Write(data)
	return err
}

// Serve reads requests until the input ends (returns nil) or ctx is done. Tool calls run
// concurrently; Serve waits for them before returning.
func (s *Server) Serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
		s.doneOnce.Do(func() { close(s.done) })
	}()

	lines := make(chan []byte)
	readErr := make(chan error, 1)
	go func() {
		r := bufio.NewReader(s.in)
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 {
				select {
				case lines <- line:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				readErr <- err
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-readErr:
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		case line := <-lines:
			if err := s.handle(ctx, &wg, line); err != nil {
				return err
			}
		}
	}
}

// handle processes one input line; it returns an error only when writing fails.
func (s *Server) handle(ctx context.Context, wg *sync.WaitGroup, line []byte) error {
	if len(bytes.TrimSpace(line)) == 0 {
		return nil
	}
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return s.fail(json.RawMessage("null"), CodeParseError, "Parse error")
	}
	if req.Method == "" {
		return nil // a response to a request we never send, or junk without an id
	}
	if len(req.ID) == 0 {
		if req.Method == "notifications/initialized" {
			s.initOnce.Do(func() { close(s.initialized) })
		}
		return nil // other notifications need no answer
	}
	switch req.Method {
	case "initialize":
		return s.reply(req.ID, s.initializeResult(req.Params))
	case "ping":
		return s.reply(req.ID, struct{}{})
	case "tools/list":
		defs := make([]ToolDef, len(s.tools))
		for i, t := range s.tools {
			defs[i] = t.def
		}
		err := s.reply(req.ID, map[string]any{"tools": defs})
		s.listedOnce.Do(func() { close(s.listed) })
		return err
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return s.fail(req.ID, CodeInvalidParams, "Invalid params")
		}
		fn := s.lookup(p.Name)
		if fn == nil {
			return s.fail(req.ID, CodeInvalidParams, fmt.Sprintf("Unknown tool: %s", p.Name))
		}
		if len(p.Arguments) == 0 {
			p.Arguments = json.RawMessage("{}")
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := fn(ctx, p.Arguments)
			if err != nil {
				res = ErrorResult(err.Error())
			}
			_ = s.reply(req.ID, res)
		}()
		return nil
	default:
		// server/discover lands here on purpose: Claude Code then falls back to initialize (MCP-01).
		return s.fail(req.ID, CodeMethodNotFound, "Method not found")
	}
}

func (s *Server) lookup(name string) ToolFunc {
	for _, t := range s.tools {
		if t.def.Name == name {
			return t.fn
		}
	}
	return nil
}

func (s *Server) initializeResult(params json.RawMessage) map[string]any {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &p)
	version := LatestProtocolVersion
	// Revisions are dates (YYYY-MM-DD), so they compare as strings.
	if p.ProtocolVersion != "" && p.ProtocolVersion <= LatestProtocolVersion {
		version = p.ProtocolVersion
	}
	res := map[string]any{
		"protocolVersion": version,
		// Only the channel and tools: never claude/channel/permission (MCP-03, AD-006).
		"capabilities": map[string]any{
			"experimental": map[string]any{"claude/channel": struct{}{}},
			"tools":        struct{}{},
		},
		"serverInfo": map[string]string{"name": s.info.Name, "version": s.info.Version},
	}
	if s.info.Instructions != "" {
		res["instructions"] = s.info.Instructions
	}
	return res
}

func (s *Server) reply(id json.RawMessage, result any) error {
	return s.write(response{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *Server) fail(id json.RawMessage, code int, msg string) error {
	return s.write(response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}
