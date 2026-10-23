package platform_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

var t0 = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// fixture is alice with two AIs (Atlas and Notes), a Telegram connection (tg, two chats) and a
// WhatsApp connection (wa, one chat).
type fixture struct {
	st    *store.Store
	bus   *events.Bus
	clock *clock
	q     *platform.Queue

	alice, atlas, notes string
	tg, wa              string
	tgGroup, tgDM       string // chats of tg
	waGroup             string // chat of wa
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "partyline.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	require.NoError(t, st.Migrate(ctx))
	f := &fixture{
		st: st, bus: events.NewBus(), clock: &clock{now: t0},
		alice: store.NewID(), atlas: store.NewID(), notes: store.NewID(),
		tg: store.NewID(), wa: store.NewID(), tgGroup: store.NewID(), tgDM: store.NewID(), waGroup: store.NewID(),
	}
	require.NoError(t, st.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateUser(ctx, db.CreateUserParams{ID: f.alice, Username: "alice", PasswordHash: "x", Role: "member"}); err != nil {
			return err
		}
		for id, name := range map[string]string{f.atlas: "atlas", f.notes: "notes"} {
			if err := q.CreateAgent(ctx, db.CreateAgentParams{ID: id, OwnerID: f.alice, Name: name}); err != nil {
				return err
			}
		}
		for id, kind := range map[string]string{f.tg: "telegram", f.wa: "whatsapp"} {
			if err := q.CreateConnector(ctx, db.CreateConnectorParams{ID: id, OwnerID: f.alice, Kind: kind, Name: kind, ConfigJson: "{}"}); err != nil {
				return err
			}
		}
		for _, c := range []struct{ id, connector, external, title, kind string }{
			{f.tgGroup, f.tg, "-100", "Family", "group"},
			{f.tgDM, f.tg, "42", "Pat", "dm"},
			{f.waGroup, f.wa, "120363000000000001@g.us", "Team", "group"},
		} {
			if err := q.CreateChat(ctx, db.CreateChatParams{ID: c.id, ConnectorID: c.connector, ExternalID: c.external, Title: c.title, Kind: c.kind}); err != nil {
				return err
			}
		}
		return nil
	}))
	f.q = platform.New(platform.Deps{Store: st, Bus: f.bus, Now: f.clock.Now})
	return f
}

// item is a message of the Telegram group for Atlas, with the platform id external.
func (f *fixture) item(external string) platform.Item {
	return platform.Item{
		AgentID: f.atlas, ConnectorID: f.tg, ChatID: f.tgGroup, ExternalID: external,
		SenderID: "5511999990000", SenderName: "Pat", Text: "hello " + external,
	}
}

func (f *fixture) enqueue(t *testing.T, it platform.Item) string {
	t.Helper()
	id, queued, err := f.q.Enqueue(context.Background(), it)
	require.NoError(t, err)
	require.True(t, queued)
	return id
}

func (f *fixture) unacked(t *testing.T, agent string) []platform.Event {
	t.Helper()
	list, err := f.q.Unacked(context.Background(), agent, 0, 100000)
	require.NoError(t, err)
	return list
}

func texts(list []platform.Event) []string {
	out := make([]string, len(list))
	for i, e := range list {
		out[i] = e.Text
	}
	return out
}

func (f *fixture) takeDropped(t *testing.T, agent string) int64 {
	t.Helper()
	n, err := f.q.TakeDropped(context.Background(), agent)
	require.NoError(t, err)
	return n
}

// seed queues n unacknowledged items for the AI straight through the store, in one transaction:
// the cap tests need thousands of rows.
func (f *fixture) seed(t *testing.T, agent string, n int, prefix string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, f.st.WithTx(ctx, func(q *db.Queries) error {
		for i := range n {
			if _, err := q.InsertPlatformItem(ctx, db.InsertPlatformItemParams{
				ID: store.NewID(), AgentID: agent, ConnectorID: f.tg, ChatID: f.tgGroup,
				ExternalID: fmt.Sprintf("%s-%d", prefix, i), Kind: "text", Text: fmt.Sprintf("%s-%d", prefix, i), ReceivedAt: t0.UnixMilli(),
			}); err != nil {
				return err
			}
		}
		return nil
	}))
}

