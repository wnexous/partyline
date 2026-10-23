package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/config"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// settableClock is a clock the test moves.
type settableClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *settableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *settableClock) Set(t time.Time) {
	c.mu.Lock()
	c.now = t
	c.mu.Unlock()
}

// CID-09, CID-10: the running app purges the platform queue (unacknowledged items after 7 days,
// acknowledged ones 24 hours after the ack), counts the first as dropped for their AI, and
// releases the blobs of what it deleted; young items and their blobs stay.
func TestAppPurgesThePlatformQueueAndReleasesItsBlobs(t *testing.T) {
	ctx := context.Background()
	env := map[string]string{"PARTYLINE_DATA_DIR": t.TempDir()}
	cfg, err := config.Load(func(k string) string { return env[k] })
	require.NoError(t, err)
	clock := &settableClock{now: time.Now()}
	a, err := New(cfg, Options{Logger: slog.New(slog.NewTextHandler(t.Output(), nil)), Now: clock.Now})
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Shutdown(context.Background()) })
	require.NoError(t, a.Store.Migrate(ctx))

	user, agent, connector, chat := store.NewID(), store.NewID(), store.NewID(), store.NewID()
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
		return q.CreateChat(ctx, db.CreateChatParams{ID: chat, ConnectorID: connector, ExternalID: "42", Title: "Family", Kind: "group"})
	}))

	start := time.Now()
	blob := func(content string) string {
		t.Helper()
		sha, _, err := a.Blobs.Put(strings.NewReader(content), 1<<20)
		require.NoError(t, err)
		old := start.Add(-2 * time.Hour)
		require.NoError(t, os.Chtimes(filepath.Join(cfg.DataDir, BlobsDir, sha[:2], sha), old, old))
		return sha
	}
	exists := func(sha string) bool {
		_, err := os.Stat(filepath.Join(cfg.DataDir, BlobsDir, sha[:2], sha))
		return err == nil
	}
	queue := func(external string, received time.Time, sha string) string {
		t.Helper()
		id, queued, err := a.Platform.Enqueue(ctx, platform.Item{
			AgentID: agent, ConnectorID: connector, ChatID: chat, ExternalID: external, SenderName: "Pat", Text: "hello", ReceivedAt: received,
			Attachments: []platform.Attachment{{Name: external + ".png", Mime: "image/png", Size: 3, SHA256: sha, Status: dto.AttachmentStatusStored}},
		})
		require.NoError(t, err)
		require.True(t, queued)
		return id
	}
	ackAt := func(id string, at time.Time) {
		t.Helper()
		clock.Set(at)
		require.NoError(t, a.Platform.Ack(ctx, agent, id))
	}
	lostBlob, ackedBlob, waitingBlob, recentBlob := blob("lost"), blob("acked"), blob("waiting"), blob("recent")
	lost := queue("lost", start.Add(-8*24*time.Hour), lostBlob)
	acked := queue("acked", start.Add(-3*24*time.Hour), ackedBlob)
	ackAt(acked, start.Add(-25*time.Hour))
	waiting := queue("waiting", start.Add(-6*24*time.Hour), waitingBlob)
	recent := queue("recent", start.Add(-3*24*time.Hour), recentBlob)
	ackAt(recent, start.Add(-time.Hour))
	clock.Set(start)

	require.NoError(t, a.Start(ctx))

	gone := func(id string) bool {
		_, err := a.Platform.Get(ctx, agent, id)
		return errors.Is(err, platform.ErrNotFound)
	}
	require.Eventually(t, func() bool { return gone(lost) && gone(acked) && !exists(lostBlob) && !exists(ackedBlob) },
		jobDeadline, 10*time.Millisecond, "the retention job did not purge the platform queue")
	require.False(t, gone(waiting), "unacknowledged for 6 days: kept")
	require.False(t, gone(recent), "acknowledged an hour ago: kept")
	require.True(t, exists(waitingBlob))
	require.True(t, exists(recentBlob))
	n, err := a.Platform.TakeDropped(ctx, agent)
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "the item nobody acknowledged was dropped; the acknowledged one was delivered")
}
