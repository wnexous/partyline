package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/testkit"
)

// catchAllEnv has an admin (audit reader), alice and bob, each owning a running fake
// connector.
type catchAllEnv struct {
	env                *testkit.Env
	admin              *testkit.Client
	alice, bob         *testkit.Client
	aliceConn, bobConn dto.Connector
}

func newCatchAllEnv(t *testing.T) *catchAllEnv {
	t.Helper()
	fake := connectortest.New()
	env := testkit.New(t, testkit.WithConnectorKinds(fake.Kind()))
	e := &catchAllEnv{env: env, admin: env.SetupAdmin(), alice: env.NewUser("alice"), bob: env.NewUser("bob")}
	create := func(c *testkit.Client) dto.Connector {
		conn := testkit.Decode[dto.Connector](c.Post("/api/v1/connectors", dto.CreateConnectorRequest{
			Kind: connectortest.KindName, Name: "wa", Config: map[string]any{"token": "fake-token-not-real"},
		}).RequireStatus(http.StatusCreated))
		return c.OperateConnector(conn.ID, c.NewAI("relay")) // a connection runs once an AI operates it
	}
	e.aliceConn, e.bobConn = create(e.alice), create(e.bob)
	return e
}

func connectorPath(c dto.Connector) string { return "/api/v1/connectors/" + c.ID }