// CID-06, CID-08: a queued item reaches the AI with everything the platform event carries.
func TestEnqueueQueuesAnItemWithEveryEventField(t *testing.T) {
	f := newFixture(t)
	at := t0.Add(-time.Minute)
	id, queued, err := f.q.Enqueue(context.Background(), platform.Item{
		AgentID: f.atlas, ConnectorID: f.tg, ChatID: f.tgGroup, ExternalID: "77",
		SenderID: "5511999990000", SenderName: "Pat", Trusted: true, FromSelf: false, Kind: dto.MessageKindImage,
		Text: "look at this", ReceivedAt: at,
		Attachments: []platform.Attachment{
			{Name: "a.png", Mime: "image/png", Size: 12, SHA256: "aa", Status: dto.AttachmentStatusStored},
			{Name: "big.mov", Mime: "video/quicktime", Size: 9000, Status: dto.AttachmentStatusTooLarge},
		},
	})
	require.NoError(t, err)
	require.True(t, queued)
	require.NotEmpty(t, id)

	list := f.unacked(t, f.atlas)
	require.Len(t, list, 1)
	e := list[0]
	require.Equal(t, id, e.ID)
	require.Equal(t, []string{"telegram", f.tg, f.tgGroup, "Family", "group", "Pat", "5511999990000", "look at this", "77"},
		[]string{e.Platform, e.ConnectionID, e.ChatID, e.ChatTitle, string(e.ChatKind), e.SenderName, e.SenderID, e.Text, e.ExternalID})
	require.True(t, e.Trusted)
	require.False(t, e.FromSelf)
	require.Equal(t, dto.MessageKindImage, e.Kind)
	require.True(t, e.ReceivedAt.Equal(at), "received_at is the platform time: %v", e.ReceivedAt)
	require.Positive(t, e.Seq)
	require.True(t, e.AckedAt.IsZero())
	require.Len(t, e.Attachments, 2)
	require.Equal(t, []string{"a.png", "image/png", "aa", string(dto.AttachmentStatusStored)},
		[]string{e.Attachments[0].Name, e.Attachments[0].Mime, e.Attachments[0].SHA256, string(e.Attachments[0].Status)})
	require.Equal(t, int64(12), e.Attachments[0].Size)
	require.Equal(t, []string{"big.mov", "", string(dto.AttachmentStatusTooLarge)},
		[]string{e.Attachments[1].Name, e.Attachments[1].SHA256, string(e.Attachments[1].Status)}, "a file without a blob is listed too")
	require.NotEmpty(t, e.Attachments[0].ID)
	require.NotEqual(t, e.Attachments[0].ID, e.Attachments[1].ID)

	// Other kinds of chat and platform, and the defaults: no kind is a text, no time is now.
	dm := f.item("78")
	dm.ChatID, dm.Kind, dm.ReceivedAt, dm.FromSelf, dm.Trusted = f.tgDM, "", time.Time{}, true, false
	f.enqueue(t, dm)
	got := f.unacked(t, f.atlas)[1]
	require.Equal(t, []any{"dm", "Pat", dto.MessageKindText, true, false}, []any{string(got.ChatKind), got.ChatTitle, got.Kind, got.FromSelf, got.Trusted})
	require.True(t, got.ReceivedAt.Equal(t0), "an item without a time is received now")
	wa := f.item("1")
	wa.ConnectorID, wa.ChatID = f.wa, f.waGroup
	f.enqueue(t, wa)
	require.Equal(t, "whatsapp", f.unacked(t, f.atlas)[2].Platform)
}

