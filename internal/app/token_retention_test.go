package app

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// TOKH-03: the daily retention job also deletes tokens revoked more than 30 days earlier;
// a token revoked 29 days earlier stays.
func TestRetentionPurgesOldRevokedTokens(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	require.NoError(t, a.Store.Migrate(ctx))
	user, old, recent := store.NewID(), store.NewID(), store.NewID()
	day := 24 * time.Hour
	require.NoError(t, a.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateUser(ctx, db.CreateUserParams{ID: user, Username: "alice", PasswordHash: "x", Role: "member"}); err != nil {
			return err
		}
		for id, age := range map[string]time.Duration{old: 31 * day, recent: 29 * day} {
			if err := q.CreateToken(ctx, db.CreateTokenParams{ID: id, UserID: user, Kind: "user", Name: id, Prefix: "pl_user_" + id[:4], TokenHash: id}); err != nil {
				return err
			}
			if _, err := q.RevokeToken(ctx, db.RevokeTokenParams{RevokedAt: sql.NullInt64{Int64: time.Now().Add(-age).UnixMilli(), Valid: true}, ID: id, UserID: user}); err != nil {
				return err
			}
		}
		return nil
	}))
	require.NoError(t, a.Start(ctx))
	require.Eventually(t, func() bool {
		list, err := a.Store.R().ListTokensByUser(ctx, user)
		return err == nil && len(list) == 1
	}, jobDeadline, 10*time.Millisecond, "retention did not delete the token revoked 31 days ago")
	list, err := a.Store.R().ListTokensByUser(ctx, user)
	require.NoError(t, err)
	require.Equal(t, recent, list[0].ID)
}
