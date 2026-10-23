package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/testkit"
)

// retiredFields are the JSON names of the chat fields the API no longer sends or reads (CID-22,
// T35): the room's chat bridging, broadcast and catch-all flag, a member's outbound and signature
// switches, a message's delivery to chats, the account's chat to notify and a connection's catch-all
// routing.
var retiredFields = []string{
	"bridge_chats", "broadcast", "catch_all",
	"outbound", "sign_outbound",
	"outbox",
	"notify_chat_id",
	"auto_route_scope", "auto_route_room_id",
}

// requireNoRetiredFields fails when a JSON object anywhere in body has a retired field as a key.
func requireNoRetiredFields(t *testing.T, what string, body []byte) {
	t.Helper()
	var v any
	require.NoError(t, json.Unmarshal(body, &v), what)
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for key, child := range x {
				require.False(t, slices.Contains(retiredFields, key), "%s carries the retired field %q at %s", what, key, path)
				walk(path+"."+key, child)
			}
		case []any:
			for i, child := range x {
				walk(fmt.Sprintf("%s[%d]", path, i), child)
			}
		}
	}
	walk("$", v)
}

// upgradedAPI is alice's installation as an upgrade from v0.4 leaves it: a room with her AI Atlas and
// bob, a connection Atlas operates with a chat, and what the retired chat features left in the columns
// the upgrade (0018) keeps, planted with values that differ from the column defaults, so that a
// response that still rendered them, or a request that still wrote them, shows. (The room's
// bridge_chats and broadcast and the connection's auto_route_scope no longer exist as columns.)
type upgradedAPI struct {
	env                    *testkit.Env
	admin, alice, bob      *testkit.Client
	atlas                  string
	room                   dto.Room
	atlasMember, bobMember dto.RoomMember
	conn                   dto.Connector
	chat                   string
}

func newUpgradedAPI(t *testing.T) *upgradedAPI {
	t.Helper()
	fake := connectortest.New()
	env := testkit.New(t, testkit.WithConnectorKinds(fake.Kind()))
	f := &upgradedAPI{env: env, admin: env.SetupAdmin(), alice: env.NewUser("alice"), bob: env.NewUser("bob")}
	f.atlas, f.room = f.alice.NewAI("Atlas"), createRoom(f.alice, "Atlas HQ")
	add := func(kind dto.MemberKind, id string) dto.RoomMember {
		return testkit.Decode[dto.RoomMember](f.alice.Post(f.roomPath()+"/members", dto.AddMemberRequest{Kind: kind, MemberID: id}).RequireStatus(http.StatusCreated))
	}
	env.AddMember(f.alice.PersonalOrg().ID, f.bob.User.ID, orgs.PermissionMember, "") // a group is made of the members of its organization
	f.atlasMember, f.bobMember = add(dto.MemberKindAgent, f.atlas), add(dto.MemberKindUser, f.bob.User.ID)
	f.conn = createFake(f.alice, "wa", true)
	f.chat = insertChat(t, env, f.conn.ID, teamChat, "Support group")
	execSQL(t, env, "UPDATE room_members SET outbound = 0, sign_outbound = 0 WHERE room_id = ?", f.room.ID)
	execSQL(t, env, "UPDATE users SET notify_chat_id = ? WHERE id = ?", f.chat, f.alice.User.ID)
	execSQL(t, env, "UPDATE connectors SET auto_route_room_id = ? WHERE id = ?", f.room.ID, f.conn.ID)
	return f
}

func (f *upgradedAPI) roomPath() string      { return "/api/v1/rooms/" + f.room.ID }
func (f *upgradedAPI) connectorPath() string { return "/api/v1/connectors/" + f.conn.ID }