// CID-26: the same platform message twice is one item, whoever asks, and the second call says so.
func TestTheSamePlatformMessageIsQueuedOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ch, cancel := f.bus.Subscribe(16)
	defer cancel()

	id, queued, err := f.q.Enqueue(ctx, f.item("77"))
	require.NoError(t, err)
	require.True(t, queued)
	again, queued, err := f.q.Enqueue(ctx, f.item("77"))
	require.NoError(t, err)
	require.False(t, queued, "a platform retry is not queued again")
	require.Empty(t, again)
	require.Len(t, f.unacked(t, f.atlas), 1)
	require.Equal(t, id, f.unacked(t, f.atlas)[0].ID)
	require.Len(t, ch, 1, "one item, one event")

	// Another AI cannot queue it either: a message of a connection is queued once.
	other := f.item("77")
	other.AgentID = f.notes
	_, queued, err = f.q.Enqueue(ctx, other)
	require.NoError(t, err)
	require.False(t, queued)
	require.Empty(t, f.unacked(t, f.notes))

	// The same platform id in another chat, or on another connection, is another message; so are
	// messages without a platform id.
	for _, tc := range []struct{ name, connector, chat string }{{"another chat", f.tg, f.tgDM}, {"another connection", f.wa, f.waGroup}} {
		it := f.item("77")
		it.ConnectorID, it.ChatID = tc.connector, tc.chat
		_, queued, err := f.q.Enqueue(ctx, it)
		require.NoError(t, err, tc.name)
		require.True(t, queued, tc.name)
	}
	for range 2 {
		f.enqueue(t, f.item(""))
	}
	require.Len(t, f.unacked(t, f.atlas), 5)
}

// An item names an AI, a connection and a chat that exist; nothing is queued or announced
// otherwise.
func TestEnqueueRefusesUnknownRows(t *testing.T) {
	f := newFixture(t)
	ch, cancel := f.bus.Subscribe(16)
	defer cancel()
	for _, tc := range []struct {
		name   string
		change func(*platform.Item)
	}{
		{"an AI that does not exist", func(it *platform.Item) { it.AgentID = "missing" }},
		{"a connection that does not exist", func(it *platform.Item) { it.ConnectorID = "missing" }},
		{"a chat that does not exist", func(it *platform.Item) { it.ChatID = "missing" }},
	} {
		it := f.item("77")
		tc.change(&it)
		_, queued, err := f.q.Enqueue(context.Background(), it)
		require.Error(t, err, tc.name)
		require.False(t, queued, tc.name)
	}
	require.Empty(t, f.unacked(t, f.atlas))
	require.Empty(t, ch, "no event for an item that was not queued")
}

// CID-08: Unacked returns an AI's own items oldest first, after a seq and up to a limit.
func TestUnackedIsOldestFirst(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	var ids []string
	for _, m := range []string{"first", "second", "third"} {
		ids = append(ids, f.enqueue(t, f.item(m)))
	}
	other := f.item("other")
	other.AgentID = f.notes
	f.enqueue(t, other)
	// Queued on another connection in between: still ordered by arrival, not by connection.
	wa := f.item("fourth")
	wa.ConnectorID, wa.ChatID = f.wa, f.waGroup
	f.enqueue(t, wa)

	list := f.unacked(t, f.atlas)
	require.Equal(t, []string{"hello first", "hello second", "hello third", "hello fourth"}, texts(list))
	for i := 1; i < len(list); i++ {
		require.Less(t, list[i-1].Seq, list[i].Seq)
	}
	require.Equal(t, ids, []string{list[0].ID, list[1].ID, list[2].ID})
	require.Equal(t, []string{"hello other"}, texts(f.unacked(t, f.notes)), "each AI has its own queue")

	after, err := f.q.Unacked(ctx, f.atlas, list[1].Seq, 100)
	require.NoError(t, err)
	require.Equal(t, []string{"hello third", "hello fourth"}, texts(after), "only items after the seq")
	limited, err := f.q.Unacked(ctx, f.atlas, 0, 2)
	require.NoError(t, err)
	require.Equal(t, []string{"hello first", "hello second"}, texts(limited), "the limit keeps the oldest")
	none, err := f.q.Unacked(ctx, f.atlas, list[3].Seq, 100)
	require.NoError(t, err)
	require.Empty(t, none)
}

