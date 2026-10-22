package api

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
)

// Every AI is permanent: whatever a v0.3 row says (temporary, archived), the API reports it
// fixed and not archived, names it by its handle (its row's name when it has no profile) and
// shows the display name of its profile (the row's when it has none).
func TestToAgentReportsTheLegacyFieldsAsConstants(t *testing.T) {
	row := db.Agent{
		ID: "01a1b2c3", Name: "proj-claude-2", DisplayName: "Proj", Harness: "claude", Machine: "laptop",
		Kind: "temporary", ArchivedAt: sql.NullInt64{Int64: 5, Valid: true},
	}
	got := toAgent(agents.Agent{Agent: row, Handle: "proj", OwnerUsername: "alice", Level: dto.LevelManage})
	require.Equal(t, []any{"proj", "Proj", dto.AgentKindFixed, false}, []any{got.Name, got.DisplayName, got.Kind, got.Archived})

	got = toAgent(agents.Agent{Agent: row, Handle: "proj", ProfileName: "Renamed", OwnerUsername: "alice", Level: dto.LevelManage})
	require.Equal(t, "Renamed", got.DisplayName, "the profile's display name wins over the row's mirror")

	got = toAgent(agents.Agent{Agent: row, OwnerUsername: "alice", Level: dto.LevelManage})
	require.Equal(t, []string{"proj-claude-2", "Proj"}, []string{got.Name, got.DisplayName}, "an agent without a profile goes by its row's name")
}
