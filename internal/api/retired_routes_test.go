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

// retiredRouteSetup returns the setup of what the retired routes used to work on: acme's running connection with
// a chat, which its AI Atlas (the AI that the role "manager" created) operates, and a second AI of acme to route the
// chat to.
func retiredRouteSetup(t *testing.T, env *testkit.Env, roles *testkit.OrgRoles) func(t *testing.T) map[string]string {
	t.Helper()
	admin := *roles.Admin
	admin.T = t
	c := createFakeIn(&admin, roles.Org.ID, "wa", true, roles.TargetAI)
	chat := insertChat(t, env, c.ID, "120363000000000001@g.us", "Support group")
	helper := env.NewAIIn(roles.Manager, roles.Org.ID, "helper")
	return func(*testing.T) map[string]string {
		return map[string]string{"connector": c.ID, "chat": chat, "agent": helper}
	}
}

// retiredRoutes are the routes of "route chat to agent" and of the app notifications (CID-22, AD-015):
// chats are never members of a conversation, and no copy of an AI's DM goes to a user's app.
func retiredRoutes() []roleRoute {
	return []roleRoute{
		{"route a chat to an agent", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Post("/api/v1/chats/"+res["chat"]+"/route", map[string]any{"agent_id": res["agent"]})
		}, everyRole(http.StatusNotFound)},
		{"list the chats of every connector", func(_ *testing.T, c *testkit.Client, _ map[string]string) *testkit.Response {
			return c.Get("/api/v1/chats")
		}, everyRole(http.StatusNotFound)},
		{"list the notify options", func(_ *testing.T, c *testkit.Client, _ map[string]string) *testkit.Response {
			return c.Get("/api/v1/me/notify-options")
		}, everyRole(http.StatusNotFound)},
		{"choose the notify chat", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Put("/api/v1/me/notify", map[string]any{"chat_id": res["chat"]})
		}, everyRole(http.StatusNotFound)},
		{"clear the notify chat", func(_ *testing.T, c *testkit.Client, _ map[string]string) *testkit.Response {
			return c.Put("/api/v1/me/notify", map[string]any{"chat_id": nil})
		}, everyRole(http.StatusNotFound)},
	}
}

// CID-22 (T34): the retired routes answer 404 not_found to every role of an organization - its admin, the
// manager of its AI, the people who only see the connection, an outsider and an admin of the instance alike -
// with a chat and an agent that exist and a request that used to be valid: there is nothing to authorize or to
// reveal.
func TestRetiredChatRoutingAndNotifyRoutesAnswerNotFound(t *testing.T) {
	fake := connectortest.New()
	env := testkit.New(t, testkit.WithConnectorKinds(fake.Kind()))
	roles := env.OrgRoles("acme")
	runRoleRoutes(t, roles, retiredRouteSetup(t, env, roles), retiredRoutes())
}

// CID-22 (T34): whoever calls them, anonymous callers and the AIs included, the retired routes do
// nothing: anonymous callers get the same 404 (the route does not exist, so it has no login to ask
// for), an AI token is refused by the rule that AIs reach agent endpoints only (TOK-03), the operator of
// the connection, another AI of its organization and another organization's AI alike. Every other method
// on the same paths is as unknown.
func TestRetiredRoutesAreUnreachableForAnonymousCallersAndAIs(t *testing.T) {
	fake := connectortest.New()
	env := testkit.New(t, testkit.WithConnectorKinds(fake.Kind()))
	roles := env.OrgRoles("acme")
	res := retiredRouteSetup(t, env, roles)(t)
	operator := roles.Manager.Token(dto.TokenKindAgent, "laptop").AsAgent("acme-ai")
	env.NewAIIn(roles.Manager, roles.Org.ID, "scout")
	sibling := roles.Manager.Token(dto.TokenKindAgent, "desk").AsAgent("scout")
	registerAgent(t, env, roles.Outsider, "helper")
	strangers := roles.Outsider.Token(dto.TokenKindAgent, "laptop").AsAgent("helper")

	for _, rt := range retiredRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			anon := env.Anon()
			anon.T = t
			rt.call(t, anon, res).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
			for name, ai := range map[string]*testkit.Client{"operator": operator, "another AI of the organization": sibling, "another organization's AI": strangers} {
				ai.T = t
				e := rt.call(t, ai, res).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
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
		roles.Admin.Do(other.method, other.path, nil).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	}
}

