package channel

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
	"github.com/wnexous/partyline/internal/users"
)

// wait bounds every wait for a signal (a frame, a notification, a response). Waits return as
// soon as the signal arrives, so the bound only matters on a loaded machine.
const wait = time.Minute

// fastTiming keeps reconnect tests short.
var fastTiming = timing{minBackoff: 50 * time.Millisecond, maxBackoff: 400 * time.Millisecond, invalidRetry: 150 * time.Millisecond, limitPause: 700 * time.Millisecond}

// builderFolder is the folder of alice's AI "builder" in the tests: the machine "testbox".
var builderFolder = agents.FolderID{Machine: "testbox", Folder: "/work/builder", Harness: "claude"}

// builderIdentity is what a channel in builderFolder sends about itself.
var builderIdentity = Identity{Machine: builderFolder.Machine, Folder: builderFolder.Folder, Harness: builderFolder.Harness}

// chanEnv is a Partyline server with alice, her agent token and a room "Support" that has the
// AI of her folder /work/builder (display name "Builder") as a member.
type chanEnv struct {
	t       *testing.T
	env     *testkit.Env
	alice   *testkit.Client
	secret  string
	agentID string
	room    dto.Room
}

func newChanEnv(t *testing.T, opts ...testkit.Option) *chanEnv {
	t.Helper()
	env := testkit.New(t, opts...)
	alice := env.NewUser("alice")
	_, secret, err := env.App.Users.CreateToken(context.Background(), alice.User.ID, users.TokenInput{Kind: dto.TokenKindAgent, Name: "laptop"})
	require.NoError(t, err)
	a, err := env.App.Agents.ResolveFolder(context.Background(), alice.User.ID, builderFolder, false)
	require.NoError(t, err)
	room := testkit.Decode[dto.Room](alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "Support"}).RequireStatus(http.StatusCreated))
	alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: a.ID}).RequireStatus(http.StatusCreated)
	return &chanEnv{t: t, env: env, alice: alice, secret: secret, agentID: a.ID, room: room}
}

func (e *chanEnv) post(text string) dto.Message {
	e.t.Helper()
	return testkit.Decode[dto.Message](e.alice.Post("/api/v1/rooms/"+e.room.ID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
}

func (e *chanEnv) cursor() int64 {
	seq, err := e.env.App.Store.R().GetAgentCursor(context.Background(), db.GetAgentCursorParams{AgentID: e.agentID, RoomID: e.room.ID})
	if err != nil {
		return 0
	}
	return seq
}

func (e *chanEnv) online() bool { return e.env.App.Gateway.Online(e.agentID) }

// options start a channel in the folder of the AI "builder".
func (e *chanEnv) options() Options {
	return Options{
		URL: e.env.URL, Token: e.secret, Machine: builderFolder.Machine, Folder: builderFolder.Folder,
		TempDir: e.t.TempDir(), Version: "test", timing: fastTiming,
	}
}

// syncBuffer is a goroutine-safe bytes.Buffer for stderr.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// session is a running channel.Run driven over pipes, as Claude Code drives the process. The
// notice the channel sends when it first connects, which says who the AI is and who its owner is
// (reason "identity"), is kept apart from the room notifications and notices in notes, so the
// tests of messages read them in the order they always had; identityNotice returns it.
type session struct {
	t        *testing.T
	in       *io.PipeWriter
	notes    chan Notification
	identity chan Notification
	resps    chan map[string]any
	stderr   *syncBuffer
	done     chan error
	nextID   int
}

func startSession(t *testing.T, o Options) *session {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &session{t: t, in: inW, notes: make(chan Notification, 256), identity: make(chan Notification, 16), resps: make(chan map[string]any, 64), stderr: &syncBuffer{}, done: make(chan error, 1), nextID: 100}
	o.Stdin, o.Stdout, o.Stderr = inR, outW, s.stderr
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 1<<20), 4<<20)
		for sc.Scan() {
			var m map[string]any
			if json.Unmarshal(sc.Bytes(), &m) != nil {
				continue
			}
			if m["method"] == NotificationMethod {
				var n struct {
					Params Notification `json:"params"`
				}
				_ = json.Unmarshal(sc.Bytes(), &n)
				if n.Params.Meta["reason"] == "identity" {
					s.identity <- n.Params
					continue
				}
				s.notes <- n.Params
				continue
			}
			s.resps <- m
		}
	}()
	finished := make(chan struct{})
	go func() {
		s.done <- Run(context.Background(), o)
		_ = outW.Close()
		close(finished)
	}()
	t.Cleanup(func() {
		_ = inW.Close()
		select {
		case <-finished:
		case <-time.After(wait):
		}
	})
	return s
}

