package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// chatIn adds to the room a chat of a new connector of alice's with the given platform (the
// connector kind), title and chat kind, and returns the chat id.
func (e *chanEnv) chatIn(platform, title string, kind dto.ChatKind) string {
	e.t.Helper()
	conn, chat := store.NewID(), store.NewID()
	ctx := context.Background()
	require.NoError(e.t, e.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateConnector(ctx, db.CreateConnectorParams{ID: conn, OwnerID: e.alice.User.ID, Kind: platform, Name: platform, ConfigJson: "{}", Enabled: true}); err != nil {
			return err
		}
		return q.CreateChat(ctx, db.CreateChatParams{ID: chat, ConnectorID: conn, ExternalID: "ext-" + chat, Title: title, Kind: string(kind)})
	}))
	e.alice.SeedChatMember(e.room.ID, chat)
	return chat
}

func (e *chanEnv) fromChat(chatID, senderID, senderName, text string) rooms.Message {
	e.t.Helper()
	m, err := e.env.App.Rooms.PostFromChat(context.Background(), e.room.ID, rooms.ChatMessage{
		ChatID: chatID, SenderID: senderID, SenderName: senderName, ExternalID: "x-" + store.NewID(), Text: text,
	})
	require.NoError(e.t, err)
	return m
}

// CTX-01, CTX-02: backlog and live pushes of chat messages name the chat, its kind and
// platform and the sender; agent and user messages carry none of those keys.
func TestPushCarriesChatContext(t *testing.T) {
	e := newChanEnv(t)
	group := e.chatIn("whatsapp", "Lollerie", dto.ChatKindGroup)
	dm := e.chatIn("telegram", "Bob Smith", dto.ChatKindDM)
	helper := e.env.NewAI(e.alice, "helper")
	e.alice.Post("/api/v1/rooms/"+e.room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: helper}).RequireStatus(http.StatusCreated)

	backlog := e.fromChat(group, "5511999990000@s.whatsapp.net", "Grandma", "from the group")
	s := e.connected(e.options())
	n := s.notification()
	require.Equal(t, "from the group", n.Content)
	require.Equal(t, map[string]string{
		"conversation": "group", "room": e.room.ID, "room_name": "Support", "message_id": backlog.ID, "author": "Grandma",
		"author_kind": "chat", "sender_name": "Grandma", "trusted": "false", "kind": "text",
		"chat_id": group, "chat_title": "Lollerie", "chat_kind": "group", "platform": "whatsapp",
		"sender_id": "5511999990000@s.whatsapp.net",
	}, n.Meta)

	live := e.fromChat(dm, "4242", "Bob", "hi from telegram")
	n = s.notification()
	require.Equal(t, map[string]string{
		"conversation": "group", "room": e.room.ID, "room_name": "Support", "message_id": live.ID, "author": "Bob",
		"author_kind": "chat", "sender_name": "Bob", "trusted": "false", "kind": "text",
		"chat_id": dm, "chat_title": "Bob Smith", "chat_kind": "dm", "platform": "telegram", "sender_id": "4242",
	}, n.Meta)

	user := e.post("from alice")
	n = s.notification()
	require.Equal(t, map[string]string{
		"conversation": "group", "room": e.room.ID, "room_name": "Support", "message_id": user.ID, "author": "alice",
		"author_kind": "user", "author_id": e.alice.User.ID, "author_handle": "alice", "sender_name": "", "trusted": "true", "kind": "text",
	}, n.Meta)

	agent := testkit.Decode[dto.Message](e.alice.WithBearer(e.secret).AsAgent("helper").
		Post("/api/v1/agent/rooms/"+e.room.ID+"/messages", dto.PostMessageRequest{Text: "from helper"}).RequireStatus(http.StatusCreated))
	n = s.notification()
	// helper is another AI of alice's, not someone who manages builder: its post is untrusted (POS-10).
	require.Equal(t, map[string]string{
		"conversation": "group", "room": e.room.ID, "room_name": "Support", "message_id": agent.ID, "author": "helper",
		"author_kind": "agent", "author_id": helper, "author_handle": "helper", "sender_name": "", "trusted": "false", "kind": "text",
	}, n.Meta)
}

// CTX-03: history items of chat messages carry the same chat fields as the push.
func TestHistoryCarriesChatContext(t *testing.T) {
	e := newChanEnv(t)
	group := e.chatIn("discord", "Guild #general", dto.ChatKindChannel)
	m := e.fromChat(group, "998877", "carol", "hello channel")
	user := e.post("hello room")
	s := e.connected(e.options())

	text, isErr := s.call("history", toolArgs("room", e.room.ID))
	require.False(t, isErr, text)
	var page struct {
		Messages []map[string]string `json:"messages"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &page))
	require.Len(t, page.Messages, 2)
	require.Equal(t, map[string]string{
		"conversation": "group", "room": e.room.ID, "room_name": "Support", "message_id": m.ID, "author": "carol",
		"author_kind": "chat", "sender_name": "carol", "trusted": "false", "kind": "text",
		"chat_id": group, "chat_title": "Guild #general", "chat_kind": "channel", "platform": "discord",
		"sender_id": "998877", "content": "hello channel",
	}, page.Messages[0])
	require.Equal(t, map[string]string{
		"conversation": "group", "room": e.room.ID, "room_name": "Support", "message_id": user.ID, "author": "alice",
		"author_kind": "user", "author_id": e.alice.User.ID, "author_handle": "alice", "sender_name": "", "trusted": "true", "kind": "text", "content": "hello room",
	}, page.Messages[1])

	// The room timeline renders the same chat reference (web, ChatRef).
	timeline := e.messages(e.room.ID)
	require.Equal(t, &dto.ChatRef{ID: group, Title: "Guild #general", Kind: dto.ChatKindChannel, Platform: "discord"}, timeline[1].Chat)
	require.Nil(t, timeline[0].Chat)
}
