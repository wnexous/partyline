package cli

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

func runAgents(env map[string]string, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = execute(context.Background(), append([]string{"partyline", "agents"}, args...), stdio{
		stdout: &out, stderr: &errOut, getenv: func(k string) string { return env[k] },
	})
	return code, out.String(), errOut.String()
}

// dialAs connects a session of alice with the identity headers project, harness and machine.
func (e *channelEnv) dialAs(t *testing.T, project, harness, machine string, name ...string) *testkit.AgentConn {
	t.Helper()
	h := http.Header{}
	if len(name) > 0 {
		h.Set(dto.HeaderAgent, name[0])
	}
	h.Set(dto.HeaderProject, project)
	h.Set(dto.HeaderHarness, harness)
	h.Set(dto.HeaderMachine, machine)
	conn, resp := e.alice.Token(dto.TokenKindAgent, "s-"+project+harness).DialAgentHeaders(h)
	require.NotNil(t, conn, "upgrade refused: %d", resp.Status)
	conn.Welcome(chanWait)
	return conn
}

// CLI-02: `partyline agents` lists the owner's agents grouped by project (projects with an
// online agent first, then by name, no project last), online agents first, each with its
// state, readable name, agent name, harness, machine and kind; --all adds archived agents.
// The output matches GET /agents.
func TestAgentsListsByProject(t *testing.T) {
	e := newChannelEnv(t)
	atlas1 := e.dialAs(t, "atlas", "claude", "laptop")
	defer atlas1.Close()
	atlas2 := e.dialAs(t, "atlas", "claude", "desk")
	defer atlas2.Close()
	aaa := e.dialAs(t, "aaa", "codex", "laptop")
	aaa.Close()
	alpha := e.dialAs(t, "atlas", "claude", "laptop", "alpha")
	alpha.Close()
	e.agent(t, "atlas-claude-2")
	_, err := e.env.App.Agents.Upsert(context.Background(), e.alice.User.ID, "builder")
	require.NoError(t, err)
	require.Eventually(t, func() bool { n := e.onlineNames(t); return !n["aaa-codex"] && !n["alpha"] }, chanWait, 20*time.Millisecond)

	code, out, errOut := runAgents(e.vars())
	require.Equal(t, 0, code, errOut)
	require.Equal(t, strings.Join([]string{
		"Atlas",
		"  online   Atlas · Claude Code     atlas-claude    claude  laptop  temporary",
		"  online   Atlas · Claude Code #2  atlas-claude-2  claude  desk    temporary",
		"  offline  alpha                   alpha           claude  laptop  fixed",
		"",
		"Aaa",
		"  offline  Aaa · Codex  aaa-codex  codex  laptop  temporary",
		"",
		"No project",
		"  offline  builder  builder  agent    fixed",
		"",
	}, "\n"), out)

	list := testkit.Decode[dto.List[dto.Agent]](e.alice.Get("/api/v1/agents").RequireStatus(http.StatusOK)).Items
	require.Len(t, list, 5, "every agent of the API is listed")
	for _, a := range list {
		require.Contains(t, out, a.DisplayName+"  ")
	}

	agent, err := e.env.App.Agents.Lookup(context.Background(), e.alice.User.ID, "aaa-codex")
	require.NoError(t, err)
	require.NoError(t, e.env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
		return q.ArchiveAgent(context.Background(), db.ArchiveAgentParams{ArchivedAt: sql.NullInt64{Int64: 1, Valid: true}, ID: agent.ID})
	}))
	_, out, _ = runAgents(e.vars())
	require.NotContains(t, out, "aaa-codex", "archived agents are hidden")
	_, out, _ = runAgents(e.vars(), "--all")
	require.Contains(t, out, "Aaa\n  offline  Aaa · Codex  aaa-codex  codex  laptop  temporary, archived\n")
}

// CLI-02: without credentials the command explains how to set them; a rejected token is an
// error.
func TestAgentsNeedsCredentials(t *testing.T) {
	code, _, errOut := runAgents(map[string]string{})
	require.Equal(t, 2, code)
	require.Contains(t, errOut, "partyline agents: set PARTYLINE_URL and PARTYLINE_TOKEN")

	e := newChannelEnv(t)
	code, _, errOut = runAgents(map[string]string{"PARTYLINE_URL": e.env.URL, "PARTYLINE_TOKEN": "pl_agent_wrong"})
	require.Equal(t, 1, code)
	require.Contains(t, errOut, "partyline agents: the server answered 401")
}
