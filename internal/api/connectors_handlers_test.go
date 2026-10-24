package api_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// Synthetic secrets; tests assert they never leave the server.
const (
	botToken  = "000000:TEST-bot-token-not-real"
	botToken2 = "111111:TEST-other-token-not-real"
)

// botKind is a synthetic kind shaped like a bot-token platform.
func botKind() connectors.Kind {
	return connectors.Kind{
		Name: "bot", Label: "Bot", CanListChats: true,
		Fields: []connectors.Field{
			{Name: "bot_token", Label: "Bot token", Type: dto.FieldTypeSecret, Required: true, Help: "From the platform"},
			{Name: "api_base", Label: "API base", Type: dto.FieldTypeURL, Default: "https://api.example.test"},
			{Name: "rooms", Label: "Rooms", Type: dto.FieldTypeStringList},
			{Name: "debug", Label: "Debug", Type: dto.FieldTypeBool},
		},
	}
}

// hookKind is a synthetic kind that receives messages over HTTP.
func hookKind() connectors.Kind {
	return connectors.Kind{
		Name: "hook", Label: "Hook", InboundHook: true,
		Fields: []connectors.Field{{Name: "secret", Label: "Secret", Type: dto.FieldTypeSecret, Required: true}},
	}
}

func connectorEnv(t *testing.T, kinds ...connectors.Kind) *testkit.Env {
	t.Helper()
	if len(kinds) == 0 {
		kinds = []connectors.Kind{botKind(), hookKind()}
	}
	return testkit.New(t, testkit.WithConnectorKinds(kinds...))
}

func disabled() *bool { f := false; return &f }

// createBot creates a disabled bot connector (nothing runs) and returns it.
func createBot(c *testkit.Client, name string) dto.Connector {
	c.T.Helper()
	return testkit.Decode[dto.Connector](c.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: "bot", Name: name, Config: map[string]any{"bot_token": botToken}, Enabled: disabled(),
	}).RequireStatus(http.StatusCreated))
}

// createBotIn creates a disabled bot connector in the organization (org may be an id or a slug) as c.
func createBotIn(c *testkit.Client, org, name string) dto.Connector {
	c.T.Helper()
	return testkit.Decode[dto.Connector](c.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		OrgID: org, Kind: "bot", Name: name, Config: map[string]any{"bot_token": botToken}, Enabled: disabled(),
	}).RequireStatus(http.StatusCreated))
}

// shareConnector gives the users sight of the connection, as an admin of its organization would by inviting
// them to it (AD-016): the connection is shared with an organization, not with users. The profile and people
// code still reads the grant the sharing routes write until T27 moves it to organizations, so the helper
// writes it too (c must manage the connection).
func shareConnector(c *testkit.Client, id string, userIDs ...string) {
	c.T.Helper()
	c.ShareConnection(id, userIDs...)
	users := make([]dto.SharingUser, len(userIDs))
	for i, uid := range userIDs {
		users[i] = dto.SharingUser{UserID: uid, Level: dto.LevelUse}
	}
	c.Put("/api/v1/connectors/"+id+"/sharing", dto.Sharing{Mode: dto.VisibilityShared, Users: users}).RequireStatus(http.StatusOK)
}

// requireNoSecret fails when a response body carries a synthetic secret (CON-03).
func requireNoSecret(t *testing.T, r *testkit.Response) {
	t.Helper()
	require.NotContains(t, string(r.Body), botToken)
	require.NotContains(t, string(r.Body), botToken2)
}

