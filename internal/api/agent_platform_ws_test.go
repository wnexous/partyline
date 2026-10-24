package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// platformWS is alice with her AIs Atlas and Notes and a connection with a group and a direct
// chat. The connection does not run: the gateway reads the platform queue, not the connection.
type platformWS struct {
	env   *testkit.Env
	alice *testkit.Client
	token *testkit.Client // alice's agent token: it acts as the AI of the folder it names
	atlas string
	notes string
	conn  dto.Connector
	group db.Chat
	dm    db.Chat
}

func newPlatformWS(t *testing.T) *platformWS {
	t.Helper()
	env, _ := fakeConnectorEnv(t)
	alice := env.NewUser("alice")
	f := &platformWS{
		env: env, alice: alice, token: alice.Token(dto.TokenKindAgent, "laptop"),
		atlas: env.NewAI(alice, "Atlas"), notes: env.NewAI(alice, "Notes"),
		conn: createFakeIdle(alice, "wa", false),
	}
	f.group = f.chat(t, f.conn, "120363000000000001@g.us", "Team", "group")
	f.dm = f.chat(t, f.conn, "5511999990000", "Pat", "dm")
	return f
}

func (f *platformWS) chat(t *testing.T, conn dto.Connector, external, title, kind string) db.Chat {
	t.Helper()
	c, err := connectors.UpsertChat(context.Background(), f.env.App.Store, f.env.App.Bus, conn.ID,
		connectors.ChatInfo{ID: external, Title: title, Kind: kind}, time.Now(), time.Now())
	require.NoError(t, err)
	return c
}

// enqueue queues a message of the chat for the AI and returns the item's id.
func (f *platformWS) enqueue(t *testing.T, agent string, chat db.Chat, external, text string, opts ...func(*platform.Item)) string {
	t.Helper()
	it := platform.Item{
		AgentID: agent, ConnectorID: chat.ConnectorID, ChatID: chat.ID, ExternalID: external,
		SenderID: "5511999990000", SenderName: "Pat", Text: text,
	}
	for _, o := range opts {
		o(&it)
	}
	id, queued, err := f.env.App.Platform.Enqueue(context.Background(), it)
	require.NoError(t, err)
	require.True(t, queued)
	return id
}

// dial opens the agent WebSocket of the AI with a client that understands platform events and
// reads the welcome frame.
func (f *platformWS) dial(t *testing.T, name string) *testkit.AgentConn {
	t.Helper()
	conn, resp := f.token.DialPlatform(name)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	conn.Welcome(wsWait)
	return conn
}

// leave closes the session and waits until the AI is offline, so that another session can connect.
func (f *platformWS) leave(t *testing.T, conn *testkit.AgentConn, agent string) {
	t.Helper()
	conn.Close()
	require.Eventually(t, func() bool { return !f.env.App.Presence.Online(agent) }, wsWait, 10*time.Millisecond)
}

func (f *platformWS) item(t *testing.T, agent, id string) platform.Event {
	t.Helper()
	e, err := f.env.App.Platform.Get(context.Background(), agent, id)
	require.NoError(t, err)
	return e
}

// acked waits until the server recorded the acknowledgement of the item.
func (f *platformWS) acked(t *testing.T, agent, id string) platform.Event {
	t.Helper()
	var e platform.Event
	require.Eventually(t, func() bool {
		var err error
		e, err = f.env.App.Platform.Get(context.Background(), agent, id)
		return err == nil && !e.AckedAt.IsZero()
	}, wsWait, 10*time.Millisecond, "the acknowledgement was not recorded")
	return e
}

// ackRoomMessage acknowledges a room message and waits until the server moved the AI's cursor
// there, so that the next connection does not deliver it again.
func (f *platformWS) ackRoomMessage(t *testing.T, conn *testkit.AgentConn, room dto.Room, m dto.Message) {
	t.Helper()
	conn.Ack(m.ID)
	require.Eventually(t, func() bool {
		seq, err := f.env.App.Store.R().GetAgentCursor(context.Background(), db.GetAgentCursorParams{AgentID: f.atlas, RoomID: room.ID})
		return err == nil && seq == m.Seq
	}, wsWait, 10*time.Millisecond)
}

