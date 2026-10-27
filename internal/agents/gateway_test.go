package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/platform"
)

// stubChecker answers PrincipalActive with a fixed value.
type stubChecker bool

func (s stubChecker) PrincipalActive(context.Context, authn.Principal) (bool, error) {
	return bool(s), nil
}

// Contract (agent gateway): the token is confirmed before the upgrade; an inactive token
// answers 401 and registers no agent. The active case registers the agent, so only the check
// decides.
func TestGatewayRefusesInactivePrincipalBeforeUpgrade(t *testing.T) {
	f := newFixture(t)
	owner := f.user(t, "alice")
	p := authn.Principal{Kind: authn.KindAgent, UserID: owner.UserID, TokenID: "tok1"}
	serve := func(active bool) *httptest.ResponseRecorder {
		g := NewGateway(GatewayDeps{Store: f.st, Registry: f.reg, Presence: f.presence, Sessions: stubChecker(active)})
		t.Cleanup(g.Close)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/ws", nil)
		req.Header.Set(dto.HeaderMachine, "devbox")
		req.Header.Set(dto.HeaderFolder, "%2Fwork%2Fbuilder")
		req = req.WithContext(authn.WithPrincipal(req.Context(), p))
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		return rec
	}
	agents := func() int {
		return f.count(t, "SELECT COUNT(*) FROM agents WHERE owner_id = ? AND folder = '/work/builder'", owner.UserID)
	}

	rec := serve(false)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	var body dto.ErrorBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, dto.ErrorCodeUnauthorized, body.Error.Code)
	require.Equal(t, 0, agents(), "no agent is registered for an inactive token")

	serve(true)
	require.Equal(t, 1, agents(), "an active token registers the agent")
}

// SESS-18: the platform events of the AI a session operates reach its principal session only. A
// labelled session's feed is never built, even when it asked for the feature, and it receives
// nothing while the principal gets the queued event.
func TestOnlyThePrincipalSessionGetsPlatformEvents(t *testing.T) {
	f := newFixture(t)
	alice := f.user(t, "alice")
	agentID := f.upsert(t, alice, "builder")
	f.exec(t, "INSERT INTO connectors (id, owner_id, kind, name, operator_iid, created_at, updated_at) VALUES ('k1', ?, 'telegram', 'tg', ?, ?, ?)",
		alice.UserID, agentID, t0.UnixMilli(), t0.UnixMilli())
	f.exec(t, "INSERT INTO chats (id, connector_id, external_id, title, kind, created_at) VALUES ('c1', 'k1', '42', 'Team', 'group', ?)", t0.UnixMilli())
	queue := platform.New(platform.Deps{Store: f.st, Bus: f.bus, Now: f.clock.Now})
	g := NewGateway(GatewayDeps{Store: f.st, Registry: f.reg, Presence: f.presence, Platform: queue, Bus: f.bus})
	t.Cleanup(g.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := authn.Principal{Kind: authn.KindAgent, UserID: alice.UserID, TokenID: "tok1", Role: string(dto.RoleMember)}
		g.ServeHTTP(w, r.WithContext(authn.WithPrincipal(r.Context(), p)))
	}))
	t.Cleanup(srv.Close)

	dial := func(liveID string) *websocket.Conn {
		t.Helper()
		h := http.Header{}
		h.Set(dto.HeaderMachine, "devbox")
		h.Set(dto.HeaderFolder, "/work/builder")
		h.Set(dto.HeaderHarness, "claude")
		h.Set(dto.HeaderLive, liveID)
		h.Set(dto.HeaderFeatures, dto.FeaturePlatform)
		ws, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/v1/agent/ws", &websocket.DialOptions{HTTPHeader: h})
		require.NoError(t, err)
		t.Cleanup(func() { _ = ws.CloseNow() })
		return ws
	}
	read := func(ws *websocket.Conn, d time.Duration, v any) bool {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), d)
		defer cancel()
		_, data, err := ws.Read(ctx)
		if err != nil {
			return false
		}
		require.NoError(t, json.Unmarshal(data, v))
		return true
	}

	principal := dial("principal-process-aaaaaaaa")
	var welcome struct {
		Type    dto.AgentFrameType `json:"type"`
		Session dto.SessionInfo    `json:"session"`
	}
	require.True(t, read(principal, time.Minute, &welcome), "the welcome frame")
	require.Equal(t, dto.AgentFrameTypeWelcome, welcome.Type)
	require.Equal(t, "", welcome.Session.Label)
	parallel := dial("parallel-process-bbbbbbbb")
	require.True(t, read(parallel, time.Minute, &welcome), "the welcome frame")
	require.Equal(t, "claude-2", welcome.Session.Label)

	_, queued, err := queue.Enqueue(context.Background(), platform.Item{
		AgentID: agentID, ConnectorID: "k1", ChatID: "c1", ExternalID: "m1", SenderID: "5511999990000", SenderName: "Pat", Text: "hello",
	})
	require.NoError(t, err)
	require.True(t, queued)

	var frame struct {
		Type dto.AgentFrameType `json:"type"`
	}
	require.True(t, read(principal, time.Minute, &frame), "the principal session receives the AI's platform event")
	require.Equal(t, dto.AgentFrameTypePlatform, frame.Type)
	require.False(t, read(parallel, 300*time.Millisecond, &frame), "a labelled session receives none")
}