// CON-01: kinds are served with their field schema.
func TestConnectorKindsServeFieldSchema(t *testing.T) {
	env := connectorEnv(t)
	env.Anon().Get("/api/v1/connector-kinds").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)

	kinds := testkit.Decode[dto.List[dto.ConnectorKind]](env.NewUser("alice").Get("/api/v1/connector-kinds").RequireStatus(http.StatusOK))
	require.Len(t, kinds.Items, 2)
	bot := kinds.Items[0]
	require.Equal(t, "bot", bot.Name)
	require.Equal(t, "Bot", bot.Label)
	require.True(t, bot.CanListChats)
	require.Equal(t, []dto.ConnectorField{
		{Name: "bot_token", Label: "Bot token", Type: dto.FieldTypeSecret, Required: true, Secret: true, Help: "From the platform"},
		{Name: "api_base", Label: "API base", Type: dto.FieldTypeURL, Default: "https://api.example.test"},
		{Name: "rooms", Label: "Rooms", Type: dto.FieldTypeStringList},
		{Name: "debug", Label: "Debug", Type: dto.FieldTypeBool},
	}, bot.Fields)
	require.Equal(t, "hook", kinds.Items[1].Name)
	require.False(t, kinds.Items[1].CanListChats)
}

// CON-02: 422 names each rejected field; config fields are named config.<field>.
func TestCreateConnectorRejectsInvalidConfig(t *testing.T) {
	env := connectorEnv(t)
	alice := env.NewUser("alice")
	create := func(req dto.CreateConnectorRequest) map[string]string {
		return alice.Post("/api/v1/connectors", req).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid).Fields
	}

	require.Equal(t, map[string]string{"config.bot_token": "required"},
		create(dto.CreateConnectorRequest{Kind: "bot", Name: "ops", Config: map[string]any{}}))
	require.Equal(t, map[string]string{"config.api_base": "invalid url"},
		create(dto.CreateConnectorRequest{Kind: "bot", Name: "ops", Config: map[string]any{"bot_token": botToken, "api_base": "not-a-url"}}))
	require.Equal(t, map[string]string{"config.debug": "must be true or false", "config.rooms": "must be a list of strings"},
		create(dto.CreateConnectorRequest{Kind: "bot", Name: "ops", Config: map[string]any{"bot_token": botToken, "debug": "yes", "rooms": "a"}}))
	require.Equal(t, map[string]string{"kind": "unknown connector kind", "name": "must have 1 to 100 characters"},
		create(dto.CreateConnectorRequest{Kind: "fax", Name: " "}))

	c := createBot(alice, "ops")
	resp := alice.Patch("/api/v1/connectors/"+c.ID, dto.UpdateConnectorRequest{Config: map[string]any{"bot_token": nil}})
	require.Equal(t, map[string]string{"config.bot_token": "required"}, resp.RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid).Fields)
	empty := ""
	resp = alice.Patch("/api/v1/connectors/"+c.ID, dto.UpdateConnectorRequest{Name: &empty})
	require.Equal(t, map[string]string{"name": "must have 1 to 100 characters"}, resp.RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid).Fields)
}

