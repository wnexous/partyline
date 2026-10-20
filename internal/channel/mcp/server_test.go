package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const wait = 2 * time.Second

// harness runs a Server over in-memory pipes: tests write client lines to in and read the
// server's lines from out.
type harness struct {
	t    *testing.T
	in   *io.PipeWriter
	out  chan map[string]any
	raw  chan string
	srv  *Server
	done chan error
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	h := &harness{t: t, in: inW, out: make(chan map[string]any, 64), raw: make(chan string, 64), done: make(chan error, 1)}
	h.srv = NewServer(inR, outW, ServerInfo{Name: "partyline", Version: "test", Instructions: "test instructions"})
	h.srv.HandleTool(ToolDef{
		Name:        "reply",
		Description: "reply to a message",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"message_id":{"type":"string"},"text":{"type":"string"}},"required":["message_id","text"]}`),
	}, func(_ context.Context, args json.RawMessage) (ToolResult, error) {
		var a struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return ToolResult{}, err
		}
		if a.Text == "fail" {
			return ErrorResult("boom"), nil
		}
		return TextResult("sent " + a.Text), nil
	})
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			var m map[string]any
			require.NoError(t, json.Unmarshal([]byte(line), &m), "line: %s", line)
			h.raw <- line
			h.out <- m
		}
	}()
	go func() { h.done <- h.srv.Serve(context.Background()) }()
	t.Cleanup(func() { _ = inW.Close(); _ = outW.Close() })
	return h
}

func (h *harness) send(line string) {
	h.t.Helper()
	_, err := io.WriteString(h.in, line+"\n")
	require.NoError(h.t, err)
}

func (h *harness) next() map[string]any {
	h.t.Helper()
	select {
	case m := <-h.out:
		<-h.raw
		return m
	case <-time.After(wait):
		require.FailNow(h.t, "no output line")
		return nil
	}
}

func (h *harness) nextRaw() string {
	h.t.Helper()
	select {
	case line := <-h.raw:
		<-h.out
		return line
	case <-time.After(wait):
		require.FailNow(h.t, "no output line")
		return ""
	}
}

func (h *harness) expectNone(d time.Duration) {
	h.t.Helper()
	select {
	case m := <-h.out:
		require.Failf(h.t, "unexpected output", "%v", m)
	case <-time.After(d):
	}
}

func (h *harness) initialize(version string) map[string]any {
	h.t.Helper()
	h.send(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"` + version + `","capabilities":{},"clientInfo":{"name":"claude-code","version":"2.1.303"}}}`)
	return h.next()
}

// matches reports whether got equals want, where the string "<any>" in want matches any string.
func matches(want, got any) bool {
	switch w := want.(type) {
	case string:
		g, ok := got.(string)
		return ok && (w == "<any>" || w == g)
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok || len(g) != len(w) {
			return false
		}
		for k, v := range w {
			if gv, ok := g[k]; !ok || !matches(v, gv) {
				return false
			}
		}
		return true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return false
		}
		for i := range w {
			if !matches(w[i], g[i]) {
				return false
			}
		}
		return true
	default:
		return want == got
	}
}

type fixtureLine struct {
	Dir string         `json:"dir"`
	Msg map[string]any `json:"msg"`
}

func loadFixture(t *testing.T) []fixtureLine {
	t.Helper()
	data, err := os.ReadFile("testdata/claude-code-2.1.303.jsonl")
	require.NoError(t, err)
	var lines []fixtureLine
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var fl fixtureLine
		require.NoError(t, json.Unmarshal([]byte(l), &fl))
		lines = append(lines, fl)
	}
	return lines
}

// MCP-01, MCP-02: replaying the transcript recorded from Claude Code 2.1.303 yields every
// recorded answer: discover → -32601, initialize → the channel capability, and the channel
// notification after initialization.
func TestGoldenTranscriptClaudeCode2_1_303(t *testing.T) {
	h := newHarness(t)
	fixture := loadFixture(t)

	// The notification is produced before the client initializes; it must wait (edge case).
	notified := make(chan error, 1)
	go func() {
		notified <- h.srv.Notify("notifications/claude/channel", map[string]any{
			"content": "ping from the room", "meta": map[string]string{"chat_id": "room-1"},
		})
	}()

	var got []map[string]any
	collect := func() {
		for {
			select {
			case m := <-h.out:
				<-h.raw
				got = append(got, m)
			case <-time.After(300 * time.Millisecond):
				return
			}
		}
	}
	for _, fl := range fixture {
		if fl.Dir != "in" {
			continue
		}
		data, err := json.Marshal(fl.Msg)
		require.NoError(t, err)
		h.send(string(data))
		collect()
	}
	require.NoError(t, <-notified)

	// Every recorded server line has a matching output. Answers keep the request order and the
	// held notification follows the initialize answer.
	at := make([]int, 0, 3)
	for _, fl := range fixture {
		if fl.Dir != "out" {
			continue
		}
		idx := -1
		for i, m := range got {
			if matches(fl.Msg, m) {
				idx = i
				break
			}
		}
		require.GreaterOrEqualf(t, idx, 0, "recorded line %v not answered; outputs: %v", fl.Msg, got)
		at = append(at, idx)
	}
	require.Len(t, at, 3)
	require.Less(t, at[0], at[1], "discover answered after initialize")
	require.Less(t, at[1], at[2], "notification emitted before initialize was answered")

	// Responses to the unrecorded requests exist too: tools/list (id 1) and tools/call (id 2).
	byID := map[float64]map[string]any{}
	for _, m := range got {
		if id, ok := m["id"].(float64); ok {
			byID[id] = m
		}
	}
	require.Contains(t, byID, float64(1))
	require.Contains(t, byID, float64(2))
	require.Equal(t, map[string]any{"content": []any{map[string]any{"type": "text", "text": "sent pong"}}, "isError": false}, byID[2]["result"])
}

