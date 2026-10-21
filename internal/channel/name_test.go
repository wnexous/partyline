package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// ID-01: the project is the git repository root's basename (the root beats the subdirectory
// the session started in), else the working directory's basename, unchanged (the server
// derives the slug and the readable name).
func TestDeriveProject(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	parent := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", parent) // never find a repository above the test dirs

	repo := filepath.Join(parent, "My Repo")
	sub := filepath.Join(repo, "pkg", "Inner Dir")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	out, err := exec.Command("git", "init", "-q", repo).CombinedOutput()
	require.NoError(t, err, string(out))

	require.Equal(t, "My Repo", DeriveProject(sub), "the git root wins over the subdirectory")
	require.Equal(t, "My Repo", DeriveProject(repo))

	plain := filepath.Join(parent, "Plain Dir")
	require.NoError(t, os.MkdirAll(plain, 0o755))
	require.Equal(t, "Plain Dir", DeriveProject(plain), "outside git: the working directory name")
}

// ID-03: the home folder (with or without a trailing slash) and / are home; a folder inside
// home is not.
func TestIsHomeDir(t *testing.T) {
	require.True(t, IsHomeDir("/home/dev", "/home/dev"))
	require.True(t, IsHomeDir("/home/dev/", "/home/dev"))
	require.True(t, IsHomeDir("/", "/home/dev"))
	require.True(t, IsHomeDir("/", ""))
	require.False(t, IsHomeDir("/home/dev/proj", "/home/dev"))
	require.False(t, IsHomeDir("/home", "/home/dev"))
	require.False(t, IsHomeDir("/home/dev", ""))
}

// headerGateway is an agent WebSocket that records each connection's agent headers. The
// i-th connection gets a welcome with script[i].welcome, then a close with script[i].close;
// connections beyond the script get a welcome with the requested name and stay open.
type gatewayStep struct {
	welcome string
	close   websocket.StatusCode
}

type dialHeaders struct{ agent, mode, project, harness, machine, session string }

func headerGateway(t *testing.T, script ...gatewayStep) (string, chan dialHeaders) {
	t.Helper()
	dials := make(chan dialHeaders, 16)
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dials <- dialHeaders{
			agent: r.Header.Get(dto.HeaderAgent), mode: r.Header.Get(dto.HeaderAgentMode), project: r.Header.Get(dto.HeaderProject),
			harness: r.Header.Get(dto.HeaderHarness), machine: r.Header.Get(dto.HeaderMachine), session: r.Header.Get(dto.HeaderSession),
		}
		i := int(n.Add(1)) - 1
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.CloseNow() }()
		name := r.Header.Get(dto.HeaderAgent)
		if i < len(script) {
			name = script[i].welcome
		}
		data, _ := json.Marshal(dto.AgentWelcomeFrame{Type: dto.AgentFrameTypeWelcome, Agent: dto.AgentInfo{ID: "a1", Name: name, Owner: "alice"}})
		if ws.Write(r.Context(), websocket.MessageText, data) != nil {
			return
		}
		if i < len(script) {
			_ = ws.Close(script[i].close, "bye")
			return
		}
		for { // answer the client's close handshake
			if _, _, err := ws.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, dials
}

func nextDial(t *testing.T, dials chan dialHeaders) dialHeaders {
	t.Helper()
	select {
	case d := <-dials:
		return d
	case <-time.After(wait):
		require.FailNow(t, "no connection attempt")
		return dialHeaders{}
	}
}

// sessionIdentity is the identity the link tests connect with.
var sessionIdentity = Identity{Project: "proj", Harness: "claude", Machine: "laptop"}

func runLink(t *testing.T, url, agent string) *link {
	t.Helper()
	l := newLink(mustURL(t, url), "pl_agent_x", agent, http.DefaultClient, discard{}, &syncBuffer{}, fastTiming)
	l.identity, l.temporary = sessionIdentity, agent == ""
	l.identity.Name = agent
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = l.run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return l
}

