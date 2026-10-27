package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// realDir creates a directory named after name under a fresh temp dir and returns its
// canonical path (the temp dir itself may be reached through a symlink).
func realDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	resolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	return resolved
}

// AID-07: the folder a process sends is absolute, has its symlinks resolved and no trailing
// separator.
func TestCanonicalFolder(t *testing.T) {
	target := realDir(t, "the lab")
	inner := filepath.Join(target, "Café Órdenes")
	require.NoError(t, os.MkdirAll(inner, 0o755))
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(inner, link))

	cases := []struct{ name, in, want string }{
		{"a symlink", link, inner},
		{"a path through a symlink", filepath.Join(link, "sub", ".."), inner},
		{"a trailing slash", inner + "/", inner},
		{"dot segments", filepath.Join(target, "other", "..", "Café Órdenes", "."), inner},
		{"a directory that does not exist is only cleaned", target + "/gone//deeper/", target + "/gone/deeper"},
	}
	for _, c := range cases {
		got, err := CanonicalFolder(c.in)
		require.NoError(t, err, c.name)
		require.Equal(t, c.want, got, c.name)
	}

	t.Chdir(inner)
	got, err := CanonicalFolder(".")
	require.NoError(t, err)
	require.Equal(t, inner, got, "a relative folder is made absolute")

	root, err := CanonicalFolder("/")
	require.NoError(t, err)
	require.Equal(t, "/", root, "the root stays the root")
}

// AID-07: headers are percent-encoded so spaces, non-ASCII characters and separators travel
// in any path, and decode to the same folder; the folder header is always sent.
func TestIdentityHeaders(t *testing.T) {
	id := Identity{Machine: "dev box", Folder: "/home/alice/the lab/Café Órdenes", Home: "/home/alice", Harness: "opencode", Adopt: "old-helper"}
	h := http.Header{}
	id.setHeaders(h)
	require.Equal(t, "dev box", h.Get(dto.HeaderMachine))
	require.Equal(t, "opencode", h.Get(dto.HeaderHarness))
	require.Equal(t, "old-helper", h.Get(dto.HeaderAdopt))
	for _, name := range []string{dto.HeaderFolder, dto.HeaderHome} {
		require.Regexp(t, regexp.MustCompile(`^[\x21-\x7e]+$`), h.Get(name), "%s is printable ASCII without spaces", name)
		require.NotContains(t, h.Get(name), "/", name)
	}
	folder, err := url.PathUnescape(h.Get(dto.HeaderFolder))
	require.NoError(t, err)
	require.Equal(t, id.Folder, folder)
	home, err := url.PathUnescape(h.Get(dto.HeaderHome))
	require.NoError(t, err)
	require.Equal(t, id.Home, home)

	bare := http.Header{}
	Identity{}.setHeaders(bare)
	require.Equal(t, UnknownMachine, bare.Get(dto.HeaderMachine), "an unreadable hostname is sent as unknown")
	_, folderSent := bare[http.CanonicalHeaderKey(dto.HeaderFolder)]
	require.True(t, folderSent, "no folder is sent as an empty folder header, which the server refuses")
	for _, name := range []string{dto.HeaderHome, dto.HeaderHarness, dto.HeaderAdopt} {
		require.NotContains(t, bare, http.CanonicalHeaderKey(name), "an empty %s is not sent", name)
	}
	for _, name := range []string{"X-Partyline-Agent", "X-Partyline-Agent-Mode", "X-Partyline-Project", "X-Partyline-Session"} {
		require.NotContains(t, h, name, "the v0.3 header %s is gone", name)
	}
}

// ORG-04, POS-07, POS-08 (AD-020): a session that asks for an organization, a position or a parent says so in
// X-Partyline-Org, X-Partyline-Position and X-Partyline-Parent. The position is percent-encoded like the folder,
// so a name with spaces or accents travels in a header. Each header is sent only when its value is set (L-035).
func TestIdentityHeadersCarryThePlacement(t *testing.T) {
	id := Identity{Machine: "devbox", Folder: "/work/acme", Org: "acme", Position: "Lead developer · Café", Parent: "@lead"}
	h := http.Header{}
	id.setHeaders(h)
	require.Equal(t, "acme", h.Get(dto.HeaderOrg))
	require.Equal(t, "@lead", h.Get(dto.HeaderParent))
	require.Equal(t, "Lead%20developer%20%C2%B7%20Caf%C3%A9", h.Get(dto.HeaderPosition), "the wire value of the position")
	position, err := url.PathUnescape(h.Get(dto.HeaderPosition))
	require.NoError(t, err)
	require.Equal(t, id.Position, position)

	placement := []string{dto.HeaderOrg, dto.HeaderPosition, dto.HeaderParent}
	for set, only := range map[string]Identity{
		dto.HeaderOrg: {Org: "acme"}, dto.HeaderPosition: {Position: "Worktrees"}, dto.HeaderParent: {Parent: "@lead"},
	} {
		h := http.Header{}
		only.setHeaders(h)
		for _, name := range placement {
			_, sent := h[http.CanonicalHeaderKey(name)]
			require.Equal(t, name == set, sent, "%s when only %s is set", name, set)
		}
	}
	bare := http.Header{}
	Identity{Machine: "devbox", Folder: "/work/acme", Harness: "claude"}.setHeaders(bare)
	for _, name := range placement {
		require.NotContains(t, bare, http.CanonicalHeaderKey(name), "no placement, no %s", name)
	}
}