// MCP-01: server/discover is answered with JSON-RPC error -32601, echoing a string id.
func TestDiscoverAnswersMethodNotFound(t *testing.T) {
	h := newHarness(t)
	h.send(`{"jsonrpc":"2.0","id":"server-discover-probe-1","method":"server/discover","params":{}}`)
	m := h.next()
	require.Equal(t, "server-discover-probe-1", m["id"])
	require.Equal(t, "2.0", m["jsonrpc"])
	require.Equal(t, float64(-32601), m["error"].(map[string]any)["code"])
	require.NotContains(t, m, "result")
}

// MCP-02: initialize returns exactly the channel and tools capabilities, the server name and
// the instructions; the protocol version is the client's when it is 2025-11-25 or older,
// otherwise 2025-11-25.
func TestInitializeNegotiatesVersion(t *testing.T) {
	for client, want := range map[string]string{
		"2026-07-28": "2025-11-25",
		"2025-11-25": "2025-11-25",
		"2025-06-18": "2025-06-18",
		"2024-11-05": "2024-11-05",
	} {
		t.Run(client, func(t *testing.T) {
			h := newHarness(t)
			m := h.initialize(client)
			require.Equal(t, float64(0), m["id"])
			res := m["result"].(map[string]any)
			require.Equal(t, want, res["protocolVersion"])
			require.Equal(t, map[string]any{"experimental": map[string]any{"claude/channel": map[string]any{}}, "tools": map[string]any{}}, res["capabilities"])
			require.Equal(t, map[string]any{"name": "partyline", "version": "test"}, res["serverInfo"])
			require.Equal(t, "test instructions", res["instructions"])
		})
	}
}

// MCP-03: the permission relay capability is never declared.
func TestNoPermissionCapability(t *testing.T) {
	h := newHarness(t)
	h.send(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2026-07-28","capabilities":{}}}`)
	line := h.nextRaw()
	require.Contains(t, line, `"claude/channel"`)
	require.NotContains(t, line, "claude/channel/permission")
}

// Edge case (AGT-05): a notification produced before notifications/initialized is held and
// emitted after it.
func TestNotificationHeldUntilInitialized(t *testing.T) {
	h := newHarness(t)
	notified := make(chan error, 1)
	go func() {
		notified <- h.srv.Notify("notifications/claude/channel", map[string]any{"content": "hi", "meta": map[string]string{"room": "r1"}})
	}()
	h.initialize("2025-11-25")
	h.expectNone(200 * time.Millisecond)
	select {
	case err := <-notified:
		require.FailNow(t, "Notify returned before the notification was written", "err: %v", err)
	default:
	}

	h.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	m := h.next()
	require.Equal(t, "notifications/claude/channel", m["method"])
	require.Equal(t, map[string]any{"content": "hi", "meta": map[string]any{"room": "r1"}}, m["params"])
	require.NotContains(t, m, "id")
	require.NoError(t, <-notified)

	// After initialization notifications go out at once.
	require.NoError(t, h.srv.Notify("notifications/claude/channel", map[string]any{"content": "again"}))
	require.Equal(t, "again", h.next()["params"].(map[string]any)["content"])
}

// Design: ping → {}, unknown request → -32601, unknown notification ignored.
func TestPingAndUnknownMethods(t *testing.T) {
	h := newHarness(t)
	h.send(`{"jsonrpc":"2.0","id":7,"method":"ping"}`)
	m := h.next()
	require.Equal(t, float64(7), m["id"])
	require.Equal(t, map[string]any{}, m["result"])

	h.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":3}}`)
	h.expectNone(150 * time.Millisecond)

	h.send(`{"jsonrpc":"2.0","id":"x-1","method":"resources/list"}`)
	m = h.next()
	require.Equal(t, "x-1", m["id"])
	require.Equal(t, float64(-32601), m["error"].(map[string]any)["code"])
}

// Design: tools/list lists the registered tools; tools/call runs them, tool failures are
// results with isError, an unknown tool is a JSON-RPC error.
func TestToolsListAndCall(t *testing.T) {
	h := newHarness(t)
	h.send(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := h.next()["result"].(map[string]any)["tools"].([]any)
	require.Len(t, tools, 1)
	tool := tools[0].(map[string]any)
	require.Equal(t, "reply", tool["name"])
	require.Equal(t, "reply to a message", tool["description"])
	require.Equal(t, "object", tool["inputSchema"].(map[string]any)["type"])

	h.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"reply","arguments":{"message_id":"m","text":"fail"}}}`)
	res := h.next()["result"].(map[string]any)
	require.Equal(t, true, res["isError"])
	require.Equal(t, []any{map[string]any{"type": "text", "text": "boom"}}, res["content"])

	h.send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nope","arguments":{}}}`)
	m := h.next()
	require.Equal(t, float64(3), m["id"])
	require.Equal(t, float64(-32602), m["error"].(map[string]any)["code"])
}

// Design: Serve returns nil when stdin reaches EOF; later notifications fail.
func TestServeReturnsNilOnEOF(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, h.in.Close())
	select {
	case err := <-h.done:
		require.NoError(t, err)
	case <-time.After(wait):
		require.FailNow(t, "Serve did not return on EOF")
	}
	require.ErrorIs(t, h.srv.Notify("notifications/claude/channel", map[string]any{}), ErrClosed)
}