// CID-22 (T35): no response of the API carries a retired chat field, whatever the database holds: not
// the rooms and their members, the messages and the activity feed, the account, the connections, the
// room an AI reads with room_info, nor the live frames of a room.
func TestResponsesCarryNoRetiredChatField(t *testing.T) {
	f := newUpgradedAPI(t)
	live, _ := f.alice.DialLive("")
	nova := registerAgent(t, f.env, f.alice, "Nova")
	atlasREST := f.alice.Token(dto.TokenKindAgent, "laptop").AsAgent("Atlas")

	check := func(what string, r *testkit.Response) {
		t.Helper()
		requireNoRetiredFields(t, what, r.Body)
	}
	check("GET /rooms", f.alice.Get("/api/v1/rooms").RequireStatus(http.StatusOK))
	check("GET /rooms/{id}", f.alice.Get(f.roomPath()).RequireStatus(http.StatusOK))
	check("POST /rooms", f.alice.Post("/api/v1/rooms", map[string]any{"name": "Fresh"}).RequireStatus(http.StatusCreated))
	check("PATCH /rooms/{id}", f.alice.Patch(f.roomPath(), map[string]any{"description": "the team"}).RequireStatus(http.StatusOK))
	check("GET /rooms/{id}/members", f.alice.Get(f.roomPath()+"/members").RequireStatus(http.StatusOK))
	check("POST /rooms/{id}/members", f.alice.Post(f.roomPath()+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: nova}).RequireStatus(http.StatusCreated))
	check("PATCH /rooms/{id}/members/{mid}", f.alice.Patch(f.roomPath()+"/members/"+f.atlasMember.ID, map[string]any{"display_name": "Atlas"}).RequireStatus(http.StatusOK))
	f.alice.Post(f.roomPath()+"/messages", dto.PostMessageRequest{Text: "hello team"}).RequireStatus(http.StatusCreated)
	check("GET /rooms/{id}/messages", f.alice.Get(f.roomPath()+"/messages").RequireStatus(http.StatusOK))
	check("GET /activity", f.alice.Get("/api/v1/activity").RequireStatus(http.StatusOK))
	check("GET /me", f.alice.Get("/api/v1/me").RequireStatus(http.StatusOK))
	check("GET /connectors", f.alice.Get("/api/v1/connectors").RequireStatus(http.StatusOK))
	check("GET /connectors/{id}", f.alice.Get(f.connectorPath()).RequireStatus(http.StatusOK))
	check("PATCH /connectors/{id}", f.alice.Patch(f.connectorPath(), map[string]any{"name": "wa 2"}).RequireStatus(http.StatusOK))
	check("POST /connectors", f.alice.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: connectortest.KindName, Name: "second", Config: map[string]any{"token": "fake-token-not-real"},
	}).RequireStatus(http.StatusCreated))
	check("GET /agent/rooms/{room}/info", atlasREST.Get(agentRoomPath(f.room.ID, "/info")).RequireStatus(http.StatusOK))

	// The frames the changes above made on the live socket carry the rooms and messages the same way.
	seen := map[dto.LiveEventType]bool{}
	for !seen[dto.LiveEventTypeRoomChanged] || !seen[dto.LiveEventTypeRoomMessage] {
		frame := live.Next(connectortest.Timeout)
		requireNoRetiredFields(t, "live frame "+string(frame.Type), frame.Data)
		seen[frame.Type] = true
	}
}

// CID-22 (T35): a room request that carries a retired field changes nothing, and the rest of its body
// works. The room is the stored catch-all room of a connection, where turning broadcast or bridging on
// used to be a 422; the room's own flags are gone from the schema (0018), so the answer is all there is
// to see.
func TestRoomRequestsCarryingRetiredFieldsChangeNothing(t *testing.T) {
	f := newUpgradedAPI(t)

	got := testkit.Decode[dto.Room](f.alice.Patch(f.roomPath(), map[string]any{
		"name": "Atlas HQ 2", "bridge_chats": false, "broadcast": true, "catch_all": false,
	}).RequireStatus(http.StatusOK))
	require.Equal(t, "Atlas HQ 2", got.Name, "the rest of the body is applied")
	f.alice.Patch(f.roomPath(), map[string]any{"broadcast": true, "bridge_chats": true}).RequireStatus(http.StatusOK)
	kept := testkit.Decode[dto.Room](f.alice.Get(f.roomPath()).RequireStatus(http.StatusOK))
	require.Equal(t, got.Name, kept.Name)
	require.Equal(t, got.Description, kept.Description)
	require.Equal(t, got.RetentionDays, kept.RetentionDays)

	created := testkit.Decode[dto.Room](f.alice.Post("/api/v1/rooms", map[string]any{"name": "Fresh", "bridge_chats": true}).RequireStatus(http.StatusCreated))
	require.Equal(t, "Fresh", created.Name)
}

// CID-22 (T35): the same for the members. The two members of the room are stored silent and unsigned;
// the requests would turn both back on. A user member used to refuse the signature switch with a 422.
func TestMemberRequestsCarryingRetiredFieldsChangeNothing(t *testing.T) {
	f := newUpgradedAPI(t)
	path := f.roomPath() + "/members/"

	got := testkit.Decode[dto.RoomMember](f.alice.Patch(path+f.atlasMember.ID, map[string]any{
		"display_name": "Atlas lead", "outbound": true, "sign_outbound": true,
	}).RequireStatus(http.StatusOK))
	require.Equal(t, "Atlas lead", got.DisplayName, "the rest of the body is applied")
	f.alice.Patch(path+f.bobMember.ID, map[string]any{"outbound": true, "sign_outbound": true}).RequireStatus(http.StatusOK)
	require.Equal(t, 2, countRows(t, f.env, "SELECT COUNT(*) FROM room_members WHERE room_id = ? AND outbound = 0 AND sign_outbound = 0", f.room.ID), "the stored switches are left alone")

	nova := registerAgent(t, f.env, f.alice, "Nova")
	added := testkit.Decode[dto.RoomMember](f.alice.Post(f.roomPath()+"/members", map[string]any{"kind": "agent", "member_id": nova, "outbound": false}).RequireStatus(http.StatusCreated))
	require.Equal(t, nova, added.MemberID)
	require.Equal(t, 1, countRows(t, f.env, "SELECT COUNT(*) FROM room_members WHERE id = ? AND outbound = 1 AND sign_outbound = 1", added.ID), "a new member takes the defaults")
}

