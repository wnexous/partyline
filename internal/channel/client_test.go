package channel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"regexp"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// liveIDShape is dto.HeaderLive's shape: the process id a session sends on every connection and
// call (SESS-29).
var liveIDShape = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

// SESS-29: an identity that holds a live id sends it with every call; one that does not (the
// tools-only processes, whose session is not on a socket) sends no such header at all.
func TestIdentitySendsTheLiveID(t *testing.T) {
	h := http.Header{}
	Identity{Machine: "testbox", Folder: "/work/builder", LiveID: "P"}.setHeaders(h)
	require.Equal(t, "P", h.Get(dto.HeaderLive))

	bare := http.Header{}
	Identity{Machine: "testbox", Folder: "/work/builder"}.setHeaders(bare)
	require.NotContains(t, bare, http.CanonicalHeaderKey(dto.HeaderLive))
}

// SESS-29: a tool call carries the live id of the process, so the server can record the session a
// message came from instead of taking the text's word for it.
func TestToolCallsSendTheLiveID(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)

	c := &client{
		base: mustURL(t, srv.URL), token: "pl_agent_x",
		identity: Identity{Machine: "testbox", Folder: "/work/builder", LiveID: "P"},
		http:     srv.Client(),
	}
	var rooms struct{}
	err := c.json(context.Background(), http.MethodGet, "agent/rooms", nil, nil, &rooms)
	require.NoError(t, err)
	require.Equal(t, "P", got.Get(dto.HeaderLive))
	require.Equal(t, "Bearer pl_agent_x", got.Get("Authorization"))
}

// liveIDRecorder forwards every request to the server and remembers the live id each path sent.
type liveIDRecorder struct {
	url string
	mu  sync.Mutex
	ids map[string]string // path -> the live id of the last request on it
}

func newLiveIDRecorder(t *testing.T, target string) *liveIDRecorder {
	t.Helper()
	proxy := httputil.NewSingleHostReverseProxy(mustURL(t, target))
	r := &liveIDRecorder{ids: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if live := req.Header.Get(dto.HeaderLive); live != "" {
			r.mu.Lock()
			r.ids[req.URL.Path] = live
			r.mu.Unlock()
		}
		proxy.ServeHTTP(w, req)
	}))
	t.Cleanup(srv.Close)
	r.url = srv.URL
	return r
}

// calls returns the live id the connection sent and the live ids every other request sent.
func (r *liveIDRecorder) calls() (socket string, rest []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for path, id := range r.ids {
		if path == "/api/v1/agent/ws" {
			socket = id
			continue
		}
		rest = append(rest, id)
	}
	return socket, rest
}

// SESS-29: the channel's tools post as the process whose socket is connected: every REST call
// carries the live id the session connected with, so its messages are labelled with its session.
func TestTheChannelToolsUseTheLiveIDOfTheConnection(t *testing.T) {
	e := newChanEnv(t)
	rec := newLiveIDRecorder(t, e.env.URL)
	o := e.options()
	o.URL = rec.url
	s := e.connected(o)

	text, isErr := s.call("list_rooms", map[string]any{})
	require.False(t, isErr, text)

	socket, rest := rec.calls()
	require.Regexp(t, liveIDShape, socket, "the connection sends the live id of its process")
	require.NotEmpty(t, rest, "the tool call reached the server")
	for _, live := range rest {
		require.Equal(t, socket, live, "every call carries the id of the connected process")
	}
}