func (f *platformWS) dropped(t *testing.T, agent string) int64 {
	t.Helper()
	n, err := f.env.App.Store.R().GetPlatformDropped(context.Background(), agent)
	require.NoError(t, err)
	return n
}

// expire drops n queued messages of Atlas for being older than 7 days.
func (f *platformWS) expire(t *testing.T, n int, prefix string) {
	t.Helper()
	old := time.Now().Add(-8 * 24 * time.Hour)
	for i := range n {
		f.enqueue(t, f.atlas, f.group, fmt.Sprintf("%s-%d", prefix, i), "stale", func(it *platform.Item) { it.ReceivedAt = old })
	}
	res, err := f.env.App.Platform.Purge(context.Background(), time.Now())
	require.NoError(t, err)
	require.Equal(t, int64(n), res.Expired)
}

// platformIDs reads n platform frames and returns the ids of their events.
func platformIDs(t *testing.T, conn *testkit.AgentConn, n int) []string {
	t.Helper()
	ids := make([]string, n)
	for i := range ids {
		ids[i] = conn.Platform(wsWait).Event.ID
	}
	return ids
}

// CID-06, CID-08: an AI that was offline gets what arrived on its connection when its session
// connects, oldest first, as platform frames that carry platform, connection, chat, sender, trust
// and attachments of a group message and of a direct one, and nothing that is queued for another
// AI.
func TestPlatformBacklogDeliversQueuedMessagesOldestFirst(t *testing.T) {
	f := newPlatformWS(t)
	sent := time.Date(2026, 10, 22, 12, 30, 0, 0, time.UTC)
	group := f.enqueue(t, f.atlas, f.group, "m1", "hello Atlas", func(it *platform.Item) {
		it.Trusted, it.Kind, it.ReceivedAt = true, dto.MessageKindImage, sent
		it.Attachments = []platform.Attachment{{Name: "photo.png", Mime: "image/png", Size: 3, Status: dto.AttachmentStatusStored}}
	})
	f.enqueue(t, f.notes, f.group, "n1", "for Notes")
	direct := f.enqueue(t, f.atlas, f.dm, "m2", "typed on the phone", func(it *platform.Item) {
		it.SenderID, it.SenderName, it.FromSelf, it.ReceivedAt = "5511999990001", "Atlas phone", true, sent.Add(time.Minute)
	})
	third := f.enqueue(t, f.atlas, f.group, "m3", "last one")

	conn := f.dial(t, "Atlas")

	// The wire shape, read without the DTO: the type of the frame and the keys of its event.
	raw := conn.Next(wsWait)
	require.Equal(t, dto.AgentFrameType("platform"), raw.Type)
	var wire struct {
		Event map[string]any `json:"event"`
	}
	require.NoError(t, json.Unmarshal(raw.Raw, &wire))
	keys := make([]string, 0, len(wire.Event))
	for k := range wire.Event {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	require.Equal(t, []string{"attachments", "chat_id", "chat_kind", "chat_title", "connection_id", "from_self", "id", "kind",
		"platform", "received_at", "sender_id", "sender_name", "text", "trusted"}, keys)
	var first dto.AgentPlatformFrame
	require.NoError(t, json.Unmarshal(raw.Raw, &first))

	require.Len(t, first.Event.Attachments, 1)
	attachment := first.Event.Attachments[0]
	require.NotEmpty(t, attachment.ID, "the attachment has an id to download it with")
	require.True(t, sent.Equal(first.Event.ReceivedAt), "received at %s", first.Event.ReceivedAt)
	first.Event.ReceivedAt = time.Time{}
	require.Equal(t, dto.PlatformEvent{
		ID: group, Platform: connectortest.KindName, ConnectionID: f.conn.ID, ChatID: f.group.ID, ChatTitle: "Team", ChatKind: dto.ChatKindGroup,
		SenderName: "Pat", SenderID: "5511999990000", Trusted: true, FromSelf: false, Kind: dto.MessageKindImage, Text: "hello Atlas",
		Attachments: []dto.Attachment{{ID: attachment.ID, Name: "photo.png", Mime: "image/png", Size: 3, Status: dto.AttachmentStatusStored}},
	}, first.Event)

	second := conn.Platform(wsWait)
	require.True(t, sent.Add(time.Minute).Equal(second.Event.ReceivedAt))
	second.Event.ReceivedAt = time.Time{}
	require.Equal(t, dto.PlatformEvent{
		ID: direct, Platform: connectortest.KindName, ConnectionID: f.conn.ID, ChatID: f.dm.ID, ChatTitle: "Pat", ChatKind: dto.ChatKindDM,
		SenderName: "Atlas phone", SenderID: "5511999990001", Trusted: false, FromSelf: true, Kind: dto.MessageKindText, Text: "typed on the phone",
		Attachments: []dto.Attachment{},
	}, second.Event)

	require.Equal(t, third, conn.Platform(wsWait).Event.ID)
	conn.ExpectNone(300 * time.Millisecond)
}

// CID-09: the session's acknowledgement of a platform event deletes the item's text at once and
// keeps what the AI still needs to answer; items it did not acknowledge keep their text and come
// again with the next connection.
func TestPlatformAckDeletesTheText(t *testing.T) {
	f := newPlatformWS(t)
	one := f.enqueue(t, f.atlas, f.group, "m1", "first text")
	two := f.enqueue(t, f.atlas, f.group, "m2", "second text")
	conn := f.dial(t, "Atlas")
	require.Equal(t, []string{one, two}, platformIDs(t, conn, 2))

	conn.AckPlatform(one)
	got := f.acked(t, f.atlas, one)
	require.Empty(t, got.Text, "the text goes with the acknowledgement")
	require.Equal(t, "Pat", got.SenderName, "the rest of the item stays")
	require.Equal(t, "m1", got.ExternalID)
	untouched := f.item(t, f.atlas, two)
	require.Equal(t, "second text", untouched.Text)
	require.True(t, untouched.AckedAt.IsZero())

	f.leave(t, conn, f.atlas)
	again := f.dial(t, "Atlas")
	e := again.Platform(wsWait).Event
	require.Equal(t, []string{two, "second text"}, []string{e.ID, e.Text}, "only the unacknowledged item comes back")
	again.ExpectNone(300 * time.Millisecond)
}

// CID-10: an AI whose messages were dropped (older than 7 days) is told how many on its next
// connection: the notice belongs to no room, comes after the room backlog and before the queued
// messages, and is not sent again.
func TestPlatformDroppedNoticeCarriesTheCountAndIsSentOnce(t *testing.T) {
	f := newPlatformWS(t)
	room := createRoom(f.alice, "Support")
	f.alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: f.atlas}).RequireStatus(http.StatusCreated)
	post := testkit.Decode[dto.Message](f.alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "room backlog"}).RequireStatus(http.StatusCreated))
	f.expire(t, 2, "old")
	waiting := f.enqueue(t, f.atlas, f.group, "new1", "still waiting")

	conn := f.dial(t, "Atlas")
	require.Equal(t, post.ID, conn.Message(wsWait).Message.ID, "the room backlog comes first")
	f.ackRoomMessage(t, conn, room, post)
	n := conn.Notice(wsWait)
	require.Equal(t, "2 platform messages were dropped (older than 7 days or over 5,000 queued)", n.Text)
	require.Equal(t, dto.RoomRef{}, n.Room, "the notice belongs to no room")
	require.Equal(t, waiting, conn.Platform(wsWait).Event.ID, "the queued messages follow the notice")
	conn.ExpectNone(300 * time.Millisecond)
	f.leave(t, conn, f.atlas)

	again := f.dial(t, "Atlas")
	require.Equal(t, waiting, again.Platform(wsWait).Event.ID, "the unacknowledged message comes again, with no second notice before it")
	again.ExpectNone(300 * time.Millisecond)
}

