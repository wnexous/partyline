package cli

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// SI-07: the leading --new-ai is taken off the harness's arguments and handed to the session as
// PARTYLINE_LAUNCH_NEW_AI=1; PARTYLINE_NEW_AI=1 stands in for it and is consumed; a launch that asks for
// neither hands none on, even from inside a session that did.
func TestLauncherPassesNewAI(t *testing.T) {
	m := claudeLaunch(t)
	r := m.run(t, "claude", "--new-ai", "--resume")
	require.Equal(t, 7, r.code, r.stderr)
	require.Equal(t, []string{"claude", "--dangerously-load-development-channels", "server:partyline", "--resume"}, r.argv,
		"the harness never gets the flag")
	require.Equal(t, m.launchedWith("PARTYLINE_LAUNCH_NEW_AI=1"), r.env)

	r = m.run(t, "claude", "--org", "acme", "--new-ai")
	require.Equal(t, 7, r.code, r.stderr)
	require.Equal(t, m.launchedWith("PARTYLINE_LAUNCH_ORG=acme", "PARTYLINE_LAUNCH_NEW_AI=1"), r.env, "among the other leading flags")

	m.getenv = map[string]string{"PARTYLINE_NEW_AI": "1"}
	m.environ = append(m.environ, "PARTYLINE_NEW_AI=1")
	r = m.run(t, "claude")
	require.Equal(t, 7, r.code, r.stderr)
	require.Equal(t, m.launchedWith("PARTYLINE_LAUNCH_NEW_AI=1"), r.env, "the variable stands in for the flag and is consumed")

	m.getenv = map[string]string{}
	m.environ = append(m.environ[:len(m.environ)-1], "PARTYLINE_LAUNCH_NEW_AI=1") // the session this launch runs in
	r = m.run(t, "claude")
	require.Equal(t, 7, r.code, r.stderr)
	require.Equal(t, m.launchBase(), r.env, "a nested launch does not inherit it")

	r = m.run(t, "claude", "--resume", "--new-ai")
	require.Equal(t, []string{"claude", "--dangerously-load-development-channels", "server:partyline", "--resume", "--new-ai"}, r.argv,
		"after the harness's own argument it is the harness's")
	require.Equal(t, m.launchBase(), r.env)
}

// SI-07, SESS-01: a --new-ai launch in a linked worktree hands no main folder, so the worktree gets its own AI;
// the launcher's question to the server says --new-ai and names no main folder either.
func TestLauncherNewAIInAWorktreeHasNoMainFolder(t *testing.T) {
	m := claudeLaunch(t)
	m.cwd = "/repo/wt"
	fakeWorktree(t, "/repo/wt", "/repo", nil)
	var header http.Header
	m.serveSession(t, func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Clone()
		require.NoError(t, json.NewEncoder(w).Encode(dto.SessionState{}))
	})

	r := m.run(t, "claude", "--new-ai", "--org", "acme")
	require.Equal(t, 7, r.code, r.stderr)
	require.Empty(t, envValues(r.env, "PARTYLINE_MAIN_FOLDER"), "the worktree keeps its own AI")
	require.Equal(t, []string{"1"}, envValues(r.env, "PARTYLINE_LAUNCH_NEW_AI"))
	require.NotNil(t, header)
	require.Equal(t, "1", header.Get(dto.HeaderNewAI))
	require.Empty(t, header.Values(dto.HeaderMainFolder))
}

// SI-07: a session reads PARTYLINE_LAUNCH_NEW_AI=1, sends X-Partyline-New-AI and no main folder, and computes none.
func TestSessionIdentityReadsNewAI(t *testing.T) {
	folder := canonicalDir(t, "repo", "wt")
	calls := 0
	fakeWorktree(t, folder, "/repo", &calls)
	id := sessionIdentity("", stdio{
		getenv: func(k string) string {
			return map[string]string{"PARTYLINE_FOLDER": folder, "PARTYLINE_LAUNCH_NEW_AI": "1", "PARTYLINE_MAIN_FOLDER": "/repo"}[k]
		},
		getwd:    func() (string, error) { return folder, nil },
		hostname: func() (string, error) { return "devbox", nil },
	})
	require.True(t, id.NewAI)
	require.Empty(t, id.MainFolder)
	require.Equal(t, 0, calls)
	h := http.Header{}
	setIdentityHeaders(h, id)
	require.Equal(t, "1", h.Get(dto.HeaderNewAI))
	require.Empty(t, h.Values(dto.HeaderMainFolder))
}
