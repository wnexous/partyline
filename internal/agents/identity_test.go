package agents

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// ID-01: the supported harnesses keep their ids, aliases and labels; an unknown harness falls back to "agent".
func TestHarnessIDAndLabel(t *testing.T) {
	for v, id := range map[string]string{"claude": "claude", "ClaudeCode": "claude", " claude-code ": "claude", "pi": "pi", "opencode": "opencode", "omp": "omp", "cursor": "agent", "": "agent"} {
		require.Equal(t, id, HarnessID(v), v)
	}
	require.Equal(t, "Claude Code", HarnessLabel("claude"))
	require.True(t, PushedHarness("pi"))
	require.True(t, PushedHarness("opencode"))
	require.False(t, PushedHarness("omp"))
}

// AD-034: codex, gemini and antigravity are no longer supported, but an AI created with one keeps its app and
// reads its old label; agy was only the launcher's name for Antigravity and is not an id any more.
func TestRemovedHarnessesKeepTheirLabels(t *testing.T) {
	for id, label := range map[string]string{"codex": "Codex", "gemini": "Gemini", "antigravity": "Antigravity"} {
		require.Equal(t, label, HarnessLabel(id))
		require.Equal(t, id, HarnessID(id), "the id of a stored row is kept")
		require.False(t, PushedHarness(id))
	}
	require.Equal(t, "agent", HarnessID("agy"))
}

// RemovedHarness names the harnesses a new session may not use, under their ids, the launcher's agy and any case.
func TestRemovedHarness(t *testing.T) {
	for v, label := range map[string]string{"codex": "Codex", "Gemini": "Gemini", " antigravity ": "Antigravity", "AGY": "Antigravity"} {
		got, ok := RemovedHarness(v)
		require.True(t, ok, v)
		require.Equal(t, label, got, v)
	}
	for _, v := range []string{"claude", "claudecode", "opencode", "pi", "omp", "agent", "", "cursor"} {
		_, ok := RemovedHarness(v)
		require.False(t, ok, v)
	}
}
