package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/channel"
	"github.com/wnexous/partyline/internal/handles"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
	"github.com/wnexous/partyline/internal/users"
)

// chanWait bounds every wait for channel output or state; waits return as soon as it arrives.
const chanWait = time.Minute

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
	return startChannelWith(t, args, env, wd, nil)
}

// startChannelWith is startChannel for a process whose nearest launched ancestor has the
// launcher variables ancestor (nil: none).
func startChannelWith(t *testing.T, args []string, env map[string]string, wd string, ancestor map[string]string) *mcpClient {
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
		getenv:   func(k string) string { return env[k] },
		getwd:    func() (string, error) { return wd, nil },
		hostname: func() (string, error) { return testMachine, nil },
	}
	if ancestor != nil {
		std.launcherEnv = func() map[string]string { return ancestor }
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

// testMachine is the hostname every CLI test process reports.
const testMachine = "testbox"

// aiAt creates alice's AI of the folder on the test machine, as its first session would, and
// returns it.
func (e *channelEnv) aiAt(t *testing.T, folder string) db.Agent {
	t.Helper()
	a, err := e.env.App.Agents.ResolveFolder(context.Background(), e.alice.User.ID, agents.FolderID{Machine: testMachine, Folder: folder}, false)
	require.NoError(t, err)
	return a
}

// legacy creates an identity of alice the way v0.3 left it, one without a folder, and returns its
// id (MIG-02).
func (e *channelEnv) legacy(t *testing.T, name string) string {
	t.Helper()
	ctx := context.Background()
	id := store.NewID()
	require.NoError(t, e.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateAgent(ctx, db.CreateAgentParams{ID: id, OwnerID: e.alice.User.ID, Name: name, CreatedAt: time.Now().UnixMilli()}); err != nil {
			return err
		}
		_, err := intelligences.Create(ctx, q, intelligences.NewParams{ID: id, Kind: handles.AI, DisplayName: name, HandleBase: name, Now: time.Now()})
		return err
	}))
	return id
}

// aiOf returns the profile of alice's AI of the folder (any machine), and whether it is online.
func (e *channelEnv) aiOf(t *testing.T, folder string) (dto.IntelligenceProfile, bool) {
	t.Helper()
	for _, a := range testkit.Decode[dto.List[dto.Agent]](e.alice.Get("/api/v1/agents").RequireStatus(http.StatusOK)).Items {
		prof := testkit.Decode[dto.IntelligenceProfile](e.alice.Get("/api/v1/intelligences/" + a.ID).RequireStatus(http.StatusOK))
		if prof.AI != nil && prof.AI.Folder == folder {
			return prof, a.Online
		}
	}
	return dto.IntelligenceProfile{}, false
}

// ai waits until alice's AI of the folder is online and returns its profile.
func (e *channelEnv) ai(t *testing.T, folder string) dto.IntelligenceProfile {
	t.Helper()
	var found dto.IntelligenceProfile
	require.Eventually(t, func() bool {
		prof, online := e.aiOf(t, folder)
		found = prof
		return prof.ID != "" && online
	}, chanWait, 20*time.Millisecond, "no online AI in %s", folder)
	return found
}

// registered waits until alice has an AI of the folder, online or not, and returns its profile.
func (e *channelEnv) registered(t *testing.T, folder string) dto.IntelligenceProfile {
	t.Helper()
	var found dto.IntelligenceProfile
	require.Eventually(t, func() bool {
		found, _ = e.aiOf(t, folder)
		return found.ID != ""
	}, chanWait, 20*time.Millisecond, "no AI in %s", folder)
	return found
}

// folders lists the folders of alice's AIs.
func (e *channelEnv) folders(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, a := range testkit.Decode[dto.List[dto.Agent]](e.alice.Get("/api/v1/agents").RequireStatus(http.StatusOK)).Items {
		prof := testkit.Decode[dto.IntelligenceProfile](e.alice.Get("/api/v1/intelligences/" + a.ID).RequireStatus(http.StatusOK))
		out = append(out, prof.AI.Folder)
	}
	slices.Sort(out)
	return out
}

// AGT-01, MCP-01, TOOL-02, AID-01 end to end: Claude Code's handshake, the AI of the working
// directory comes online with the directory's name, a room message arrives as a channel
// notification and the reply tool stores the reply in the room.
func TestChannelEndToEnd(t *testing.T) {
	e := newChannelEnv(t)
	c := startChannel(t, nil, map[string]string{"PARTYLINE_URL": e.env.URL, "PARTYLINE_TOKEN": e.secret, "HOME": "/home/someone"}, "/home/someone/src/My Project")

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
	require.Equal(t, channel.Instructions, res["instructions"], "MCP-02: the real channel instructions")
	c.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})

	prof := e.ai(t, "/home/someone/src/My Project")
	agent := prof.AI
	require.Equal(t, []any{"My Project", "my-project", "claude", testMachine, "~/src/My Project"},
		[]any{prof.DisplayName, prof.Handle, agent.App, agent.Machine, agent.FolderLabel},
		"AID-01: the session's machine, folder and harness reach the server")
	room := testkit.Decode[dto.Room](e.alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "Support"}).RequireStatus(http.StatusCreated))
	e.alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: prof.ID}).RequireStatus(http.StatusCreated)
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
	require.Equal(t, prof.ID, reply.AuthorID)

	// Claude Code exits: stdin closes, the command exits 0 and the agent goes offline.
	require.NoError(t, c.in.Close())
	select {
	case code := <-c.code:
		require.Equal(t, 0, code, "stderr: %s", c.stderr.String())
	case <-time.After(chanWait):
		require.FailNow(t, "channel did not exit")
	}
	require.Eventually(t, func() bool { return !e.env.App.Gateway.Online(prof.ID) }, chanWait, 20*time.Millisecond)
}

// The --url, --token and --as flags win over the environment: the legacy identity the flag
// names is adopted, not the one PARTYLINE_AGENT names.
func TestChannelFlagsOverrideEnvironment(t *testing.T) {
	e := newChannelEnv(t)
	flagged := e.legacy(t, "night-shift")
	ignored := e.legacy(t, "ignored")
	c := startChannel(t, []string{"--url", e.env.URL, "--token", e.secret, "--as", "Night Shift"},
		map[string]string{"PARTYLINE_URL": "http://127.0.0.1:1", "PARTYLINE_TOKEN": "pl_agent_wrong", "PARTYLINE_AGENT": "ignored"}, "/tmp/wd")
	c.send(map[string]any{"jsonrpc": "2.0", "id": 0, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25"}})
	c.next()
	c.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	require.Equal(t, flagged, e.ai(t, "/tmp/wd").ID)
	prof, _ := e.aiOf(t, "")
	require.Equal(t, ignored, prof.ID, "the other legacy identity stays unlinked")
}

// PARTYLINE_AGENT names the identity to adopt when no flag is given.
func TestChannelAgentFromEnvironment(t *testing.T) {
	e := newChannelEnv(t)
	billing := e.legacy(t, "billing-api")
	startChannel(t, nil, map[string]string{"PARTYLINE_URL": e.env.URL, "PARTYLINE_TOKEN": e.secret, "PARTYLINE_AGENT": "billing-api"}, "/tmp/wd")
	require.Equal(t, billing, e.ai(t, "/tmp/wd").ID)
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