// CON-03: secrets are never returned; set secrets read as the mask; a PATCH with the mask
// keeps the stored secret.
func TestConnectorSecretsAreMasked(t *testing.T) {
	env := connectorEnv(t)
	alice := env.NewUser("alice")
	bob := env.NewUser("bob")

	resp := alice.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: "bot", Name: "ops", Config: map[string]any{"bot_token": botToken, "rooms": []string{"general"}}, Enabled: disabled(),
	}).RequireStatus(http.StatusCreated)
	requireNoSecret(t, resp)
	c := testkit.Decode[dto.Connector](resp)
	require.Equal(t, map[string]any{
		"bot_token": dto.SecretMask, "api_base": "https://api.example.test", "rooms": []any{"general"},
	}, c.Config)
	require.Equal(t, "bot", c.Kind)
	require.Equal(t, "ops", c.Name)
	require.Equal(t, dto.LevelManage, c.MyLevel)
	require.Equal(t, alice.User.ID, c.Owner.ID)
	require.False(t, c.Enabled)
	require.Equal(t, dto.ConnectorStatusStopped, c.Status)
	shareConnector(alice, c.ID, bob.User.ID)

	for _, r := range []*testkit.Response{
		alice.Get("/api/v1/connectors/" + c.ID).RequireStatus(http.StatusOK),
		alice.Get("/api/v1/connectors").RequireStatus(http.StatusOK),
		bob.Get("/api/v1/connectors/" + c.ID).RequireStatus(http.StatusOK),
		bob.Get("/api/v1/connectors").RequireStatus(http.StatusOK),
	} {
		requireNoSecret(t, r)
		require.Contains(t, string(r.Body), `"bot_token":"`+dto.SecretMask+`"`)
	}

	name := "ops-2"
	resp = alice.Patch("/api/v1/connectors/"+c.ID, dto.UpdateConnectorRequest{
		Name: &name, Config: map[string]any{"bot_token": dto.SecretMask, "api_base": "https://other.example.test"},
	}).RequireStatus(http.StatusOK)
	requireNoSecret(t, resp)
	updated := testkit.Decode[dto.Connector](resp)
	require.Equal(t, "ops-2", updated.Name)
	require.Equal(t, "https://other.example.test", updated.Config["api_base"])
	require.Equal(t, dto.SecretMask, updated.Config["bot_token"])
	require.Equal(t, botToken, storedConfig(t, env, c.ID)["bot_token"], "the mask kept the stored secret")

	requireNoSecret(t, alice.Patch("/api/v1/connectors/"+c.ID, dto.UpdateConnectorRequest{
		Config: map[string]any{"bot_token": botToken2},
	}).RequireStatus(http.StatusOK))
	require.Equal(t, botToken2, storedConfig(t, env, c.ID)["bot_token"])
}

func storedConfig(t *testing.T, env *testkit.Env, id string) connectors.Config {
	t.Helper()
	row, err := env.App.Store.R().GetConnector(context.Background(), id)
	require.NoError(t, err)
	cfg, err := connectors.Decode(env.App.Box, row.ConfigJson, row.SecretsEnc)
	require.NoError(t, err)
	return cfg
}

// SEC-04: the raw row never holds the secret in clear; it lives sealed in secrets_enc.
func TestConnectorSecretIsEncryptedAtRest(t *testing.T) {
	env := connectorEnv(t)
	c := createBot(env.NewUser("alice"), "ops")
	row, err := env.App.Store.R().GetConnector(context.Background(), c.ID)
	require.NoError(t, err)
	require.NotContains(t, row.ConfigJson, botToken)
	require.NotContains(t, row.ConfigJson, "bot_token")
	require.NotEmpty(t, row.SecretsEnc)
	require.False(t, bytes.Contains(row.SecretsEnc, []byte(botToken)))
	require.False(t, bytes.Contains(row.SecretsEnc, []byte("TEST-bot-token")))
	require.Equal(t, botToken, storedConfig(t, env, c.ID)["bot_token"], "the instance key opens it")
}

// Webhook-style kinds report their inbound URL; other kinds leave it empty.
func TestConnectorInboundURL(t *testing.T) {
	env := connectorEnv(t)
	alice := env.NewUser("alice")
	hook := testkit.Decode[dto.Connector](alice.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: "hook", Name: "ci", Config: map[string]any{"secret": "hook-secret-not-real"}, Enabled: disabled(),
	}).RequireStatus(http.StatusCreated))
	require.Equal(t, env.URL+"/api/v1/hooks/"+hook.ID, hook.InboundURL)
	require.Empty(t, createBot(alice, "ops").InboundURL)
}

// connectionWant is the expected status of a route on a connection of the organization by role: the admin
// of the organization, the other members of it - a manager, workers, a plain member, an inviter and a guest
// all use it - and everybody else, an outsider and an admin of the instance included.
func connectionWant(admin, member, hidden int) map[string]int {
	return map[string]int{
		"admin": admin, "manager": member, "target": member, "peer": member, "member": member, "inviter": member, "guest": member,
		"outsider": hidden, "instance_admin": hidden,
	}
}

