package channel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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

// The refusals of send_message about where a message goes, as the tool words them: more than one
// target, none, and a connection or a chat on its own (CID-16).
const (
	severalTargets = `give exactly one of room, to, or connection with chat, not several: room posts in a room or DM you belong to, to messages an intelligence directly, connection with chat writes to a chat of a connection you operate`
	noTarget       = `room, to, or connection with chat is required: name the room to post in, the @handle of the intelligence to message, or the connection and the chat to write to`
	halfTarget     = `connection and chat go together: give both (the connection_id and the chat_id of a message from your connection), or neither`
)

// anotherConnection creates a fake connection of the user, operated by the AI, with a group chat of its
// own, and returns its id and the chat.
func (c *connectionChan) anotherConnection(owner *testkit.Client, operator string) (string, db.Chat) {
	c.t.Helper()
	on := true
	conn := testkit.Decode[dto.Connector](owner.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: connectortest.KindName, Name: "other", Config: map[string]any{"token": "fake-token-not-real"}, Enabled: &on,
	}).RequireStatus(http.StatusCreated))
	conn = owner.OperateConnector(conn.ID, operator)
	chat, err := connectors.UpsertChat(context.Background(), c.env.App.Store, c.env.App.Bus, conn.ID,
		connectors.ChatInfo{ID: "-1001234567890", Title: "Other group", Kind: "group"}, time.Now(), time.Now())
	require.NoError(c.t, err)
	return conn.ID, chat
}

// CID-16: send_message with connection and chat writes to a chat of a connection the AI operates: the
// text goes to the chat's platform id through the connection, unsigned and without a quote, and the
// tool says it was sent. A direct chat as well as a group; an empty or blank room or to counts as not
// given, as models that send every parameter do.
func TestToolSendMessageWritesToAChatOfAConnection(t *testing.T) {
	c := newConnectionChan(t)
	s := c.connected(c.options())

	text, isErr := s.call("send_message", toolArgs("connection", c.conn, "chat", c.group.ID, "text", "good morning, team"))
	require.False(t, isErr, text)
	require.Equal(t, "sent", text)
	sent := c.inst.WaitSend(t)
	require.Equal(t, []any{groupChatExternal, "good morning, team", true, "", ""},
		[]any{sent.ChatID, sent.Text, sent.Unsigned, sent.Signature(), sent.ReplyToID})

	text, isErr = s.call("send_message", toolArgs("room", "", "to", "  ", "connection", c.conn, "chat", c.dm.ID, "text", "oi, Pat"))
	require.False(t, isErr, text)
	require.Equal(t, "sent", text)
	sent = c.inst.WaitSend(t)
	require.Equal(t, []string{dmChatExternal, "oi, Pat"}, []string{sent.ChatID, sent.Text})
	require.Empty(t, c.messages(c.room.ID), "nothing was posted to the room")
}

// CID-16: exactly one of room, to, or connection with chat says where the message goes. Several, or
// none, or a connection or a chat alone, is a tool error that sends and posts nothing; the text is
// still required.
func TestToolSendMessageNeedsExactlyOneTarget(t *testing.T) {
	c := newConnectionChan(t)
	s := c.connected(c.options())

	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"room and a connection chat":     {toolArgs("room", "Support", "connection", c.conn, "chat", c.group.ID, "text", "x"), severalTargets},
		"to and a connection chat":       {toolArgs("to", "@alice", "connection", c.conn, "chat", c.group.ID, "text", "x"), severalTargets},
		"room, to and a connection chat": {toolArgs("room", "Support", "to", "@alice", "connection", c.conn, "chat", c.group.ID, "text", "x"), severalTargets},
		"room and to":                    {toolArgs("room", "Support", "to", "@alice", "text", "x"), severalTargets},
		"none":                           {toolArgs("text", "x"), noTarget},
		"blank everything":               {toolArgs("room", " ", "to", "", "connection", " ", "chat", "", "text", "x"), noTarget},
		"a connection without a chat":    {toolArgs("connection", c.conn, "text", "x"), halfTarget},
		"a chat without a connection":    {toolArgs("chat", c.group.ID, "text", "x"), halfTarget},
		"a room and a lone connection":   {toolArgs("room", "Support", "connection", c.conn, "text", "x"), halfTarget},
		"a to and a lone chat":           {toolArgs("to", "@alice", "chat", c.group.ID, "text", "x"), halfTarget},
	} {
		text, isErr := s.call("send_message", tc.args)
		require.True(t, isErr, "%s: %s", name, text)
		require.Equal(t, tc.want, text, name)
	}
	text, isErr := s.call("send_message", toolArgs("connection", c.conn, "chat", c.group.ID))
	require.True(t, isErr, text)
	require.Equal(t, "text is required", text)
	require.Empty(t, c.inst.Sent())
	require.Empty(t, c.messages(c.room.ID))
}

