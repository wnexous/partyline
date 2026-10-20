package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
	"github.com/wnexous/partyline/internal/users"
)

const chanWait = 10 * time.Second

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// mcpClient drives `partyline channel` over pipes the way Claude Code does.
type mcpClient struct {
	t      *testing.T
	in     *io.PipeWriter
	lines  chan map[string]any
	stderr *lockedBuffer
	code   chan int
}

func startChannel(t *testing.T, args []string, env map[string]string, wd string) *mcpClient {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := &mcpClient{t: t, in: inW, lines: make(chan map[string]any, 64), stderr: &lockedBuffer{}, code: make(chan int, 1)}
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 1<<20), 4<<20)
		for sc.Scan() {
			var m map[string]any
			if json.Unmarshal(sc.Bytes(), &m) == nil {
				c.lines <- m
			}
		}
	}()
	std := stdio{
		stdin: inR, stdout: outW, stderr: c.stderr,
		getenv: func(k string) string { return env[k] },
		getwd:  func() (string, error) { return wd, nil },
	}
	go func() {
		c.code <- execute(context.Background(), append([]string{"partyline", "channel"}, args...), std)
		_ = outW.Close()
	}()
	t.Cleanup(func() { _ = inW.Close() })
	return c
}

func (c *mcpClient) send(v any) {
	c.t.Helper()
	data, err := json.Marshal(v)
	require.NoError(c.t, err)
	_, err = c.in.Write(append(data, '\n'))
	require.NoError(c.t, err)
}

func (c *mcpClient) next() map[string]any {
	c.t.Helper()
	select {
	case m := <-c.lines:
		return m
	case <-time.After(chanWait):
		require.FailNow(c.t, "no output from the channel", "stderr: %s", c.stderr.String())
		return nil
	}
}

// channelEnv is a server with alice and her agent token.
type channelEnv struct {
	env    *testkit.Env
	alice  *testkit.Client
	secret string
}

func newChannelEnv(t *testing.T) *channelEnv {
	t.Helper()
	env := testkit.New(t)
	alice := env.NewUser("alice")
	_, secret, err := env.App.Users.CreateToken(context.Background(), alice.User.ID, users.TokenInput{Kind: dto.TokenKindAgent, Name: "laptop"})
	require.NoError(t, err)
	return &channelEnv{env: env, alice: alice, secret: secret}
}

// agent waits until alice's agent named name is listed online and returns it.
func (e *channelEnv) agent(t *testing.T, name string) dto.Agent {
	t.Helper()
	var found dto.Agent
	require.Eventually(t, func() bool {
		list := testkit.Decode[dto.List[dto.Agent]](e.alice.Get("/api/v1/agents").RequireStatus(http.StatusOK))
		for _, a := range list.Items {
			if a.Name == name && a.Online {
				found = a
				return true
			}
		}
		return false
	}, chanWait, 20*time.Millisecond)
	return found
}