// CID-22 (T34): the retired requests change nothing. Routing a chat to an agent makes no room, not even
// one that is deleted again, and a chat chosen for notifications is not stored: the admin's account
// reads as it did, and nothing is copied to the chat when an AI writes to a user.
func TestRetiredRoutesChangeNothing(t *testing.T) {
	fake := connectortest.New()
	env := testkit.New(t, testkit.WithConnectorKinds(fake.Kind()), testkit.WithOutbox())
	roles := env.OrgRoles("acme")
	res := retiredRouteSetup(t, env, roles)(t)
	admin := roles.Admin
	before := admin.Get("/api/v1/me").RequireStatus(http.StatusOK).Body

	for _, rt := range retiredRoutes() {
		rt.call(t, admin, res).RequireStatus(http.StatusNotFound)
	}

	require.Empty(t, testkit.Decode[dto.List[dto.Room]](admin.Get("/api/v1/rooms").RequireStatus(http.StatusOK)).Items, "no room was made")
	after := admin.Get("/api/v1/me").RequireStatus(http.StatusOK)
	require.JSONEq(t, string(before), string(after.Body), "the account is unchanged")
	require.NotContains(t, string(after.Body), "notify_chat_id")
	require.Zero(t, countRows(t, env, "SELECT COUNT(*) FROM users WHERE notify_chat_id IS NOT NULL"), "no chat was chosen")
	require.Zero(t, countRows(t, env, "SELECT COUNT(*) FROM rooms"))

	// A 404 body names nothing of the chat, the connection or the agent that were asked about.
	body := string(admin.Post("/api/v1/chats/"+res["chat"]+"/route", map[string]any{"agent_id": res["agent"]}).RequireStatus(http.StatusNotFound).Body)
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

// ORG-02, AD-016 (T51): organization membership is the only way to share, so the routes that shared a room, an
// AI and a connection one by one are gone. They answer 404 not_found to every role of an organization - the
// creator of the group, the admin, the manager above the AI, the people who only see the resources, an
// outsider and the admin of the instance - with resources that exist and with the requests that used to be
// valid (a DM answered 422 dm_no_sharing; it is as unknown as the rest). An AI token is refused by the rule that
// AIs reach agent endpoints only (TOK-03), and anonymous callers get the 404 of a route that does not exist.
// Every other method on the same paths is as unknown. A room no longer carries a visibility preset.
func TestShareRoutesAreRetired(t *testing.T) {
	env := connectorEnv(t)
	roles := env.OrgRoles("acme")
	group := groupIn(t, env, roles.Member, roles.Org.ID, "ops") // the role "member" created it and manages it
	conn := createBotIn(roles.Admin, roles.Org.ID, "ops")
	dm := dmWith(roles.Target, roles.TargetAI, http.StatusCreated)
	peer := roles.Peer.User.ID
	// The AIs: one of the organization the group, the AI and the connection belong to, and one elsewhere.
	ais := map[string]*testkit.Client{
		"an AI of the organization": roles.Manager.Token(dto.TokenKindAgent, "tools").AsAgent("acme-ai"),
		"an AI elsewhere":           roles.Outsider.Token(dto.TokenKindAgent, "tools").AsAgent("elsewhere"),
	}
	auditBefore := auditActions(t, roles.InstanceAdmin)

	// What each route took before: a preset of private, shared (with users at a level) or public.
	type sharing struct {
		name, path string
		bodies     []map[string]any
	}
	preset := func(level string) []map[string]any {
		return []map[string]any{
			{"mode": "private"},
			{"mode": "shared", "users": []map[string]any{{"user_id": peer, "level": level}}},
			{"mode": "public", "public_level": level},
		}
	}
	routes := []sharing{
		{"room", "/api/v1/rooms/" + group.ID + "/sharing", preset("post")},
		{"direct message", "/api/v1/rooms/" + dm.ID + "/sharing", preset("post")},
		{"agent", "/api/v1/agents/" + roles.TargetAI + "/sharing", preset("use")},
		{"connector", "/api/v1/connectors/" + conn.ID + "/sharing", preset("use")},
	}

	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			for _, role := range roles.All() {
				c := *role.Client
				c.T = t
				c.Get(rt.path).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
				for _, body := range rt.bodies {
					c.Put(rt.path, body).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
				}
				for _, other := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete} {
					c.Do(other, rt.path, rt.bodies[0]).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
				}
			}

			anon := env.Anon()
			anon.T = t
			anon.Get(rt.path).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
			anon.Put(rt.path, rt.bodies[2]).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

			for who, ai := range ais {
				ai.T = t
				e := ai.Get(rt.path).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
				require.Equal(t, "agent tokens can only call agent endpoints", e.Message, who)
				ai.Put(rt.path, rt.bodies[2]).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
			}
		})
	}

	// The requests changed nothing: a public preset opened nothing, and nothing was audited.
	roles.Peer.Get("/api/v1/rooms/"+group.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	roles.Outsider.Get("/api/v1/rooms/"+group.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	roles.Outsider.Get("/api/v1/connectors/"+conn.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	roles.Outsider.Get("/api/v1/agents/"+roles.TargetAI).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	for _, body := range [][]byte{
		roles.Member.Get("/api/v1/rooms/" + group.ID).RequireStatus(http.StatusOK).Body,
		roles.Target.Get("/api/v1/rooms/" + dm.ID).RequireStatus(http.StatusOK).Body,
		roles.Member.Get("/api/v1/rooms").RequireStatus(http.StatusOK).Body,
	} {
		require.NotContains(t, string(body), `"visibility"`, "a room carries no sharing preset")
	}
	require.Equal(t, auditBefore, auditActions(t, roles.InstanceAdmin), "the retired requests wrote no audit entry")
}
