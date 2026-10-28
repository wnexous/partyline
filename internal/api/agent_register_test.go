package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// PUSH-02, AID-04: POST /agent/register resolves the AI of the folder named in the headers
// without a WebSocket (tools-only channel): it answers the AI, REST tools work as it from then
// on and it stays offline. Registering again returns the same AI.
func TestAgentRegisterWithoutSocket(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop").AsFolder(testkit.Folder{Machine: "devbox", Path: "/work/Tools Box", Harness: "omp"})

	info := testkit.Decode[dto.AgentInfo](tok.Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK))
	require.Equal(t, "Tools Box", info.DisplayName, "the folder's name")
	require.Equal(t, "alice", info.Owner)
	require.NotEmpty(t, info.ID)

	again := testkit.Decode[dto.AgentInfo](tok.Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK))
	require.Equal(t, info, again)
	tok.Get("/api/v1/agent/rooms").RequireStatus(http.StatusOK)
	list := ownedAIs(t, alice)
	require.Len(t, list, 1, "registered once")
	require.Equal(t, []any{info.ID, false, "omp"}, []any{list[0].ID, list[0].Online, list[0].Harness}, "and offline")
}

// PUSH-02: only agent tokens register agents.
func TestAgentRegisterAuth(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	env.Anon().AsAgent("x").Post("/api/v1/agent/register", nil).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	alice.Token(dto.TokenKindUser, "cli").AsAgent("x").Post("/api/v1/agent/register", nil).
		RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	require.Empty(t, ownedAIs(t, alice))
}

// PUSH-02 with the cap: registering an AI beyond agents.MaxAgentsPerOwner answers 409 "agent
// limit reached"; the folders that have an AI still register.
func TestAgentRegisterLimit(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	for i := 1; i <= agents.MaxAgentsPerOwner; i++ {
		tok.AsAgent(fmt.Sprintf("a%d", i)).Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK)
	}
	e := tok.AsAgent("one-too-many").Post("/api/v1/agent/register", nil).RequireError(http.StatusConflict, dto.ErrorCodeConflict)
	require.Equal(t, "agent limit reached", e.Message)
	tok.AsAgent("a7").Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK)
	require.Len(t, ownedAIs(t, alice), agents.MaxAgentsPerOwner)
}

// ONB-09: an agent token revokes itself with DELETE /agent/token (setup --uninstall), without
// an AI; it is refused from then on. Other tokens of the owner keep working.
func TestAgentRevokesOwnToken(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	other := alice.Token(dto.TokenKindAgent, "desktop").AsAgent("desk")
	other.Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK)

	tok.Delete("/api/v1/agent/token").RequireStatus(http.StatusNoContent)
	tok.AsAgent("x").Post("/api/v1/agent/register", nil).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
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
// (agents shared by others left out). Agent tokens only, no folder needed.
func TestOwnerAgentsForAgentTokens(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	conn, resp := tok.DialAgent("atlas")
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	defer conn.Close()
	info := conn.Welcome(wsWait).Agent
	env.NewAI(alice, "builder")
	shared := env.NewAI(bob, "helper")
	shareAI(bob, shared, alice) // alice joins bob's organization: she sees the AI without hosting it

	list := testkit.Decode[dto.List[dto.Agent]](tok.Get("/api/v1/agent/agents").RequireStatus(http.StatusOK)).Items
	names := map[string]bool{}
	for _, a := range list {
		names[a.Intelligence.DisplayName] = a.Online
	}
	require.Equal(t, map[string]bool{info.DisplayName: true, "builder": false}, names, "alice's own agents, not the one shared with her")
	require.Equal(t, listedAgent(t, alice, info.ID), func() dto.Agent {
		for _, a := range list {
			if a.ID == info.ID {
				return a
			}
		}
		return dto.Agent{}
	}())

	alice.Token(dto.TokenKindUser, "cli").Get("/api/v1/agent/agents").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	env.Anon().Get("/api/v1/agent/agents").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
}