// CID-22: chat_ids, and recipients, its older name, are accepted and ignored whatever the target:
// the message goes where room, to, or connection and chat say, to everyone there and to no chat in
// particular.
func TestToolSendMessageStillAcceptsChatIDsAndIgnoresThem(t *testing.T) {
	c := newConnectionChan(t)
	s := c.connected(c.options())
	ignored := map[string]any{"chat_ids": []string{"no-such-chat"}, "recipients": []string{"nor-this"}}
	with := func(kv ...any) map[string]any {
		args := toolArgs(kv...)
		for k, v := range ignored {
			args[k] = v
		}
		return args
	}

	text, isErr := s.call("send_message", with("room", "Support", "text", "to the room"))
	require.False(t, isErr, text)
	stored := c.messages(c.room.ID)[0]
	require.Equal(t, []any{"to the room", c.agentID}, []any{stored.Text, stored.AuthorID})
	require.Empty(t, stored.Recipients, "the post names no chat")

	text, isErr = s.call("send_message", with("connection", c.conn, "chat", c.dm.ID, "text", "to the chat"))
	require.False(t, isErr, text)
	require.Equal(t, "sent", text)
	sent := c.inst.WaitSend(t)
	require.Equal(t, []string{dmChatExternal, "to the chat"}, []string{sent.ChatID, sent.Text})
}

// CID-22: the schema of send_message names the three ways to say where a message goes and no longer
// has chat_ids (nor recipients, its older name); the text is the only required argument, because the
// schema cannot say "exactly one of"; connection and chat tell where their values come from.
func TestToolSendMessageSchemaHasNoChatIDs(t *testing.T) {
	c := newConnectionChan(t)
	s := c.connected(c.options())
	s.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	found := false
	for _, item := range s.response()["result"].(map[string]any)["tools"].([]any) {
		tool := item.(map[string]any)
		if tool["name"] != "send_message" {
			continue
		}
		found = true
		schema := tool["inputSchema"].(map[string]any)
		props := schema["properties"].(map[string]any)
		names := make([]string, 0, len(props))
		for name := range props {
			names = append(names, name)
		}
		require.ElementsMatch(t, []string{"room", "to", "connection", "chat", "text"}, names)
		require.Equal(t, []any{"text"}, schema["required"])
		require.Contains(t, props["connection"].(map[string]any)["description"], "connection_id")
		require.Contains(t, props["chat"].(map[string]any)["description"], "chat_id")
		require.Contains(t, tool["description"], "exactly one")
	}
	require.True(t, found, "send_message is listed")
}

// CID-03, CID-17, CID-18: what the server refuses reaches the tool in its own words with the code
// named, so that the AI can tell the refusals apart: a connection of its owner that another AI
// operates is not_operator, a chat of another connection is unknown_chat, and a connection its owner
// cannot see is plain "not found", as if it did not exist. A platform that does not take the message
// at once is no error: the tool says Partyline retries.
func TestToolSendMessageToAConnectionChatRefusals(t *testing.T) {
	c := newConnectionChan(t)
	s := c.connected(c.options())
	notes := c.env.NewAI(c.alice, "notes")
	theirs, theirChat := c.anotherConnection(c.alice, notes)
	bob := c.env.NewUser("bob")
	hidden, hiddenChat := c.anotherConnection(bob, c.env.NewAI(bob, "bobai"))

	for name, tc := range map[string]struct {
		connection, chat, want string
	}{
		"a connection another AI of the owner operates": {theirs, theirChat.ID, "not_operator: only the AI that operates the connection can send through it"},
		"a chat of another connection":                  {c.conn, theirChat.ID, "unknown_chat: the chat does not belong to this connection"},
		"a chat nobody has":                             {c.conn, "no-such-chat", "unknown_chat: the chat does not belong to this connection"},
		"a connection the owner cannot see":             {hidden, hiddenChat.ID, "not found"},
		"a connection nobody has":                       {"no-such-connection", c.group.ID, "not found"},
	} {
		text, isErr := s.call("send_message", toolArgs("connection", tc.connection, "chat", tc.chat, "text", "x"))
		require.True(t, isErr, "%s: %s", name, text)
		require.Equal(t, tc.want, text, name)
	}
	require.Empty(t, c.inst.Sent())

	c.inst.SetSendError(errors.New("platform unavailable"))
	text, isErr := s.call("send_message", toolArgs("connection", c.conn, "chat", c.group.ID, "text", "good morning"))
	require.False(t, isErr, text)
	require.Equal(t, "not sent yet: platform unavailable; partyline retries", text)
}

