package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

const noBroadcastWarning = "not sent to any chat: this room only answers explicit chats (use reply or chat_ids)"

func getRoom(c *testkit.Client, id string) dto.Room {
	return testkit.Decode[dto.Room](c.Get("/api/v1/rooms/" + id).RequireStatus(http.StatusOK))
}

// ROUTE-07: choosing a room as a connector's catch-all room turns its broadcast and chat
// bridging off; while it is one, turning either on answers 422 for the room owner and for
// another manager alike (here bob, whose connector targets alice's room with her grant).
// Once no connector targets it, broadcast can be turned on again.
func TestCatchAllRoomStopsBroadcastAndBridge(t *testing.T) {
	e := newCatchAllEnv(t)
	room := createRoom(e.alice, "inbox")
	e.alice.Patch("/api/v1/rooms/"+room.ID, dto.UpdateRoomRequest{BridgeChats: ptr(true)}).RequireStatus(http.StatusOK)
	before := getRoom(e.alice, room.ID)
	require.True(t, before.Broadcast, "rooms broadcast by default")
	require.False(t, before.CatchAll)

	shareRoom(e.alice, room.ID, dto.SharingUser{UserID: e.bob.User.ID, Level: dto.LevelManage})
	e.bob.Patch(connectorPath(e.bobConn), dto.UpdateConnectorRequest{
		AutoRouteScope: ptr(dto.AutoRouteScopeDMs), AutoRouteRoomID: &room.ID,
	}).RequireStatus(http.StatusOK)
	after := getRoom(e.alice, room.ID)
	require.False(t, after.Broadcast)
	require.False(t, after.BridgeChats)
	require.True(t, after.CatchAll)

	for _, c := range []*testkit.Client{e.alice, e.bob} {
		fields := c.Patch("/api/v1/rooms/"+room.ID, dto.UpdateRoomRequest{Broadcast: ptr(true)}).
			RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid).Fields
		require.Equal(t, map[string]string{"broadcast": "must stay off while the room is a catch-all room"}, fields)
		fields = c.Patch("/api/v1/rooms/"+room.ID, dto.UpdateRoomRequest{BridgeChats: ptr(true)}).
			RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid).Fields
		require.Equal(t, map[string]string{"bridge_chats": "must stay off while the room is a catch-all room"}, fields)
	}
	require.False(t, getRoom(e.alice, room.ID).Broadcast)

	e.bob.Patch(connectorPath(e.bobConn), dto.UpdateConnectorRequest{
		AutoRouteScope: ptr(dto.AutoRouteScopeOff), AutoRouteRoomID: ptr(""),
	}).RequireStatus(http.StatusOK)
	got := testkit.Decode[dto.Room](e.alice.Patch("/api/v1/rooms/"+room.ID, dto.UpdateRoomRequest{Broadcast: ptr(true)}).RequireStatus(http.StatusOK))
	require.True(t, got.Broadcast)
	require.False(t, got.CatchAll)
}

// ROUTE-06 (HTTP): in a room with broadcast off, user and agent posts without recipients are
// stored, pushed to the agent and queued to no chat, and the response carries the warning;
// replies (agent reply tool or reply_to) go to the replied chat; explicit recipients work.
// History does not repeat the warning.
func TestNoBroadcastRoomPosts(t *testing.T) {
	f := newAgentRoom(t)
	family := f.chatMember(t, "Family")
	f.chatMember(t, "Work")
	f.alice.Patch("/api/v1/rooms/"+f.room.ID, dto.UpdateRoomRequest{Broadcast: ptr(false)}).RequireStatus(http.StatusOK)
	conn := f.dial(t)
	conn.Welcome(wsWait)

	fromUser := f.post(f.alice, "hello everyone")
	require.Empty(t, outboxChatIDs(fromUser), "queued to no chat")
	require.Equal(t, noBroadcastWarning, fromUser.Warning)
	require.Equal(t, fromUser.ID, conn.Message(wsWait).Message.ID, "agents still get it")

	fromAgent := testkit.Decode[dto.Message](f.rest().Post(agentRoomPath(f.room.ID, "/messages"), dto.PostMessageRequest{Text: "hi all"}).RequireStatus(http.StatusCreated))
	require.Empty(t, outboxChatIDs(fromAgent))
	require.Equal(t, noBroadcastWarning, fromAgent.Warning)

	target := f.postFromChat(t, family, "can you help?")
	require.Equal(t, target.ID, conn.Message(wsWait).Message.ID)
	reply := testkit.Decode[dto.Message](f.rest().Post("/api/v1/agent/messages/"+target.ID+"/reply", dto.ReplyRequest{Text: "sure"}).RequireStatus(http.StatusCreated))
	require.Equal(t, []string{family}, outboxChatIDs(reply))
	require.Empty(t, reply.Warning)
	userReply := testkit.Decode[dto.Message](f.alice.Post("/api/v1/rooms/"+f.room.ID+"/messages", dto.PostMessageRequest{Text: "me too", ReplyTo: target.ID}).RequireStatus(http.StatusCreated))
	require.Equal(t, []string{family}, outboxChatIDs(userReply))
	require.Empty(t, userReply.Warning)
	explicit := testkit.Decode[dto.Message](f.rest().Post(agentRoomPath(f.room.ID, "/messages"), dto.PostMessageRequest{Text: "only family", Recipients: []string{family}}).RequireStatus(http.StatusCreated))
	require.Equal(t, []string{family}, outboxChatIDs(explicit))
	require.Empty(t, explicit.Warning)

	page := testkit.Decode[dto.Page[dto.Message]](f.alice.Get("/api/v1/rooms/" + f.room.ID + "/messages").RequireStatus(http.StatusOK))
	require.Len(t, page.Items, 6, "every post is stored")
	for _, m := range page.Items {
		require.Empty(t, m.Warning, "the warning answers the post only")
	}
}