func (s *session) send(v any) {
	s.t.Helper()
	data, err := json.Marshal(v)
	require.NoError(s.t, err)
	_, err = s.in.Write(append(data, '\n'))
	require.NoError(s.t, err)
}

func (s *session) response() map[string]any {
	s.t.Helper()
	select {
	case m := <-s.resps:
		return m
	case <-time.After(wait):
		require.FailNow(s.t, "no response")
		return nil
	}
}

// handshake performs Claude Code's initialize sequence.
func (s *session) handshake() map[string]any {
	s.t.Helper()
	s.send(map[string]any{"jsonrpc": "2.0", "id": 0, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "claude-code", "version": "2.1.303"},
	}})
	res := s.response()
	s.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	return res
}

func (s *session) notification() Notification {
	s.t.Helper()
	select {
	case n := <-s.notes:
		return n
	case <-time.After(wait):
		require.FailNow(s.t, "no channel notification")
		return Notification{}
	}
}

func (s *session) expectNoNotification(d time.Duration) {
	s.t.Helper()
	select {
	case n := <-s.notes:
		require.Failf(s.t, "unexpected notification", "%+v", n)
	case <-time.After(d):
	}
}

// identityNotice returns the notice the channel sent about who the AI is, waiting for it.
func (s *session) identityNotice() Notification {
	s.t.Helper()
	select {
	case n := <-s.identity:
		return n
	case <-time.After(wait):
		require.FailNow(s.t, "no identity notice")
		return Notification{}
	}
}

// expectNoIdentityNotice fails when the channel sends another identity notice within d.
func (s *session) expectNoIdentityNotice(d time.Duration) {
	s.t.Helper()
	select {
	case n := <-s.identity:
		require.Failf(s.t, "unexpected identity notice", "%+v", n)
	case <-time.After(d):
	}
}

// call runs tools/call and returns the result text and isError.
func (s *session) call(tool string, args any) (string, bool) {
	s.t.Helper()
	s.nextID++
	id := s.nextID
	s.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	m := s.response()
	require.Equal(s.t, float64(id), m["id"], "response: %v", m)
	res, ok := m["result"].(map[string]any)
	require.True(s.t, ok, "not a result: %v", m)
	content := res["content"].([]any)
	require.Len(s.t, content, 1)
	return content[0].(map[string]any)["text"].(string), res["isError"] == true
}

// closeStdin ends the session as Claude Code does on exit and returns Run's result.
func (s *session) closeStdin() (time.Duration, error) {
	s.t.Helper()
	start := time.Now()
	require.NoError(s.t, s.in.Close())
	select {
	case err := <-s.done:
		return time.Since(start), err
	case <-time.After(wait):
		require.FailNow(s.t, "Run did not return after stdin closed")
		return 0, nil
	}
}

// proxy forwards TCP connections to a target and can cut them all, like a server restart.
type proxy struct {
	t      *testing.T
	ln     net.Listener
	target string
	mu     sync.Mutex
	conns  []net.Conn
	down   bool
}

func newProxy(t *testing.T, target string) *proxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	p := &proxy{t: t, ln: ln, target: target}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			down := p.down
			p.mu.Unlock()
			if down {
				_ = c.Close()
				continue
			}
			up, err := net.Dial("tcp", target)
			if err != nil {
				_ = c.Close()
				continue
			}
			p.mu.Lock()
			p.conns = append(p.conns, c, up)
			p.mu.Unlock()
			go func() { _, _ = io.Copy(up, c); _ = up.Close() }()
			go func() { _, _ = io.Copy(c, up); _ = c.Close() }()
		}
	}()
	t.Cleanup(func() { _ = ln.Close(); p.drop() })
	return p
}

func (p *proxy) url() string { return fmt.Sprintf("http://%s", p.ln.Addr()) }

// stop cuts every connection and refuses new ones until start.
func (p *proxy) stop() {
	p.mu.Lock()
	p.down = true
	p.mu.Unlock()
	p.drop()
}

func (p *proxy) start() {
	p.mu.Lock()
	p.down = false
	p.mu.Unlock()
}

func (p *proxy) drop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
}
