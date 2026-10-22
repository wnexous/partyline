package connectors_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

type outboundEnv struct {
	*inboundEnv
	chat   string
	room   dto.Room
	agent  *testkit.Client    // agent REST client ("helper")
	helper *testkit.AgentConn // helper's live connection
}

// newOutboundEnv runs the outbox worker with a fake connector whose group chat is routed to
// alice's agent "helper" (connected).
func newOutboundEnv(t *testing.T, opts ...testkit.Option) *outboundEnv {
	t.Helper()
	e := newInboundEnv(t, append([]testkit.Option{testkit.WithOutbox()}, opts...)...)
	e.emit(text(groupID, "M0", "hello"))
	chat := e.chatID(t, groupID)

	e.env.NewAI(e.alice, "helper")
	token := e.alice.Token(dto.TokenKindAgent, "laptop")
	conn, _ := token.DialAgent("helper")
	conn.Welcome(connectortest.Timeout)
	var agents dto.List[dto.Agent]
	e.alice.Get("/api/v1/agents").RequireStatus(http.StatusOK).Decode(&agents)
	require.Len(t, agents.Items, 1)
	room := testkit.Decode[dto.Room](e.alice.Post("/api/v1/chats/"+chat+"/route", dto.RouteChatRequest{AgentID: agents.Items[0].ID}).RequireStatus(http.StatusCreated))
	return &outboundEnv{inboundEnv: e, chat: chat, room: room, agent: token.AsAgent("helper"), helper: conn}
}

