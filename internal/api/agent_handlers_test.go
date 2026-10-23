package api_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/testkit"
)

// rest returns the agent's REST client (agent token + the folder headers of builder).
func (f *agentRoom) rest() *testkit.Client { return f.agent.AsAgent("builder") }

func agentRoomPath(room, suffix string) string { return "/api/v1/agent/rooms/" + room + suffix }

// chatMember adds a chat of alice's connector to the room and returns its chat id.
func (f *agentRoom) chatMember(t *testing.T, title string) string {
	t.Helper()
	_, chat := storeChat(t, f.env, f.alice.User.ID, title)
	f.alice.SeedChatMember(f.room.ID, chat)
	return chat
}

// TOK-03: agent tokens reach only agent routes; sessions and user tokens never reach them.
func TestAgentAndUserRoutesAreSeparated(t *testing.T) {
	f := newAgentRoom(t)
	msg := f.post(f.alice, "hello")
	agent := f.rest()
	userRoutes := []struct{ method, path string }{
		{"GET", "/api/v1/me"}, {"GET", "/api/v1/rooms"}, {"POST", "/api/v1/rooms"},
		{"GET", "/api/v1/rooms/" + f.room.ID + "/messages"}, {"POST", "/api/v1/rooms/" + f.room.ID + "/messages"},
		{"GET", "/api/v1/agents"}, {"DELETE", "/api/v1/agents/" + f.agentID}, {"GET", "/api/v1/tokens"},
		{"GET", "/api/v1/users"}, {"GET", "/api/v1/admin/users"},
	}
	for _, rt := range userRoutes {
		agent.Do(rt.method, rt.path, nil).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	}
	agentRoutes := []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/v1/agent/rooms", nil},
		{"GET", agentRoomPath(f.room.ID, "/info"), nil},
		{"GET", agentRoomPath(f.room.ID, "/messages"), nil},
		{"POST", agentRoomPath(f.room.ID, "/messages"), dto.PostMessageRequest{Text: "x"}},
		{"POST", "/api/v1/agent/messages/" + msg.ID + "/reply", dto.ReplyRequest{Text: "x"}},
		{"POST", "/api/v1/agent/messages/" + msg.ID + "/react", dto.ReactRequest{Emoji: "👍"}},
		{"GET", "/api/v1/agent/attachments/" + msg.ID, nil},
	}
	for name, c := range map[string]*testkit.Client{"session": f.alice, "user token": f.alice.Token(dto.TokenKindUser, "script")} {
		for _, rt := range agentRoutes {
			e := c.Do(rt.method, rt.path, rt.body).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
			require.Equal(t, "agent token required", e.Message, "%s %s %s", name, rt.method, rt.path)
		}
	}
	for _, rt := range agentRoutes {
		f.env.Anon().Do(rt.method, rt.path, rt.body).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	}
}

// TOOL-09: list_rooms returns the agent's rooms with members and the agents' online state.
func TestAgentRoomsListsMembersWithPresence(t *testing.T) {
	f := newTwoAgents(t)
	createRoom(f.alice, "Elsewhere") // the agent is not a member
	f.helper.Close()
	require.Eventually(t, func() bool { return !f.env.App.Presence.Online(f.helperID) }, wsWait, 20*time.Millisecond)

	list := testkit.Decode[dto.List[dto.AgentRoom]](f.rest().Get("/api/v1/agent/rooms").RequireStatus(http.StatusOK))
	require.Len(t, list.Items, 1)
	room := list.Items[0]
	require.Equal(t, f.room.ID, room.ID)
	require.Equal(t, "Support", room.Name)
	online := map[string]bool{}
	users := 0
	for _, m := range room.Members {
		if m.Kind == dto.MemberKindUser {
			users++ // bobby is in the room as a user, which has no online state to show
			require.Nil(t, m.Online)
			continue
		}
		require.Equal(t, dto.MemberKindAgent, m.Kind)
		require.NotNil(t, m.Online)
		online[m.Name+"@"+m.Owner] = *m.Online
	}
	require.Equal(t, map[string]bool{"builder@alice": true, "helper@bobby": false}, online)
	require.Equal(t, 1, users)
}

