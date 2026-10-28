package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
)

// SESS-51 (data): migration 0028 adds agents.principal_session, ” for every AI that existed before it
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