// outbox returns the message's delivery to the chat once it satisfies done.
func (e *outboundEnv) outbox(t *testing.T, messageID string, done func(dto.OutboxItem) bool) dto.OutboxItem {
	t.Helper()
	deadline := time.Now().Add(connectortest.Timeout)
	for {
		for _, m := range e.messages(t, e.room.ID) {
			if m.ID == messageID && len(m.Outbox) == 1 && done(m.Outbox[0]) {
				return m.Outbox[0]
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("outbox of %s never reached the expected state", messageID)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// DLV-07 through a connector: an agent reply to a chat message reaches the connector with
// the chat's platform id and the quoted message's platform id and sender.
func TestAgentReplyReachesConnectorWithQuote(t *testing.T) {
	e := newOutboundEnv(t)
	e.emit(text(groupID, "M1", "where is my order?"))
	msgs := e.messages(t, e.room.ID)
	require.Len(t, msgs, 1)

	reply := testkit.Decode[dto.Message](e.agent.Post("/api/v1/agent/messages/"+msgs[0].ID+"/reply", dto.ReplyRequest{Text: "on its way"}).RequireStatus(http.StatusCreated))
	sent := e.inst.WaitSend(t)
	require.Equal(t, groupID, sent.ChatID)
	require.Equal(t, "on its way", sent.Text)
	require.Equal(t, "M1", sent.ReplyToID)
	require.Equal(t, senderID, sent.ReplyToSenderID)
	require.Equal(t, reply.ID, sent.MessageID)
	require.Equal(t, e.room.ID, sent.RoomID)
	require.Equal(t, "helper", sent.AuthorName)
	require.Empty(t, sent.Files)

	item := e.outbox(t, reply.ID, func(o dto.OutboxItem) bool { return o.Status == dto.OutboxStatusSent })
	require.Equal(t, 1, item.Attempts)

	// A reply quoting the message Partyline sent quotes its platform id, without a sender.
	again := testkit.Decode[dto.Message](e.agent.Post("/api/v1/agent/messages/"+reply.ID+"/reply", dto.ReplyRequest{Text: "tracking attached"}).RequireStatus(http.StatusCreated))
	require.Equal(t, reply.ID, again.ReplyTo)
	sent = e.inst.WaitSend(t)
	require.Equal(t, "ext-1", sent.ReplyToID)
	require.Empty(t, sent.ReplyToSenderID)
}

// WA-03, DLV-07: a reply quotes the replied message only in the chat it came from. With two
// outbound chats in the room, a reply to chat B's message reaches chat B quoting it and chat
// A without a quote.
func TestReplyQuotesOnlyInTheOriginChat(t *testing.T) {
	e := newOutboundEnv(t)
	e.emit(text(otherID, "B0", "hello from B"))
	chatB := e.chatID(t, otherID)
	on := true
	e.alice.Post("/api/v1/rooms/"+e.room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindChat, MemberID: chatB, Outbound: &on}).RequireStatus(http.StatusCreated)
	e.emit(text(otherID, "B1", "question from B"))
	msgs := e.messages(t, e.room.ID)
	require.Len(t, msgs, 1)
	require.Equal(t, "B1", msgs[0].ExternalID)

	e.alice.Post("/api/v1/rooms/"+e.room.ID+"/messages", dto.PostMessageRequest{Text: "answer", ReplyTo: msgs[0].ID}).RequireStatus(http.StatusCreated)
	byChat := map[string]connectors.Outbound{}
	for range 2 {
		sent := e.inst.WaitSend(t)
		byChat[sent.ChatID] = sent
	}
	require.Equal(t, "answer", byChat[otherID].Text)
	require.Equal(t, "B1", byChat[otherID].ReplyToID, "the origin chat quotes its message")
	require.Equal(t, senderID, byChat[otherID].ReplyToSenderID)
	require.Equal(t, "answer", byChat[groupID].Text)
	require.Empty(t, byChat[groupID].ReplyToID, "chat A never saw B1 under that id")
	require.Empty(t, byChat[groupID].ReplyToSenderID)
}

// Attachments of a room message reach the connector as files with their content.
func TestUserMessageWithFileReachesConnector(t *testing.T) {
	e := newOutboundEnv(t)
	content := []byte("synthetic report")
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormField(dto.MultipartMessageField)
	require.NoError(t, err)
	require.NoError(t, json.NewEncoder(part).Encode(dto.PostMessageRequest{Text: "the report"}))
	file, err := w.CreateFormFile(dto.MultipartFileField, "report.txt")
	require.NoError(t, err)
	_, err = file.Write(content)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	e.alice.DoRaw(http.MethodPost, "/api/v1/rooms/"+e.room.ID+"/messages", w.FormDataContentType(), &body).RequireStatus(http.StatusCreated)

	sent := e.inst.WaitSend(t)
	require.Equal(t, "the report", sent.Text)
	require.Empty(t, sent.ReplyToID)
	require.Len(t, sent.Files, 1)
	require.Equal(t, "report.txt", sent.Files[0].Name)
	require.Equal(t, int64(len(content)), sent.Files[0].Size)
	r, err := sent.Files[0].Open()
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, content, got)
}

// CON-05, DLV-07: a disabled connector sends nothing; the delivery fails and is retried.
func TestDisabledConnectorSendFailsIntoRetry(t *testing.T) {
	e := newOutboundEnv(t)
	off := false
	e.alice.Patch("/api/v1/connectors/"+e.conn.ID, dto.UpdateConnectorRequest{Enabled: &off}).RequireStatus(http.StatusOK)
	e.inst.WaitStop(t)

	msg := testkit.Decode[dto.Message](e.alice.Post("/api/v1/rooms/"+e.room.ID+"/messages", dto.PostMessageRequest{Text: "anyone there?"}).RequireStatus(http.StatusCreated))
	item := e.outbox(t, msg.ID, func(o dto.OutboxItem) bool { return o.Attempts >= 1 })
	require.Equal(t, dto.OutboxStatusPending, item.Status, "queued for a retry")
	require.Equal(t, "connector is not running", item.LastError)
	require.Empty(t, e.inst.Sent())
}

// patchAgentMember changes the settings of the room's only agent member.
func (e *outboundEnv) patchAgentMember(t *testing.T, roomID string, req dto.UpdateMemberRequest) {
	t.Helper()
	for _, m := range testkit.Decode[dto.List[dto.RoomMember]](e.alice.Get("/api/v1/rooms/" + roomID + "/members").RequireStatus(http.StatusOK)).Items {
		if m.Kind == dto.MemberKindAgent {
			e.alice.Patch("/api/v1/rooms/"+roomID+"/members/"+m.ID, req).RequireStatus(http.StatusOK)
			return
		}
	}
	t.Fatal("no agent member")
}

func (e *outboundEnv) agentPost(t *testing.T, roomID, text string) dto.Message {
	t.Helper()
	return testkit.Decode[dto.Message](e.agent.Post("/api/v1/agent/rooms/"+roomID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
}

func (e *outboundEnv) authorName(t *testing.T, roomID, messageID string) string {
	t.Helper()
	for _, m := range e.messages(t, roomID) {
		if m.ID == messageID {
			return m.AuthorName
		}
	}
	t.Fatalf("message %s not in room", messageID)
	return ""
}

// MEM-01, MEM-02: the connector gets the agent name as author by default, the member's
// display name when set (also shown in the room timeline), and no author when the member's
// signature is off. Settings are per room: the same agent in another room keeps its name.
// Users keep their own display name; the connector's operator is not signed.
func TestOutboundAuthorFollowsMemberSettings(t *testing.T) {
	e := newOutboundEnv(t)

	m := e.agentPost(t, e.room.ID, "default")
	sent := e.inst.WaitSend(t)
	require.Equal(t, m.ID, sent.MessageID)
	require.Equal(t, "helper", sent.AuthorName)

	e.patchAgentMember(t, e.room.ID, dto.UpdateMemberRequest{DisplayName: ptr("Lolo")})
	m = e.agentPost(t, e.room.ID, "named")
	require.Equal(t, "Lolo", m.AuthorName, "the post response shows the display name")
	sent = e.inst.WaitSend(t)
	require.Equal(t, m.ID, sent.MessageID)
	require.Equal(t, "Lolo", sent.AuthorName)
	require.Equal(t, "named", sent.Text)
	require.Equal(t, "Lolo", e.authorName(t, e.room.ID, m.ID), "the timeline shows the display name")

	e.patchAgentMember(t, e.room.ID, dto.UpdateMemberRequest{SignOutbound: ptr(false)})
	m = e.agentPost(t, e.room.ID, "unsigned")
	sent = e.inst.WaitSend(t)
	require.Equal(t, m.ID, sent.MessageID)
	require.Equal(t, "", sent.AuthorName)
	require.Equal(t, "unsigned", sent.Text)
	require.Equal(t, "Lolo", e.authorName(t, e.room.ID, m.ID), "the timeline still names the author")

	// Another room with the same agent and chat: no display name, signature on.
	other := testkit.Decode[dto.Room](e.alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "other"}).RequireStatus(http.StatusCreated))
	members := testkit.Decode[dto.List[dto.RoomMember]](e.alice.Get("/api/v1/rooms/" + e.room.ID + "/members").RequireStatus(http.StatusOK)).Items
	for _, mb := range members {
		e.alice.Post("/api/v1/rooms/"+other.ID+"/members", dto.AddMemberRequest{Kind: mb.Kind, MemberID: mb.MemberID}).RequireStatus(http.StatusCreated)
	}
	m = e.agentPost(t, other.ID, "elsewhere")
	sent = e.inst.WaitSend(t)
	require.Equal(t, m.ID, sent.MessageID)
	require.Equal(t, "helper", sent.AuthorName)
	require.Equal(t, "helper", e.authorName(t, other.ID, m.ID))

	// alice operates the connector: her own account sends her messages without an author line
	// (ACC-04; TestUsersAreSignedUnlessTheyOperateTheAccount covers the users who are named),
	// though the connector still learns who wrote it.
	u := testkit.Decode[dto.Message](e.alice.Post("/api/v1/rooms/"+e.room.ID+"/messages", dto.PostMessageRequest{Text: "from alice"}).RequireStatus(http.StatusCreated))
	sent = e.inst.WaitSend(t)
	require.Equal(t, u.ID, sent.MessageID)
	require.Equal(t, "alice", sent.AuthorName)
	require.Empty(t, sent.Signature())
}

func ptr[T any](v T) *T { return &v }

// PROJ-06, PROJ-07 (MEM-01, MEM-02 for projects): the active session of a member project signs
// with its readable name by default, with the project member's display name when set, and
// without an author when the project member's signature is off.
func TestOutboundAuthorFollowsProjectMemberSettings(t *testing.T) {
	e := newOutboundEnv(t)
	ctx := context.Background()
	atlas := e.env.NewAI(e.alice, "atlas-claude")
	project := store.NewID()
	require.NoError(t, e.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateProject(ctx, db.CreateProjectParams{ID: project, OwnerID: e.alice.User.ID, Slug: "atlas", Name: "Atlas", CreatedAt: 1}); err != nil {
			return err
		}
		return q.SetAgentProject(ctx, db.SetAgentProjectParams{ProjectID: sql.NullString{String: project, Valid: true}, ID: atlas})
	}))
	session, resp := e.alice.Token(dto.TokenKindAgent, "desk").DialAgent("atlas-claude")
	require.NotNil(t, session, "upgrade refused: %d", resp.Status)
	info := session.Welcome(connectortest.Timeout).Agent
	require.Equal(t, atlas, info.ID)
	member := testkit.Decode[dto.RoomMember](e.alice.Post("/api/v1/rooms/"+e.room.ID+"/members",
		dto.AddMemberRequest{Kind: dto.MemberKindProject, MemberID: project}).RequireStatus(http.StatusCreated))
	rest := e.alice.Token(dto.TokenKindAgent, "desk-rest").AsAgent("atlas-claude")
	post := func(text string) dto.Message {
		t.Helper()
		m := testkit.Decode[dto.Message](rest.Post("/api/v1/agent/rooms/"+e.room.ID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
		require.Equal(t, info.ID, m.AuthorID)
		return m
	}

	m := post("default")
	sent := e.inst.WaitSend(t)
	require.Equal(t, m.ID, sent.MessageID)
	require.Equal(t, "atlas-claude", sent.AuthorName)

	e.alice.Patch("/api/v1/rooms/"+e.room.ID+"/members/"+member.ID, dto.UpdateMemberRequest{DisplayName: ptr("Atlas bot")}).RequireStatus(http.StatusOK)
	m = post("named")
	sent = e.inst.WaitSend(t)
	require.Equal(t, m.ID, sent.MessageID)
	require.Equal(t, "Atlas bot", sent.AuthorName)
	require.Equal(t, "Atlas bot", e.authorName(t, e.room.ID, m.ID), "the timeline shows the project member's display name")

	e.alice.Patch("/api/v1/rooms/"+e.room.ID+"/members/"+member.ID, dto.UpdateMemberRequest{SignOutbound: ptr(false)}).RequireStatus(http.StatusOK)
	m = post("unsigned")
	sent = e.inst.WaitSend(t)
	require.Equal(t, m.ID, sent.MessageID)
	require.Equal(t, "", sent.AuthorName)
}
