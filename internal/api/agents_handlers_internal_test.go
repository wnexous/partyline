package api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
)

// An AI is described by its profile: the display name people read, its @handle and its avatar.
func TestToAgentDescribesTheAIByItsProfile(t *testing.T) {
	row := db.Agent{ID: "01a1b2c3", Name: "proj-claude-2", Harness: "claude", Machine: "laptop"}
	got := toAgent(agents.Agent{
		Agent: row, Handle: "proj", ProfileName: "Renamed", ProfileAvatar: "abc",
		OwnerUsername: "alice", OwnerDisplayName: "Alice A.", Level: dto.LevelManage,
	})
	require.Equal(t, dto.IntelligenceRef{
		ID: "01a1b2c3", Kind: dto.IntelligenceKindAI, Role: dto.IntelligenceRoleAI,
		DisplayName: "Renamed", Handle: "proj", AvatarURL: "/api/v1/intelligences/01a1b2c3/avatar?v=abc",
	}, got.Intelligence)
	require.Equal(t, dto.User{ID: "", Username: "alice", DisplayName: "Alice A."}, got.Owner)

	got = toAgent(agents.Agent{Agent: row, OwnerUsername: "alice", Level: dto.LevelManage})
	require.Equal(t, "", got.Intelligence.AvatarURL, "no avatar: no URL")
}

// The agent JSON carries none of the v0.3 identity fields: no name (the @handle and display name
// live in the profile), kind, archived flag or project.
func TestToAgentHasNoLegacyIdentityFields(t *testing.T) {
	raw, err := json.Marshal(toAgent(agents.Agent{Agent: db.Agent{ID: "a1", Name: "legacy"}, Handle: "atlas", ProfileName: "Atlas"}))
	require.NoError(t, err)
	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &keys))
	for _, legacy := range []string{"name", "display_name", "kind", "archived", "project"} {
		require.NotContains(t, keys, legacy)
	}
	require.Contains(t, keys, "intelligence")
}