// CID-10: the count starts again after a notice: a connection is told only about what was dropped
// since the last one, and nothing when nothing was.
func TestPlatformDroppedNoticeCountsAnewAfterTheNotice(t *testing.T) {
	f := newPlatformWS(t)

	f.expire(t, 2, "first")
	conn := f.dial(t, "Atlas")
	require.Equal(t, "2 platform messages were dropped (older than 7 days or over 5,000 queued)", conn.Notice(wsWait).Text)
	conn.ExpectNone(300 * time.Millisecond)
	f.leave(t, conn, f.atlas)

	quiet := f.dial(t, "Atlas")
	quiet.ExpectNone(300 * time.Millisecond)
	f.leave(t, quiet, f.atlas)

	f.expire(t, 3, "second")
	last := f.dial(t, "Atlas")
	require.Equal(t, "3 platform messages were dropped (older than 7 days or over 5,000 queued)", last.Notice(wsWait).Text)
}

// CID-10, design refinement 4: a session that did not opt in to platform events cannot show the
// notice, so it does not consume the count; the next session that opted in is told.
func TestPlatformDroppedCountWaitsForASessionThatUnderstandsIt(t *testing.T) {
	f := newPlatformWS(t)
	f.expire(t, 1, "old")

	old, resp := f.token.DialAgent("Atlas")
	require.NotNil(t, old, "upgrade refused: %d %s", resp.Status, resp.Body)
	old.Welcome(wsWait)
	old.ExpectNone(300 * time.Millisecond)
	require.Equal(t, int64(1), f.dropped(t, f.atlas), "the count is still there")
	f.leave(t, old, f.atlas)

	conn := f.dial(t, "Atlas")
	require.Equal(t, "1 platform messages were dropped (older than 7 days or over 5,000 queued)", conn.Notice(wsWait).Text)
	require.Zero(t, f.dropped(t, f.atlas))
}