// CID-22: react and send_file work in rooms only: given a message that arrived on one of the AI's
// connections they answer unsupported, with the way out, and do nothing; an id the AI does not hold
// is still "message not found" (so another AI's messages cannot be probed).
func TestToolReactAndSendFileOnAMessageOfAConnectionAreUnsupported(t *testing.T) {
	c := newConnectionChan(t)
	id := c.enqueue(c.group, "M1", "where is my order?")
	s := c.connected(c.options())
	file := filepath.Join(t.TempDir(), "report.txt")
	require.NoError(t, os.WriteFile(file, []byte("synthetic report"), 0o600))

	text, isErr := s.call("react", toolArgs("message_id", id, "emoji", "👍"))
	require.True(t, isErr, text)
	require.Equal(t, "unsupported: reactions work on room messages only; a message from a connection cannot get one, answer it with reply", text)

	text, isErr = s.call("send_file", toolArgs("room", "Support", "path", file, "reply_to", id))
	require.True(t, isErr, text)
	require.Equal(t, "unsupported: a message from a connection cannot be quoted in a room (files go to rooms only); answer it with reply, or with send_message naming the connection and the chat", text)

	text, isErr = s.call("react", toolArgs("message_id", "no-such-message", "emoji", "👍"))
	require.True(t, isErr, text)
	require.Equal(t, "message not found", text)
	require.Empty(t, c.inst.Sent())
	require.Empty(t, c.messages(c.room.ID), "nothing was posted")
}

// CID-31: send_message with connection and, as chat, the address of someone the connection never
// heard from starts the chat: the text goes to that address through the connection, unsigned, and the
// tool says it was sent. What is no address stays an unknown chat, and the 11th chat started in an
// hour is the server's "rate limited" in its own words.
func TestToolSendMessageStartsAChatAtAnAddress(t *testing.T) {
	c := newConnectionChan(t)
	s := c.connected(c.options())

	text, isErr := s.call("send_message", toolArgs("connection", c.conn, "chat", "5541999990000", "text", "good morning"))
	require.False(t, isErr, text)
	require.Equal(t, "sent", text)
	sent := c.inst.WaitSend(t)
	require.Equal(t, []any{"5541999990000", "good morning", true}, []any{sent.ChatID, sent.Text, sent.Unsigned})

	text, isErr = s.call("send_message", toolArgs("connection", c.conn, "chat", "hello", "text", "x"))
	require.True(t, isErr, text)
	require.Equal(t, "unknown_chat: the chat does not belong to this connection", text)

	for i := range 9 {
		text, isErr = s.call("send_message", toolArgs("connection", c.conn, "chat", fmt.Sprintf("55429999900%02d", i), "text", "good morning"))
		require.False(t, isErr, text)
	}
	text, isErr = s.call("send_message", toolArgs("connection", c.conn, "chat", "5541999990099", "text", "one too many"))
	require.True(t, isErr, text)
	require.Equal(t, "rate limited: at most 10 new chats an hour", text)
	text, isErr = s.call("send_message", toolArgs("connection", c.conn, "chat", "5541999990000", "text", "an address it wrote to is free"))
	require.False(t, isErr, text)
	require.Len(t, c.inst.Sent(), 11)
}

// CID-31: the tool says that chat may also be the address of a chat the AI has not heard from, which
// the schema of the tool tells where it comes from: the platform's own address, and what it is for
// each platform.
func TestToolSendMessageTellsHowToStartAChat(t *testing.T) {
	c := newConnectionChan(t)
	s := c.connected(c.options())
	s.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	for _, item := range s.response()["result"].(map[string]any)["tools"].([]any) {
		tool := item.(map[string]any)
		if tool["name"] != "send_message" {
			continue
		}
		chat := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)["chat"].(map[string]any)["description"].(string)
		for _, want := range []string{"chat_id", "address", "WhatsApp", "country code", "Telegram chat id", "Discord channel id", "10"} {
			require.Contains(t, chat, want)
		}
		require.Contains(t, tool["description"], "never wrote to you", "the description says a conversation can be started")
		return
	}
	t.Fatal("send_message is not listed")
}
