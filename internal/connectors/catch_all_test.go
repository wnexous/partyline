package connectors_test

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// Synthetic DM peers (WhatsApp-style ids).
const (
	dmA = "5511999990001@s.whatsapp.net"
	dmB = "5511999990002@s.whatsapp.net"
	dmC = "5511999990003@s.whatsapp.net"
)

func dmText(chat, id, body string) connectors.Inbound {
	return connectors.Inbound{
		ChatID: chat, ChatTitle: "Customer", ChatKind: "dm", MessageID: id,
		SenderID: chat, SenderName: "Customer", Text: body, At: time.Now(),
	}
}

func channelText(chat, id, body string) connectors.Inbound {
	m := text(chat, id, body)
	m.ChatKind = "channel"
	return m
}

// catchAll is an inbound env with alice's room "catch-all" holding her connected agent.
type catchAll struct {
	*inboundEnv
	room  dto.Room
	agent *testkit.AgentConn
}

func newCatchAll(t *testing.T, opts ...testkit.Option) *catchAll {
	t.Helper()
	e := newInboundEnv(t, opts...)
	agent, _ := e.alice.Token(dto.TokenKindAgent, "laptop").DialAgent("helper")
	require.NotNil(t, agent)
	agent.Welcome(connectortest.Timeout)
	agents := testkit.Decode[dto.List[dto.Agent]](e.alice.Get("/api/v1/agents").RequireStatus(http.StatusOK)).Items
	require.Len(t, agents, 1)
	room := testkit.Decode[dto.Room](e.alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "catch-all"}).RequireStatus(http.StatusCreated))
	e.alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: agents[0].ID}).RequireStatus(http.StatusCreated)
	return &catchAll{inboundEnv: e, room: room, agent: agent}
}

func (c *catchAll) setScope(t *testing.T, scope dto.AutoRouteScope, roomID string) {
	t.Helper()
	c.alice.Patch("/api/v1/connectors/"+c.conn.ID, dto.UpdateConnectorRequest{AutoRouteScope: &scope, AutoRouteRoomID: &roomID}).RequireStatus(http.StatusOK)
}

// chatMembers returns the room's chat members by chat id.
func (c *catchAll) chatMembers(t *testing.T, roomID string) map[string]dto.RoomMember {
	t.Helper()
	out := map[string]dto.RoomMember{}
	for _, m := range testkit.Decode[dto.List[dto.RoomMember]](c.alice.Get("/api/v1/rooms/" + roomID + "/members").RequireStatus(http.StatusOK)).Items {
		if m.Kind == dto.MemberKindChat {
			require.NotContains(t, out, m.MemberID, "a chat is a member once")
			out[m.MemberID] = m
		}
	}
	return out
}

func (c *inboundEnv) connector(t *testing.T) dto.Connector {
	t.Helper()
	return testkit.Decode[dto.Connector](c.alice.Get("/api/v1/connectors/" + c.conn.ID).RequireStatus(http.StatusOK))
}

// ROUTE-01, ROUTE-02, ROUTE-05 (independent test): with scope dms, a DM from a new chat joins
// the catch-all room as an outbound member and reaches the agent untrusted; a new group does
// not join; a chat already in another room stays out of the catch-all room. A DM from the
// owner's verified identity is trusted.
func TestCatchAllRoutesNewDMs(t *testing.T) {
	c := newCatchAll(t)
	// A DM routed by hand before the catch-all is on.
	c.emit(dmText(dmB, "B0", "before"))
	other := c.routedRoom(t, "other", c.chatID(t, dmB))
	c.setScope(t, dto.AutoRouteScopeDMs, c.room.ID)

	c.emit(dmText(dmA, "A1", "oi, quero um orçamento"))
	got := c.agent.Message(connectortest.Timeout)
	require.Equal(t, c.room.ID, got.Room.ID)
	require.Equal(t, "oi, quero um orçamento", got.Message.Text)
	require.Equal(t, dto.AuthorKindChat, got.Message.AuthorKind)
	require.False(t, got.Trusted, "an unverified sender stays untrusted")
	members := c.chatMembers(t, c.room.ID)
	chatA := c.chatID(t, dmA)
	require.Contains(t, members, chatA)
	require.True(t, members[chatA].Outbound)
	require.Len(t, c.messages(t, c.room.ID), 1)

	before := c.messageCount(t)
	c.emit(text(groupID, "G1", "group hello"))
	c.emit(dmText(dmB, "B1", "routed elsewhere"))
	require.Equal(t, before+1, c.messageCount(t), "only the DM in the other room is stored")
	members = c.chatMembers(t, c.room.ID)
	require.NotContains(t, members, c.chatID(t, groupID))
	require.NotContains(t, members, c.chatID(t, dmB))
	require.Len(t, c.messages(t, other.ID), 1)

	// ROUTE-05: the owner's verified identity is trusted when it writes from a new chat.
	code := testkit.Decode[dto.PairingCode](c.alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: c.conn.ID}).RequireStatus(http.StatusCreated))
	c.emit(dmText(dmC, "C0", "!link "+code.Code))
	c.emit(dmText(dmC, "C1", "it is alice"))
	got = c.agent.Message(connectortest.Timeout)
	require.Equal(t, "it is alice", got.Message.Text, "the group and the other room's DM never reached the agent")
	require.True(t, got.Trusted)
}