// ORG-03, OMEM-04 (design, "Connections" matrix): a connection is created, changed, enabled, given its
// operator and deleted by the admins of its organization only (403 for its other members, 404 for those
// outside it, an admin of the instance included), and read - the connection, its chats - by every member.
func TestConnectorRoutesAuthMatrix(t *testing.T) {
	env := connectorEnv(t)
	roles := env.OrgRoles("acme")
	conn := func(res map[string]string, suffix string) string {
		return "/api/v1/connectors/" + res["connector"] + suffix
	}
	setup := func(*testing.T) map[string]string {
		return map[string]string{"connector": createBotIn(roles.Admin, roles.Org.ID, "ops").ID}
	}
	name, enabled, operator := "renamed", true, roles.TargetAI
	runRoleRoutes(t, roles, setup, []roleRoute{
		{"create connector", func(_ *testing.T, c *testkit.Client, _ map[string]string) *testkit.Response {
			return c.Post("/api/v1/connectors", dto.CreateConnectorRequest{
				OrgID: roles.Org.ID, Kind: "bot", Name: "new", Config: map[string]any{"bot_token": botToken}, Enabled: disabled(),
			})
		}, connectionWant(201, 403, 403)},
		{"get connector", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Get(conn(res, ""))
		}, connectionWant(200, 200, 404)},
		{"list chats", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Get(conn(res, "/chats"))
		}, connectionWant(200, 200, 404)},
		{"refresh chats", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Post(conn(res, "/chats/refresh"), nil)
		}, connectionWant(409, 403, 404)}, // the admin may ask; the disabled connection is not running (409)
		{"rename connector", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Patch(conn(res, ""), dto.UpdateConnectorRequest{Name: &name})
		}, connectionWant(200, 403, 404)},
		{"enable connector", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Patch(conn(res, ""), dto.UpdateConnectorRequest{Enabled: &enabled})
		}, connectionWant(200, 403, 404)},
		{"choose the operator", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Patch(conn(res, ""), dto.UpdateConnectorRequest{OperatorID: &operator})
		}, connectionWant(200, 403, 404)},
		{"delete connector", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Delete(conn(res, ""))
		}, connectionWant(204, 403, 404)},
		{"get sharing", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Get(conn(res, "/sharing"))
		}, connectionWant(200, 200, 404)},
		{"put sharing", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Put(conn(res, "/sharing"), dto.Sharing{Mode: dto.VisibilityPrivate})
		}, connectionWant(200, 403, 404)},
	})

	// An AI token reaches no connection route (TOK-03), and anonymous callers are 401.
	res := setup(t)
	ai := roles.Admin.Token(dto.TokenKindAgent, "bot")
	ai.Get(conn(res, "")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	ai.Post("/api/v1/connectors", dto.CreateConnectorRequest{OrgID: roles.Org.ID, Kind: "bot", Name: "x"}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	env.Anon().Get(conn(res, "")).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	env.Anon().Post("/api/v1/connectors", dto.CreateConnectorRequest{OrgID: roles.Org.ID, Kind: "bot", Name: "x"}).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
}

// ORG-02, AD-016: GET /connectors lists the connections of the organizations the caller belongs to, whatever their
// permission there, and none of any other: the admins manage them, the other members use them.
func TestConnectorListFollowsMembership(t *testing.T) {
	env := connectorEnv(t)
	acme, globex := env.OrgRoles("acme"), env.OrgRoles("globex")
	inAcme := createBotIn(acme.Admin, acme.Org.ID, "in-acme")
	inGlobex := createBotIn(globex.Admin, globex.Org.ID, "in-globex")
	env.AddMember(globex.Org.ID, acme.Member.User.ID, orgs.PermissionMember, "") // acme's Member works in globex too

	levels := func(c *testkit.Client) map[string]dto.Level {
		t.Helper()
		out := map[string]dto.Level{}
		for _, item := range testkit.Decode[dto.List[dto.Connector]](c.Get("/api/v1/connectors").RequireStatus(http.StatusOK)).Items {
			out[item.Name] = item.MyLevel
		}
		return out
	}
	require.Equal(t, map[string]dto.Level{"in-acme": dto.LevelManage}, levels(acme.Admin))
	require.Equal(t, map[string]dto.Level{"in-acme": dto.LevelUse}, levels(acme.Manager))
	require.Equal(t, map[string]dto.Level{"in-acme": dto.LevelUse}, levels(acme.Guest), "a guest uses the connections of the organization too")
	require.Equal(t, map[string]dto.Level{"in-acme": dto.LevelUse, "in-globex": dto.LevelUse}, levels(acme.Member), "an identity in two organizations sees both")
	require.Equal(t, map[string]dto.Level{"in-globex": dto.LevelManage}, levels(globex.Admin))
	require.Empty(t, levels(acme.Outsider))
	require.Empty(t, levels(acme.InstanceAdmin), "an admin of the instance has no implicit access")
	require.Equal(t, acme.Org.ID, inAcme.OrgID)
	require.Equal(t, globex.Org.ID, inGlobex.OrgID)
}

// ORG-02, ORG-03: a connection belongs to the organization the creator names - by id or by slug, in any case - or,
// when none is named, to the creator's default organization, and the creator must be an admin there. Anything else
// is forbidden: a member who is not an admin, a guest, an organization the creator is not in and one that does not
// exist, all answered alike so that nothing is told about organizations the caller does not belong to.
func TestCreateConnectorNamesItsOrganization(t *testing.T) {
	env := connectorEnv(t)
	roles := env.OrgRoles("acme")
	create := func(c *testkit.Client, org string) *testkit.Response {
		return c.Post("/api/v1/connectors", dto.CreateConnectorRequest{
			OrgID: org, Kind: "bot", Name: "ops", Config: map[string]any{"bot_token": botToken}, Enabled: disabled(),
		})
	}

	byID := testkit.Decode[dto.Connector](create(roles.Admin, roles.Org.ID).RequireStatus(http.StatusCreated))
	require.Equal(t, roles.Org.ID, byID.OrgID)
	require.Equal(t, []any{dto.LevelManage, roles.Admin.User.ID}, []any{byID.MyLevel, byID.Owner.ID})
	require.Equal(t, roles.Org.ID, testkit.Decode[dto.Connector](create(roles.Admin, " ACME ").RequireStatus(http.StatusCreated)).OrgID, "by slug, in any case")
	personal := roles.Admin.PersonalOrg()
	require.Equal(t, personal.ID, testkit.Decode[dto.Connector](create(roles.Admin, "").RequireStatus(http.StatusCreated)).OrgID,
		"no organization named: the creator's default, which is their personal one")

	// The default organization is the creator's to choose; where they are no admin they cannot create.
	require.NoError(t, env.App.Orgs.SetDefault(context.Background(), roles.Member.User.ID, roles.Org.ID))
	create(roles.Member, "").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	create(roles.Member, roles.Org.ID).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	create(roles.Guest, roles.Org.ID).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	denied := create(roles.Outsider, roles.Org.ID).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	missing := create(roles.Outsider, "no-such-organization").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	require.Equal(t, denied, missing, "an organization that does not exist answers like one the caller is not in")
	create(roles.InstanceAdmin, roles.Org.ID).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	// A refusal creates nothing.
	require.Empty(t, testkit.Decode[dto.List[dto.Connector]](roles.Outsider.Get("/api/v1/connectors").RequireStatus(http.StatusOK)).Items)

	// Whoever belongs to no organization has nowhere to put one: 422, and nothing is created.
	nobody := env.NewUser("nobody")
	require.NoError(t, env.App.Orgs.Delete(context.Background(), nobody.User.ID, nobody.PersonalOrg().ID))
	e := create(nobody, "").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeOrganizationRequired)
	require.Contains(t, e.Fields, "org_id")
	require.Empty(t, testkit.Decode[dto.List[dto.Connector]](nobody.Get("/api/v1/connectors").RequireStatus(http.StatusOK)).Items)

	// The invalid-input errors still come with the organization: a member with a bad config is told 403, not 422.
	badConfig := dto.CreateConnectorRequest{OrgID: roles.Org.ID, Kind: "bot", Name: "ops"}
	roles.Member.Post("/api/v1/connectors", badConfig).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	roles.Admin.Post("/api/v1/connectors", badConfig).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
}

