package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/testkit"
)

// Auth matrix of the chat routes: viewer is the connector's use grantee.
func TestChatRoutesAuthMatrix(t *testing.T) {
	fake := connectortest.New()
	m := newMatrix(t, testkit.WithConnectorKinds(fake.Kind()))
	setup := func(t *testing.T, m *matrix) matrixResource {
		owner := m.as(t, roleOwner)
		c := createFake(owner, "wa", true)
		shareConnector(owner, c.ID, m.clients[roleViewer].User.ID)
		chat := insertChat(t, m.env, c.ID, "120363000000000001@g.us", "Support group")
		return matrixResource{"connector": c.ID, "chat": chat}
	}
	routes := []matrixRoute{
		{"list connector chats", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Get("/api/v1/connectors/" + res["connector"] + "/chats")
		}, statuses(200, 200, 404, 404, 404)},
		{"refresh chats", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Post("/api/v1/connectors/"+res["connector"]+"/chats/refresh", nil)
		}, statuses(204, 403, 404, 404, 404)},
		{"route chat", func(t *testing.T, m *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			agent := registerAgent(t, m.env, c, "helper")
			return c.Post("/api/v1/chats/"+res["chat"]+"/route", dto.RouteChatRequest{AgentID: agent})
		}, statuses(201, 201, 404, 404, 404)},
	}
	m.run(t, setup, routes)

	// GET /chats: owner and grantee see the chat, nobody else does.
	res := setup(t, m)
	for _, role := range matrixRoles {
		list := testkit.Decode[dto.List[dto.Chat]](m.as(t, role).Get("/api/v1/chats").RequireStatus(http.StatusOK))
		var ids []string
		for _, c := range list.Items {
			if c.ID == res["chat"] {
				ids = append(ids, c.ID)
				require.Equal(t, res["connector"], c.ConnectorID)
				require.Equal(t, connectortest.KindName, c.ConnectorKind)
				require.Equal(t, "wa", c.ConnectorName)
				require.Equal(t, "Support group", c.Title)
			}
		}
		if role == roleOwner || role == roleViewer {
			require.Len(t, ids, 1, string(role))
		} else {
			require.Empty(t, ids, string(role))
		}
	}
}

// CON-09: refresh records the chats the platform lists; it needs a running connector of
// a kind that can list chats.
func TestRefreshChats(t *testing.T) {
	fake := connectortest.New()
	env := testkit.New(t, testkit.WithConnectorKinds(fake.Kind(), hookKind()))
	alice := env.NewUser("alice")
	c := createFake(alice, "wa", true)
	inst := fake.Instance(t, c.ID)
	inst.WaitRun(t)
	inst.SetChats(
		connectors.ChatInfo{ID: "120363000000000001@g.us", Title: "Support group", Kind: "group"},
		connectors.ChatInfo{ID: "120363000000000002@g.us", Title: "Billing", Kind: "group"},
	)
	alice.Post("/api/v1/connectors/"+c.ID+"/chats/refresh", nil).RequireStatus(http.StatusNoContent)
	chats := testkit.Decode[dto.List[dto.Chat]](alice.Get("/api/v1/connectors/" + c.ID + "/chats").RequireStatus(http.StatusOK)).Items
	require.Len(t, chats, 2)
	require.Equal(t, "Billing", chats[0].Title)
	require.Equal(t, "120363000000000002@g.us", chats[0].ExternalID)
	require.Equal(t, dto.ChatKindGroup, chats[0].Kind)
	require.Nil(t, chats[0].LastSeenAt, "listed, not seen")
	require.Equal(t, "Support group", chats[1].Title)

	off := false
	alice.Patch("/api/v1/connectors/"+c.ID, dto.UpdateConnectorRequest{Enabled: &off}).RequireStatus(http.StatusOK)
	alice.Post("/api/v1/connectors/"+c.ID+"/chats/refresh", nil).RequireError(http.StatusConflict, dto.ErrorCodeConflict)

	hook := testkit.Decode[dto.Connector](alice.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: "hook", Name: "ci", Config: map[string]any{"secret": "hook-secret-not-real"}, Enabled: disabled(),
	}).RequireStatus(http.StatusCreated))
	alice.Post("/api/v1/connectors/"+hook.ID+"/chats/refresh", nil).RequireError(http.StatusConflict, dto.ErrorCodeConflict)
}

// UI-13: routing a chat creates a private room of the caller with the chat and the agent.
func TestRouteChatToAgent(t *testing.T) {
	fake := connectortest.New()
	env := testkit.New(t, testkit.WithConnectorKinds(fake.Kind()))
	alice := env.NewUser("alice")
	bob := env.NewUser("bob")
	c := createFake(alice, "wa", false)
	chat := insertChat(t, env, c.ID, "120363000000000001@g.us", "Support group")
	agent := registerAgent(t, env, alice, "helper")

	alice.Post("/api/v1/chats/"+chat+"/route", dto.RouteChatRequest{}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	bobsAgent := registerAgent(t, env, bob, "other")
	alice.Post("/api/v1/chats/"+chat+"/route", dto.RouteChatRequest{AgentID: bobsAgent}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	require.Empty(t, testkit.Decode[dto.List[dto.Room]](alice.Get("/api/v1/rooms").RequireStatus(http.StatusOK)).Items, "no room is left behind")

	room := testkit.Decode[dto.Room](alice.Post("/api/v1/chats/"+chat+"/route", dto.RouteChatRequest{AgentID: agent}).RequireStatus(http.StatusCreated))
	require.Equal(t, "Support group", room.Name)
	require.Equal(t, dto.VisibilityPrivate, room.Visibility)
	require.Equal(t, dto.LevelManage, room.MyLevel)
	require.Equal(t, alice.User.ID, room.Owner.ID)
	members := testkit.Decode[dto.List[dto.RoomMember]](alice.Get("/api/v1/rooms/" + room.ID + "/members").RequireStatus(http.StatusOK)).Items
	require.Len(t, members, 2)
	require.Equal(t, dto.MemberKindChat, members[0].Kind)
	require.Equal(t, chat, members[0].MemberID)
	require.Equal(t, dto.MemberKindAgent, members[1].Kind)
	require.Equal(t, agent, members[1].MemberID)
}
