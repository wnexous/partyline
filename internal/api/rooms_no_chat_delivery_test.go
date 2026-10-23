package api_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/app"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/testkit"
)

// execSQL runs a statement on the app's database through a second connection, to plant a row
// that no API writes any more (the way an upgraded database still holds it).
func execSQL(t *testing.T, env *testkit.Env, query string, args ...any) {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(env.App.Config.DataDir, app.DatabaseFile)+"?_pragma=busy_timeout(5000)")
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	_, err = raw.ExecContext(context.Background(), query, args...)
	require.NoError(t, err)
}

// chatRoom is alice's room "Support" with her AI builder as a member, plus two chats of a running
// fake connection that sit in the room as members, the way a room built before the connection proxy
// holds them (the API refuses chats as members, CID-19). The app runs the workers that send, so a
// delivery that was still planned would reach the connection.
type chatRoom struct {
	*agentRoom
	inst  *connectortest.Instance
	conn  dto.Connector
	chat  string
	other string
}

func newChatRoom(t *testing.T) *chatRoom {
	t.Helper()
	fake := connectortest.New()
	env := testkit.New(t, testkit.WithConnectorKinds(fake.Kind()), testkit.WithOutbox())
	alice := env.NewUser("alice")
	f := &agentRoom{env: env, alice: alice, agent: alice.Token(dto.TokenKindAgent, "laptop")}
	f.agentID = registerAgent(t, env, alice, "builder")
	f.room = createRoom(alice, "Support")
	alice.Post("/api/v1/rooms/"+f.room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: f.agentID}).RequireStatus(http.StatusCreated)

	conn := createFake(alice, "wa", true)
	inst := fake.Instance(t, conn.ID)
	inst.WaitRun(t)
	r := &chatRoom{agentRoom: f, inst: inst, conn: conn}
	r.chat = insertChat(t, env, conn.ID, "120363000000000001@g.us", "Team")
	r.other = insertChat(t, env, conn.ID, "120363000000000002@g.us", "Announcements")
	alice.SeedChatMember(f.room.ID, r.chat)
	alice.SeedChatMember(f.room.ID, r.other)
	return r
}

// requireNoChatDelivery checks that nothing was planned for any chat and nothing reached the
// platform: no send is queued (the only way out to a platform is an AI's send, platform_sends) and the
// connection sent nothing.
func (f *chatRoom) requireNoChatDelivery(t *testing.T) {
	t.Helper()
	require.Zero(t, countRows(t, f.env, "SELECT COUNT(*) FROM platform_sends"), "no delivery is planned for any chat")
	require.Empty(t, f.inst.Sent(), "nothing reached the connection")
}

// CID-22 (T33): a room no longer sends to chats. A post that names chats, as a v0.4 CLI sends it
// (recipients, or chat_ids in the tool), is accepted whatever the names are and the names are
// ignored: they are not stored, no delivery is planned, nothing reaches the connection, and the
// room's agents still get the message. Each rule of the retired delivery to chats (DLV-03/04: a
// listen-only chat is refused, ROUTE-10: at most ten chats) answers 201 now, and the member switch
// that made a chat listen-only is gone from the API (T35).
func TestPostsNamingChatsAreAcceptedAndReachNoChat(t *testing.T) {
	f := newChatRoom(t)
	conn := f.dial(t)
	conn.Welcome(wsWait)

	var eleven []string
	for i := range 11 {
		eleven = append(eleven, fmt.Sprintf("chat-%d", i))
	}
	named := []struct {
		name string
		ids  []string
	}{
		{"nobody", nil},
		{"a chat of the room", []string{f.chat}},
		{"another chat of the room", []string{f.other}},
		{"a chat nobody has", []string{"no-such-chat"}},
		{"eleven chats", eleven},
	}
	posts := []struct {
		name  string
		agent bool
		post  func(dto.PostMessageRequest) *testkit.Response
	}{
		{"user", false, func(r dto.PostMessageRequest) *testkit.Response {
			return f.alice.Post("/api/v1/rooms/"+f.room.ID+"/messages", r)
		}},
		{"user with a file", false, func(r dto.PostMessageRequest) *testkit.Response {
			return postMultipart(f.alice, f.room.ID, r, upload{"notes.txt", "text/plain", "notes"})
		}},
		{"agent", true, func(r dto.PostMessageRequest) *testkit.Response {
			return f.rest().Post(agentRoomPath(f.room.ID, "/messages"), r)
		}},
		{"agent with a file", true, func(r dto.PostMessageRequest) *testkit.Response {
			return postMultipartTo(f.rest(), agentRoomPath(f.room.ID, "/messages"), r, upload{"notes.txt", "text/plain", "notes"})
		}},
	}
	for _, p := range posts {
		for _, n := range named {
			t.Run(p.name+"/"+n.name, func(t *testing.T) {
				resp := p.post(dto.PostMessageRequest{Text: "hello", Recipients: n.ids}).RequireStatus(http.StatusCreated)
				m := testkit.Decode[dto.Message](resp)
				require.Equal(t, "hello", m.Text)
				require.Empty(t, m.Recipients, "the names are not stored")
				keys := keysOf(t, resp.Body)
				for _, retired := range []string{"recipients", "outbox", "warning"} {
					require.NotContains(t, keys, retired)
				}
				if !p.agent { // an agent never gets its own message back (DLV-02)
					require.Equal(t, m.ID, conn.Message(wsWait).Message.ID, "the room's agents still get the message")
				}
				f.requireNoChatDelivery(t)
			})
		}
	}
}