// CID-22 (T35): the same for a connection. It is stored as routing to the room; the requests would
// route it elsewhere or clear it. The old validations are gone with the fields: a scope that is not one,
// a room the caller cannot manage and a room that does not exist were a 422 and two 403s.
func TestConnectionRequestsCarryingRetiredFieldsChangeNothing(t *testing.T) {
	f := newUpgradedAPI(t)
	hidden := createRoom(f.bob, "bob only")

	got := testkit.Decode[dto.Connector](f.alice.Patch(f.connectorPath(), map[string]any{
		"name": "wa 2", "auto_route_scope": "all", "auto_route_room_id": "",
	}).RequireStatus(http.StatusOK))
	require.Equal(t, "wa 2", got.Name, "the rest of the body is applied")
	f.alice.Patch(f.connectorPath(), map[string]any{"auto_route_scope": "everyone"}).RequireStatus(http.StatusOK)
	f.alice.Patch(f.connectorPath(), map[string]any{"auto_route_scope": "all", "auto_route_room_id": hidden.ID}).RequireStatus(http.StatusOK)
	f.alice.Patch(f.connectorPath(), map[string]any{"auto_route_room_id": "0190a000-0000-7000-8000-000000000000"}).RequireStatus(http.StatusOK)
	require.Equal(t, 1, countRows(t, f.env, "SELECT COUNT(*) FROM connectors WHERE id = ? AND auto_route_room_id = ?", f.conn.ID, f.room.ID), "the stored routing is left alone")

	for _, entry := range auditPage(f.admin, "").Items {
		changed, _ := entry.Detail["changed"].([]any)
		require.NotContains(t, changed, "auto_route", "%s names no routing change", entry.Action)
	}
}

// CID-22 (T35): deleting the room a connection used to route to (a stored value now) does nothing to
// the connection: it does not stop, say "auto-route room unavailable" or publish a status.
func TestDeletingAFormerCatchAllRoomLeavesTheConnectionAlone(t *testing.T) {
	f := newUpgradedAPI(t)
	live, _ := f.alice.DialLive("")
	live.Expect(dto.LiveEventTypeHello, nil, connectortest.Timeout)

	f.alice.Delete(f.roomPath()).RequireStatus(http.StatusNoContent)

	got := testkit.Decode[dto.Connector](f.alice.Get(f.connectorPath()).RequireStatus(http.StatusOK))
	require.Empty(t, got.LastError)
	live.ExpectNone(300*time.Millisecond, dto.LiveEventTypeRoomDeleted, dto.LiveEventTypeAccessChanged)
}

// retiredRoomFieldSetup returns the setup of the room routes the retired fields used to act on: a group of
// the organization created by the role "member" with her AI Atlas as a member, the roles "target" and
// "guest" in it, and another AI of hers, Nova, to add.
func retiredRoomFieldSetup(env *testkit.Env, roles *testkit.OrgRoles) func(t *testing.T) map[string]string {
	return func(t *testing.T) map[string]string {
		owner := *roles.Member
		owner.T = t
		room := groupIn(t, env, &owner, roles.Org.ID, "Atlas HQ")
		addToRoom(t, env, &owner, room.ID, roles.Target)
		addToRoom(t, env, &owner, room.ID, roles.Guest)
		member := testkit.Decode[dto.RoomMember](owner.Post("/api/v1/rooms/"+room.ID+"/members",
			dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: env.NewAIIn(&owner, roles.Org.ID, "Atlas")}).RequireStatus(http.StatusCreated))
		nova := env.NewAIIn(&owner, roles.Org.ID, "Nova")
		shareAI(&owner, nova, roles.Admin) // until AIs join groups through their organization, a manager adds an AI it may use
		return map[string]string{"room": room.ID, "member": member.ID, "ai": nova}
	}
}

