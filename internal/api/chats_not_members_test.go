package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/testkit"
)

// chatRoomSetup returns the setup of the chat-member matrix: a fresh group of the organization with the
// roles "target" and "guest" in it, and a chat of its creator's own connection that is not a member: a
// request to add it is valid in every way but its kind.
func chatRoomSetup(env *testkit.Env, roles *testkit.OrgRoles) func(t *testing.T) map[string]string {
	return func(t *testing.T) map[string]string {
		creator := *roles.Member
		creator.T = t
		r := groupIn(t, env, &creator, roles.Org.ID, "matrix")
		addToRoom(t, env, &creator, r.ID, roles.Target)
		addToRoom(t, env, &creator, r.ID, roles.Guest)
		_, chat := storeChat(t, env, creator.User.ID, "group")
		return map[string]string{"room": r.ID, "chat": chat}
	}
}

// CID-19 (authorization matrix of POST /rooms/{id}/members with a chat, L-018): the creator and the admins
// of the organization, who may manage the room, are told the request is the problem (422); the people in
// the room may not change members at all (403), and everybody else, an instance admin included, gets 404.
// The rights on the room are decided before the kind of the member, as for any other member.
func TestAddChatMemberAuthMatrix(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	setup := chatRoomSetup(env, roles)
	runRoleRoutes(t, roles, setup, []roleRoute{
		{"add chat member", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Post("/api/v1/rooms/"+res["room"]+"/members", dto.AddMemberRequest{Kind: dto.MemberKindChat, MemberID: res["chat"]})
		}, groupWant(422, 422, 403, 404)},
	})

	// Anonymous callers and AI tokens, the creator's own included, are not users who manage rooms.
	res := setup(t)
	path := "/api/v1/rooms/" + res["room"] + "/members"
	body := dto.AddMemberRequest{Kind: dto.MemberKindChat, MemberID: res["chat"]}
	env.Anon().Post(path, body).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	token := roles.Member.Token(dto.TokenKindAgent, "laptop")
	token.Post(path, body).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	token.AsAgent("Atlas").Post(path, body).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	members := testkit.Decode[dto.List[dto.RoomMember]](roles.Member.Get(path).RequireStatus(http.StatusOK)).Items
	require.Len(t, members, 2, "no refusal added anything: only the two people the setup put in the room")
}

// CID-19 (L-017, L-018): a manager of a room who asks to add a chat, a request that is valid in every
// other way, gets 422 chats_not_members, whoever owns the chat's connection, whether the chat
// exists or not, whether it asks for outbound delivery or not (the chat branch of ACL-06 is
// retired). Nothing is added, nothing is announced, and users and AIs still join as before.
func TestAddingAChatAsAMemberIsRefused(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	atlas := env.NewAI(alice, "atlas")
	_, mine := storeChat(t, env, alice.User.ID, "family")
	bobConn, bobs := storeChat(t, env, bob.User.ID, "team")
	shareConnector(bob, bobConn, alice.User.ID) // alice joins the organization of that chat's connection
	room := createRoom(alice, "ops")
	live, _ := alice.DialLive("")
	live.Expect(dto.LiveEventTypeHello, nil, wsWait)
	path := "/api/v1/rooms/" + room.ID + "/members"

	for name, req := range map[string]map[string]any{
		"a chat of the caller's connection":         {"kind": "chat", "member_id": mine},
		"a listen-only chat (a v0.4 client)":        {"kind": "chat", "member_id": mine, "outbound": false},
		"a chat of a connection the caller may use": {"kind": "chat", "member_id": bobs},
		"a chat that does not exist":                {"kind": "chat", "member_id": "no-such-chat"},
		"a chat without an id":                      {"kind": "chat"},
	} {
		e := alice.Post(path, req).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeChatsNotMembers)
		require.NotEmpty(t, e.Message, name)
	}
	require.Empty(t, testkit.Decode[dto.List[dto.RoomMember]](alice.Get(path).RequireStatus(http.StatusOK)).Items, "nothing was added")
	require.Zero(t, testkit.Decode[dto.Room](alice.Get("/api/v1/rooms/"+room.ID).RequireStatus(http.StatusOK)).MemberCount)
	live.ExpectNone(300 * time.Millisecond)

	// The other kinds still join, and a chat next to them is as refused.
	alice.Post(path, dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: atlas}).RequireStatus(http.StatusCreated)
	joinOrgOf(t, env, room.ID, bob, orgs.PermissionMember)
	alice.Post(path, dto.AddMemberRequest{Kind: dto.MemberKindUser, MemberID: bob.User.ID}).RequireStatus(http.StatusCreated)
	alice.Post(path, dto.AddMemberRequest{Kind: dto.MemberKindChat, MemberID: mine}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeChatsNotMembers)
	require.Len(t, testkit.Decode[dto.List[dto.RoomMember]](alice.Get(path).RequireStatus(http.StatusOK)).Items, 2)

	// A kind that is not one still says which are: a chat is no longer among them.
	e := alice.Post(path, dto.AddMemberRequest{Kind: "robot", MemberID: "x"}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Equal(t, map[string]string{"kind": "must be agent or user"}, e.Fields)
}

// CID-19: a direct message holds its two intelligences whatever anybody asks, so a chat asked into one
// is answered by the rule of the direct message, before the kind of the member.
func TestAddingAChatToADirectMessageIsRefused(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	atlas := env.NewAI(alice, "atlas")
	_, chat := storeChat(t, env, alice.User.ID, "family")
	dm := dmWith(alice, atlas, http.StatusCreated)

	alice.Post("/api/v1/rooms/"+dm.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindChat, MemberID: chat}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeDMMembersFixed)
	require.Len(t, testkit.Decode[dto.List[dto.RoomMember]](alice.Get("/api/v1/rooms/"+dm.ID+"/members").RequireStatus(http.StatusOK)).Items, 2)
}

// CID-19: a new group refuses a chat among its members, as it refuses anything that is not an
// intelligence it can see: the answer is 422 members_refused naming it, and no group exists
// afterwards.
func TestNewGroupRefusesAChatAsAMember(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	_, chat := storeChat(t, env, alice.User.ID, "family")
	env.AddMember(alice.PersonalOrg().ID, bob.User.ID, orgs.PermissionMember, "")

	e := createGroup(alice, "Launch", bob.User.ID, chat).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeMembersRefused)

	require.Equal(t, []dto.RefusedMember{{ID: chat, Reason: dto.RefusedReasonNotFound}}, e.Refused)
	require.Empty(t, roomListOf(t, alice), "nothing was created")
}

// CID-19: the members rooms built before the connection proxy hold stay readable and removable until
// the upgrade drops them: the room still lists the chat, and its manager can take it out.
func TestOldChatMembersCanStillBeListedAndRemoved(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	_, chat := storeChat(t, env, alice.User.ID, "family")
	room := createRoom(alice, "ops")
	member := alice.SeedChatMember(room.ID, chat)
	path := "/api/v1/rooms/" + room.ID + "/members"

	listed := testkit.Decode[dto.List[dto.RoomMember]](alice.Get(path).RequireStatus(http.StatusOK)).Items
	require.Len(t, listed, 1)
	require.Equal(t, []any{dto.MemberKindChat, chat, "family"}, []any{listed[0].Kind, listed[0].MemberID, listed[0].Name})
	alice.Delete(path + "/" + member.ID).RequireStatus(http.StatusNoContent)
	require.Empty(t, testkit.Decode[dto.List[dto.RoomMember]](alice.Get(path).RequireStatus(http.StatusOK)).Items)
}