// CID-22 (T33): answering a message of a chat, or reacting to it, in a room where the chat's messages
// are history stores the reply or the reaction in the room only. Before, the reply went to the chat it
// answered (TOOL-02) and the reaction became a platform reaction (REACT-01).
func TestRepliesAndReactionsToChatHistoryReachNoChat(t *testing.T) {
	f := newChatRoom(t)
	target := f.postFromChat(t, f.chat, "can you help?")

	reply := testkit.Decode[dto.Message](f.rest().Post("/api/v1/agent/messages/"+target.ID+"/reply", dto.ReplyRequest{Text: "sure"}).RequireStatus(http.StatusCreated))
	require.Equal(t, target.ID, reply.ReplyTo)
	require.Empty(t, reply.Recipients)

	userReply := testkit.Decode[dto.Message](f.alice.Post("/api/v1/rooms/"+f.room.ID+"/messages", dto.PostMessageRequest{Text: "me too", ReplyTo: target.ID}).RequireStatus(http.StatusCreated))
	require.Empty(t, userReply.Recipients)

	reaction := testkit.Decode[dto.Message](f.rest().Post("/api/v1/agent/messages/"+target.ID+"/react", dto.ReactRequest{Emoji: "👍"}).RequireStatus(http.StatusCreated))
	require.Equal(t, dto.MessageKindReaction, reaction.Kind)
	require.Empty(t, reaction.Recipients)
	f.requireNoChatDelivery(t)
}

// CID-22 (T33): a DM an AI sends to a v0.4 CLI's chat_ids is accepted and the ids are ignored; before,
// ids that were no chat of the new DM refused the post (ROUTE-10, DLV-04) and left no DM behind.
func TestAgentDMNamingChatsIsAcceptedAndReachesNoChat(t *testing.T) {
	f := newChatRoom(t)
	for _, ids := range [][]string{{f.chat}, {f.other}, {"no-such-chat"}} {
		m := testkit.Decode[dto.Message](f.rest().Post("/api/v1/agent/dms", dto.AgentDMRequest{To: "@alice", Text: "hi", ChatIDs: ids}).RequireStatus(http.StatusCreated))
		require.Empty(t, m.Recipients)
	}
	f.requireNoChatDelivery(t)
}

// CID-22 (T33): the copy of an AI's DM that went to the app its user chose (NOTI-02) is gone. The
// column stays in an upgraded database, so a user who had chosen a chat before the upgrade still
// holds one; the AI's message in the DM reaches the user in the app only, and nothing is queued to send
// to the chat.
func TestAnAIsDMIsNotCopiedToTheUsersChosenApp(t *testing.T) {
	f := newChatRoom(t)
	execSQL(t, f.env, "UPDATE users SET notify_chat_id = ? WHERE id = ?", f.chat, f.alice.User.ID)
	dm := testkit.Decode[dto.Room](f.alice.Post("/api/v1/conversations/dm", dto.OpenDMRequest{With: f.agentID}).RequireStatus(http.StatusCreated))

	f.rest().Post(agentRoomPath(dm.ID, "/messages"), dto.PostMessageRequest{Text: "build is green"}).RequireStatus(http.StatusCreated)
	seen := testkit.Decode[dto.Page[dto.Message]](f.alice.Get("/api/v1/rooms/" + dm.ID + "/messages").RequireStatus(http.StatusOK)).Items
	require.Len(t, seen, 1)
	require.Equal(t, "build is green", seen[0].Text)
	f.requireNoChatDelivery(t)
}

