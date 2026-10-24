package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// retiredRouteSetup prepares what the retired routes used to work on: the owner's running connection
// with a chat, shared for use with the viewer, and a second AI of the owner to route the chat to.
func retiredRouteSetup(t *testing.T, m *matrix) matrixResource {
	owner := m.as(t, roleOwner)
	c := createFake(owner, "wa", true)
	shareConnector(owner, c.ID, m.clients[roleViewer].User.ID)
	chat := insertChat(t, m.env, c.ID, "120363000000000001@g.us", "Support group")
	agent := registerAgent(t, m.env, owner, "helper")
	return matrixResource{"connector": c.ID, "chat": chat, "agent": agent}
}

// retiredRoutes are the routes of "route chat to agent" and of the app notifications (CID-22, AD-015):
// chats are never members of a conversation, and no copy of an AI's DM goes to a user's app.
func retiredRoutes() []matrixRoute {
	notFound := statuses(http.StatusNotFound, http.StatusNotFound, http.StatusNotFound, http.StatusNotFound, http.StatusNotFound)
	return []matrixRoute{
		{"route a chat to an agent", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Post("/api/v1/chats/"+res["chat"]+"/route", map[string]any{"agent_id": res["agent"]})
		}, notFound},
		{"list the chats of every connector", func(_ *testing.T, _ *matrix, c *testkit.Client, _ matrixResource) *testkit.Response {
			return c.Get("/api/v1/chats")
		}, notFound},
		{"list the notify options", func(_ *testing.T, _ *matrix, c *testkit.Client, _ matrixResource) *testkit.Response {
			return c.Get("/api/v1/me/notify-options")
		}, notFound},
		{"choose the notify chat", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Put("/api/v1/me/notify", map[string]any{"chat_id": res["chat"]})
		}, notFound},
		{"clear the notify chat", func(_ *testing.T, _ *matrix, c *testkit.Client, _ matrixResource) *testkit.Response {
			return c.Put("/api/v1/me/notify", map[string]any{"chat_id": nil})
		}, notFound},
	}
}

// CID-22 (T34): the retired routes answer 404 not_found to the owner, the use grantee, a poster of
// nothing, a stranger and an admin alike, with a chat and an agent that exist and a request that used
// to be valid: there is nothing to authorize or to reveal.
func TestRetiredChatRoutingAndNotifyRoutesAnswerNotFound(t *testing.T) {
	fake := connectortest.New()
	m := newMatrix(t, testkit.WithConnectorKinds(fake.Kind()))
	m.run(t, retiredRouteSetup, retiredRoutes())
}

// CID-22 (T34): whoever calls them, anonymous callers and the AIs included, the retired routes do
// nothing: anonymous callers get the same 404 (the route does not exist, so it has no login to ask
// for), an AI token is refused by the rule that AIs reach agent endpoints only (TOK-03), the operator of
// the connection, another AI of its owner and another user's AI alike. Every other method on the same
// paths is as unknown.
func TestRetiredRoutesAreUnreachableForAnonymousCallersAndAIs(t *testing.T) {
	fake := connectortest.New()
	m := newMatrix(t, testkit.WithConnectorKinds(fake.Kind()))
	res := retiredRouteSetup(t, m)
	owner := m.as(t, roleOwner)
	operator := owner.Token(dto.TokenKindAgent, "laptop").AsAgent("Atlas")
	sibling := owner.Token(dto.TokenKindAgent, "desk").AsAgent("helper")
	registerAgent(t, m.env, m.clients[roleStranger], "helper")
	strangers := m.clients[roleStranger].Token(dto.TokenKindAgent, "laptop").AsAgent("helper")

	for _, rt := range retiredRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			anon := m.env.Anon()
			anon.T = t
			rt.call(t, m, anon, res).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
			for name, ai := range map[string]*testkit.Client{"operator": operator, "another AI of the owner": sibling, "another user's AI": strangers} {
				ai.T = t
				e := rt.call(t, m, ai, res).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
				require.Equal(t, "agent tokens can only call agent endpoints", e.Message, name)
			}
		})
	}

	for _, other := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/chats/" + res["chat"] + "/route"},
		{http.MethodPut, "/api/v1/chats/" + res["chat"] + "/route"},
		{http.MethodDelete, "/api/v1/chats/" + res["chat"] + "/route"},
		{http.MethodPost, "/api/v1/chats"},
		{http.MethodPost, "/api/v1/me/notify-options"},
		{http.MethodGet, "/api/v1/me/notify"},
		{http.MethodDelete, "/api/v1/me/notify"},
	} {
		owner.Do(other.method, other.path, nil).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	}
}