// ROUTE-01 scopes: groups takes group and channel chats but no DMs; all takes every kind;
// off routes nothing.
func TestCatchAllScopes(t *testing.T) {
	c := newCatchAll(t)
	c.setScope(t, dto.AutoRouteScopeGroups, c.room.ID)
	c.emit(text(groupID, "G1", "group"))
	c.emit(channelText("channel-1", "H1", "channel"))
	c.emit(dmText(dmA, "A1", "dm"))
	members := c.chatMembers(t, c.room.ID)
	require.Contains(t, members, c.chatID(t, groupID))
	require.Contains(t, members, c.chatID(t, "channel-1"))
	require.NotContains(t, members, c.chatID(t, dmA))

	off := dto.AutoRouteScopeOff
	c.alice.Patch("/api/v1/connectors/"+c.conn.ID, dto.UpdateConnectorRequest{AutoRouteScope: &off}).RequireStatus(http.StatusOK)
	c.emit(dmText(dmB, "B1", "dm while off"))
	c.emit(text(otherID, "O1", "group while off"))
	members = c.chatMembers(t, c.room.ID)
	require.NotContains(t, members, c.chatID(t, dmB))
	require.NotContains(t, members, c.chatID(t, otherID))

	c.setScope(t, dto.AutoRouteScopeAll, c.room.ID)
	c.emit(dmText(dmB, "B2", "dm"))
	c.emit(text(otherID, "O2", "group"))
	members = c.chatMembers(t, c.room.ID)
	require.Contains(t, members, c.chatID(t, dmB))
	require.Contains(t, members, c.chatID(t, otherID))
}

// ROUTE-01 edge case: two first messages of a new chat arriving at once add the chat once;
// both messages are stored.
func TestCatchAllConcurrentFirstMessagesAddOnce(t *testing.T) {
	c := newCatchAll(t)
	c.setScope(t, dto.AutoRouteScopeAll, c.room.ID)
	var wg sync.WaitGroup
	for _, id := range []string{"A1", "A2", "A3", "A4"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.inst.Emit(context.Background(), dmText(dmA, id, "burst "+id))
		}()
	}
	wg.Wait()
	members := c.chatMembers(t, c.room.ID) // fails on a duplicate membership
	require.Len(t, members, 1)
	require.Len(t, c.messages(t, c.room.ID), 4)
}

// ROUTE-02 edge case: a catch-all chat removed from the room is not added back for 24 hours
// (fake clock: still out just before, back just after); a manager may add it back at once.
func TestCatchAllRemovedChatWaits24Hours(t *testing.T) {
	clock := testkit.NewClock(time.Now())
	c := newCatchAll(t, testkit.WithClock(clock.Now))
	c.setScope(t, dto.AutoRouteScopeAll, c.room.ID)
	c.emit(dmText(dmA, "A1", "first"))
	chatA := c.chatID(t, dmA)
	member := c.chatMembers(t, c.room.ID)[chatA]
	c.alice.Delete("/api/v1/rooms/" + c.room.ID + "/members/" + member.ID).RequireStatus(http.StatusNoContent)

	before := c.messageCount(t)
	c.emit(dmText(dmA, "A2", "right after"))
	clock.Advance(24*time.Hour - time.Minute)
	c.emit(dmText(dmA, "A3", "almost a day later"))
	require.NotContains(t, c.chatMembers(t, c.room.ID), chatA)
	require.Equal(t, before, c.messageCount(t), "nothing stored while skipped")

	clock.Advance(2 * time.Minute)
	c.emit(dmText(dmA, "A4", "a day later"))
	require.Contains(t, c.chatMembers(t, c.room.ID), chatA)
	msgs := c.messages(t, c.room.ID)
	require.Equal(t, "a day later", msgs[0].Text)

	// Removed again, then added back by a manager at once: messages flow again.
	member = c.chatMembers(t, c.room.ID)[chatA]
	c.alice.Delete("/api/v1/rooms/" + c.room.ID + "/members/" + member.ID).RequireStatus(http.StatusNoContent)
	c.alice.Post("/api/v1/rooms/"+c.room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindChat, MemberID: chatA}).RequireStatus(http.StatusCreated)
	c.emit(dmText(dmA, "A5", "manual"))
	require.Equal(t, "manual", c.messages(t, c.room.ID)[0].Text)
}