// CID-22 (T33): the live stream has no delivery status of messages to chats any more (LIVE-04): a post
// in a room with chat members is announced as a message, and nothing about a delivery follows.
func TestLiveStreamHasNoOutboxStatus(t *testing.T) {
	f := newChatRoom(t)
	live := dialLive(t, f.alice)

	m := f.post(f.alice, "hello")
	var got dto.Message
	live.Expect(dto.LiveEventTypeRoomMessage, &got, liveWait)
	require.Equal(t, m.ID, got.ID)
	live.ExpectNone(quiet, dto.LiveEventTypeRoomChanged)
	f.requireNoChatDelivery(t)
}

// CID-22 (T33): naming chats in a post neither grants nor refuses anything: the post keeps the
// authorization of the route it goes through, and a refused post stores nothing. The user route needs
// the post level on the room (the creator, an admin of the organization and the people in the room 201,
// everybody else 404, anonymous callers 401); the agent route is the AI's own, for a member only: its
// creator's other AI and another user's AI of the same folder name are refused, and so are users with a
// session or a token. Each request is otherwise valid and names the room's chat.
func TestPostsNamingChatsKeepTheAuthorizationOfTheirRoute(t *testing.T) {
	fake := connectortest.New()
	env := testkit.New(t, testkit.WithConnectorKinds(fake.Kind()))
	roles := env.OrgRoles("acme")
	owner := roles.Member
	room := groupIn(t, env, owner, roles.Org.ID, "Support")
	addToRoom(t, env, owner, room.ID, roles.Target)
	addToRoom(t, env, owner, room.ID, roles.Guest)
	conn := createFake(owner, "wa", true)
	chat := insertChat(t, env, conn.ID, "120363000000000001@g.us", "Team")
	owner.SeedChatMember(room.ID, chat)
	member := env.NewAIIn(owner, roles.Org.ID, "builder")
	owner.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: member}).RequireStatus(http.StatusCreated)
	env.NewAIIn(owner, roles.Org.ID, "outsider")
	env.NewAI(roles.Outsider, "builder")
	req := dto.PostMessageRequest{Text: "hello", Recipients: []string{chat}}

	userPost := []roomRoute{{"post naming a chat", func(_ *testing.T, c *testkit.Client, _ map[string]string) *testkit.Response {
		return c.Post("/api/v1/rooms/"+room.ID+"/messages", req)
	}, groupWant(http.StatusCreated, http.StatusCreated, http.StatusCreated, http.StatusNotFound)}}
	runRoomRoutes(t, roles, func(*testing.T) map[string]string { return map[string]string{} }, userPost)
	env.Anon().Post("/api/v1/rooms/"+room.ID+"/messages", req).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)

	agentPath := agentRoomPath(room.ID, "/messages")
	laptop := owner.Token(dto.TokenKindAgent, "laptop")
	laptop.AsAgent("builder").Post(agentPath, req).RequireStatus(http.StatusCreated)
	for name, ai := range map[string]*testkit.Client{
		"another AI of the creator":                 laptop.AsAgent("outsider"),
		"another user's AI of the same folder name": roles.Outsider.Token(dto.TokenKindAgent, "laptop").AsAgent("builder"),
	} {
		ai.T = t
		e := ai.Post(agentPath, req).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		require.Equal(t, "not a member of room "+room.ID, e.Message, name)
	}
	for name, c := range map[string]*testkit.Client{"session": owner, "user token": owner.Token(dto.TokenKindUser, "script")} {
		c.T = t
		e := c.Post(agentPath, req).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		require.Equal(t, "agent token required", e.Message, name)
	}
	env.Anon().Post(agentPath, req).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)

	// Only the five that were allowed stored something: the four people who may post and the member AI.
	require.Equal(t, 5, countRows(t, env, "SELECT COUNT(*) FROM messages WHERE room_id = ?", room.ID))
	require.Zero(t, countRows(t, env, "SELECT COUNT(*) FROM platform_sends"))
}
