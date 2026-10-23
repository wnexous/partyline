package platform_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/platform"
)

// CID-08, CID-09: Take hands an AI its oldest unacknowledged items, at most limit, with their
// text, and acknowledges them in the same step: the text is gone from the queue, the rest of the
// item stays, and the next call goes on with the items after them.
func TestTakeReturnsTheOldestItemsAndAcknowledgesThem(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	var ids []string
	for _, m := range []string{"first", "second", "third"} {
		ids = append(ids, f.enqueue(t, f.item(m)))
	}

	got, err := f.q.Take(ctx, f.atlas, 2)
	require.NoError(t, err)
	require.Equal(t, []string{"hello first", "hello second"}, texts(got), "the oldest two, with their text")
	require.Equal(t, ids[:2], []string{got[0].ID, got[1].ID})
	require.True(t, got[0].AckedAt.IsZero(), "the events are the items as they were taken")

	for i, id := range ids[:2] {
		kept, err := f.q.Get(ctx, f.atlas, id)
		require.NoError(t, err)
		require.False(t, kept.AckedAt.IsZero(), "item %d acknowledged", i)
		require.Empty(t, kept.Text, "the text is deleted at once")
		require.Equal(t, "Pat", kept.SenderName, "the rest of the item stays")
	}
	require.Equal(t, []string{"hello third"}, texts(f.unacked(t, f.atlas)))

	rest, err := f.q.Take(ctx, f.atlas, 20)
	require.NoError(t, err)
	require.Equal(t, []string{"hello third"}, texts(rest))
	none, err := f.q.Take(ctx, f.atlas, 20)
	require.NoError(t, err)
	require.NotNil(t, none, "an empty queue gives an empty list")
	require.Empty(t, none)
}

// Take never touches another AI's items, takes nothing for a limit of zero, and announces
// nothing: a taken item is consumed, not queued.
func TestTakeLeavesOtherAIsAndZeroLimitsAlone(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.enqueue(t, f.item("mine"))
	other := f.item("theirs")
	other.AgentID = f.notes
	f.enqueue(t, other)
	ch, cancel := f.bus.Subscribe(16)
	defer cancel()

	for _, limit := range []int{0, -1} {
		got, err := f.q.Take(ctx, f.atlas, limit)
		require.NoError(t, err)
		require.Empty(t, got, "limit %d", limit)
	}
	require.Len(t, f.unacked(t, f.atlas), 1, "nothing was taken")

	got, err := f.q.Take(ctx, f.atlas, 20)
	require.NoError(t, err)
	require.Equal(t, []string{"hello mine"}, texts(got))
	require.Equal(t, []string{"hello theirs"}, texts(f.unacked(t, f.notes)), "Notes keeps its item and its text")
	require.Empty(t, ch, "taking announces nothing")
}

// Take carries the files of the items it returns, and the rows stay for the 24 hours of CID-09.
func TestTakeCarriesTheFilesOfTheItems(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	it := f.item("77")
	it.Attachments = []platform.Attachment{
		{Name: "a.png", Mime: "image/png", Size: 12, SHA256: "aa", Status: dto.AttachmentStatusStored},
		{Name: "big.mov", Mime: "video/quicktime", Size: 9000, Status: dto.AttachmentStatusTooLarge},
	}
	f.enqueue(t, it)

	got, err := f.q.Take(ctx, f.atlas, 20)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Len(t, got[0].Attachments, 2)
	require.Equal(t, []string{"a.png", "image/png", "aa", string(dto.AttachmentStatusStored)},
		[]string{got[0].Attachments[0].Name, got[0].Attachments[0].Mime, got[0].Attachments[0].SHA256, string(got[0].Attachments[0].Status)})
	kept, err := f.q.Get(ctx, f.atlas, got[0].ID)
	require.NoError(t, err)
	require.Len(t, kept.Attachments, 2, "the files stay after the item was taken")
}