// ROUTE-03: when the catch-all room is deleted, auto-routing stops: the connector reports
// "auto-route room unavailable" and the new chat's message is not stored.
func TestCatchAllDeletedRoomIsUnavailable(t *testing.T) {
	c := newCatchAll(t)
	c.setScope(t, dto.AutoRouteScopeAll, c.room.ID)
	c.alice.Delete("/api/v1/rooms/" + c.room.ID).RequireStatus(http.StatusNoContent)
	before := c.messageCount(t)

	c.emit(dmText(dmA, "A1", "anyone there?"))
	require.Equal(t, "auto-route room unavailable", c.connector(t).LastError)
	require.Equal(t, before, c.messageCount(t))
}

// ROUTE-03 with a second owner: alice's connector routes into bob's room through a manage
// grant; once bob takes the grant back, auto-routing stops with the same status detail and
// nothing is stored or added.
func TestCatchAllLostManageIsUnavailable(t *testing.T) {
	c := newCatchAll(t)
	bob := c.env.NewUser("bob")
	bobs := testkit.Decode[dto.Room](bob.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "bob's catch-all"}).RequireStatus(http.StatusCreated))
	bob.Put("/api/v1/rooms/"+bobs.ID+"/sharing", dto.Sharing{Mode: dto.VisibilityShared, Users: []dto.SharingUser{{UserID: c.alice.User.ID, Level: dto.LevelManage}}}).RequireStatus(http.StatusOK)
	c.setScope(t, dto.AutoRouteScopeAll, bobs.ID)
	c.emit(dmText(dmA, "A1", "routed into bob's room"))
	require.Len(t, testkit.Decode[dto.Page[dto.Message]](bob.Get("/api/v1/rooms/"+bobs.ID+"/messages").RequireStatus(http.StatusOK)).Items, 1)
	require.Empty(t, c.connector(t).LastError)

	bob.Put("/api/v1/rooms/"+bobs.ID+"/sharing", dto.Sharing{Mode: dto.VisibilityShared, Users: []dto.SharingUser{{UserID: c.alice.User.ID, Level: dto.LevelPost}}}).RequireStatus(http.StatusOK)
	c.emit(dmText(dmB, "B1", "after the grant changed"))
	require.Equal(t, "auto-route room unavailable", c.connector(t).LastError)
	page := testkit.Decode[dto.Page[dto.Message]](bob.Get("/api/v1/rooms/" + bobs.ID + "/messages").RequireStatus(http.StatusOK)).Items
	require.Len(t, page, 1, "nothing new stored")
	members := testkit.Decode[dto.List[dto.RoomMember]](bob.Get("/api/v1/rooms/" + bobs.ID + "/members").RequireStatus(http.StatusOK)).Items
	require.Len(t, members, 1, "only the first chat joined")
}

// dmPeer is a synthetic WhatsApp-style DM id.
func dmPeer(n int) string { return fmt.Sprintf("55119%08d@s.whatsapp.net", n) }

