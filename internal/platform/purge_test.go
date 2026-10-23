package platform_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

const (
	day = 24 * time.Hour
	ms  = time.Millisecond
)

// withFile adds one stored file with the given blob hash to the item.
func withFile(it platform.Item, sha string) platform.Item {
	it.Attachments = append(it.Attachments, platform.Attachment{
		Name: sha + ".png", Mime: "image/png", Size: 3, SHA256: sha, Status: dto.AttachmentStatusStored,
	})
	return it
}

func nullMs(t time.Time) sql.NullInt64 { return sql.NullInt64{Int64: t.UnixMilli(), Valid: true} }

// received is an item of Atlas received at the given time.
func (f *fixture) received(external string, at time.Time) platform.Item {
	it := f.item(external)
	it.ReceivedAt = at
	return it
}

// ackAt acknowledges the item at the given time.
func (f *fixture) ackAt(t *testing.T, agent, id string, at time.Time) {
	t.Helper()
	f.clock.Set(at)
	require.NoError(t, f.q.Ack(context.Background(), agent, id))
}

func (f *fixture) has(t *testing.T, agent, id string) bool {
	t.Helper()
	_, err := f.q.Get(context.Background(), agent, id)
	if err == nil {
		return true
	}
	require.ErrorIs(t, err, platform.ErrNotFound)
	return false
}

func (f *fixture) files(t *testing.T, sha string) int64 {
	t.Helper()
	n, err := f.st.R().CountPlatformAttachmentsBySHA(context.Background(), sha)
	require.NoError(t, err)
	return n
}

func (f *fixture) purge(t *testing.T, now time.Time) platform.PurgeResult {
	t.Helper()
	res, err := f.q.Purge(context.Background(), now)
	require.NoError(t, err)
	return res
}

// CID-10: an item nobody acknowledged is deleted after 7 days and counted as dropped for its AI:
// at 7 days minus a millisecond it stays, at 7 days it goes with its files.
func TestPurgeDeletesAnUnackedItemAtSevenDays(t *testing.T) {
	f := newFixture(t)
	id := f.enqueue(t, withFile(f.received("1", t0), "aa"))

	res := f.purge(t, t0.Add(platform.UnackedTTL-ms))
	require.Zero(t, res.Expired, "7 days minus 1 ms is not old enough")
	require.True(t, f.has(t, f.atlas, id))
	require.Equal(t, int64(1), f.files(t, "aa"))
	require.Zero(t, f.takeDropped(t, f.atlas))

	res = f.purge(t, t0.Add(platform.UnackedTTL))
	require.Equal(t, int64(1), res.Expired)
	require.False(t, f.has(t, f.atlas, id), "deleted at 7 days")
	require.Zero(t, f.files(t, "aa"), "with its files")
	require.Empty(t, f.unacked(t, f.atlas))
	require.Equal(t, int64(1), f.takeDropped(t, f.atlas), "and counted as dropped, for the next connection's notice")
	require.Equal(t, []string{"aa"}, res.Files, "its blob is up for release")
	require.Equal(t, 7*day, platform.UnackedTTL, "the spec's 7 days")
}

// CID-10: each AI is told about its own losses; items that are still young stay.
func TestPurgeCountsDroppedItemsPerAI(t *testing.T) {
	f := newFixture(t)
	f.enqueue(t, f.received("old-1", t0.Add(-9*day)))
	f.enqueue(t, f.received("old-2", t0.Add(-8*day)))
	f.enqueue(t, f.received("young", t0.Add(-6*day)))
	n := f.received("old-n", t0.Add(-30*day))
	n.AgentID, n.ChatID = f.notes, f.tgDM
	f.enqueue(t, n)
	quiet := f.received("quiet", t0.Add(-1*day))
	quiet.AgentID, quiet.ChatID = f.notes, f.tgDM
	f.enqueue(t, quiet)

	res := f.purge(t, t0)

	require.Equal(t, int64(3), res.Expired)
	require.Equal(t, []string{"hello young"}, texts(f.unacked(t, f.atlas)))
	require.Equal(t, []string{"hello quiet"}, texts(f.unacked(t, f.notes)))
	require.Equal(t, int64(2), f.takeDropped(t, f.atlas))
	require.Equal(t, int64(1), f.takeDropped(t, f.notes))
}