// SES-02: the live id is random per process and fits the header format the server accepts.
func TestNewLiveID(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		id := NewLiveID()
		require.Regexp(t, regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`), id)
		require.False(t, seen[id], "ids do not repeat")
		seen[id] = true
	}
}

// TOOL-06: the download directory is named after the folder, whatever the folder is called.
func TestDownloadDirName(t *testing.T) {
	cases := map[string]string{
		"/work/Night Shift":        "night-shift",
		"/work/Café Órdenes":       "cafe-ordenes",
		"/work/builder":            "builder",
		"/":                        "agent",
		"/work/..":                 "agent",
		"/work/../../etc":          "etc",
		"/work/日本語":                "agent",
		`C:\Users\me\Acme Corp`:    "c-users-me-acme-corp",
		"/work/" + "x/../../../..": "agent",
	}
	for folder, want := range cases {
		got := downloadDirName(folder)
		require.Equal(t, want, got, folder)
		require.NotContains(t, got, "/")
		require.NotContains(t, got, "..")
	}
}

// recordingServer answers the agent WebSocket (welcome, then it drops the first connection to
// make the channel reconnect) and the REST routes the tests call, recording the headers of every
// request in order.
type request struct {
	path   string
	header http.Header
}

func recordingServer(t *testing.T) (string, chan request) {
	t.Helper()
	reqs := make(chan request, 32)
	var dials int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs <- request{path: r.URL.Path, header: r.Header.Clone()}
		if r.URL.Path != "/api/v1/agent/ws" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		dials++
		first := dials == 1
		data, _ := json.Marshal(dto.AgentWelcomeFrame{Type: dto.AgentFrameTypeWelcome, Agent: dto.AgentInfo{ID: "a1", Owner: "alice"}})
		_ = ws.Write(r.Context(), websocket.MessageText, data)
		if first {
			_ = ws.Close(websocket.StatusInternalError, "drop") // the channel reconnects
			return
		}
		defer func() { _ = ws.CloseNow() }()
		for {
			if _, _, err := ws.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, reqs
}

func nextRequest(t *testing.T, reqs chan request) request {
	t.Helper()
	select {
	case r := <-reqs:
		return r
	case <-time.After(wait):
		require.FailNow(t, "no request")
		return request{}
	}
}

// requireIdentity checks the identity headers of a request and that the v0.3 naming headers are
// not there.
func requireIdentity(t *testing.T, r request, id Identity) {
	t.Helper()
	require.Equal(t, id.Machine, r.header.Get(dto.HeaderMachine), r.path)
	folder, err := url.PathUnescape(r.header.Get(dto.HeaderFolder))
	require.NoError(t, err, r.path)
	require.Equal(t, id.Folder, folder, r.path)
	home, err := url.PathUnescape(r.header.Get(dto.HeaderHome))
	require.NoError(t, err, r.path)
	require.Equal(t, id.Home, home, r.path)
	require.Equal(t, id.Harness, r.header.Get(dto.HeaderHarness), r.path)
	require.Equal(t, id.Adopt, r.header.Get(dto.HeaderAdopt), r.path)
	// the placement headers are sent exactly when the identity asks for a placement (L-035)
	position, err := url.PathUnescape(r.header.Get(dto.HeaderPosition))
	require.NoError(t, err, r.path)
	require.Equal(t, id.Position, position, r.path)
	for _, h := range []struct{ name, want string }{{dto.HeaderOrg, id.Org}, {dto.HeaderPosition, id.Position}, {dto.HeaderParent, id.Parent}} {
		_, sent := r.header[http.CanonicalHeaderKey(h.name)]
		require.Equal(t, h.want != "", sent, "%s: %s is sent only when set", r.path, h.name)
	}
	require.Equal(t, id.Org, r.header.Get(dto.HeaderOrg), r.path)
	require.Equal(t, id.Parent, r.header.Get(dto.HeaderParent), r.path)
	for _, name := range []string{"X-Partyline-Agent", "X-Partyline-Agent-Mode", "X-Partyline-Project", "X-Partyline-Session"} {
		require.NotContains(t, r.header, name, "%s: the v0.3 header %s is gone", r.path, name)
	}
}

// AID-07: every dial and every call carries the identity; a WebSocket carries the live id of
// the process, the same one on every reconnect. Without a placement none of the placement headers is sent.
func TestEveryConnectionAndCallSendsTheIdentity(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "the lab", "Café")
	everyConnectionSendsTheIdentity(t, Identity{Machine: "dev box", Folder: folder, Home: "/home/alice", Harness: "pi", Adopt: "old-helper"})
}

// ORG-04, POS-07, POS-08: a session that asked for an organization, a position and a parent says so on every
// dial and every call, on the live WebSocket, on the tools-only server, in listen and in the turn-end hook.
func TestEveryConnectionAndCallSendsThePlacement(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "the lab", "Café")
	everyConnectionSendsTheIdentity(t, Identity{
		Machine: "dev box", Folder: folder, Home: "/home/alice", Harness: "pi", Adopt: "old-helper",
		Org: "acme", Position: "Lead developer · Café", Parent: "@lead",
	})
}

func everyConnectionSendsTheIdentity(t *testing.T, id Identity) {
	t.Helper()
	live := regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

	t.Run("full channel", func(t *testing.T) {
		url, reqs := recordingServer(t)
		s := startSession(t, Options{
			URL: url, Token: "pl_agent_x", Machine: id.Machine, Folder: id.Folder, Home: id.Home, Harness: id.Harness, Adopt: id.Adopt,
			Org: id.Org, Position: id.Position, Parent: id.Parent, TempDir: t.TempDir(), timing: fastTiming,
		})
		s.handshake()
		first, second := nextRequest(t, reqs), nextRequest(t, reqs)
		requireIdentity(t, first, id)
		requireIdentity(t, second, id)
		require.Regexp(t, live, first.header.Get(dto.HeaderLive))
		require.Equal(t, first.header.Get(dto.HeaderLive), second.header.Get(dto.HeaderLive), "a reconnect is the same process")

		s.send(map[string]any{"jsonrpc": "2.0", "id": 9, "method": "tools/call", "params": map[string]any{"name": "list_rooms", "arguments": map[string]any{}}})
		call := nextRequest(t, reqs)
		require.Equal(t, "/api/v1/agent/rooms", call.path)
		requireIdentity(t, call, id)
		require.Equal(t, first.header.Get(dto.HeaderLive), call.header.Get(dto.HeaderLive),
			"a REST call carries the live id of the process whose socket is connected (SESS-29)")
	})

	t.Run("channel without a harness is Claude Code", func(t *testing.T) {
		url, reqs := recordingServer(t)
		startSession(t, Options{URL: url, Token: "pl_agent_x", Machine: "devbox", Folder: "/work/acme", TempDir: t.TempDir(), timing: fastTiming}).handshake()
		require.Equal(t, HarnessClaude, nextRequest(t, reqs).header.Get(dto.HeaderHarness))
	})

	t.Run("tools only", func(t *testing.T) {
		url, reqs := recordingServer(t)
		s := startSession(t, Options{
			URL: url, Token: "pl_agent_x", Machine: id.Machine, Folder: id.Folder, Home: id.Home, Harness: id.Harness, Adopt: id.Adopt,
			Org: id.Org, Position: id.Position, Parent: id.Parent, ToolsOnly: true, TempDir: t.TempDir(), timing: fastTiming,
		})
		s.handshake()
		s.send(map[string]any{"jsonrpc": "2.0", "id": 9, "method": "tools/call", "params": map[string]any{"name": "wait_for_messages", "arguments": map[string]any{"timeout_seconds": 0}}})
		call := nextRequest(t, reqs)
		require.Equal(t, "/api/v1/agent/pending", call.path)
		requireIdentity(t, call, id)
		select {
		case r := <-reqs:
			require.Failf(t, "unexpected request", "%s", r.path)
		case <-time.After(200 * time.Millisecond):
		}
	})

	t.Run("listen", func(t *testing.T) {
		url, reqs := recordingServer(t)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- Listen(ctx, ListenOptions{
				URL: url, Token: "pl_agent_x", Machine: id.Machine, Folder: id.Folder, Home: id.Home, Harness: id.Harness, Adopt: id.Adopt,
				Org: id.Org, Position: id.Position, Parent: id.Parent, Stdout: &syncBuffer{}, Stderr: &syncBuffer{}, timing: fastTiming,
			})
		}()
		first, second := nextRequest(t, reqs), nextRequest(t, reqs)
		requireIdentity(t, first, id)
		require.Regexp(t, live, first.header.Get(dto.HeaderLive))
		require.Equal(t, first.header.Get(dto.HeaderLive), second.header.Get(dto.HeaderLive))
		cancel()
		require.NoError(t, <-done)
	})

	t.Run("pending", func(t *testing.T) {
		url, reqs := recordingServer(t)
		_, err := Pending(context.Background(), PendingOptions{URL: url, Token: "pl_agent_x", Identity: id})
		require.NoError(t, err)
		call := nextRequest(t, reqs)
		require.Equal(t, "/api/v1/agent/pending", call.path)
		requireIdentity(t, call, id)
		select {
		case r := <-reqs:
			require.Failf(t, "unexpected request: the hook registers nothing first", "%s", r.path)
		case <-time.After(100 * time.Millisecond):
		}
	})
}
