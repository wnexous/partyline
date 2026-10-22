package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
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