// CID-09: an acknowledged item and its files go 24 hours after the ack: 24 hours minus a
// millisecond after it everything is still there, at 24 hours the item and its files are
// deleted and the blob is released. It is not counted as dropped.
func TestPurgeDeletesAnAckedItemTwentyFourHoursAfterTheAck(t *testing.T) {
	f := newFixture(t)
	id := f.enqueue(t, withFile(f.received("1", t0), "aa"))
	ackedAt := t0.Add(2 * time.Hour)
	f.ackAt(t, f.atlas, id, ackedAt)

	res := f.purge(t, ackedAt.Add(platform.AckedTTL-ms))
	require.Zero(t, res.Acked, "24 hours minus 1 ms after the ack")
	got, err := f.q.Get(context.Background(), f.atlas, id)
	require.NoError(t, err, "the item stays, so reply keeps working")
	require.Len(t, got.Attachments, 1, "and its files stay downloadable")
	require.Equal(t, int64(1), f.files(t, "aa"))
	require.Empty(t, res.Files)

	res = f.purge(t, ackedAt.Add(platform.AckedTTL))
	require.Equal(t, int64(1), res.Acked)
	require.False(t, f.has(t, f.atlas, id), "deleted 24 hours after the ack")
	require.Zero(t, f.files(t, "aa"))
	require.Equal(t, []string{"aa"}, res.Files)
	require.Zero(t, f.takeDropped(t, f.atlas), "an acknowledged item was delivered, not dropped")
	require.Equal(t, day, platform.AckedTTL, "the spec's 24 hours")
}

// CID-09: the 24 hours run from the ack, not from the arrival: an old item acknowledged an hour
// ago stays; and an item nobody acknowledged is not governed by the 24 hours.
func TestPurgeCountsTheTwentyFourHoursFromTheAck(t *testing.T) {
	f := newFixture(t)
	acked := f.enqueue(t, f.received("acked", t0.Add(-6*day)))
	f.ackAt(t, f.atlas, acked, t0.Add(-time.Hour))
	waiting := f.enqueue(t, f.received("waiting", t0.Add(-3*day)))

	res := f.purge(t, t0)

	require.Zero(t, res.Acked)
	require.Zero(t, res.Expired)
	require.True(t, f.has(t, f.atlas, acked))
	require.True(t, f.has(t, f.atlas, waiting), "unacked items live for 7 days, not 24 hours")
	require.Equal(t, []string{"hello waiting"}, texts(f.unacked(t, f.atlas)))
}

// An item acknowledged less than 24 hours ago but received more than 7 days ago is not a lost
// item: the 7 days apply to unacknowledged items only.
func TestPurgeDoesNotExpireAnAckedItemByItsArrival(t *testing.T) {
	f := newFixture(t)
	id := f.enqueue(t, f.received("1", t0.Add(-10*day)))
	f.ackAt(t, f.atlas, id, t0.Add(-2*time.Hour))

	res := f.purge(t, t0)

	require.Zero(t, res.Expired)
	require.Zero(t, res.Acked)
	require.True(t, f.has(t, f.atlas, id))
	require.Zero(t, f.takeDropped(t, f.atlas))
}