// CID-08: at most 50 platform frames are in flight (sent and not acknowledged) on a connection;
// each acknowledgement releases the next one, oldest first, whether it was queued before the
// connection or arrives while it is open.
func TestPlatformWindowKeepsAtMostFiftyFramesInFlight(t *testing.T) {
	f := newPlatformWS(t)
	ids := make([]string, 60)
	for i := range ids {
		ids[i] = f.enqueue(t, f.atlas, f.group, fmt.Sprintf("m%02d", i), fmt.Sprintf("message %d", i))
	}
	conn := f.dial(t, "Atlas")

	require.Equal(t, ids[:50], platformIDs(t, conn, 50))
	conn.ExpectNone(300 * time.Millisecond) // the 51st waits for an acknowledgement

	conn.AckPlatform(ids[0])
	require.Equal(t, ids[50], conn.Platform(wsWait).Event.ID, "one acknowledgement releases exactly the next item")
	conn.ExpectNone(300 * time.Millisecond)

	for _, id := range ids[1:10] {
		conn.AckPlatform(id)
	}
	require.Equal(t, ids[51:60], platformIDs(t, conn, 9))
	conn.ExpectNone(300 * time.Millisecond)

	// Live items obey the same window: items 10 to 59 are in flight, which is 50.
	live := f.enqueue(t, f.atlas, f.group, "live", "arrives while the window is full")
	conn.ExpectNone(300 * time.Millisecond)
	conn.AckPlatform(ids[10])
	require.Equal(t, live, conn.Platform(wsWait).Event.ID)
}

// CID-08: a slot is freed when the session acknowledges a frame, whatever became of the item
// meanwhile: the session acknowledges every frame it received, and an item the server deleted (here
// by expiry) must not hold its slot for as long as the connection lives.
func TestPlatformAckOfADeletedItemStillReleasesItsSlot(t *testing.T) {
	f := newPlatformWS(t)
	ids := []string{f.enqueue(t, f.atlas, f.group, "m00", "will expire", func(it *platform.Item) {
		it.ReceivedAt = time.Now().Add(-7*24*time.Hour - time.Minute) // the oldest, so in flight first
	})}
	for i := 1; i <= 50; i++ {
		ids = append(ids, f.enqueue(t, f.atlas, f.group, fmt.Sprintf("m%02d", i), fmt.Sprintf("message %d", i)))
	}
	conn := f.dial(t, "Atlas")
	require.Equal(t, ids[:50], platformIDs(t, conn, 50))
	res, err := f.env.App.Platform.Purge(context.Background(), time.Now())
	require.NoError(t, err)
	require.Equal(t, int64(1), res.Expired)

	conn.AckPlatform(ids[0])
	require.Equal(t, ids[50], conn.Platform(wsWait).Event.ID)
}