// ROUTE-08, ROUTE-11: a connector auto-adds at most 60 chats per hour. The 61st new chat in
// the hour is not added, not even recorded as a chat, and nothing is stored; the connector
// reports "auto-route limit reached", and chats already routed keep flowing. An hour later the
// chat is added and the detail clears.
func TestCatchAllHourlyCap(t *testing.T) {
	clock := testkit.NewClock(time.Now())
	c := newCatchAll(t, testkit.WithClock(clock.Now))
	c.setScope(t, dto.AutoRouteScopeDMs, c.room.ID)
	for i := range 60 {
		c.emit(dmText(dmPeer(i), fmt.Sprintf("M%d", i), "hello"))
	}
	require.Len(t, c.chatMembers(t, c.room.ID), 60)
	require.Empty(t, c.connector(t).LastError)

	before := c.messageCount(t)
	c.emit(dmText(dmPeer(60), "M60", "one too many"))
	require.Len(t, c.chatMembers(t, c.room.ID), 60)
	require.False(t, c.hasChat(t, dmPeer(60)), "a capped chat leaves no chat record")
	require.Equal(t, before, c.messageCount(t), "nothing stored")
	require.Equal(t, "auto-route limit reached", c.connector(t).LastError)
	c.emit(dmText(dmPeer(0), "M0b", "already routed"))
	require.Equal(t, before+1, c.messageCount(t), "routed chats keep flowing")

	clock.Advance(time.Hour + time.Second)
	c.emit(dmText(dmPeer(60), "M60b", "an hour later"))
	require.Contains(t, c.chatMembers(t, c.room.ID), c.chatID(t, dmPeer(60)))
	require.Empty(t, c.connector(t).LastError, "the next successful route clears the detail")
}

// ROUTE-08, ROUTE-11: a catch-all room holds at most 1,000 chats: with 999 chats it takes one
// more, then a new chat is not added nor recorded, nothing is stored and the connector reports
// the limit.
func TestCatchAllRoomCap(t *testing.T) {
	c := newCatchAll(t)
	c.setScope(t, dto.AutoRouteScopeAll, c.room.ID)
	c.seedRoomChats(t, 999)

	c.emit(dmText(dmA, "A1", "the thousandth"))
	require.Contains(t, c.chatMembers(t, c.room.ID), c.chatID(t, dmA))
	require.Empty(t, c.connector(t).LastError)

	before := c.messageCount(t)
	c.emit(dmText(dmB, "B1", "room is full"))
	require.Len(t, c.chatMembers(t, c.room.ID), 1000)
	require.False(t, c.hasChat(t, dmB), "a capped chat leaves no chat record")
	require.Equal(t, before, c.messageCount(t), "nothing stored")
	require.Equal(t, "auto-route limit reached", c.connector(t).LastError)
}

// ROUTE-09: "auto-route room unavailable" clears once a chat is routed again (bob restores
// alice's manage grant) and when the catch-all settings are saved again; a status detail that
// catch-all routing did not set is left alone.
func TestCatchAllStatusDetailClears(t *testing.T) {
	c := newCatchAll(t)
	bob := c.env.NewUser("bob")
	bobs := testkit.Decode[dto.Room](bob.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "bob's catch-all"}).RequireStatus(http.StatusCreated))
	share := func(level dto.Level) {
		bob.Put("/api/v1/rooms/"+bobs.ID+"/sharing", dto.Sharing{Mode: dto.VisibilityShared, Users: []dto.SharingUser{{UserID: c.alice.User.ID, Level: level}}}).RequireStatus(http.StatusOK)
	}
	share(dto.LevelManage)
	c.setScope(t, dto.AutoRouteScopeAll, bobs.ID)
	share(dto.LevelPost)
	c.emit(dmText(dmA, "A1", "lost"))
	require.Equal(t, "auto-route room unavailable", c.connector(t).LastError)

	share(dto.LevelManage)
	c.emit(dmText(dmB, "B1", "routed again"))
	require.Empty(t, c.connector(t).LastError, "a successful route clears the detail")

	bob.Delete("/api/v1/rooms/" + bobs.ID).RequireStatus(http.StatusNoContent)
	c.emit(dmText(dmC, "C1", "room gone"))
	require.Equal(t, "auto-route room unavailable", c.connector(t).LastError)
	fresh := testkit.Decode[dto.Room](c.alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "new catch-all"}).RequireStatus(http.StatusCreated))
	c.setScope(t, dto.AutoRouteScopeAll, fresh.ID)
	require.Empty(t, c.connector(t).LastError, "saving the catch-all settings clears the detail")

	ctx := context.Background()
	require.NoError(t, c.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.SetConnectorLastError(ctx, db.SetConnectorLastErrorParams{LastError: "connection lost", ID: c.conn.ID})
	}))
	c.setScope(t, dto.AutoRouteScopeDMs, fresh.ID)
	require.Equal(t, "connection lost", c.connector(t).LastError, "other details stay")
}

