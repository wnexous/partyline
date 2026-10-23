package api_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/testkit"
)

// Auth matrix of the chat routes that are left (the list of a connection's chats and its refresh):
// viewer is the connector's use grantee. Routing a chat to an agent and the list of every usable chat
// are retired (retired_routes_test.go).
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
	}
	m.run(t, setup, routes)
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

// CON-09: a platform failure during refresh answers 502 unavailable with a fixed message;
// the platform's own text (which may be anything) is not echoed.
func TestRefreshChatsPlatformFailure(t *testing.T) {
	fake := connectortest.New()
	env := testkit.New(t, testkit.WithConnectorKinds(fake.Kind()))
	alice := env.NewUser("alice")
	c := createFake(alice, "wa", true)
	inst := fake.Instance(t, c.ID)
	inst.WaitRun(t)
	inst.SetListError(errors.New("GET /group/list: <html>internal page 10.0.0.5</html>"))

	resp := alice.Post("/api/v1/connectors/"+c.ID+"/chats/refresh", nil)
	apiErr := resp.RequireError(http.StatusBadGateway, dto.ErrorCodeUnavailable)
	require.Equal(t, "the platform did not answer the request", apiErr.Message)
	require.NotContains(t, resp.String(), "internal page")
	require.NotContains(t, resp.String(), "10.0.0.5")
}
