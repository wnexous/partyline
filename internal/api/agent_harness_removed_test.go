package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// AD-034: Codex, Gemini and Antigravity are no longer supported. A session or a call of an old CLI that names one
// in X-Partyline-Harness (the launcher's id, or agy) gets 410 harness_removed, on the WebSocket and on the agent
// routes, with a message that names the harness and the ones to use; nothing is created.
func TestRemovedHarnessesAreRefused(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	for header, label := range map[string]string{"codex": "Codex", "gemini": "Gemini", "antigravity": "Antigravity", "agy": "Antigravity", "Codex": "Codex"} {
		t.Run(header, func(t *testing.T) {
			folder := testkit.Folder{Machine: "devbox", Path: "/work/old", Harness: header}
			want := label + " is no longer supported by Partyline; use Claude Code, pi, OpenCode or omp"

			for _, call := range []func() *testkit.Response{
				func() *testkit.Response { return tok.AsFolder(folder).Get("/api/v1/agent/rooms") },
				func() *testkit.Response { return tok.AsFolder(folder).Post("/api/v1/agent/register", nil) },
				func() *testkit.Response { return tok.AsFolder(folder).Get("/api/v1/agent/session") },
				func() *testkit.Response { return tok.AsFolder(folder).Get("/api/v1/agent/pending") },
			} {
				e := call().RequireError(http.StatusGone, dto.ErrorCodeHarnessRemoved)
				require.Equal(t, want, e.Message)
			}

			conn, resp := tok.DialFolder(folder)
			require.Nil(t, conn, "the upgrade is refused")
			require.Equal(t, http.StatusGone, resp.Status)
			require.Contains(t, resp.String(), want)
			require.Contains(t, resp.String(), string(dto.ErrorCodeHarnessRemoved))
			require.Empty(t, ownedAIs(t, alice), "no AI is created")
		})
	}
}

// The supported harnesses, and a request that names none, are not touched by the refusal.
func TestSupportedHarnessesAreNotRefused(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	for _, h := range []string{"claude", "claudecode", "opencode", "pi", "omp", "agent", ""} {
		tok.AsFolder(testkit.Folder{Machine: "devbox", Path: "/work/" + h + "-ok", Harness: h}).Get("/api/v1/agent/rooms").RequireStatus(http.StatusOK)
	}
}

// An AI created with one of those harnesses before they were dropped stays: its row, its app label and its
// profile are shown as before, and it is still one of its owner's AIs.
func TestAIOfARemovedHarnessKeepsItsLabel(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	for harness, label := range map[string]string{"codex": "Codex", "gemini": "Gemini", "antigravity": "Antigravity"} {
		// The registry takes the id as it is stored: only the headers of a new session are refused.
		a, err := env.App.Agents.ResolveFolder(t.Context(), alice.User.ID, agents.FolderID{Machine: "devbox", Folder: "/work/" + harness, Harness: harness}, false)
		require.NoError(t, err)
		require.Equal(t, harness, a.Harness)
		require.Equal(t, label, agents.HarnessLabel(a.Harness))
		prof := testkit.Decode[dto.IntelligenceProfile](alice.Get("/api/v1/intelligences/" + a.ID).RequireStatus(http.StatusOK))
		require.Equal(t, harness, prof.AI.App)
		require.Equal(t, label, prof.Apps[0].Label)
	}
	require.Len(t, ownedAIs(t, alice), 3)
}
