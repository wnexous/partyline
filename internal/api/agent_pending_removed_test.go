package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// AD-035 (follow-up, v0.12.1): GET /agent/pending, the pull path no supported harness uses, is gone.
// An old client gets 410 gone with a message that says where messages go now, never a queue and never
// a long poll; a removed harness keeps its own 410 harness_removed (see TestRemovedHarnessesAreRefused);
// users and anonymous callers are refused as on every agent route, and the call resolves and creates
// nothing.
func TestAgentPendingRouteIsGone(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	before := ownedAIs(t, alice)

	for _, query := range []string{"", "?platform=1", "?wait=30", "?platform=true"} {
		e := tok.AsFolder(testkit.FolderOf("tools-box")).Get("/api/v1/agent/pending"+query).RequireError(http.StatusGone, dto.ErrorCodeGone)
		require.Contains(t, e.Message, "removed in v0.12.1", query)
		require.Contains(t, e.Message, "messages are pushed to the agent WebSocket", query)
	}
	require.Equal(t, before, ownedAIs(t, alice), "the removed route creates no AI")

	alice.Token(dto.TokenKindUser, "script").Get("/api/v1/agent/pending").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	env.Anon().Get("/api/v1/agent/pending").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
}
