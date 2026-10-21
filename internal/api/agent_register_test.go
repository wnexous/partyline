package api_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// PUSH-02: POST /agent/register registers the agent named by X-Partyline-Agent without a
// WebSocket (tools-only channel): it answers the agent, REST tools work from then on and the
// agent stays offline. Registering again returns the same agent.
func TestAgentRegisterWithoutSocket(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop").WithHeader(dto.HeaderAgent, "Codex Box")

	tok.Get("/api/v1/agent/rooms").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	info := testkit.Decode[dto.AgentInfo](tok.Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK))
	require.Equal(t, "codex-box", info.Name, "the name is slugified")
	require.Equal(t, "alice", info.Owner)
	require.NotEmpty(t, info.ID)

	again := testkit.Decode[dto.AgentInfo](tok.Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK))
	require.Equal(t, info, again)
	tok.Get("/api/v1/agent/rooms").RequireStatus(http.StatusOK)
	require.Equal(t, map[string]bool{"codex-box": false}, agentNames(t, alice), "registered and offline")
}

// PUSH-02: only agent tokens register agents.
func TestAgentRegisterAuth(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	env.Anon().WithHeader(dto.HeaderAgent, "x").Post("/api/v1/agent/register", nil).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	alice.Token(dto.TokenKindUser, "cli").WithHeader(dto.HeaderAgent, "x").Post("/api/v1/agent/register", nil).
		RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	require.Empty(t, agentNames(t, alice))
}

// PUSH-02 with the agent cap: registering a 51st agent answers 409 "agent limit reached".
func TestAgentRegisterLimit(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	for i := 1; i <= 50; i++ {
		tok.WithHeader(dto.HeaderAgent, fmt.Sprintf("a%d", i)).Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK)
	}
	e := tok.WithHeader(dto.HeaderAgent, "a51").Post("/api/v1/agent/register", nil).RequireError(http.StatusConflict, dto.ErrorCodeConflict)
	require.Equal(t, "agent limit reached", e.Message)
	tok.WithHeader(dto.HeaderAgent, "a7").Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK)
}

// ONB-09: an agent token revokes itself with DELETE /agent/token (setup --uninstall), without
// a registered agent; it is refused from then on. Other tokens of the owner keep working.
func TestAgentRevokesOwnToken(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	other := alice.Token(dto.TokenKindAgent, "desktop").WithHeader(dto.HeaderAgent, "desk")
	other.Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK)

	tok.Delete("/api/v1/agent/token").RequireStatus(http.StatusNoContent)
	tok.WithHeader(dto.HeaderAgent, "x").Post("/api/v1/agent/register", nil).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	tok.Delete("/api/v1/agent/token").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	other.Get("/api/v1/agent/rooms").RequireStatus(http.StatusOK)

	tokens := testkit.Decode[dto.List[dto.Token]](alice.Get("/api/v1/tokens?include_revoked=true").RequireStatus(http.StatusOK))
	revoked := map[string]bool{}
	for _, tk := range tokens.Items {
		revoked[tk.Name] = tk.RevokedAt != nil
	}
	require.Equal(t, map[string]bool{"laptop": true, "desktop": false}, revoked)
}

// ONB-09: only agent tokens revoke themselves there: anonymous 401, user tokens 403 (and stay
// valid).
func TestAgentRevokeOwnTokenAuth(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	env.Anon().Delete("/api/v1/agent/token").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	user := alice.Token(dto.TokenKindUser, "cli")
	user.Delete("/api/v1/agent/token").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	toks := testkit.Decode[dto.List[dto.Token]](user.Get("/api/v1/tokens").RequireStatus(http.StatusOK))
	require.Len(t, toks.Items, 1)
	require.Nil(t, toks.Items[0].RevokedAt, "the user token stays valid")
}

// CLI-02: GET /agent/agents lists the agents the token's owner owns, as GET /agents shows them
// (archived ones only with include_archived=true; agents shared by others left out). Agent
// tokens only, no registered agent needed.
func TestOwnerAgentsForAgentTokens(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	conn, info := dialSession(t, tok, sessionHeaders("ruthe", "claude", "laptop", "", ""))
	defer conn.Close()
	builder := registerAgent(t, env, alice, "builder")
	shared := registerAgent(t, env, bob, "helper")
	shareAgent(bob, shared, alice.User.ID)
	require.NoError(t, env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
		if err := q.UpdateAgentIdentity(context.Background(), db.UpdateAgentIdentityParams{Kind: string(dto.AgentKindTemporary), Harness: "agent", ID: builder}); err != nil {
			return err
		}
		return q.ArchiveAgent(context.Background(), db.ArchiveAgentParams{ArchivedAt: sql.NullInt64{Int64: 1, Valid: true}, ID: builder})
	}))

	list := testkit.Decode[dto.List[dto.Agent]](tok.Get("/api/v1/agent/agents").RequireStatus(http.StatusOK)).Items
	require.Len(t, list, 1)
	require.Equal(t, listedAgent(t, alice, info.ID), list[0])

	all := testkit.Decode[dto.List[dto.Agent]](tok.Get("/api/v1/agent/agents?include_archived=true").RequireStatus(http.StatusOK)).Items
	names := map[string]bool{}
	for _, a := range all {
		names[a.Name] = a.Archived
	}
	require.Equal(t, map[string]bool{info.Name: false, "builder": true}, names)

	alice.Token(dto.TokenKindUser, "cli").Get("/api/v1/agent/agents").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	env.Anon().Get("/api/v1/agent/agents").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
}