// CID-09: an ack clears the text at once and keeps the rest of the item (it is still the target
// of a reply); the item stops being delivered. Files stay.
func TestAckClearsTheTextAndKeepsTheMetadata(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	it := f.item("77")
	it.Trusted = true
	it.Attachments = []platform.Attachment{{Name: "a.png", Mime: "image/png", Size: 1, SHA256: "aa", Status: dto.AttachmentStatusStored}}
	id := f.enqueue(t, it)
	keep := f.enqueue(t, f.item("78"))
	f.clock.Advance(time.Minute)

	require.NoError(t, f.q.Ack(ctx, f.atlas, id))

	require.Equal(t, []string{"hello 78"}, texts(f.unacked(t, f.atlas)), "an acked item is delivered no more")
	got, err := f.q.Get(ctx, f.atlas, id)
	require.NoError(t, err)
	require.Empty(t, got.Text, "the text is gone")
	require.Equal(t, []string{"77", "Pat", "5511999990000", f.tgGroup, f.tg}, []string{got.ExternalID, got.SenderName, got.SenderID, got.ChatID, got.ConnectionID},
		"the metadata a reply needs stays")
	require.True(t, got.Trusted)
	require.True(t, got.AckedAt.Equal(t0.Add(time.Minute)), "acked at the time of the ack: %v", got.AckedAt)
	require.Len(t, got.Attachments, 1, "files stay downloadable")
	untouched, err := f.q.Get(ctx, f.atlas, keep)
	require.NoError(t, err)
	require.Equal(t, "hello 78", untouched.Text, "another item keeps its text")
	require.True(t, untouched.AckedAt.IsZero())
}

// CID-09: acking twice, or acking something that does not exist, is not an error and changes
// nothing: the first ack time stays.
func TestAckIsIdempotent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.enqueue(t, f.item("77"))
	f.clock.Advance(time.Minute)
	require.NoError(t, f.q.Ack(ctx, f.atlas, id))
	f.clock.Advance(time.Hour)
	require.NoError(t, f.q.Ack(ctx, f.atlas, id))
	require.NoError(t, f.q.Ack(ctx, f.atlas, "no-such-item"))

	got, err := f.q.Get(ctx, f.atlas, id)
	require.NoError(t, err)
	require.True(t, got.AckedAt.Equal(t0.Add(time.Minute)), "the second ack keeps the first time: %v", got.AckedAt)
}

// An ack by an AI that does not own the item is a no-op: the item keeps its text and stays
// queued for its AI.
func TestAckByAnotherAgentIsANoOp(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.enqueue(t, f.item("77"))

	require.NoError(t, f.q.Ack(ctx, f.notes, id))

	list := f.unacked(t, f.atlas)
	require.Len(t, list, 1)
	require.Equal(t, "hello 77", list[0].Text)
	require.True(t, list[0].AckedAt.IsZero())
}

// Get finds an item of the AI, acked or not; another AI's item and an unknown id are not found.
func TestGetIsScopedToTheAI(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.enqueue(t, f.item("77"))

	got, err := f.q.Get(ctx, f.atlas, id)
	require.NoError(t, err)
	require.Equal(t, "hello 77", got.Text)
	_, err = f.q.Get(ctx, f.notes, id)
	require.ErrorIs(t, err, platform.ErrNotFound, "another AI's item")
	_, err = f.q.Get(ctx, f.atlas, "no-such-item")
	require.ErrorIs(t, err, platform.ErrNotFound)
}

// CID-11: the 5,001st unacknowledged item of an AI drops the oldest one and counts it; with
// exactly 5,000 nothing is dropped.
func TestTheFiveThousandAndFirstItemDropsTheOldest(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	require.Equal(t, 5000, platform.MaxQueued, "the spec's bound")
	f.seed(t, f.atlas, platform.MaxQueued-1, "old")

	f.enqueue(t, f.item("fifty"))
	require.Len(t, f.unacked(t, f.atlas), platform.MaxQueued, "5,000 items fit")
	require.Zero(t, f.takeDropped(t, f.atlas), "nothing dropped at the bound")

	f.enqueue(t, f.item("last"))

	list := f.unacked(t, f.atlas)
	require.Len(t, list, platform.MaxQueued, "still 5,000")
	require.Equal(t, "old-1", list[0].Text, "the oldest item (old-0) was dropped")
	require.Equal(t, "hello last", list[len(list)-1].Text, "the new item is kept")
	require.Equal(t, int64(1), f.takeDropped(t, f.atlas), "the counter reads 1")
	require.Zero(t, f.takeDropped(t, f.atlas))

	// Each overflow adds one.
	f.enqueue(t, f.item("again"))
	f.enqueue(t, f.item("and again"))
	require.Equal(t, int64(2), f.takeDropped(t, f.atlas))
	require.Len(t, f.unacked(t, f.atlas), platform.MaxQueued)
	got, err := f.q.Unacked(ctx, f.atlas, 0, 1)
	require.NoError(t, err)
	require.Equal(t, "old-3", got[0].Text)
}

