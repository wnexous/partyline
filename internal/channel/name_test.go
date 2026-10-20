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

// NAME-01: the name is the git repository root's basename (the root beats the subdirectory
// the session started in), else the working directory's basename, slugified.
func TestDeriveName(t *testing.T) {
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

	require.Equal(t, "my-repo", DeriveName(sub), "the git root wins over the subdirectory")
	require.Equal(t, "my-repo", DeriveName(repo))

	plain := filepath.Join(parent, "Plain Dir")
	require.NoError(t, os.MkdirAll(plain, 0o755))
	require.Equal(t, "plain-dir", DeriveName(plain), "outside git: the working directory name")
}

// headerGateway is an agent WebSocket that records each connection's agent headers. The
// i-th connection gets a welcome with script[i].welcome, then a close with script[i].close;
// connections beyond the script get a welcome with the requested name and stay open.
type gatewayStep struct {
	welcome string
	close   websocket.StatusCode
}

type dialHeaders struct{ agent, mode string }

func headerGateway(t *testing.T, script ...gatewayStep) (string, chan dialHeaders) {
	t.Helper()
	dials := make(chan dialHeaders, 16)
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dials <- dialHeaders{agent: r.Header.Get(dto.HeaderAgent), mode: r.Header.Get(dto.HeaderAgentMode)}
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

func runLink(t *testing.T, url, agent string, unique bool) *link {
	t.Helper()
	l := newLink(mustURL(t, url), "pl_agent_x", agent, http.DefaultClient, discard{}, &syncBuffer{}, fastTiming)
	l.unique = unique
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = l.run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return l
}

// NAME-02: a derived name asks for a free name (mode header), then keeps the assigned name
// on reconnect; after being replaced it asks for a free name again instead of fighting.
func TestLinkDerivedNameAsksForFreeNameThenKeepsIt(t *testing.T) {
	url, dials := headerGateway(t,
		gatewayStep{welcome: "proj-2", close: websocket.StatusInternalError}, // connection dropped
		gatewayStep{welcome: "proj-2", close: closeReplaced},                 // another session took proj-2
	)
	l := runLink(t, url, "proj", true)

	require.Equal(t, dialHeaders{agent: "proj", mode: dto.AgentModeUnique}, nextDial(t, dials))
	require.Equal(t, dialHeaders{agent: "proj-2", mode: ""}, nextDial(t, dials), "a reconnect keeps the assigned name")
	require.Equal(t, dialHeaders{agent: "proj", mode: dto.AgentModeUnique}, nextDial(t, dials), "after replaced: a free name again")
	require.Eventually(t, func() bool { return l.name() == "proj" }, wait, 10*time.Millisecond)
}

// NAME-03: an explicit name never sends the mode header and keeps its name whatever the
// welcome says.
func TestLinkExplicitNameSendsNoMode(t *testing.T) {
	url, dials := headerGateway(t, gatewayStep{welcome: "builder", close: websocket.StatusInternalError})
	l := runLink(t, url, "builder", false)
	require.Equal(t, dialHeaders{agent: "builder", mode: ""}, nextDial(t, dials))
	require.Equal(t, dialHeaders{agent: "builder", mode: ""}, nextDial(t, dials))
	require.Equal(t, "builder", l.name())
}

// NAME-02 end to end: two derived sessions with the same name connect as builder and
// builder-2, and each one's tools act as its own agent (only builder is in room Support).
func TestDerivedSessionsGetDistinctNamesAndToolsUseThem(t *testing.T) {
	e := newChanEnv(t)
	o := e.options()
	o.UniqueName = true
	first := startSession(t, o)
	first.handshake()
	require.Eventually(t, e.online, wait, 10*time.Millisecond)

	o2 := e.options()
	o2.UniqueName = true
	second := startSession(t, o2)
	second.handshake()

	var text string
	require.Eventually(t, func() bool {
		var isErr bool
		text, isErr = second.call("room_info", toolArgs("room", "Support"))
		return isErr && strings.Contains(text, "not a member of room Support")
	}, wait, 20*time.Millisecond, "the second session acts as builder-2: %s", text)
	require.True(t, e.online(), "the first session was not replaced")

	text, isErr := first.call("room_info", toolArgs("room", "Support"))
	require.False(t, isErr, text)

	list := testkit.Decode[dto.List[dto.Agent]](e.alice.Get("/api/v1/agents").RequireStatus(http.StatusOK))
	online := map[string]bool{}
	for _, a := range list.Items {
		online[a.Name] = a.Online
	}
	require.Equal(t, map[string]bool{"builder": true, "builder-2": true}, online)
}
