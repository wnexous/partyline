package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// The handle comes with each listed agent; an agent without a profile has none. (What v0.3
// archived stays listed after the upgrade: TestUpgradeV03ListsEveryAgent.)
func TestListVisibleAgentsReturnsTheHandleOfEachProfile(t *testing.T) {
	e := newSchemaEnv(t)
	e.must("INSERT INTO users (id, username, password_hash, role, created_at) VALUES ('u1', 'alice', 'x', 'member', 1)")
	e.must("INSERT INTO agents (id, owner_id, name, created_at) VALUES ('a1', 'u1', 'old-temp', 1)")
	e.must("INSERT INTO agents (id, owner_id, name, created_at) VALUES ('a2', 'u1', 'plain', 1)")
	require.NoError(t, e.intelligence("a1", "ai", "old-temp-handle"))

	rows, err := e.s.R().ListVisibleAgents(context.Background(), "u1")
	require.NoError(t, err)
	got := map[string]string{}
	for _, r := range rows {
		got[r.Agent.ID] = r.Handle
	}
	require.Equal(t, map[string]string{"a1": "old-temp-handle", "a2": ""}, got, "the handle comes with the row; an agent without a profile has none")
}