// ID-01, T7: a temporary session sends its project, harness and machine and no agent name;
// once the welcome assigned a name, reconnects send that name with mode temporary; after being
// replaced it asks for a new name again instead of fighting.
func TestLinkTemporarySessionKeepsTheAssignedName(t *testing.T) {
	url, dials := headerGateway(t,
		gatewayStep{welcome: "proj-claude-2", close: websocket.StatusInternalError}, // connection dropped
		gatewayStep{welcome: "proj-claude-2", close: closeReplaced},                 // another session took it
		gatewayStep{welcome: "proj-claude", close: websocket.StatusInternalError},   // a new name, dropped
	)
	l := runLink(t, url, "")

	id := dialHeaders{project: "proj", harness: "claude", machine: "laptop"}
	require.Equal(t, id, nextDial(t, dials))
	again := id
	again.agent, again.mode = "proj-claude-2", dto.AgentModeTemporary
	require.Equal(t, again, nextDial(t, dials), "a reconnect keeps the assigned name")
	require.Equal(t, id, nextDial(t, dials), "after replaced: a new name again")
	again.agent = "proj-claude"
	require.Equal(t, again, nextDial(t, dials), "then it keeps the new name")
	require.Eventually(t, func() bool { return l.name() == "proj-claude" }, wait, 10*time.Millisecond)
}

// ID-01 (fix iteration 1): the session key travels as X-Partyline-Session on every dial, so
// the server gives the session its own temporary agent; a value that is not a valid key is
// not sent.
func TestLinkSendsTheSessionKey(t *testing.T) {
	for key, want := range map[string]string{"pl-0123456789abcdef": "pl-0123456789abcdef", "4242": "4242", "bad key/x": ""} {
		url, dials := headerGateway(t, gatewayStep{welcome: "proj-claude", close: websocket.StatusInternalError})
		l := newLink(mustURL(t, url), "pl_agent_x", "", http.DefaultClient, discard{}, &syncBuffer{}, fastTiming)
		l.identity, l.temporary = sessionIdentity, true
		l.identity.SessionKey = key
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { _ = l.run(ctx); close(done) }()
		require.Equal(t, dialHeaders{project: "proj", harness: "claude", machine: "laptop", session: want}, nextDial(t, dials), key)
		require.Equal(t, dialHeaders{agent: "proj-claude", mode: dto.AgentModeTemporary, project: "proj", harness: "claude", machine: "laptop", session: want},
			nextDial(t, dials), "%s: reconnects send it too", key)
		cancel()
		<-done
	}
}

// ID-02: an explicit name is sent alone (no mode) with the identity headers and kept whatever
// the welcome says.
func TestLinkExplicitNameSendsNoMode(t *testing.T) {
	url, dials := headerGateway(t, gatewayStep{welcome: "builder", close: websocket.StatusInternalError})
	l := runLink(t, url, "builder")
	want := dialHeaders{agent: "builder", project: "proj", harness: "claude", machine: "laptop"}
	require.Equal(t, want, nextDial(t, dials))
	require.Equal(t, want, nextDial(t, dials))
	require.Equal(t, "builder", l.name())
}

// ID-01 end to end: two temporary sessions of one project connect as proj-claude and
// proj-claude-2 ("Proj · Claude Code" and "… #2"), and each one's tools act as its own agent
// (only proj-claude is in room Support).
func TestTemporarySessionsGetDistinctNamesAndToolsUseThem(t *testing.T) {
	e := newChanEnv(t)
	o := e.options()
	o.Agent, o.Project, o.Machine = "", "proj", "laptop"
	first := startSession(t, o)
	first.handshake()
	var firstID string
	require.Eventually(t, func() bool {
		a, err := e.env.App.Agents.Lookup(context.Background(), e.alice.User.ID, "proj-claude")
		firstID = a.ID
		return err == nil && e.env.App.Gateway.Online(a.ID)
	}, wait, 10*time.Millisecond)
	e.alice.Post("/api/v1/rooms/"+e.room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: firstID}).RequireStatus(http.StatusCreated)

	second := startSession(t, o)
	second.handshake()

	var text string
	require.Eventually(t, func() bool {
		var isErr bool
		text, isErr = second.call("room_info", toolArgs("room", "Support"))
		return isErr && strings.Contains(text, "not a member of room Support")
	}, wait, 20*time.Millisecond, "the second session acts as proj-claude-2: %s", text)

	text, isErr := first.call("room_info", toolArgs("room", "Support"))
	require.False(t, isErr, text)

	list := testkit.Decode[dto.List[dto.Agent]](e.alice.Get("/api/v1/agents").RequireStatus(http.StatusOK))
	got := map[string]string{}
	for _, a := range list.Items {
		if a.Online {
			got[a.Name] = a.DisplayName + "/" + a.Harness + "/" + a.Machine + "/" + string(a.Kind)
		}
	}
	require.Equal(t, map[string]string{
		"proj-claude":   "Proj · Claude Code/claude/laptop/temporary",
		"proj-claude-2": "Proj · Claude Code #2/claude/laptop/temporary",
	}, got)
}
