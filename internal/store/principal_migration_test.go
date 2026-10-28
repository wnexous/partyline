package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
)

// SESS-52 (data): migration 0028 adds agents.principal_session, ” for every AI that existed before it
// (no session pinned: the folder's first live session acts as the principal), and SetAgentPrincipalSession
// pins and unpins a harness session id without touching the rest of the row.
func TestMigration0028AddsThePinnedPrincipalSession(t *testing.T) {
	ctx := context.Background()
	e := &schemaEnv{t: t, s: openMigratedBefore(t, "0028")}
	require.NotContains(t, e.columnsOf("agents"), "principal_session", "fixture sanity: schema before 0028")
	sessionsFixture(e)
	rowBefore := e.rowsOf("agents", e.columnsOf("agents"))

	require.NoError(t, e.s.applyMigration(ctx, "0028_fixed_principal.sql"))
	require.Contains(t, versions(t, e.s), "0028_fixed_principal")
	cols := e.columnsOf("agents")
	require.Equal(t, "principal_session", cols[len(cols)-1], "the column is added at the end of agents")
	require.Equal(t, rowBefore, e.rowsOf("agents", columnsWithout(cols, "principal_session")), "no value of the AI changed")

	a, err := e.s.R().GetAgent(ctx, "a1")
	require.NoError(t, err)
	require.Empty(t, a.PrincipalSession, "an AI from before the upgrade pins no session")

	require.NoError(t, e.s.WithTx(ctx, func(q *db.Queries) error {
		return q.SetAgentPrincipalSession(ctx, db.SetAgentPrincipalSessionParams{PrincipalSession: "15cf0000-aaaa", ID: "a1"})
	}))
	a, err = e.s.R().GetAgent(ctx, "a1")
	require.NoError(t, err)
	require.Equal(t, "15cf0000-aaaa", a.PrincipalSession)
	require.Equal(t, "atlas", a.Name, "the rest of the row is untouched")

	require.NoError(t, e.s.WithTx(ctx, func(q *db.Queries) error {
		return q.SetAgentPrincipalSession(ctx, db.SetAgentPrincipalSessionParams{ID: "a1"})
	}))
	a, err = e.s.R().GetAgent(ctx, "a1")
	require.NoError(t, err)
	require.Empty(t, a.PrincipalSession, "an empty id unpins")
}

// SESS-54 (data): migration 0028 also creates agent_session_labels, which remembers a parallel session's
// label by harness session id across a server restart; a row is replaced by the next label, read only while
// recent, pruned when old and removed with its AI.
func TestMigration0028RemembersTheLabelOfAParallelSession(t *testing.T) {
	ctx := context.Background()
	e := &schemaEnv{t: t, s: openMigratedBefore(t, "0028")}
	require.Empty(t, e.columnsOf("agent_session_labels"), "fixture sanity: the table does not exist before 0028")
	sessionsFixture(e)
	require.NoError(t, e.s.applyMigration(ctx, "0028_fixed_principal.sql"))

	put := func(session, label string, at int64) {
		require.NoError(t, e.s.WithTx(ctx, func(q *db.Queries) error {
			return q.UpsertSessionLabel(ctx, db.UpsertSessionLabelParams{AgentID: "a1", HarnessSession: session, Label: label, UpdatedAt: at})
		}))
	}
	get := func(session string, since int64) (string, error) {
		return e.s.R().GetSessionLabel(ctx, db.GetSessionLabelParams{AgentID: "a1", HarnessSession: session, UpdatedAt: since})
	}
	put("sess-b", "claude-2", 100)
	put("sess-b", "claude-3", 200)
	label, err := get("sess-b", 150)
	require.NoError(t, err)
	require.Equal(t, "claude-3", label, "the next label replaces the row")
	_, err = get("sess-b", 250)
	require.ErrorIs(t, err, sql.ErrNoRows, "an old memory is not read")
	require.Error(t, e.exec(`INSERT INTO agent_session_labels (agent_id, harness_session, label, updated_at) VALUES ('a1', 'x', '', 1)`), "a label is never empty")

	put("sess-old", "wt", 10)
	require.NoError(t, e.s.WithTx(ctx, func(q *db.Queries) error {
		return q.PruneSessionLabels(ctx, db.PruneSessionLabelsParams{AgentID: "a1", UpdatedAt: 150})
	}))
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM agent_session_labels`), "the old row is pruned")

	e.must(`DELETE FROM agents WHERE id = 'a1'`)
	require.Equal(t, 0, e.count(`SELECT COUNT(*) FROM agent_session_labels`), "the labels go with their AI")
}