// retiredRoomFieldRoutes are the requests that used to change a room or a member through a retired
// field, each carrying nothing but retired fields.
func retiredRoomFieldRoutes() []roleRoute {
	return []roleRoute{
		{"PATCH a room with bridge_chats and broadcast", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Patch("/api/v1/rooms/"+res["room"], map[string]any{"bridge_chats": true, "broadcast": false})
		}, groupWant(200, 200, 403, 404)},
		{"POST a member with outbound", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Post("/api/v1/rooms/"+res["room"]+"/members", map[string]any{"kind": "agent", "member_id": res["ai"], "outbound": false})
		}, groupWant(201, 201, 403, 404)},
		{"PATCH a member with outbound and sign_outbound", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Patch("/api/v1/rooms/"+res["room"]+"/members/"+res["member"], map[string]any{"outbound": false, "sign_outbound": false})
		}, groupWant(200, 200, 403, 404)},
	}
}

// retiredConnectorFieldSetup prepares the connection the retired fields used to act on, shared for use
// with the viewer, and a room to point it at.
func retiredConnectorFieldSetup(t *testing.T, m *matrix) matrixResource {
	owner := m.as(t, roleOwner)
	room := createRoom(owner, "Atlas HQ")
	conn := createFake(owner, "wa", true)
	shareConnector(owner, conn.ID, m.clients[roleViewer].User.ID)
	return matrixResource{"room": room.ID, "connector": conn.ID}
}

// retiredConnectorFieldRoutes are the requests that used to change a connection through a retired field.
func retiredConnectorFieldRoutes() []matrixRoute {
	return []matrixRoute{
		{"PATCH a connection with auto_route_scope and auto_route_room_id", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Patch("/api/v1/connectors/"+res["connector"], map[string]any{"auto_route_scope": "all", "auto_route_room_id": res["room"]})
		}, statuses(200, 403, 404, 404, 404)},
	}
}

// CID-22 (T35): a request that carries only retired fields keeps the authorization of its route. The
// people who manage the room are answered and change nothing; the people in the room and everybody else
// get the refusal the same route gives them for any other body, so ignoring the fields opens no way in.
func TestRetiredFieldRequestsKeepTheAuthorizationOfTheirRoutes(t *testing.T) {
	fake := connectortest.New()
	m := newMatrix(t, testkit.WithConnectorKinds(fake.Kind()))
	m.run(t, retiredConnectorFieldSetup, retiredConnectorFieldRoutes())

	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	runRoleRoutes(t, roles, retiredRoomFieldSetup(env, roles), retiredRoomFieldRoutes())
}

// CID-22 (T35): the same requests from anonymous callers and from every kind of AI token are refused:
// 401 without a login, and 403 for the AI that operates the connection, another AI of the owner and an
// AI of another user alike, since AI tokens reach agent endpoints only (TOK-03).
func TestRetiredFieldRequestsAreRefusedToAnonymousCallersAndAIs(t *testing.T) {
	fake := connectortest.New()
	m := newMatrix(t, testkit.WithConnectorKinds(fake.Kind()))
	connRes := retiredConnectorFieldSetup(t, m)
	owner := m.as(t, roleOwner)
	registerAgent(t, m.env, owner, "Atlas")
	registerAgent(t, m.env, owner, "Nova")
	registerAgent(t, m.env, m.clients[roleStranger], "Atlas")
	operator := owner.Token(dto.TokenKindAgent, "laptop").AsAgent("Atlas")
	sibling := owner.Token(dto.TokenKindAgent, "desk").AsAgent("Nova")
	strangers := m.clients[roleStranger].Token(dto.TokenKindAgent, "laptop").AsAgent("Atlas")

	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	roomRes := retiredRoomFieldSetup(env, roles)(t)
	roomOperator := roles.Member.Token(dto.TokenKindAgent, "laptop").AsAgent("Atlas")
	roomSibling := roles.Member.Token(dto.TokenKindAgent, "desk").AsAgent("Nova")
	roomStrangers := roles.Outsider.Token(dto.TokenKindAgent, "laptop").AsAgent("Atlas")

	refused := func(t *testing.T, call func(c *testkit.Client) *testkit.Response, anon *testkit.Client, ais map[string]*testkit.Client) {
		anon.T = t
		call(anon).RequireStatus(http.StatusUnauthorized)
		for name, ai := range ais {
			ai.T = t
			e := call(ai).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
			require.Equal(t, "agent tokens can only call agent endpoints", e.Message, name)
		}
	}
	for _, rt := range retiredConnectorFieldRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			refused(t, func(c *testkit.Client) *testkit.Response { return rt.call(t, m, c, connRes) }, m.env.Anon(),
				map[string]*testkit.Client{"operator": operator, "another AI of the owner": sibling, "another user's AI": strangers})
		})
	}
	for _, rt := range retiredRoomFieldRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			refused(t, func(c *testkit.Client) *testkit.Response { return rt.call(t, c, roomRes) }, env.Anon(),
				map[string]*testkit.Client{"member": roomOperator, "another AI of the creator": roomSibling, "another user's AI": roomStrangers})
		})
	}
}