// seedRoomChats adds n synthetic group chats of the connector to the catch-all room.
func (c *catchAll) seedRoomChats(t *testing.T, n int) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UnixMilli()
	require.NoError(t, c.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		for i := range n {
			chat := store.NewID()
			if err := q.CreateChat(ctx, db.CreateChatParams{ID: chat, ConnectorID: c.conn.ID, ExternalID: fmt.Sprintf("seed-%d", i), Kind: "group", CreatedAt: now}); err != nil {
				return err
			}
			if err := q.CreateRoomMember(ctx, db.CreateRoomMemberParams{ID: store.NewID(), RoomID: c.room.ID, Kind: "chat", MemberID: chat, Outbound: true, AddedBy: c.alice.User.ID, CreatedAt: now}); err != nil {
				return err
			}
		}
		return nil
	}))
}

// ownPipeline is a second inbound pipeline on the env's app whose catch-all routes call hook
// once they passed the caps and before the chat is added.
func (c *catchAll) ownPipeline(hook func(connectorID string)) *connectors.Pipeline {
	a := c.env.App
	p := connectors.NewPipeline(connectors.PipelineDeps{
		Store: a.Store, Bus: a.Bus, Rooms: a.Rooms, Blobs: a.Blobs, Identities: a.Identities, MaxUploadBytes: 1 << 20,
	})
	p.SetRouteHook(hook)
	return p
}

func (c *catchAll) source() connectors.Source {
	return connectors.Source{ConnectorID: c.conn.ID, Kind: connectortest.KindName, OwnerID: c.alice.User.ID}
}

// barrier holds each caller until n callers arrived or wait passed, so routes that are not
// serialized all pass the caps before any of them adds its chat.
type barrier struct {
	mu      sync.Mutex
	n       int
	arrived int
	all     chan struct{}
	wait    time.Duration
}

func newBarrier(n int, wait time.Duration) *barrier {
	return &barrier{n: n, all: make(chan struct{}), wait: wait}
}

func (b *barrier) hold(string) {
	b.mu.Lock()
	b.arrived++
	if b.arrived == b.n {
		close(b.all)
	}
	b.mu.Unlock()
	select {
	case <-b.all:
	case <-time.After(b.wait):
	}
}

// burst handles first messages of the DM peers from..from+n-1 at once.
func (c *catchAll) burst(p *connectors.Pipeline, from, n int) {
	var wg sync.WaitGroup
	for i := from; i < from+n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.Handle(context.Background(), c.source(), dmText(dmPeer(i), fmt.Sprintf("M%d", i), "burst"))
		}()
	}
	wg.Wait()
}

// ROUTE-08 under concurrency: with 59 chats auto-added in the hour, five new chats writing at
// once add exactly one more and the connector reports the limit; routes that passed the cap
// together would add all five.
func TestCatchAllHourlyCapHoldsUnderBurst(t *testing.T) {
	c := newCatchAll(t)
	c.setScope(t, dto.AutoRouteScopeDMs, c.room.ID)
	p := c.ownPipeline(nil)
	for i := range 59 {
		p.Handle(context.Background(), c.source(), dmText(dmPeer(i), fmt.Sprintf("M%d", i), "hello"))
	}
	require.Len(t, c.chatMembers(t, c.room.ID), 59)

	p.SetRouteHook(newBarrier(5, 500*time.Millisecond).hold)
	c.burst(p, 59, 5)
	require.Len(t, c.chatMembers(t, c.room.ID), 60)
	require.Equal(t, "auto-route limit reached", c.connector(t).LastError)
}

// ROUTE-08 under concurrency: a catch-all room with 999 chats takes exactly one of five new
// chats writing at once.
func TestCatchAllRoomCapHoldsUnderBurst(t *testing.T) {
	c := newCatchAll(t)
	c.setScope(t, dto.AutoRouteScopeAll, c.room.ID)
	c.seedRoomChats(t, 999)
	c.burst(c.ownPipeline(newBarrier(5, 500*time.Millisecond).hold), 0, 5)
	require.Len(t, c.chatMembers(t, c.room.ID), 1000)
	require.Equal(t, "auto-route limit reached", c.connector(t).LastError)
}

