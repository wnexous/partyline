package app

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// The daily retention job deletes the finished sends of the platform queue's outbound side 30 days
// after their last change (they hold no text, only what the echo and reply checks need); a send that
// finished 29 days ago stays, and so does one still waiting, whatever its age.
func TestRetentionPurgesOldFinishedPlatformSends(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	require.NoError(t, a.Store.Migrate(ctx))
	user, agent, connector, chat := store.NewID(), store.NewID(), store.NewID(), store.NewID()
	now := time.Now()
	day := 24 * time.Hour
	oldSent, youngSent, oldFailed, oldWaiting := store.NewID(), store.NewID(), store.NewID(), store.NewID()
	require.NoError(t, a.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateUser(ctx, db.CreateUserParams{ID: user, Username: "alice", PasswordHash: "x", Role: "member"}); err != nil {
			return err
		}
		if err := q.CreateAgent(ctx, db.CreateAgentParams{ID: agent, OwnerID: user, Name: "atlas"}); err != nil {
			return err
		}
		if err := q.CreateConnector(ctx, db.CreateConnectorParams{ID: connector, OwnerID: user, Kind: "telegram", Name: "tg", ConfigJson: "{}"}); err != nil {
			return err
		}
		if err := q.CreateChat(ctx, db.CreateChatParams{ID: chat, ConnectorID: connector, ExternalID: "42", Title: "Family", Kind: "group"}); err != nil {
			return err
		}
		insert := func(id string, age time.Duration) error {
			at := now.Add(-age).UnixMilli()
			return q.InsertPlatformSend(ctx, db.InsertPlatformSendParams{
				ID: id, ConnectorID: connector, ChatID: chat, AgentID: agent, NextAttemptAt: at, CreatedAt: at, UpdatedAt: at,
			})
		}
		for id, age := range map[string]time.Duration{oldSent: 31 * day, youngSent: 29 * day} {
			if err := insert(id, age); err != nil {
				return err
			}
			if err := q.MarkPlatformSendSent(ctx, db.MarkPlatformSendSentParams{ExternalID: "ext-" + id[:6], UpdatedAt: now.Add(-age).UnixMilli(), ID: id}); err != nil {
				return err
			}
		}
		if err := insert(oldFailed, 31*day); err != nil {
			return err
		}
		if err := q.MarkPlatformSendFailed(ctx, db.MarkPlatformSendFailedParams{Attempts: 5, LastError: "gone", UpdatedAt: now.Add(-31 * day).UnixMilli(), ID: oldFailed}); err != nil {
			return err
		}
		if err := insert(oldWaiting, 31*day); err != nil {
			return err
		}
		return q.MarkPlatformSendRetry(ctx, db.MarkPlatformSendRetryParams{
			Attempts: 1, NextAttemptAt: now.Add(time.Hour).UnixMilli(), LastError: "later", UpdatedAt: now.Add(-31 * day).UnixMilli(), ID: oldWaiting,
		})
	}))

	require.NoError(t, a.Start(ctx))

	exists := func(id string) bool {
		_, err := a.Store.R().GetPlatformSend(ctx, id)
		return !errors.Is(err, sql.ErrNoRows)
	}
	require.Eventually(t, func() bool { return !exists(oldSent) && !exists(oldFailed) },
		jobDeadline, 10*time.Millisecond, "retention did not delete the sends that finished 31 days ago")
	require.True(t, exists(youngSent), "finished 29 days ago: kept")
	require.True(t, exists(oldWaiting), "still waiting for a retry: kept")
}