// TOOL-03: the agent posts by room id or name.
func TestAgentPostsByIDOrName(t *testing.T) {
	f := newAgentRoom(t)

	m := testkit.Decode[dto.Message](f.rest().Post(agentRoomPath(f.room.ID, "/messages"), dto.PostMessageRequest{Text: "by id"}).RequireStatus(http.StatusCreated))
	require.Equal(t, f.room.ID, m.RoomID)
	require.Equal(t, dto.AuthorKindAgent, m.AuthorKind)
	require.Equal(t, f.agentID, m.AuthorID)
	require.Equal(t, f.alice.User.ID, m.AuthorUserID)

	for _, ref := range []string{"Support", "support"} {
		m = testkit.Decode[dto.Message](f.rest().Post(agentRoomPath(ref, "/messages"), dto.PostMessageRequest{Text: "by name"}).RequireStatus(http.StatusCreated))
		require.Equal(t, f.room.ID, m.RoomID)
		require.Equal(t, "by name", m.Text)
	}
}

// TOOL-04: rooms without the agent (or unknown) answer 403 "not a member of room <room>";
// a name shared by two of its rooms is 409.
func TestAgentRoomRefErrors(t *testing.T) {
	f := newAgentRoom(t)
	other := createRoom(f.alice, "Private")
	for _, ref := range []string{other.ID, "Private", "nowhere"} {
		e := f.rest().Post(agentRoomPath(ref, "/messages"), dto.PostMessageRequest{Text: "hi"}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		require.Equal(t, "not a member of room "+ref, e.Message)
		e = f.rest().Get(agentRoomPath(ref, "/messages")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		require.Equal(t, "not a member of room "+ref, e.Message)
	}

	dup := createRoom(f.alice, "support")
	f.alice.Post("/api/v1/rooms/"+dup.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: f.agentID}).RequireStatus(http.StatusCreated)
	e := f.rest().Post(agentRoomPath("Support", "/messages"), dto.PostMessageRequest{Text: "hi"}).RequireError(http.StatusConflict, dto.ErrorCodeConflict)
	require.Equal(t, "ambiguous room name", e.Message)
	f.rest().Post(agentRoomPath(dup.ID, "/messages"), dto.PostMessageRequest{Text: "by id still works"}).RequireStatus(http.StatusCreated)
}

// TOOL-05: history returns at most 100 (default 20) messages, newest first, before a message
// id, with the trusted flag for this agent.
func TestAgentHistory(t *testing.T) {
	f := newAgentRoom(t)
	bob := f.env.NewUser("bobby")
	addToRoom(t, f.env, f.alice, f.room.ID, bob)
	alice := authn.Principal{Kind: authn.KindUser, UserID: f.alice.User.ID}
	var posted []rooms.Message
	for i := 1; i <= 105; i++ {
		m, err := f.env.App.Rooms.Post(context.Background(), alice, f.room.ID, rooms.PostInput{Text: fmt.Sprintf("message %d", i)})
		require.NoError(t, err)
		posted = append(posted, m)
	}
	fromBob := f.post(bob, "from bob")

	page := testkit.Decode[dto.Page[dto.AgentMessage]](f.rest().Get(agentRoomPath(f.room.ID, "/messages")).RequireStatus(http.StatusOK))
	require.Len(t, page.Items, 20)
	require.True(t, page.HasMore)
	require.Equal(t, fromBob.ID, page.Items[0].Message.ID)
	require.False(t, page.Items[0].Trusted, "bob is not the agent's owner")
	require.Equal(t, posted[104].ID, page.Items[1].Message.ID)
	require.True(t, page.Items[1].Trusted)
	require.Equal(t, dto.RoomRef{ID: f.room.ID, Name: "Support"}, page.Items[1].Room)

	page = testkit.Decode[dto.Page[dto.AgentMessage]](f.rest().Get(agentRoomPath(f.room.ID, "/messages?limit=101")).RequireStatus(http.StatusOK))
	require.Len(t, page.Items, 100, "limit is clamped to 100")
	require.True(t, page.HasMore)

	page = testkit.Decode[dto.Page[dto.AgentMessage]](f.rest().Get(agentRoomPath(f.room.ID, "/messages?limit=3&before="+posted[50].ID)).RequireStatus(http.StatusOK))
	ids := []string{}
	for _, m := range page.Items {
		ids = append(ids, m.Message.ID)
	}
	require.Equal(t, []string{posted[49].ID, posted[48].ID, posted[47].ID}, ids)

	other := createRoom(f.alice, "Other")
	stranger := testkit.Decode[dto.Message](f.alice.Post("/api/v1/rooms/"+other.ID+"/messages", dto.PostMessageRequest{Text: "x"}).RequireStatus(http.StatusCreated))
	f.rest().Get(agentRoomPath(f.room.ID, "/messages?before="+stranger.ID)).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
}

// TOOL-02: a reply sets reply_to, whoever wrote the replied message. A reply to a chat message (history)
// goes to no chat any more: a room sends nothing to a platform (CID-22).
func TestAgentReply(t *testing.T) {
	f := newAgentRoom(t)
	family := f.chatMember(t, "Family")
	fromChat, err := f.env.App.Rooms.PostFromChat(context.Background(), f.room.ID, rooms.ChatMessage{
		ChatID: family, SenderID: "5511999990000", SenderName: "Grandma", ExternalID: "ext-1", Text: "dinner?",
	})
	require.NoError(t, err)

	r := testkit.Decode[dto.Message](f.rest().Post("/api/v1/agent/messages/"+fromChat.ID+"/reply", dto.ReplyRequest{Text: "at seven"}).RequireStatus(http.StatusCreated))
	require.Equal(t, fromChat.ID, r.ReplyTo)
	require.Equal(t, f.room.ID, r.RoomID)
	require.Equal(t, f.agentID, r.AuthorID)
	require.Empty(t, r.Recipients)

	fromUser := f.post(f.alice, "status?")
	r = testkit.Decode[dto.Message](f.rest().Post("/api/v1/agent/messages/"+fromUser.ID+"/reply", dto.ReplyRequest{Text: "all green"}).RequireStatus(http.StatusCreated))
	require.Equal(t, fromUser.ID, r.ReplyTo)
	require.Empty(t, r.Recipients)

	other := createRoom(f.alice, "Private")
	elsewhere := testkit.Decode[dto.Message](f.alice.Post("/api/v1/rooms/"+other.ID+"/messages", dto.PostMessageRequest{Text: "x"}).RequireStatus(http.StatusCreated))
	f.rest().Post("/api/v1/agent/messages/"+elsewhere.ID+"/reply", dto.ReplyRequest{Text: "x"}).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	f.rest().Post("/api/v1/agent/messages/unknown/reply", dto.ReplyRequest{Text: "x"}).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// TOOL-07: a multipart post uploads a file with caption and reply_to; the agent downloads
// attachments of its rooms only.
func TestAgentSendAndDownloadFile(t *testing.T) {
	f := newAgentRoom(t)
	question := f.post(f.alice, "send the report")
	m := testkit.Decode[dto.Message](postMultipartTo(f.rest(), agentRoomPath(f.room.ID, "/messages"),
		dto.PostMessageRequest{Text: "here it is", ReplyTo: question.ID}, upload{"report.txt", "text/plain", "all good"}).RequireStatus(http.StatusCreated))
	require.Equal(t, "here it is", m.Text)
	require.Equal(t, question.ID, m.ReplyTo)
	require.Len(t, m.Attachments, 1)
	require.Equal(t, "report.txt", m.Attachments[0].Name)

	got := f.rest().Get("/api/v1/agent/attachments/" + m.Attachments[0].ID).RequireStatus(http.StatusOK)
	require.Equal(t, "all good", string(got.Body))

	other := createRoom(f.alice, "Private")
	foreign := testkit.Decode[dto.Message](postMultipart(f.alice, other.ID, dto.PostMessageRequest{Text: "secret"}, upload{"s.txt", "text/plain", "s"}).RequireStatus(http.StatusCreated))
	f.rest().Get("/api/v1/agent/attachments/"+foreign.Attachments[0].ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// TOOL-08: the 31st post within a minute is rejected with 429.
func TestAgentPostRateLimit(t *testing.T) {
	f := newAgentRoom(t)
	for i := 1; i <= 30; i++ {
		f.rest().Post(agentRoomPath(f.room.ID, "/messages"), dto.PostMessageRequest{Text: fmt.Sprintf("post %d", i)}).RequireStatus(http.StatusCreated)
	}
	f.rest().Post(agentRoomPath(f.room.ID, "/messages"), dto.PostMessageRequest{Text: "one too many"}).RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	msg := f.post(f.alice, "reply to me")
	f.rest().Post("/api/v1/agent/messages/"+msg.ID+"/reply", dto.ReplyRequest{Text: "also limited"}).RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
}
