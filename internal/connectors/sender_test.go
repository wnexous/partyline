package connectors_test

import (
	"bytes"
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
	"github.com/wnexous/partyline/internal/testkit"
)

type outboundEnv struct {
	*inboundEnv
	chat  string
	room  dto.Room
	agent *testkit.Client // agent REST client ("helper")
}

// newOutboundEnv runs the outbox worker with a fake connector whose group chat is routed to
// alice's agent "helper" (connected).
func newOutboundEnv(t *testing.T) *outboundEnv {
	t.Helper()
	e := newInboundEnv(t, testkit.WithOutbox())
	e.emit(text(groupID, "M0", "hello"))
	chat := e.chatID(t, groupID)

	token := e.alice.Token(dto.TokenKindAgent, "laptop")
	conn, _ := token.DialAgent("helper")
	conn.Welcome(connectortest.Timeout)
	var agents dto.List[dto.Agent]
	e.alice.Get("/api/v1/agents").RequireStatus(http.StatusOK).Decode(&agents)
	require.Len(t, agents.Items, 1)
	room := testkit.Decode[dto.Room](e.alice.Post("/api/v1/chats/"+chat+"/route", dto.RouteChatRequest{AgentID: agents.Items[0].ID}).RequireStatus(http.StatusCreated))
	return &outboundEnv{inboundEnv: e, chat: chat, room: room, agent: token.WithHeader(dto.HeaderAgent, "helper")}
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