// CID-25: the unacknowledged items of a session that was taken over go to the session that took
// the AI over; the acknowledged ones do not.
func TestTakeoverGivesTheNewSessionTheUnacknowledgedPlatformMessages(t *testing.T) {
	f := newPlatformWS(t)
	pending := f.enqueue(t, f.atlas, f.group, "m1", "not acknowledged")
	done := f.enqueue(t, f.atlas, f.group, "m2", "acknowledged")
	holder := f.dial(t, "Atlas")
	require.Equal(t, []string{pending, done}, platformIDs(t, holder, 2))
	holder.AckPlatform(done)
	f.acked(t, f.atlas, done)

	folder := testkit.FolderOf("Atlas").WithPlatform()
	folder.Takeover = true
	taker, resp := f.token.DialFolder(folder)
	require.NotNil(t, taker, "upgrade refused: %d %s", resp.Status, resp.Body)
	taker.Welcome(wsWait)
	require.Equal(t, agents.CloseTakenOver, websocket.CloseStatus(holder.Closed(wsWait)))

	e := taker.Platform(wsWait).Event
	require.Equal(t, []string{pending, "not acknowledged"}, []string{e.ID, e.Text})
	taker.ExpectNone(300 * time.Millisecond)

	next := f.enqueue(t, f.atlas, f.group, "m3", "after the takeover")
	require.Equal(t, next, taker.Platform(wsWait).Event.ID, "the new session receives what arrives from now on")
}

// CID-06: a session that did not opt in with the platform feature gets room messages only: no
// platform frame at connect or live, whether it sends no feature header or one that names other
// features (also one that merely contains the word); its items stay queued untouched, and the
// session that opts in later receives them.
func TestSessionWithoutThePlatformFeatureGetsNoPlatformFrames(t *testing.T) {
	f := newPlatformWS(t)
	first := f.enqueue(t, f.atlas, f.group, "m1", "queued before")

	plain, resp := f.token.DialAgent("Atlas")
	require.NotNil(t, plain, "upgrade refused: %d %s", resp.Status, resp.Body)
	plain.Welcome(wsWait)
	second := f.enqueue(t, f.atlas, f.group, "m2", "arrives live")
	plain.ExpectNone(300 * time.Millisecond)
	f.leave(t, plain, f.atlas)

	for _, features := range [][]string{{"experimental", "other"}, {"platforms"}, {"non-platform"}} {
		folder := testkit.FolderOf("Atlas")
		folder.Features = features
		named, resp := f.token.DialFolder(folder)
		require.NotNil(t, named, "upgrade refused: %d %s", resp.Status, resp.Body)
		named.Welcome(wsWait)
		named.ExpectNone(300 * time.Millisecond)
		f.leave(t, named, f.atlas)
	}

	for id, text := range map[string]string{first: "queued before", second: "arrives live"} {
		e := f.item(t, f.atlas, id)
		require.Equal(t, text, e.Text, "a session that cannot show an item neither loses nor acknowledges it")
		require.True(t, e.AckedAt.IsZero())
	}

	conn := f.dial(t, "Atlas")
	require.Equal(t, []string{first, second}, platformIDs(t, conn, 2))
}

// The feature header is a comma separated list: platform among other features, with optional
// spaces, opts in.
func TestPlatformFeatureIsOneItemOfAList(t *testing.T) {
	f := newPlatformWS(t)
	id := f.enqueue(t, f.atlas, f.group, "m1", "queued")
	folder := testkit.FolderOf("Atlas")
	folder.Features = []string{"experimental", " platform "}
	conn, resp := f.token.DialFolder(folder)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	conn.Welcome(wsWait)
	require.Equal(t, id, conn.Platform(wsWait).Event.ID)
}

