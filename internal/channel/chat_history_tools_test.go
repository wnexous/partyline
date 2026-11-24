package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/platform"
)

// keepHistory turns the chat history of the connection on for days, as its owner does (CH-01).
func (c *connectionChan) keepHistory(days int64) {
	c.t.Helper()
	c.alice.Patch("/api/v1/connectors/"+c.conn, dto.UpdateConnectorRequest{HistoryDays: &days}).RequireStatus(http.StatusOK)
}

// kept records a message of Pat's in the history of the chat.
func (c *connectionChan) kept(chatID, external, text string, at time.Time) {
	c.t.Helper()
	kept, err := c.env.App.History.Record(context.Background(), platform.HistoryMessage{
		ConnectorID: c.conn, ChatID: chatID, ExternalID: external, SenderID: "5511999990000", SenderName: "Pat", Text: text, At: at,
	})
	require.NoError(c.t, err)
	require.True(c.t, kept)
}

// CH-05, CH-06: history with connection and chat reads the chat's history, oldest first, paged with
// before; without history it says history_off and that the owner turns it on; a room and a connection
// together, or half a connection, are refused as for send_message.
func TestToolHistoryReadsAChatOfAConnection(t *testing.T) {
	c := newConnectionChan(t)
	s := c.connected(c.options())

	text, isErr := s.call("history", toolArgs("connection", c.conn, "chat", c.dm.ID))
	require.True(t, isErr)
	require.Contains(t, text, "chat history is off for this connection: an admin of its organization can turn it on in the connection's settings")

	c.keepHistory(30)
	base := time.Now().Add(-time.Hour)
	c.kept(c.dm.ID, "M1", "first", base)
	c.kept(c.dm.ID, "M2", "second", base.Add(time.Minute))
	c.kept(c.dm.ID, "M3", "third", base.Add(2*time.Minute))

	text, isErr = s.call("history", toolArgs("connection", c.conn, "chat", dmChatExternal, "limit", 2, "room", ""))
	require.False(t, isErr, text)
	var page dto.ChatHistory
	require.NoError(t, json.Unmarshal([]byte(text), &page))
	require.Equal(t, []any{"Pat", "second", "third", true}, []any{page.ChatTitle, page.Messages[0].Text, page.Messages[1].Text, page.HasMore})
	require.Equal(t, "Pat", page.Messages[0].From)

	text, isErr = s.call("history", toolArgs("connection", c.conn, "chat", c.dm.ID, "before", page.Messages[0].MessageID))
	require.False(t, isErr, text)
	require.NoError(t, json.Unmarshal([]byte(text), &page))
	require.Len(t, page.Messages, 1)
	require.Equal(t, "first", page.Messages[0].Text)
	require.False(t, page.HasMore)

	text, isErr = s.call("history", toolArgs("connection", c.conn))
	require.True(t, isErr)
	require.Equal(t, halfTarget, text)
	text, isErr = s.call("history", toolArgs("room", c.room.ID, "connection", c.conn, "chat", c.dm.ID))
	require.True(t, isErr)
	require.Contains(t, text, "give room, or connection with chat")
	text, isErr = s.call("history", toolArgs())
	require.True(t, isErr)
	require.Contains(t, text, "give room, or connection with chat")
}

// CH-08: fetch_history asks the platform for older messages of the chat and says they come later into
// the history; the count is passed on.
func TestToolFetchHistoryAsksThePlatform(t *testing.T) {
	c := newConnectionChan(t)
	old := c.inst
	c.keepHistory(-1)
	// Turning the history on restarts the connection (CH-10).
	require.Eventually(t, func() bool { return c.fake.Lookup(c.conn) != old }, wait, 5*time.Millisecond)
	c.inst = c.fake.Lookup(c.conn)
	c.inst.WaitRun(t)
	c.kept(c.group.ID, "G1", "hello", time.Now())
	s := c.connected(c.options())

	text, isErr := s.call("fetch_history", toolArgs("connection", c.conn, "chat", c.group.ID, "count", 20))
	require.False(t, isErr, text)
	require.Equal(t, "asked the platform for up to 20 older messages: they are added to the chat's history as the phone sends them, usually within a minute; read them with history", text)
	require.Eventually(t, func() bool { return len(c.fake.Lookup(c.conn).Fetches()) == 1 }, wait, 10*time.Millisecond)
	got := c.fake.Lookup(c.conn).Fetches()[0]
	require.Equal(t, []any{groupChatExternal, "G1", 20}, []any{got.ChatID, got.AnchorID, got.Count})

	text, isErr = s.call("fetch_history", toolArgs("connection", c.conn))
	require.True(t, isErr)
	require.Equal(t, "chat is required", text)
}