// CID-22 (T34): the retired requests change nothing. Routing a chat to an agent makes no room, not even
// one that is deleted again, and a chat chosen for notifications is not stored: the owner's account
// reads as it did, and nothing is copied to the chat when an AI writes to the owner.
func TestRetiredRoutesChangeNothing(t *testing.T) {
	fake := connectortest.New()
	m := newMatrix(t, testkit.WithConnectorKinds(fake.Kind()), testkit.WithOutbox())
	res := retiredRouteSetup(t, m)
	owner := m.as(t, roleOwner)
	before := owner.Get("/api/v1/me").RequireStatus(http.StatusOK).Body

	for _, rt := range retiredRoutes() {
		rt.call(t, m, owner, res).RequireStatus(http.StatusNotFound)
	}

	require.Empty(t, testkit.Decode[dto.List[dto.Room]](owner.Get("/api/v1/rooms").RequireStatus(http.StatusOK)).Items, "no room was made")
	after := owner.Get("/api/v1/me").RequireStatus(http.StatusOK)
	require.JSONEq(t, string(before), string(after.Body), "the account is unchanged")
	require.NotContains(t, string(after.Body), "notify_chat_id")
	require.Zero(t, countRows(t, m.env, "SELECT COUNT(*) FROM users WHERE notify_chat_id IS NOT NULL"), "no chat was chosen")
	require.Zero(t, countRows(t, m.env, "SELECT COUNT(*) FROM rooms"))

	// A 404 body names nothing of the chat, the connection or the agent that were asked about.
	body := string(owner.Post("/api/v1/chats/"+res["chat"]+"/route", map[string]any{"agent_id": res["agent"]}).RequireStatus(http.StatusNotFound).Body)
	for _, named := range []string{"Support group", res["connector"], res["chat"], res["agent"]} {
		require.NotContains(t, body, named)
	}
}

// ORG-02, AD-022 (T57): machines are internal. The route that labelled a machine and the machine tree that
// `partyline agents` read are gone: both answer 404 not_found to the callers that used them, with a machine and
// AIs that exist and a request that used to be valid, and nothing is stored. An agent token is still refused
// on the user route by the rule that AIs reach agent endpoints only (TOK-03); anonymous callers get the 404 of a
// route that does not exist.
func TestMachineRoutesAreRetired(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	aiAt(t, env, alice, "Atlas", "atlas", "devbox", "/home/alice/atlas", "/home/alice", "claude")
	agent := alice.Token(dto.TokenKindAgent, "laptop")
	label := map[string]any{"label": "Studio"}

	alice.Put("/api/v1/machines/devbox", label).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	env.Anon().Put("/api/v1/machines/devbox", label).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	agent.Put("/api/v1/machines/devbox", label).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	agent.Get("/api/v1/agent/contacts/tree").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	alice.Get("/api/v1/agent/contacts/tree").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	env.Anon().Get("/api/v1/agent/contacts/tree").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	for _, other := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/machines/devbox"},
		{http.MethodDelete, "/api/v1/machines/devbox"},
		{http.MethodGet, "/api/v1/machines"},
	} {
		alice.Do(other.method, other.path, nil).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	}

	m, err := env.App.Store.R().GetMachine(context.Background(), db.GetMachineParams{OwnerID: alice.User.ID, Hostname: "devbox"})
	require.NoError(t, err)
	require.Empty(t, m.Label, "no label was stored")
	require.Equal(t, "/home/alice", m.Home, "the machine stays, for the home folder of the folder labels")
}