// CID-06: a platform event reaches the session of the AI that holds it as it is queued, and no
// other: not another AI of the same owner, and not the AI of another user with the same machine
// and folder, whether that user is a colleague who sees Atlas or has nothing to do with it.
func TestPlatformLivePushReachesOnlyTheAIThatHoldsTheItem(t *testing.T) {
	f := newPlatformWS(t)
	bob, carol := f.env.NewUser("bob"), f.env.NewUser("carol")
	shareAI(f.alice, f.atlas, bob) // bob joins Atlas's organization: he sees it
	atlas := f.dial(t, "Atlas")
	notes := f.dial(t, "Notes")
	var others []*testkit.AgentConn
	for _, u := range []*testkit.Client{bob, carol} { // each one's own AI of the same folder
		conn, resp := u.Token(dto.TokenKindAgent, "desk").DialPlatform("Atlas")
		require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
		conn.Welcome(wsWait)
		others = append(others, conn)
	}

	forAtlas := f.enqueue(t, f.atlas, f.group, "m1", "for Atlas")
	require.Equal(t, forAtlas, atlas.Platform(wsWait).Event.ID)
	forNotes := f.enqueue(t, f.notes, f.group, "m2", "for Notes")
	require.Equal(t, forNotes, notes.Platform(wsWait).Event.ID)

	atlas.ExpectNone(300 * time.Millisecond)
	notes.ExpectNone(300 * time.Millisecond)
	for _, conn := range others {
		conn.ExpectNone(300 * time.Millisecond)
	}
}

// CID-09: only the AI that holds an item can acknowledge it: another AI of the same owner that
// names the item's id changes nothing.
func TestPlatformAckOfAnotherAIsItemChangesNothing(t *testing.T) {
	f := newPlatformWS(t)
	id := f.enqueue(t, f.atlas, f.group, "m1", "for Atlas only")
	notes := f.dial(t, "Notes")
	notes.AckPlatform(id)
	own := f.enqueue(t, f.notes, f.group, "n1", "for Notes")
	require.Equal(t, own, notes.Platform(wsWait).Event.ID)
	notes.AckPlatform(own) // acknowledgements are handled in order: once this one is recorded the first was handled
	f.acked(t, f.notes, own)

	e := f.item(t, f.atlas, id)
	require.True(t, e.AckedAt.IsZero(), "not acknowledged")
	require.Equal(t, "for Atlas only", e.Text)
}

// CID-05 (T21 hands a connection's items to its new operator): when items move to the AI and the
// bus says so without an item id, the session receives the moved items even though they are older
// than what it already holds, and does not receive again what it holds in flight.
func TestPlatformMovedItemsReachTheNewOperatorsSession(t *testing.T) {
	f := newPlatformWS(t)
	moved1 := f.enqueue(t, f.notes, f.group, "m1", "Notes held it")
	moved2 := f.enqueue(t, f.notes, f.group, "m2", "Notes held it too")
	held := f.enqueue(t, f.atlas, f.group, "a1", "Atlas already has it")
	conn := f.dial(t, "Atlas")
	require.Equal(t, held, conn.Platform(wsWait).Event.ID)

	ctx := context.Background()
	require.NoError(t, f.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		n, err := f.env.App.Platform.MoveConnector(ctx, q, f.conn.ID, f.atlas)
		require.Equal(t, int64(2), n)
		return err
	}))
	f.env.App.Bus.Publish(events.PlatformQueued{AgentID: f.atlas})

	require.Equal(t, []string{moved1, moved2}, platformIDs(t, conn, 2), "oldest first, without the item in flight")
	conn.ExpectNone(300 * time.Millisecond)
}