// A file used by several expired items is listed once; a file without a blob is not listed.
func TestPurgeListsEachBlobOnce(t *testing.T) {
	f := newFixture(t)
	a := withFile(withFile(f.received("1", t0.Add(-8*day)), "aa"), "bb")
	a.Attachments = append(a.Attachments, platform.Attachment{Name: "big.mov", Mime: "video/quicktime", Size: 9, Status: dto.AttachmentStatusTooLarge})
	f.enqueue(t, a)
	f.enqueue(t, withFile(f.received("2", t0.Add(-8*day)), "aa"))
	acked := f.enqueue(t, withFile(f.received("3", t0.Add(-8*day)), "cc"))
	f.ackAt(t, f.atlas, acked, t0.Add(-2*day))

	res := f.purge(t, t0)

	require.Equal(t, int64(2), res.Expired)
	require.Equal(t, int64(1), res.Acked)
	require.ElementsMatch(t, []string{"aa", "bb", "cc"}, res.Files)
}

// The purge works through any number of items in bounded transactions and counts every one.
func TestPurgeHandlesMoreItemsThanOneBatch(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	const atlas, notes, acked = 2300, 1200, 2100
	insert := func(agent string, n int, prefix string, at time.Time, ackAt time.Time) {
		require.NoError(t, f.st.WithTx(ctx, func(q *db.Queries) error {
			for i := range n {
				id := store.NewID()
				if _, err := q.InsertPlatformItem(ctx, db.InsertPlatformItemParams{
					ID: id, AgentID: agent, ConnectorID: f.tg, ChatID: f.tgGroup, ExternalID: fmt.Sprintf("%s-%d", prefix, i),
					Kind: "text", Text: "x", ReceivedAt: at.UnixMilli(),
				}); err != nil {
					return err
				}
				if ackAt.IsZero() {
					continue
				}
				if _, err := q.AckPlatformItem(ctx, db.AckPlatformItemParams{AckedAt: nullMs(ackAt), ID: id, AgentID: agent}); err != nil {
					return err
				}
			}
			return nil
		}))
	}
	insert(f.atlas, atlas, "a", t0.Add(-8*day), time.Time{})
	insert(f.notes, notes, "n", t0.Add(-8*day), time.Time{})
	insert(f.atlas, acked, "k", t0.Add(-8*day), t0.Add(-2*day))
	insert(f.atlas, 50, "fresh", t0, time.Time{})

	res := f.purge(t, t0)

	require.Equal(t, int64(atlas+notes), res.Expired)
	require.Equal(t, int64(acked), res.Acked)
	require.Equal(t, int64(atlas), f.takeDropped(t, f.atlas))
	require.Equal(t, int64(notes), f.takeDropped(t, f.notes))
	require.Len(t, f.unacked(t, f.atlas), 50, "only the fresh items are left")
	again := f.purge(t, t0)
	require.Zero(t, again.Expired+again.Acked, "a second pass finds nothing")
}

// The runner purges when it starts and again every interval, hands the released blobs to its
// callback and stops with its context.
func TestRunPurgesWhenItStartsAndEveryInterval(t *testing.T) {
	f := newFixture(t)
	first := f.enqueue(t, withFile(f.received("1", t0.Add(-8*day)), "aa"))

	var mu sync.Mutex
	var released []string
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		done <- f.q.Run(ctx, 5*time.Millisecond, func(_ context.Context, shas []string) {
			mu.Lock()
			released = append(released, shas...)
			mu.Unlock()
		}, nil)
	}()
	got := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), released...)
	}

	gone := func(id string) func() bool {
		return func() bool {
			_, err := f.q.Get(context.Background(), f.atlas, id)
			return errors.Is(err, platform.ErrNotFound)
		}
	}
	require.Eventually(t, gone(first), 5*time.Second, 5*time.Millisecond, "the first pass purged")
	require.Equal(t, []string{"aa"}, got())

	// Later the clock moves on; the next pass picks up what has expired meanwhile.
	second := f.enqueue(t, withFile(f.received("2", t0), "bb"))
	f.clock.Set(t0.Add(platform.UnackedTTL))
	require.Eventually(t, gone(second), 5*time.Second, 5*time.Millisecond, "a later pass purged")
	require.Eventually(t, func() bool { return len(got()) == 2 }, 5*time.Second, 5*time.Millisecond)
	require.Equal(t, []string{"aa", "bb"}, got())

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the runner did not stop with its context")
	}
}
