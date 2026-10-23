package channel

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// Platform ids of the chats of the connection of connectionChan (synthetic).
const (
	groupChatExternal = "120363000000000001@g.us"
	dmChatExternal    = "5511999990000"
)

// connectionChan is the channel fixture plus a running fake connection of alice's that the AI
// "builder" operates, with a group chat and a direct chat; the messages queued for the AI are
// those of that connection.
type connectionChan struct {
	*chanEnv
	inst  *connectortest.Instance
	conn  string
	group db.Chat
	dm    db.Chat
}

func newConnectionChan(t *testing.T, opts ...testkit.Option) *connectionChan {
	t.Helper()
	fake := connectortest.New()
	e := newChanEnv(t, append([]testkit.Option{testkit.WithConnectorKinds(fake.Kind())}, opts...)...)
	on := true
	conn := testkit.Decode[dto.Connector](e.alice.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: connectortest.KindName, Name: "wa", Config: map[string]any{"token": "fake-token-not-real"}, Enabled: &on,
	}).RequireStatus(http.StatusCreated))
	conn = e.alice.OperateConnector(conn.ID, e.agentID)
	inst := fake.Instance(t, conn.ID)
	inst.WaitRun(t)
	c := &connectionChan{chanEnv: e, inst: inst, conn: conn.ID}
	c.group = c.chat(groupChatExternal, "Team", "group")
	c.dm = c.chat(dmChatExternal, "Pat", "dm")
	return c
}

// chat records a chat of the connection.
func (c *connectionChan) chat(external, title, kind string) db.Chat {
	c.t.Helper()
	chat, err := connectors.UpsertChat(context.Background(), c.env.App.Store, c.env.App.Bus, c.conn,
		connectors.ChatInfo{ID: external, Title: title, Kind: kind}, time.Now(), time.Now())
	require.NoError(c.t, err)
	return chat
}

// enqueue queues a message of the chat for the AI and returns the item's id.
func (c *connectionChan) enqueue(chat db.Chat, external, text string) string {
	c.t.Helper()
	id, queued, err := c.env.App.Platform.Enqueue(context.Background(), platform.Item{
		AgentID: c.agentID, ConnectorID: c.conn, ChatID: chat.ID, ExternalID: external, SenderID: "5511999990000", SenderName: "Pat", Text: text,
	})
	require.NoError(c.t, err)
	require.True(c.t, queued)
	return id
}

// CID-15: the reply tool answers a message that arrived on the AI's connection: the text goes to its
// chat through the connection, unsigned, quoting the message, and the tool says it was sent.
func TestToolReplyToAPlatformMessageSendsThroughTheConnection(t *testing.T) {
	c := newConnectionChan(t)
	id := c.enqueue(c.group, "M1", "where is my order?")
	s := c.connected(c.options())

	text, isErr := s.call("reply", toolArgs("message_id", id, "text", "on its way"))
	require.False(t, isErr, text)
	require.Equal(t, "sent", text)
	sent := c.inst.WaitSend(t)
	require.Equal(t, []any{groupChatExternal, "on its way", true, "M1"}, []any{sent.ChatID, sent.Text, sent.Unsigned, sent.ReplyToID})
}

// CID-18: when the platform refuses, the tool answers with the reason and says that Partyline
// retries; that is no tool error, because the message is not lost and the AI must not send it again.
func TestToolReplyToAPlatformMessageThatIsNotSentYet(t *testing.T) {
	c := newConnectionChan(t)
	id := c.enqueue(c.dm, "M2", "oi")
	s := c.connected(c.options())
	c.inst.SetSendError(errors.New("platform unavailable"))

	text, isErr := s.call("reply", toolArgs("message_id", id, "text", "oi, tudo bem?"))
	require.False(t, isErr, text)
	require.Equal(t, "not sent yet: platform unavailable; partyline retries", text)
	require.Empty(t, c.inst.Sent())
}

// An id that is neither a message of a room nor one of the AI's connection stays what it was: the
// tool reports "message not found".
func TestToolReplyToAnUnknownMessageIsStillNotFound(t *testing.T) {
	c := newConnectionChan(t)
	s := c.connected(c.options())
	text, isErr := s.call("reply", toolArgs("message_id", "unknown", "text", "x"))
	require.True(t, isErr)
	require.Equal(t, "message not found", text)
}