// CID-11: the bound counts unacknowledged items of the AI only: acked items, and the items of
// another AI, do not use it up.
func TestTheBoundCountsOnlyTheAIsUnackedItems(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.seed(t, f.atlas, platform.MaxQueued, "old")
	f.seed(t, f.notes, 10, "elsewhere")
	list := f.unacked(t, f.atlas)
	for _, e := range list[:10] {
		require.NoError(t, f.q.Ack(ctx, f.atlas, e.ID))
	}

	for i := range 10 {
		f.enqueue(t, f.item(fmt.Sprintf("n%d", i)))
	}
	require.Len(t, f.unacked(t, f.atlas), platform.MaxQueued, "4,990 unacked plus 10 new ones are 5,000")
	require.Zero(t, f.takeDropped(t, f.atlas), "10 acked items did not count")
	got, err := f.q.Get(ctx, f.atlas, list[0].ID)
	require.NoError(t, err)
	require.False(t, got.AckedAt.IsZero(), "an acked item is not dropped by the bound")
	require.Len(t, f.unacked(t, f.notes), 10, "another AI's queue is untouched")

	f.enqueue(t, f.item("over"))
	require.Equal(t, int64(1), f.takeDropped(t, f.atlas))
	require.Zero(t, f.takeDropped(t, f.notes))
}

// CID-10: TakeDropped reads the counter and zeroes it, for that AI only; an AI that does not
// exist has nothing dropped.
func TestTakeDroppedReadsAndResetsTheCounter(t *testing.T) {
	f := newFixture(t)
	f.seed(t, f.atlas, platform.MaxQueued, "atlas")
	f.seed(t, f.notes, platform.MaxQueued, "notes") // a platform id queues once per connection: no shared ids
	for i := range 3 {
		f.enqueue(t, f.item(fmt.Sprintf("x%d", i)))
	}
	other := f.item("n")
	other.AgentID = f.notes
	f.enqueue(t, other)

	require.Equal(t, int64(3), f.takeDropped(t, f.atlas))
	require.Zero(t, f.takeDropped(t, f.atlas), "reading resets")
	require.Equal(t, int64(1), f.takeDropped(t, f.notes), "each AI counts its own")
	require.Zero(t, f.takeDropped(t, "no-such-agent"))
}

// The event the gateway listens to: one PlatformQueued per queued item, after the item exists,
// carrying the AI, the item and its seq; nothing for a duplicate, an ack or a drop.
func TestEnqueuePublishesPlatformQueued(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ch, cancel := f.bus.Subscribe(16)
	defer cancel()

	id := f.enqueue(t, f.item("77"))

	require.Len(t, ch, 1)
	e, ok := (<-ch).(events.PlatformQueued)
	require.True(t, ok, "a PlatformQueued event")
	require.Equal(t, f.atlas, e.AgentID)
	require.Equal(t, id, e.ItemID)
	list := f.unacked(t, f.atlas)
	require.Equal(t, list[0].Seq, e.Seq, "the seq lets the gateway skip what it already sent")

	_, _, err := f.q.Enqueue(ctx, f.item("77"))
	require.NoError(t, err)
	require.NoError(t, f.q.Ack(ctx, f.atlas, id))
	require.Empty(t, ch, "no event for a duplicate or an ack")
}

// moveTo moves the connection's items to the AI inside a transaction, as Service.Update will.
func (f *fixture) moveTo(t *testing.T, connector, agent string) int64 {
	t.Helper()
	ctx := context.Background()
	var moved int64
	require.NoError(t, f.st.WithTx(ctx, func(q *db.Queries) (err error) {
		moved, err = f.q.MoveConnector(ctx, q, connector, agent)
		return err
	}))
	return moved
}