// ROUTE-04: a connector manager sets the catch-all scope and room; the caller needs manage on
// it, and an unknown or hidden room answers the same 403; scopes outside off|dms|groups|all get 422. A
// refused request changes nothing. New connectors start with the catch-all off.
func TestCatchAllSettings(t *testing.T) {
	e := newCatchAllEnv(t)
	require.Equal(t, dto.AutoRouteScopeOff, e.aliceConn.AutoRouteScope)
	require.Empty(t, e.aliceConn.AutoRouteRoomID)
	path := connectorPath(e.aliceConn)
	mine := createRoom(e.alice, "catch-all")

	got := testkit.Decode[dto.Connector](e.alice.Patch(path, dto.UpdateConnectorRequest{
		AutoRouteScope: ptr(dto.AutoRouteScopeDMs), AutoRouteRoomID: &mine.ID,
	}).RequireStatus(http.StatusOK))
	require.Equal(t, dto.AutoRouteScopeDMs, got.AutoRouteScope)
	require.Equal(t, mine.ID, got.AutoRouteRoomID)
	got = testkit.Decode[dto.Connector](e.alice.Get(path).RequireStatus(http.StatusOK))
	require.Equal(t, dto.AutoRouteScopeDMs, got.AutoRouteScope)
	require.Equal(t, mine.ID, got.AutoRouteRoomID)
	for _, scope := range []dto.AutoRouteScope{dto.AutoRouteScopeGroups, dto.AutoRouteScopeAll} {
		require.Equal(t, scope, testkit.Decode[dto.Connector](e.alice.Patch(path, dto.UpdateConnectorRequest{AutoRouteScope: &scope}).RequireStatus(http.StatusOK)).AutoRouteScope)
	}

	entry := findAudit(t, auditPage(e.admin, "").Items, "connector.update")
	require.Equal(t, e.aliceConn.ID, entry.TargetID)
	require.Contains(t, entry.Detail["changed"], "auto_route")

	unchanged := func() {
		t.Helper()
		got := testkit.Decode[dto.Connector](e.alice.Get(path).RequireStatus(http.StatusOK))
		require.Equal(t, dto.AutoRouteScopeAll, got.AutoRouteScope)
		require.Equal(t, mine.ID, got.AutoRouteRoomID)
	}

	// A room bob owns and shares with alice for posting only, and one she cannot see.
	bobs := createRoom(e.bob, "bob's")
	shareRoom(e.bob, bobs.ID, dto.SharingUser{UserID: e.alice.User.ID, Level: dto.LevelPost})
	hidden := createRoom(e.bob, "hidden")
	for _, id := range []string{bobs.ID, hidden.ID} {
		e.alice.Patch(path, dto.UpdateConnectorRequest{AutoRouteRoomID: &id}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		unchanged()
	}

	// ROUTE-04: an unknown room answers exactly like a hidden one, so the answer does not
	// tell whether a room id exists.
	unknown := "0190a000-0000-7000-8000-000000000000"
	unknownErr := e.alice.Patch(path, dto.UpdateConnectorRequest{AutoRouteRoomID: &unknown}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	hiddenErr := e.alice.Patch(path, dto.UpdateConnectorRequest{AutoRouteRoomID: &hidden.ID}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	require.Equal(t, hiddenErr, unknownErr)
	unchanged()
	fields := e.alice.Patch(path, dto.UpdateConnectorRequest{AutoRouteScope: ptr(dto.AutoRouteScope("everyone"))}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid).Fields
	require.Contains(t, fields, "auto_route_scope")
	unchanged()
	// Clearing the room keeps the scope: the connector then gives each new chat a conversation of
	// its own with its operator instead of adding it to a catch-all room (ACC-02, spec issue 2).
	got = testkit.Decode[dto.Connector](e.alice.Patch(path, dto.UpdateConnectorRequest{AutoRouteRoomID: ptr("")}).RequireStatus(http.StatusOK))
	require.Equal(t, dto.AutoRouteScopeAll, got.AutoRouteScope)
	require.Empty(t, got.AutoRouteRoomID)
	require.Equal(t, dto.AutoRouteScopeAll, testkit.Decode[dto.Connector](e.alice.Get(path).RequireStatus(http.StatusOK)).AutoRouteScope)

	got = testkit.Decode[dto.Connector](e.alice.Patch(path, dto.UpdateConnectorRequest{
		AutoRouteScope: ptr(dto.AutoRouteScopeOff), AutoRouteRoomID: ptr(""),
	}).RequireStatus(http.StatusOK))
	require.Equal(t, dto.AutoRouteScopeOff, got.AutoRouteScope)
	require.Empty(t, got.AutoRouteRoomID)
}

// ROUTE-04 per owner: manage granted on someone else's room counts; bob's own connector
// cannot point at alice's room until she grants him manage, and alice, who uses bob's
// connector without managing it, cannot change its catch-all.
func TestCatchAllSettingsSecondOwner(t *testing.T) {
	e := newCatchAllEnv(t)
	room := createRoom(e.alice, "shared catch-all")
	path := connectorPath(e.bobConn)
	req := dto.UpdateConnectorRequest{AutoRouteScope: ptr(dto.AutoRouteScopeAll), AutoRouteRoomID: &room.ID}

	e.bob.Patch(path, req).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	shareRoom(e.alice, room.ID, dto.SharingUser{UserID: e.bob.User.ID, Level: dto.LevelManage})
	got := testkit.Decode[dto.Connector](e.bob.Patch(path, req).RequireStatus(http.StatusOK))
	require.Equal(t, room.ID, got.AutoRouteRoomID)

	e.bob.Put(path+"/sharing", dto.Sharing{Mode: dto.VisibilityShared, Users: []dto.SharingUser{{UserID: e.alice.User.ID}}}).RequireStatus(http.StatusOK)
	e.alice.Patch(path, dto.UpdateConnectorRequest{AutoRouteScope: ptr(dto.AutoRouteScopeOff)}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	require.Equal(t, dto.AutoRouteScopeAll, testkit.Decode[dto.Connector](e.bob.Get(path).RequireStatus(http.StatusOK)).AutoRouteScope)
}

// ROUTE-03 with per-chat conversations (ACC-02): deleting a connector's catch-all room stops its
// routing at once and says so, rather than leaving the connector with a scope and no room, which
// now means "a conversation for each new chat": the scope goes off and the status detail is the
// literal "auto-route room unavailable", published live. Choosing a new room or scope starts
// routing again and clears the detail.
func TestDeletingTheCatchAllRoomStopsRouting(t *testing.T) {
	e := newCatchAllEnv(t)
	path := connectorPath(e.aliceConn)
	room := createRoom(e.alice, "catch-all")
	e.alice.Patch(path, dto.UpdateConnectorRequest{AutoRouteScope: ptr(dto.AutoRouteScopeAll), AutoRouteRoomID: &room.ID}).RequireStatus(http.StatusOK)
	other := createRoom(e.alice, "unrelated")
	live, _ := e.alice.DialLive("")

	e.alice.Delete("/api/v1/rooms/" + other.ID).RequireStatus(http.StatusNoContent)
	got := testkit.Decode[dto.Connector](e.alice.Get(path).RequireStatus(http.StatusOK))
	require.Equal(t, dto.AutoRouteScopeAll, got.AutoRouteScope, "another room's deletion changes nothing")
	require.Empty(t, got.LastError)

	e.alice.Delete("/api/v1/rooms/" + room.ID).RequireStatus(http.StatusNoContent)
	got = testkit.Decode[dto.Connector](e.alice.Get(path).RequireStatus(http.StatusOK))
	require.Equal(t, dto.AutoRouteScopeOff, got.AutoRouteScope)
	require.Empty(t, got.AutoRouteRoomID)
	require.Equal(t, "auto-route room unavailable", got.LastError)
	var status dto.ConnectorStatusData
	for status.ConnectorID != e.aliceConn.ID || status.LastError == "" {
		live.Expect(dto.LiveEventTypeConnectorStatus, &status, connectortest.Timeout)
	}
	require.Equal(t, "auto-route room unavailable", status.LastError)
	require.Empty(t, testkit.Decode[dto.Connector](e.bob.Get(connectorPath(e.bobConn)).RequireStatus(http.StatusOK)).LastError, "bob's connector is not touched")

	fresh := createRoom(e.alice, "new catch-all")
	got = testkit.Decode[dto.Connector](e.alice.Patch(path, dto.UpdateConnectorRequest{AutoRouteScope: ptr(dto.AutoRouteScopeDMs), AutoRouteRoomID: &fresh.ID}).RequireStatus(http.StatusOK))
	require.Empty(t, got.LastError, "saving the settings clears the detail (ROUTE-09)")
}
