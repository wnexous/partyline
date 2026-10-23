package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

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

// ROUTE-07 edge case: when no connector targets a room any more, because the catch-all was
// cleared or moved to another room, its broadcast stays off until a manager turns it on.
func TestBroadcastStaysOffAfterCatchAllIsCleared(t *testing.T) {
	e := newCatchAllEnv(t)
	first, second := createRoom(e.alice, "inbox"), createRoom(e.alice, "inbox 2")
	path := connectorPath(e.aliceConn)
	e.alice.Patch(path, dto.UpdateConnectorRequest{AutoRouteScope: ptr(dto.AutoRouteScopeAll), AutoRouteRoomID: &first.ID}).RequireStatus(http.StatusOK)
	e.alice.Patch(path, dto.UpdateConnectorRequest{AutoRouteRoomID: &second.ID}).RequireStatus(http.StatusOK)
	moved := getRoom(e.alice, first.ID)
	require.False(t, moved.CatchAll)
	require.False(t, moved.Broadcast, "moving the catch-all away leaves broadcast off")

	e.alice.Patch(path, dto.UpdateConnectorRequest{AutoRouteScope: ptr(dto.AutoRouteScopeOff), AutoRouteRoomID: ptr("")}).RequireStatus(http.StatusOK)
	cleared := getRoom(e.alice, second.ID)
	require.False(t, cleared.CatchAll)
	require.False(t, cleared.Broadcast, "clearing the catch-all leaves broadcast off")
	require.False(t, cleared.BridgeChats)
}
