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
	return newConnectionChanOfKind(t, nil, opts...)
}

// newConnectionChanOfKind is newConnectionChan for a variant of the fake kind: tune changes the kind
// before it is registered (a platform without a capability).
func newConnectionChanOfKind(t *testing.T, tune func(*connectors.Kind), opts ...testkit.Option) *connectionChan {
	t.Helper()
	fake := connectortest.New()
	kind := fake.Kind()
	if tune != nil {
		tune(&kind)
	}
	e := newChanEnv(t, append([]testkit.Option{testkit.WithConnectorKinds(kind)}, opts...)...)
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

// CID-22, CID-33: react works on a message that arrived on one of the AI's connections: the reaction goes
// through the platform from the account and the tool says so, and posts nothing to a room. A message of a
// connection cannot be quoted in a room (send_file with reply_to is unsupported, with the way out); an id
// the AI does not hold is still "message not found" (so another AI's messages cannot be probed).
func TestToolReactToAMessageOfAConnection(t *testing.T) {
	c := newConnectionChan(t)
	id := c.enqueue(c.group, "M1", "where is my order?")
	s := c.connected(c.options())

	text, isErr := s.call("react", toolArgs("message_id", id, "emoji", "👍"))
	require.False(t, isErr, text)
	require.Equal(t, "reacted", text)
	r := c.inst.WaitReact(t)
	require.Equal(t, []any{groupChatExternal, "M1", "5511999990000", "👍"}, []any{r.ChatID, r.TargetID, r.TargetSenderID, r.Emoji})

	text, isErr = s.call("send_file", toolArgs("room", "Support", "path", reportFile(t, "report.txt", "synthetic report"), "reply_to", id))
	require.True(t, isErr, text)
	require.Equal(t, "unsupported: a message from a connection cannot be quoted in a room; answer it with reply, or with send_message or send_file naming the connection and the chat", text)

	text, isErr = s.call("react", toolArgs("message_id", "no-such-message", "emoji", "👍"))
	require.True(t, isErr, text)
	require.Equal(t, "message not found", text)
	require.Empty(t, c.inst.Sent())
	require.Empty(t, c.messages(c.room.ID), "nothing was posted")
}

// CID-33: what the server refuses reaches the tool in its own words: more than one emoji is a field
// error, a platform that refuses the reaction (Telegram, for an emoji outside its set) a tool error with the
// reason, and a platform without reactions (the webhook) unsupported with the way out.
func TestToolReactRefusals(t *testing.T) {
	c := newConnectionChan(t)
	id := c.enqueue(c.group, "M1", "where is my order?")
	s := c.connected(c.options())

	text, isErr := s.call("react", toolArgs("message_id", id, "emoji", "👍👍"))
	require.True(t, isErr, text)
	require.Equal(t, "validation failed (emoji: must be one emoji)", text)

	c.inst.SetReactError(errors.New("setMessageReaction: emoji is not one of Telegram's allowed reactions"))
	text, isErr = s.call("react", toolArgs("message_id", id, "emoji", "🤖"))
	require.True(t, isErr, text)
	require.Equal(t, "the platform did not take the reaction: setMessageReaction: emoji is not one of Telegram's allowed reactions", text)

	webhook := newConnectionChanOfKind(t, func(k *connectors.Kind) { k.CanReact = false })
	wid := webhook.enqueue(webhook.group, "M1", "where is my order?")
	ws := webhook.connected(webhook.options())
	text, isErr = ws.call("react", toolArgs("message_id", wid, "emoji", "👍"))
	require.True(t, isErr, text)
	require.Equal(t, "unsupported: the platform of this connection does not take reactions: answer the message with reply", text)
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

// reportFile writes a file for the AI to send and returns its path.
func reportFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// CID-32: send_file with connection and chat sends a local file to a chat of a connection the AI
// operates: the platform gets the file under its name, with its type from the extension, and the caption as
// the text, unsigned; the tool says it was sent. The chat may be an address the connection never heard
// from (CID-31).
func TestToolSendFileSendsAFileThroughAConnection(t *testing.T) {
	c := newConnectionChan(t)
	s := c.connected(c.options())
	path := reportFile(t, "report.txt", "synthetic report")

	text, isErr := s.call("send_file", toolArgs("connection", c.conn, "chat", c.group.ID, "path", path, "caption", "the report"))
	require.False(t, isErr, text)
	require.Equal(t, "sent", text)
	sent := c.inst.WaitSend(t)
	require.Equal(t, []any{groupChatExternal, "the report", true}, []any{sent.ChatID, sent.Text, sent.Unsigned})
	require.Len(t, sent.Files, 1)
	require.Equal(t, []any{"report.txt", "text/plain; charset=utf-8", int64(16)}, []any{sent.Files[0].Name, sent.Files[0].Mime, sent.Files[0].Size})

	text, isErr = s.call("send_file", toolArgs("room", " ", "connection", c.conn, "chat", "5541999990000", "path", path))
	require.False(t, isErr, text)
	require.Equal(t, "sent", text)
	sent = c.inst.WaitSend(t)
	require.Equal(t, []string{"5541999990000", ""}, []string{sent.ChatID, sent.Text}, "a new address, and no caption")
	require.Empty(t, c.messages(c.room.ID), "nothing was posted to a room")
}

// CID-32: send_file still takes a room, and exactly one of room or connection with chat says where the
// file goes: both, neither, a connection or a chat alone, and a quote for a file sent through a connection
// are tool errors that read the file nowhere and send nothing.
func TestToolSendFileNeedsExactlyOneTarget(t *testing.T) {
	c := newConnectionChan(t)
	s := c.connected(c.options())
	path := reportFile(t, "report.txt", "x")

	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"a room and a connection chat": {toolArgs("room", "Support", "connection", c.conn, "chat", c.group.ID, "path", path),
			"give either room, or connection with chat, not both: room uploads the file to a room or DM you belong to, connection with chat sends it to a chat of a connection you operate"},
		"none":                        {toolArgs("path", path), "room, or connection with chat, is required: name the room to upload the file to, or the connection and the chat to send it to"},
		"blank everything":            {toolArgs("room", " ", "connection", "", "chat", " ", "path", path), "room, or connection with chat, is required: name the room to upload the file to, or the connection and the chat to send it to"},
		"a connection without chat":   {toolArgs("connection", c.conn, "path", path), halfTarget},
		"a chat without a connection": {toolArgs("chat", c.group.ID, "path", path), halfTarget},
		"a quote through a connection": {toolArgs("connection", c.conn, "chat", c.group.ID, "path", path, "reply_to", "some-message"),
			"reply_to applies to rooms: a file sent through a connection is not a quote; to answer a message from a connection with a file, give its connection and chat"},
		"no path": {toolArgs("connection", c.conn, "chat", c.group.ID), "path is required"},
	} {
		text, isErr := s.call("send_file", tc.args)
		require.True(t, isErr, "%s: %s", name, text)
		require.Equal(t, tc.want, text, name)
	}
	text, isErr := s.call("send_file", toolArgs("connection", c.conn, "chat", c.group.ID, "path", filepath.Join(t.TempDir(), "missing.txt")))
	require.True(t, isErr, text)
	require.Contains(t, text, "read ")
	require.Empty(t, c.inst.Sent())
}

// CID-03, CID-17, CID-32: what the server refuses reaches the tool in its own words with the code
// named, as for send_message: a connection another AI operates, a chat of another connection, and a
// platform without a media send (unsupported, with what to do instead).
func TestToolSendFileRefusals(t *testing.T) {
	c := newConnectionChan(t)
	s := c.connected(c.options())
	path := reportFile(t, "report.txt", "x")
	notes := c.env.NewAI(c.alice, "notes")
	theirs, theirChat := c.anotherConnection(c.alice, notes)

	for name, tc := range map[string]struct{ connection, chat, want string }{
		"a connection another AI operates": {theirs, theirChat.ID, "not_operator: only the AI that operates the connection can send through it"},
		"a chat of another connection":     {c.conn, theirChat.ID, "unknown_chat: the chat does not belong to this connection"},
		"a connection nobody has":          {"no-such-connection", c.group.ID, "not found"},
	} {
		text, isErr := s.call("send_file", toolArgs("connection", tc.connection, "chat", tc.chat, "path", path))
		require.True(t, isErr, "%s: %s", name, text)
		require.Equal(t, tc.want, text, name)
	}
	require.Empty(t, c.inst.Sent())

	webhook := newConnectionChanOfKind(t, func(k *connectors.Kind) { k.CanSendFiles = false })
	ws := webhook.connected(webhook.options())
	text, isErr := ws.call("send_file", toolArgs("connection", webhook.conn, "chat", webhook.group.ID, "path", path))
	require.True(t, isErr, text)
	require.Equal(t, "unsupported: the platform of this connection cannot be sent files: write to the chat with send_message", text)
}

// CID-32: a platform that refuses the file is a tool error with the reason, and the AI is told nothing
// will send it again; when the caption went out and the file did not, it says so.
func TestToolSendFileThePlatformRefuses(t *testing.T) {
	c := newConnectionChan(t)
	s := c.connected(c.options())
	path := reportFile(t, "report.txt", "x")

	c.inst.SetSendError(errors.New("sendDocument: error 413"))
	text, isErr := s.call("send_file", toolArgs("connection", c.conn, "chat", c.group.ID, "path", path, "caption", "the report"))
	require.True(t, isErr, text)
	require.Equal(t, "the platform did not take the file: sendDocument: error 413", text)

	c.inst.SetSendError(nil)
	c.inst.SetSendErrorAfterFirst(errors.New("sendDocument: error 413"))
	text, isErr = s.call("send_file", toolArgs("connection", c.conn, "chat", c.group.ID, "path", path, "caption", "the report"))
	require.True(t, isErr, text)
	require.Equal(t, "the platform did not take the file: sendDocument: error 413; the caption was sent, the file was not", text)
}

// CID-32: the schema of send_file names both ways to say where the file goes and requires only the path
// (the schema cannot say "exactly one of"); the description says a file through a connection is sent once.
func TestToolSendFileSchemaHasConnectionAndChat(t *testing.T) {
	c := newConnectionChan(t)
	s := c.connected(c.options())
	s.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	for _, item := range s.response()["result"].(map[string]any)["tools"].([]any) {
		tool := item.(map[string]any)
		if tool["name"] != "send_file" {
			continue
		}
		schema := tool["inputSchema"].(map[string]any)
		props := schema["properties"].(map[string]any)
		names := make([]string, 0, len(props))
		for name := range props {
			names = append(names, name)
		}
		require.ElementsMatch(t, []string{"room", "connection", "chat", "path", "caption", "reply_to"}, names)
		require.Equal(t, []any{"path"}, schema["required"])
		require.Contains(t, props["connection"].(map[string]any)["description"], "connection_id")
		require.Contains(t, props["chat"].(map[string]any)["description"], "address")
		require.Contains(t, tool["description"], "connection")
		require.Contains(t, tool["description"], "sent once")
		require.NotContains(t, tool["description"], "rooms only")
		return
	}
	t.Fatal("send_file is not listed")
}
