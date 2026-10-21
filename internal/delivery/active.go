package delivery

import (
	"context"
	"database/sql"

	"github.com/wnexous/partyline/internal/store/db"
)

// ActiveSession returns the active session of a project (PROJ-07): the most recently
// connected of its agents that is online. ok is false when none is online; the project's
// messages then wait for its next session (PROJ-08).
func ActiveSession(ctx context.Context, q *db.Queries, online func(agentID string) bool, projectID string) (a db.Agent, ok bool, err error) {
	if online == nil || projectID == "" {
		return db.Agent{}, false, nil
	}
	agents, err := q.ListProjectSessions(ctx, sql.NullString{String: projectID, Valid: true})
	if err != nil {
		return db.Agent{}, false, err
	}
	for _, a := range agents {
		if online(a.ID) {
			return a, true, nil
		}
	}
	return db.Agent{}, false, nil
}