// A lagged bus may have dropped the event of a queued item: the session reads its queue again.
func TestPlatformLaggedBusRereadsTheQueue(t *testing.T) {
	f := newPlatformWS(t)
	// The session has read its queue once it has delivered the item that was waiting.
	waiting := f.enqueue(t, f.atlas, f.group, "m0", "waiting at connect")
	conn := f.dial(t, "Atlas")
	require.Equal(t, waiting, conn.Platform(wsWait).Event.ID)
	ctx := context.Background()
	id := store.NewID()
	require.NoError(t, f.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		_, err := q.InsertPlatformItem(ctx, db.InsertPlatformItemParams{
			ID: id, AgentID: f.atlas, ConnectorID: f.conn.ID, ChatID: f.group.ID, ExternalID: "silent", SenderName: "Pat",
			Kind: "text", Text: "queued without an event", ReceivedAt: time.Now().UnixMilli(),
		})
		return err
	}))
	conn.ExpectNone(300 * time.Millisecond) // nothing told the session

	f.env.App.Bus.Publish(events.Lagged{})
	require.Equal(t, id, conn.Platform(wsWait).Event.ID)
}

// The platform frames follow the room backlog, and a connection that is gone mid-delivery loses
// nothing: the items it did not acknowledge come back.
func TestPlatformFramesFollowTheRoomBacklogAndSurviveAReconnect(t *testing.T) {
	f := newPlatformWS(t)
	room := createRoom(f.alice, "Support")
	f.alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: f.atlas}).RequireStatus(http.StatusCreated)
	post := testkit.Decode[dto.Message](f.alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "in the room"}).RequireStatus(http.StatusCreated))
	one := f.enqueue(t, f.atlas, f.group, "m1", "platform one")
	two := f.enqueue(t, f.atlas, f.group, "m2", "platform two")

	conn := f.dial(t, "Atlas")
	require.Equal(t, post.ID, conn.Message(wsWait).Message.ID)
	f.ackRoomMessage(t, conn, room, post)
	require.Equal(t, one, conn.Platform(wsWait).Event.ID)
	conn.AckPlatform(one)
	require.Equal(t, two, conn.Platform(wsWait).Event.ID)
	f.acked(t, f.atlas, one)
	f.leave(t, conn, f.atlas) // gone before acknowledging the second

	again := f.dial(t, "Atlas")
	require.Equal(t, two, again.Platform(wsWait).Event.ID)
	again.ExpectNone(300 * time.Millisecond)
}

// CID-06 (design: delivery to the session): the welcome frame lists the connections the AI operates,
// by connection name, with their platform and account; another AI's connections are not listed, and an AI that operates none
// gets an empty list, not null.
func TestWelcomeListsTheConnectionsTheAIOperates(t *testing.T) {
	f := newPlatformWS(t)
	second := createFakeIdle(f.alice, "tg", false)
	elsewhere := createFakeIdle(f.alice, "notes-line", false)
	f.alice.OperateConnector(f.conn.ID, f.atlas)
	f.alice.OperateConnector(second.ID, f.atlas)
	f.alice.OperateConnector(elsewhere.ID, f.notes)
	ctx := context.Background()
	require.NoError(t, f.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.SetConnectorAccountLabel(ctx, db.SetConnectorAccountLabelParams{AccountLabel: "+55 11 *****-0000", ID: f.conn.ID})
	}))
	idle := f.env.NewAI(f.alice, "Idle")

	welcome := func(name, agent string) (dto.AgentWelcomeFrame, string) {
		t.Helper()
		conn, resp := f.token.DialPlatform(name)
		require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
		raw := conn.Next(wsWait)
		require.Equal(t, dto.AgentFrameTypeWelcome, raw.Type)
		var w dto.AgentWelcomeFrame
		require.NoError(t, json.Unmarshal(raw.Raw, &w))
		f.leave(t, conn, agent)
		return w, string(raw.Raw)
	}

	atlas, _ := welcome("Atlas", f.atlas)
	require.Equal(t, []dto.AgentConnection{
		{ID: second.ID, Platform: connectortest.KindName, AccountLabel: ""},
		{ID: f.conn.ID, Platform: connectortest.KindName, AccountLabel: "+55 11 *****-0000"},
	}, atlas.Connections, "listed by connection name (tg, then wa)")
	notes, _ := welcome("Notes", f.notes)
	require.Equal(t, []dto.AgentConnection{{ID: elsewhere.ID, Platform: connectortest.KindName}}, notes.Connections)
	_, raw := welcome("Idle", idle)
	require.Contains(t, raw, `"connections":[]`)
}