// CID-05: MoveConnector hands over every item of one connection, acknowledged or not, and none
// of another connection; the new AI sees the unacknowledged ones oldest first and can still
// reply to the acknowledged ones, which the old AI can no longer.
func TestMoveConnectorMovesAckedAndUnackedItemsOfOneConnection(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acked := f.enqueue(t, f.item("1"))
	f.enqueue(t, f.item("2"))
	elsewhere := f.item("w1") // another connection of the same AI
	elsewhere.ConnectorID, elsewhere.ChatID = f.wa, f.waGroup
	elsewhereID := f.enqueue(t, elsewhere)
	f.enqueue(t, f.item("3"))
	mine := f.item("n1") // an item of the connection that the new AI holds already
	mine.AgentID, mine.ChatID = f.notes, f.tgDM
	f.enqueue(t, mine)
	require.NoError(t, f.q.Ack(ctx, f.atlas, acked))
	ch, cancel := f.bus.Subscribe(16)
	defer cancel()

	moved := f.moveTo(t, f.tg, f.notes)

	require.Equal(t, int64(3), moved, "Atlas's three items of the connection, the acked one included")
	require.Equal(t, []string{"hello 2", "hello 3", "hello n1"}, texts(f.unacked(t, f.notes)), "oldest first, with the item Notes held")
	require.Equal(t, []string{"hello w1"}, texts(f.unacked(t, f.atlas)), "the other connection stays with Atlas")
	got, err := f.q.Get(ctx, f.notes, acked)
	require.NoError(t, err)
	require.False(t, got.AckedAt.IsZero(), "the acknowledged item moved with its state")
	_, err = f.q.Get(ctx, f.atlas, acked)
	require.ErrorIs(t, err, platform.ErrNotFound, "the old operator lost it")
	_, err = f.q.Get(ctx, f.atlas, elsewhereID)
	require.NoError(t, err)
	require.Zero(t, f.takeDropped(t, f.notes))
	require.Empty(t, ch, "the caller announces the move after it commits")

	// The new operator acks what it was handed; the old one's ack is a no-op.
	list := f.unacked(t, f.notes)
	require.NoError(t, f.q.Ack(ctx, f.atlas, list[0].ID))
	require.Len(t, f.unacked(t, f.notes), 3, "Atlas can no longer ack it")
	require.NoError(t, f.q.Ack(ctx, f.notes, list[0].ID))
	require.Len(t, f.unacked(t, f.notes), 2)
}

// Moving to the AI that holds the items, or moving a connection without items, changes
// nothing; a failed transaction leaves every item with its AI.
func TestMoveConnectorNoOpsAndRollback(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.enqueue(t, f.item("1"))
	f.enqueue(t, f.item("2"))

	require.Zero(t, f.moveTo(t, f.tg, f.atlas), "the items are Atlas's already")
	require.Zero(t, f.moveTo(t, f.wa, f.notes), "no item on this connection")
	require.Len(t, f.unacked(t, f.atlas), 2)

	boom := fmt.Errorf("boom")
	err := f.st.WithTx(ctx, func(q *db.Queries) error {
		moved, err := f.q.MoveConnector(ctx, q, f.tg, f.notes)
		require.NoError(t, err)
		require.Equal(t, int64(2), moved)
		return boom
	})
	require.ErrorIs(t, err, boom)
	require.Len(t, f.unacked(t, f.atlas), 2, "rolled back with the caller's transaction")
	require.Empty(t, f.unacked(t, f.notes))
}

// CID-11: the bound holds for the AI that receives a connection's items too: moving them in
// drops the oldest unacknowledged items over 5,000 and counts them for the new operator.
func TestMoveConnectorKeepsTheNewOperatorWithinTheBound(t *testing.T) {
	f := newFixture(t)
	f.seed(t, f.notes, platform.MaxQueued-3, "held")
	for _, m := range []string{"1", "2", "3"} {
		f.enqueue(t, f.item(m))
	}

	require.Equal(t, int64(3), f.moveTo(t, f.tg, f.notes))
	require.Len(t, f.unacked(t, f.notes), platform.MaxQueued, "exactly 5,000 fit")
	require.Zero(t, f.takeDropped(t, f.notes))

	f.enqueue(t, f.item("4")) // Atlas's, on the same connection
	require.Equal(t, int64(1), f.moveTo(t, f.tg, f.notes))
	list := f.unacked(t, f.notes)
	require.Len(t, list, platform.MaxQueued)
	require.Equal(t, "held-1", list[0].Text, "the oldest item was dropped")
	require.Equal(t, int64(1), f.takeDropped(t, f.notes), "and counted for the new operator")
	require.Zero(t, f.takeDropped(t, f.atlas))
}
