package app

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
)

// OMEM-07: the daily retention job deletes the organization invitations whose 14 days are over, and keeps
// the pending ones.
func TestRetentionDeletesExpiredOrganizationInvitations(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	require.NoError(t, a.Store.Migrate(ctx))
	now := time.Now()
	require.NoError(t, a.Store.WithTx(ctx, func(q *db.Queries) error {
		for _, id := range []string{"u-alice", "u-bob", "u-carol"} {
			if err := q.CreateUser(ctx, db.CreateUserParams{ID: id, Username: id[2:], PasswordHash: "x", Role: "member", CreatedAt: 1}); err != nil {
				return err
			}
			if err := q.CreateIntelligence(ctx, db.CreateIntelligenceParams{ID: id, Kind: "human", DisplayName: id, Handle: id[2:], CreatedAt: 1, UpdatedAt: 1}); err != nil {
				return err
			}
		}
		if err := q.CreateOrganization(ctx, db.CreateOrganizationParams{ID: "o1", Name: "Acme", Slug: "acme", CreatedAt: 1, UpdatedAt: 1}); err != nil {
			return err
		}
		for invitee, expires := range map[string]time.Time{"u-bob": now.Add(-time.Minute), "u-carol": now.Add(time.Hour)} {
			if err := q.UpsertOrgInvitation(ctx, db.UpsertOrgInvitationParams{
				ID: "inv-" + invitee, OrgID: "o1", InviteeIid: invitee, Permission: "guest", InvitedBy: "u-alice",
				CreatedAt: 1, ExpiresAt: expires.UnixMilli(),
			}); err != nil {
				return err
			}
		}
		return nil
	}))
	require.NoError(t, a.Start(ctx))
	require.Eventually(t, func() bool {
		_, err := a.Store.R().GetOrgInvitation(ctx, "inv-u-bob")
		return errors.Is(err, sql.ErrNoRows)
	}, jobDeadline, 10*time.Millisecond, "retention did not delete the expired invitation")
	_, err := a.Store.R().GetOrgInvitation(ctx, "inv-u-carol")
	require.NoError(t, err, "the pending invitation stays")
}
