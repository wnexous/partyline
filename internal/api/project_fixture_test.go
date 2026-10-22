package api_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// Sessions no longer join v0.3 projects, but the project members stay until the projects are
// removed, so their tests build projects straight in the store.

// createProject stores a project of the owner and returns its id.
func createProject(t *testing.T, env *testkit.Env, owner *testkit.Client, slug, name string) string {
	t.Helper()
	ctx := context.Background()
	id := store.NewID()
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.CreateProject(ctx, db.CreateProjectParams{ID: id, OwnerID: owner.User.ID, Slug: slug, Name: name, CreatedAt: 1})
	}))
	return id
}

// putInProject makes the agent a session of the project.
func putInProject(t *testing.T, env *testkit.Env, agentID, projectID string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.SetAgentProject(ctx, db.SetAgentProjectParams{ProjectID: sql.NullString{String: projectID, Valid: true}, ID: agentID})
	}))
}

// sessionProject creates a project called slug (named like the slug with an upper-case first
// letter) with one AI in it, and returns that AI's id and the project's id.
func sessionProject(t *testing.T, env *testkit.Env, owner *testkit.Client, slug string) (agentID, projectID string) {
	t.Helper()
	projectID = createProject(t, env, owner, slug, projectName(slug))
	agentID = env.NewAI(owner, slug+"-claude")
	putInProject(t, env, agentID, projectID)
	return agentID, projectID
}

func projectName(slug string) string { return string(slug[0]-'a'+'A') + slug[1:] }
