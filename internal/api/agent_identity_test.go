package api_test

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// listedAgent returns the agent with id from GET /agents of c.
func listedAgent(t *testing.T, c *testkit.Client, id string) dto.Agent {
	t.Helper()
	list := testkit.Decode[dto.List[dto.Agent]](c.Get("/api/v1/agents").RequireStatus(http.StatusOK))
	for _, a := range list.Items {
		if a.ID == id {
			return a
		}
	}
	require.Failf(t, "agent not listed", "%s", id)
	return dto.Agent{}
}

// PROJ-01: every agent carries its project {id, slug, name}, display name, harness, machine,
// kind and archived flag; an agent of no project has no project field.
func TestAgentListCarriesIdentity(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	id := registerAgent(t, env, alice, "builder")

	a := listedAgent(t, alice, id)
	require.Nil(t, a.Project)
	require.Equal(t, []any{"builder", "builder", "agent", "", dto.AgentKindFixed, false},
		[]any{a.Name, a.DisplayName, a.Harness, a.Machine, a.Kind, a.Archived})

	ctx := context.Background()
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateProjectIfMissing(ctx, db.CreateProjectIfMissingParams{ID: "p1", OwnerID: alice.User.ID, Slug: "proj", Name: "Proj", CreatedAt: 1}); err != nil {
			return err
		}
		return q.UpdateAgentIdentity(ctx, db.UpdateAgentIdentityParams{
			ProjectID: sql.NullString{String: "p1", Valid: true}, Harness: "claude", Machine: "laptop",
			Kind: string(dto.AgentKindTemporary), AutoName: "Proj · Claude Code", ID: id,
		})
	}))

	a = listedAgent(t, alice, id)
	require.Equal(t, &dto.ProjectRef{ID: "p1", Slug: "proj", Name: "Proj"}, a.Project)
	require.Equal(t, []any{"Proj · Claude Code", "claude", "laptop", dto.AgentKindTemporary, false},
		[]any{a.DisplayName, a.Harness, a.Machine, a.Kind, a.Archived})
}
