package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Archiving is gone: an agent that v0.3 archived (and a temporary one) is listed like any other
// after the upgrade, with its handle.
func TestListAgentsForUserIncludesWhatV03Archived(t *testing.T) {
	e := newSchemaEnv(t)
	e.must("INSERT INTO users (id, username, display_name, password_hash, role, created_at) VALUES ('u1', 'alice', '', 'x', 'member', 1)")
	e.must("INSERT INTO agents (id, owner_id, name, created_at, kind, archived_at) VALUES ('a1', 'u1', 'old-temp', 1, 'temporary', 5)")
	e.must("INSERT INTO agents (id, owner_id, name, created_at) VALUES ('a2', 'u1', 'plain', 1)")
	require.NoError(t, e.intelligence("a1", "ai", "old-temp-handle"))

	rows, err := e.s.R().ListAgentsForUser(context.Background(), "u1")
	require.NoError(t, err)
	got := map[string]string{}
	for _, r := range rows {
		got[r.Agent.ID] = r.Handle
	}
	require.Equal(t, map[string]string{"a1": "old-temp-handle", "a2": ""}, got, "the handle comes with the row; an agent without a profile has none")
}