// Two callers that take at the same time never get the same item, and together they get all.
func TestConcurrentTakesNeverReturnAnItemTwice(t *testing.T) {
	f := newFixture(t)
	f.seed(t, f.atlas, 40, "m")

	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			for range 30 { // bounded: a Take that returns the same items again must fail the test, not hang it
				got, err := f.q.Take(context.Background(), f.atlas, 3)
				if err != nil {
					t.Error(err)
					return
				}
				if len(got) == 0 {
					return
				}
				mu.Lock()
				for _, e := range got {
					seen[e.ID]++
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	require.Len(t, seen, 40, "every item was taken")
	for id, n := range seen {
		require.Equal(t, 1, n, "item %s taken %d times", id, n)
	}
	require.Empty(t, f.unacked(t, f.atlas))
}

// CID-09: a file of an item is found by its id for the AI that holds the item, acknowledged or
// not, with what serving it needs; another AI, an unknown id and a purged file are not found.
func TestAttachmentIsFoundForTheAIThatHoldsTheItem(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	it := f.item("77")
	it.Attachments = []platform.Attachment{
		{Name: "a.png", Mime: "image/png", Size: 12, SHA256: "aa", Status: dto.AttachmentStatusStored},
		{Name: "big.mov", Mime: "video/quicktime", Size: 9000, Status: dto.AttachmentStatusTooLarge},
	}
	id := f.enqueue(t, it)
	item, err := f.q.Get(ctx, f.atlas, id)
	require.NoError(t, err)
	stored, tooLarge := item.Attachments[0], item.Attachments[1]

	got, err := f.q.Attachment(ctx, f.atlas, stored.ID)
	require.NoError(t, err)
	require.Equal(t, platform.Attachment{ID: stored.ID, Name: "a.png", Mime: "image/png", Size: 12, SHA256: "aa", Status: dto.AttachmentStatusStored}, got)
	got, err = f.q.Attachment(ctx, f.atlas, tooLarge.ID)
	require.NoError(t, err)
	require.Equal(t, dto.AttachmentStatusTooLarge, got.Status)
	require.Empty(t, got.SHA256, "a file that was not stored has no blob")

	require.NoError(t, f.q.Ack(ctx, f.atlas, id))
	_, err = f.q.Attachment(ctx, f.atlas, stored.ID)
	require.NoError(t, err, "still there after the acknowledgement")

	_, err = f.q.Attachment(ctx, f.notes, stored.ID)
	require.ErrorIs(t, err, platform.ErrNotFound, "another AI's file")
	_, err = f.q.Attachment(ctx, f.atlas, "missing")
	require.ErrorIs(t, err, platform.ErrNotFound)

	f.purge(t, t0.Add(platform.AckedTTL-ms))
	_, err = f.q.Attachment(ctx, f.atlas, stored.ID)
	require.NoError(t, err, "24 hours minus 1 ms after the acknowledgement")
	f.purge(t, t0.Add(platform.AckedTTL))
	_, err = f.q.Attachment(ctx, f.atlas, stored.ID)
	require.ErrorIs(t, err, platform.ErrNotFound, "24 hours after the acknowledgement")
}

// A file follows its item to the connection's new operator (CID-05), and only to it.
func TestAttachmentFollowsTheItemToTheNewOperator(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	it := f.item("77")
	it.Attachments = []platform.Attachment{{Name: "a.png", Mime: "image/png", Size: 12, SHA256: "aa", Status: dto.AttachmentStatusStored}}
	id := f.enqueue(t, it)
	item, err := f.q.Get(ctx, f.atlas, id)
	require.NoError(t, err)
	file := item.Attachments[0].ID

	f.moveTo(t, f.tg, f.notes)
	_, err = f.q.Attachment(ctx, f.atlas, file)
	require.ErrorIs(t, err, platform.ErrNotFound, "the old operator lost it")
	_, err = f.q.Attachment(ctx, f.notes, file)
	require.NoError(t, err, "the new operator holds it")
}