// AGT-01, MCP-01, TOOL-02 end to end: Claude Code's handshake, the agent comes online under
// the slug of the working directory name, a room message arrives as a channel notification
// and the reply tool stores the reply in the room.
func TestChannelEndToEnd(t *testing.T) {
	e := newChannelEnv(t)
	c := startChannel(t, nil, map[string]string{"PARTYLINE_URL": e.env.URL, "PARTYLINE_TOKEN": e.secret}, "/home/someone/src/My Project")

	c.send(map[string]any{"jsonrpc": "2.0", "id": "server-discover-probe-1", "method": "server/discover", "params": map[string]any{}})
	m := c.next()
	require.Equal(t, "server-discover-probe-1", m["id"])
	require.Equal(t, float64(-32601), m["error"].(map[string]any)["code"])

	c.send(map[string]any{"jsonrpc": "2.0", "id": 0, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "claude-code", "version": "2.1.303"},
	}})
	res := c.next()["result"].(map[string]any)
	require.Equal(t, "2025-11-25", res["protocolVersion"])
	require.Equal(t, map[string]any{"experimental": map[string]any{"claude/channel": map[string]any{}}, "tools": map[string]any{}}, res["capabilities"])
	require.Equal(t, "partyline", res["serverInfo"].(map[string]any)["name"])
	c.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})

	agent := e.agent(t, "my-project")
	room := testkit.Decode[dto.Room](e.alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "Support"}).RequireStatus(http.StatusCreated))
	e.alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: agent.ID}).RequireStatus(http.StatusCreated)
	question := testkit.Decode[dto.Message](e.alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "reply with pong"}).RequireStatus(http.StatusCreated))

	n := c.next()
	require.Equal(t, "notifications/claude/channel", n["method"])
	params := n["params"].(map[string]any)
	require.Equal(t, "reply with pong", params["content"])
	meta := params["meta"].(map[string]any)
	require.Equal(t, question.ID, meta["message_id"])
	require.Equal(t, room.ID, meta["room"])
	require.Equal(t, "true", meta["trusted"])

	c.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
		"name": "reply", "arguments": map[string]any{"message_id": question.ID, "text": "pong"},
		"_meta": map[string]any{"claudecode/toolUseId": "toolu_01SyntheticFixture000001", "progressToken": 2},
	}})
	r := c.next()
	require.Equal(t, float64(2), r["id"])
	result := r["result"].(map[string]any)
	require.Equal(t, false, result["isError"], "result: %v", result)

	page := testkit.Decode[dto.Page[dto.Message]](e.alice.Get("/api/v1/rooms/" + room.ID + "/messages").RequireStatus(http.StatusOK))
	reply := page.Items[0]
	require.Equal(t, "pong", reply.Text)
	require.Equal(t, question.ID, reply.ReplyTo)
	require.Equal(t, dto.AuthorKindAgent, reply.AuthorKind)
	require.Equal(t, agent.ID, reply.AuthorID)

	// Claude Code exits: stdin closes, the command exits 0 and the agent goes offline.
	require.NoError(t, c.in.Close())
	select {
	case code := <-c.code:
		require.Equal(t, 0, code, "stderr: %s", c.stderr.String())
	case <-time.After(chanWait):
		require.FailNow(t, "channel did not exit")
	}
	require.Eventually(t, func() bool { return !e.env.App.Gateway.Online(agent.ID) }, chanWait, 20*time.Millisecond)
}

// The --url, --token and --agent flags win over the environment.
func TestChannelFlagsOverrideEnvironment(t *testing.T) {
	e := newChannelEnv(t)
	c := startChannel(t, []string{"--url", e.env.URL, "--token", e.secret, "--agent", "Night Shift"},
		map[string]string{"PARTYLINE_URL": "http://127.0.0.1:1", "PARTYLINE_TOKEN": "pl_agent_wrong", "PARTYLINE_AGENT": "ignored"}, "/tmp/wd")
	c.send(map[string]any{"jsonrpc": "2.0", "id": 0, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25"}})
	c.next()
	c.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	e.agent(t, "night-shift")
}

// PARTYLINE_AGENT names the agent when no flag is given.
func TestChannelAgentFromEnvironment(t *testing.T) {
	e := newChannelEnv(t)
	startChannel(t, nil, map[string]string{"PARTYLINE_URL": e.env.URL, "PARTYLINE_TOKEN": e.secret, "PARTYLINE_AGENT": "billing-api"}, "/tmp/wd")
	e.agent(t, "billing-api")
}

// Without a URL or token the command fails with usage exit code 2.
func TestChannelRequiresURLAndToken(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"no url":   {"PARTYLINE_TOKEN": "pl_agent_x"},
		"no token": {"PARTYLINE_URL": "http://127.0.0.1:1"},
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			code := execute(context.Background(), []string{"partyline", "channel"}, stdio{
				stdin: bytes.NewReader(nil), stdout: &out, stderr: &out, getenv: func(k string) string { return env[k] },
			})
			require.Equal(t, 2, code)
			require.Contains(t, out.String(), "PARTYLINE_URL and PARTYLINE_TOKEN")
		})
	}
}
