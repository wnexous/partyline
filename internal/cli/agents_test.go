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
	"github.com/wnexous/partyline/internal/store"
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

// dialAI connects a session of alice as the AI of the folder /work/<name> on the machine with
// the harness.
func (e *channelEnv) dialAI(t *testing.T, name, harness, machine string) *testkit.AgentConn {
	t.Helper()
	conn, resp := e.alice.Token(dto.TokenKindAgent, "s-"+name).DialFolder(testkit.Folder{Machine: machine, Path: "/work/" + name, Harness: harness})
	require.NotNil(t, conn, "upgrade refused: %d", resp.Status)
	conn.Welcome(chanWait)
	return conn
}

// rows turns the output of `partyline agents` into one "|"-joined line per field-split line.
func rows(out string) []string {
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		lines = append(lines, strings.Join(strings.Fields(l), "|"))
	}
	return lines
}

// CLI-02: `partyline agents` lists the owner's agents grouped by project (projects with an
// online agent first, then by name, no project last), online agents first, each with its state,
// readable name, agent name, harness and machine. The output matches GET /agents.
func TestAgentsListsByProject(t *testing.T) {
	e := newChannelEnv(t)
	atlas := e.dialAI(t, "atlas", "claude", "laptop")
	defer atlas.Close()
	desk := e.dialAI(t, "atlas-2", "claude", "desk")
	defer desk.Close()
	e.dialAI(t, "aaa", "codex", "laptop").Close()
	e.aiAt(t, "/work/builder")
	require.Eventually(t, func() bool { return !e.env.App.Presence.Online(e.registered(t, "/work/aaa").ID) }, chanWait, 20*time.Millisecond)

	byDisplay := map[string]dto.Agent{}
	list := testkit.Decode[dto.List[dto.Agent]](e.alice.Get("/api/v1/agents").RequireStatus(http.StatusOK)).Items
	require.Len(t, list, 4, "every agent of the API is listed")
	for _, a := range list {
		byDisplay[a.DisplayName] = a
	}
	ctx := context.Background()
	project := store.NewID()
	require.NoError(t, e.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateProject(ctx, db.CreateProjectParams{ID: project, OwnerID: e.alice.User.ID, Slug: "atlas", Name: "Atlas", CreatedAt: 1}); err != nil {
			return err
		}
		for _, name := range []string{"Atlas", "Atlas-2"} {
			if err := q.SetAgentProject(ctx, db.SetAgentProjectParams{ProjectID: sql.NullString{String: project, Valid: true}, ID: byDisplay[name].ID}); err != nil {
				return err
			}
		}
		return nil
	}))

	code, out, errOut := runAgents(e.vars())
	require.Equal(t, 0, code, errOut)
	require.Equal(t, []string{
		"Atlas",
		"online|Atlas|" + byDisplay["Atlas"].Name + "|claude|laptop",
		"online|Atlas-2|" + byDisplay["Atlas-2"].Name + "|claude|desk",
		"No|project",
		"offline|Aaa|" + byDisplay["Aaa"].Name + "|codex|laptop",
		"offline|Builder|builder|agent|" + testMachine,
	}, filterBlank(rows(out)), "online agents first within a group, the project with an online agent first, no project last")
	require.NotContains(t, out, "archived", "AIs are never archived")

	code, _, errOut = runAgents(e.vars(), "--all")
	require.Equal(t, 2, code, "--all went away with archiving")
	require.Contains(t, errOut, "flag provided but not defined: -all")
}

func filterBlank(lines []string) []string {
	var out []string
	for _, l := range lines {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
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