// statusEvents subscribes to the bus and returns a function that drains the connector.status
// events of the connector published so far.
func (c *catchAll) statusEvents(t *testing.T) func() []events.ConnectorStatus {
	t.Helper()
	ch, cancel := c.env.App.Bus.Subscribe(256)
	t.Cleanup(cancel)
	return func() []events.ConnectorStatus {
		var out []events.ConnectorStatus
		for {
			select {
			case e := <-ch:
				if s, ok := e.(events.ConnectorStatus); ok && s.ConnectorID == c.conn.ID {
					out = append(out, s)
				}
			default:
				return out
			}
		}
	}
}

// ROUTE-09: clearing an auto-route detail is announced once as a connector.status event with
// the connector's status and no detail, after a successful route and after a catch-all
// settings save.
func TestCatchAllClearedDetailIsAnnounced(t *testing.T) {
	c := newCatchAll(t)
	c.setScope(t, dto.AutoRouteScopeAll, c.room.ID)
	ctx := context.Background()
	limited := func() {
		require.NoError(t, c.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
			return q.SetConnectorLastError(ctx, db.SetConnectorLastErrorParams{LastError: connectors.AutoRouteLimited, ID: c.conn.ID})
		}))
	}
	status := string(c.connector(t).Status)
	cleared := []events.ConnectorStatus{{ConnectorID: c.conn.ID, Status: status}}
	drain := c.statusEvents(t)

	limited()
	c.emit(dmText(dmA, "A1", "routed"))
	require.Empty(t, c.connector(t).LastError)
	require.Equal(t, cleared, drain(), "a successful route announces the cleared detail")

	limited()
	c.setScope(t, dto.AutoRouteScopeDMs, c.room.ID)
	require.Empty(t, c.connector(t).LastError)
	require.Equal(t, cleared, drain(), "a catch-all save announces the cleared detail")
}

// hasChat tells whether the connector recorded a chat with the platform id.
func (c *inboundEnv) hasChat(t *testing.T, external string) bool {
	t.Helper()
	for _, chat := range c.chats(t) {
		if chat.ExternalID == external {
			return true
		}
	}
	return false
}

// ROUTE-11, CON-07: chats that are not routed for other reasons are still recorded: a chat
// outside the scope, a chat of a connector whose catch-all room is gone, and a chat that only
// sent an empty message.
func TestCatchAllUnroutedChatsAreRecorded(t *testing.T) {
	c := newCatchAll(t)
	c.setScope(t, dto.AutoRouteScopeGroups, c.room.ID)
	c.emit(dmText(dmA, "A1", "out of scope"))
	require.True(t, c.hasChat(t, dmA))
	empty := dmText(dmB, "B1", "")
	c.emit(empty)
	require.True(t, c.hasChat(t, dmB))

	c.setScope(t, dto.AutoRouteScopeAll, c.room.ID)
	c.alice.Delete("/api/v1/rooms/" + c.room.ID).RequireStatus(http.StatusNoContent)
	c.emit(dmText(dmC, "C1", "room gone"))
	require.True(t, c.hasChat(t, dmC))
	require.Equal(t, "auto-route room unavailable", c.connector(t).LastError)
}

// ROUTE-11: catch-all additions lock per connector: while a route of alice's connector is held
// between its cap check and the addition, a new chat of her second connector is routed to the
// same room without waiting.
func TestCatchAllRoutesLockPerConnector(t *testing.T) {
	c := newCatchAll(t)
	c.setScope(t, dto.AutoRouteScopeAll, c.room.ID)
	all := dto.AutoRouteScopeAll
	second := testkit.Decode[dto.Connector](c.alice.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: connectortest.KindName, Name: "wa 2", Config: map[string]any{"token": "fake-token-not-real"},
	}).RequireStatus(http.StatusCreated))
	c.alice.Patch("/api/v1/connectors/"+second.ID, dto.UpdateConnectorRequest{AutoRouteScope: &all, AutoRouteRoomID: &c.room.ID}).RequireStatus(http.StatusOK)

	held, release := make(chan struct{}), make(chan struct{})
	p := c.ownPipeline(func(connectorID string) {
		if connectorID == c.conn.ID {
			close(held)
			<-release
		}
	})
	ctx := context.Background()
	doneFirst := make(chan struct{})
	go func() {
		defer close(doneFirst)
		p.Handle(ctx, c.source(), dmText(dmA, "A1", "flood"))
	}()
	select {
	case <-held:
	case <-time.After(connectortest.Timeout):
		t.Fatal("the first connector's route never reached the hook")
	}
	doneSecond := make(chan struct{})
	go func() {
		defer close(doneSecond)
		p.Handle(ctx, connectors.Source{ConnectorID: second.ID, Kind: connectortest.KindName, OwnerID: c.alice.User.ID}, dmText(dmB, "B1", "other connector"))
	}()
	select {
	case <-doneSecond:
	case <-time.After(connectortest.Timeout):
		close(release)
		<-doneFirst
		t.Fatal("the second connector waited for the first connector's route")
	}
	require.Len(t, c.chatMembers(t, c.room.ID), 1, "only the second connector's chat joined so far")
	close(release)
	<-doneFirst
	require.Len(t, c.chatMembers(t, c.room.ID), 2)
}

