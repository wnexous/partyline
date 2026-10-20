package connectors_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
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

func (c *catchAll) connector(t *testing.T) dto.Connector {
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