// insertChat stores a chat of the connector, as its inbound pipeline would.
func insertChat(t *testing.T, env *testkit.Env, connectorID, externalID, title string) string {
	t.Helper()
	id := store.NewID()
	require.NoError(t, env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
		return q.CreateChat(context.Background(), db.CreateChatParams{ID: id, ConnectorID: connectorID, ExternalID: externalID, Title: title, Kind: "group"})
	}))
	return id
}

// CON-05 (data part): deleting a connector removes its chats from rooms.
func TestDeleteConnectorRemovesItsChatsFromRooms(t *testing.T) {
	env := connectorEnv(t)
	alice := env.NewUser("alice")
	c := createBot(alice, "ops")
	chat := insertChat(t, env, c.ID, "-1001", "Support group")
	room := createRoom(alice, "Support")
	alice.SeedChatMember(room.ID, chat)

	alice.Delete("/api/v1/connectors/" + c.ID).RequireStatus(http.StatusNoContent)
	alice.Get("/api/v1/connectors/"+c.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	members := testkit.Decode[dto.List[dto.RoomMember]](alice.Get("/api/v1/rooms/" + room.ID + "/members").RequireStatus(http.StatusOK))
	require.Empty(t, members.Items)
	_, err := env.App.Store.R().GetChat(context.Background(), chat)
	require.Error(t, err, "the chat is gone")
	levels, err := env.App.Access.Sharing(context.Background(), access.KindConnector, c.ID)
	require.NoError(t, err)
	require.Empty(t, levels.Users)
}

// AUD-01 (connector part): create, update, enable, disable and delete are audited without secrets.
func TestConnectorChangesAreAudited(t *testing.T) {
	env := connectorEnv(t)
	admin := env.SetupAdmin()
	alice := env.NewUser("alice")
	c := createBot(alice, "ops")
	name := "ops-2"
	enabled, off := true, false
	alice.Patch("/api/v1/connectors/"+c.ID, dto.UpdateConnectorRequest{Name: &name, Config: map[string]any{"bot_token": botToken2}}).RequireStatus(http.StatusOK)
	alice.Patch("/api/v1/connectors/"+c.ID, dto.UpdateConnectorRequest{Enabled: &enabled}).RequireStatus(http.StatusOK)
	alice.Patch("/api/v1/connectors/"+c.ID, dto.UpdateConnectorRequest{Enabled: &off}).RequireStatus(http.StatusOK)
	alice.Delete("/api/v1/connectors/" + c.ID).RequireStatus(http.StatusNoContent)

	resp := admin.Get("/api/v1/admin/audit").RequireStatus(http.StatusOK)
	requireNoSecret(t, resp)
	entries := testkit.Decode[dto.Page[dto.AuditEntry]](resp).Items
	for _, action := range []string{"connector.create", "connector.update", "connector.enable", "connector.disable", "connector.delete"} {
		e := findAudit(t, entries, action)
		require.Equal(t, "connector", e.TargetKind, action)
		require.Equal(t, c.ID, e.TargetID, action)
		require.NotNil(t, e.Actor, action)
		require.Equal(t, alice.User.ID, e.Actor.ID, action)
	}
	update := findAudit(t, entries, "connector.update")
	require.Equal(t, []any{"name", "config"}, update.Detail["changed"])
	require.Equal(t, "bot", findAudit(t, entries, "connector.create").Detail["kind"])
	require.Equal(t, alice.PersonalOrg().ID, findAudit(t, entries, "connector.create").Detail["org_id"], "the audit names the organization")
	require.False(t, strings.Contains(string(resp.Body), "bot_token\":\""), "config values are not in the audit detail")
}
