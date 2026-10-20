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
		return testkit.Decode[dto.Connector](c.Post("/api/v1/connectors", dto.CreateConnectorRequest{
			Kind: connectortest.KindName, Name: "wa", Config: map[string]any{"token": "fake-token-not-real"},
		}).RequireStatus(http.StatusCreated))
	}
	e.aliceConn, e.bobConn = create(e.alice), create(e.bob)
	return e
}

func connectorPath(c dto.Connector) string { return "/api/v1/connectors/" + c.ID }

// ROUTE-04: a connector manager sets the catch-all scope and room; the room must exist (422)
// and the caller needs manage on it (403); scopes outside off|dms|groups|all get 422. A
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

	unknown := "0190a000-0000-7000-8000-000000000000"
	fields := e.alice.Patch(path, dto.UpdateConnectorRequest{AutoRouteRoomID: &unknown}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid).Fields
	require.Contains(t, fields, "auto_route_room_id")
	unchanged()
	fields = e.alice.Patch(path, dto.UpdateConnectorRequest{AutoRouteScope: ptr(dto.AutoRouteScope("everyone"))}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid).Fields
	require.Contains(t, fields, "auto_route_scope")
	unchanged()
	// A scope other than off needs a room.
	fields = e.alice.Patch(path, dto.UpdateConnectorRequest{AutoRouteRoomID: ptr("")}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid).Fields
	require.Contains(t, fields, "auto_route_room_id")
	unchanged()

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