// ROUTE-08, ROUTE-11: the hourly cap counts each connector's own routes. With alice's first
// connector at 60 routes in the hour, its next new chat is refused with the limit detail, while
// a new chat of her second connector, routed to the same room, still joins and that connector
// reports nothing.
func TestCatchAllHourlyCapIsPerConnector(t *testing.T) {
	c := newCatchAll(t)
	c.setScope(t, dto.AutoRouteScopeAll, c.room.ID)
	all := dto.AutoRouteScopeAll
	second := testkit.Decode[dto.Connector](c.alice.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: connectortest.KindName, Name: "wa 2", Config: map[string]any{"token": "fake-token-not-real"},
	}).RequireStatus(http.StatusCreated))
	c.alice.Patch("/api/v1/connectors/"+second.ID, dto.UpdateConnectorRequest{AutoRouteScope: &all, AutoRouteRoomID: &c.room.ID}).RequireStatus(http.StatusOK)
	p := c.ownPipeline(nil)
	ctx := context.Background()
	for i := range 60 {
		p.Handle(ctx, c.source(), dmText(dmPeer(i), fmt.Sprintf("M%d", i), "hello"))
	}
	p.Handle(ctx, c.source(), dmText(dmPeer(60), "M60", "one too many"))
	require.Len(t, c.chatMembers(t, c.room.ID), 60)
	require.Equal(t, "auto-route limit reached", c.connector(t).LastError)

	p.Handle(ctx, connectors.Source{ConnectorID: second.ID, Kind: connectortest.KindName, OwnerID: c.alice.User.ID}, dmText(dmA, "A1", "other connector"))
	require.Len(t, c.chatMembers(t, c.room.ID), 61, "the second connector's chat joined")
	got := testkit.Decode[dto.Connector](c.alice.Get("/api/v1/connectors/" + second.ID).RequireStatus(http.StatusOK))
	require.Empty(t, got.LastError, "the second connector is under its own cap")
}

// ROUTE-11, CTX-08: a chat held out by the 24-hour skip is still recorded: its message updates
// the stored title and last-seen time, while the chat stays out of the room and nothing is
// stored.
func TestCatchAllSkippedChatIsRecorded(t *testing.T) {
	clock := testkit.NewClock(time.Now())
	c := newCatchAll(t, testkit.WithClock(clock.Now))
	c.setScope(t, dto.AutoRouteScopeAll, c.room.ID)
	c.emit(dmText(dmA, "A1", "first"))
	chatA := c.chatID(t, dmA)
	member := c.chatMembers(t, c.room.ID)[chatA]
	c.alice.Delete("/api/v1/rooms/" + c.room.ID + "/members/" + member.ID).RequireStatus(http.StatusNoContent)
	before := c.messageCount(t)

	clock.Advance(time.Hour)
	later := dmText(dmA, "A2", "while skipped")
	later.ChatTitle = "Customer Renamed"
	later.At = clock.Now()
	c.emit(later)
	require.NotContains(t, c.chatMembers(t, c.room.ID), chatA, "still skipped")
	require.Equal(t, before, c.messageCount(t), "nothing stored")
	var got dto.Chat
	for _, chat := range c.chats(t) {
		if chat.ExternalID == dmA {
			got = chat
		}
	}
	require.Equal(t, chatA, got.ID)
	require.Equal(t, "Customer Renamed", got.Title)
	require.NotNil(t, got.LastSeenAt)
	require.Equal(t, later.At.UnixMilli(), got.LastSeenAt.UnixMilli())
}
