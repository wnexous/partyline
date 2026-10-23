package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/testkit"
)

func reactPath(messageID string) string { return "/api/v1/agent/messages/" + messageID + "/react" }

// REACT-01: a reaction to a chat message is stored as a reaction message (text = emoji,
// reply_to = target) in the room, and goes to no chat: a room sends nothing to a platform (CID-22).
func TestAgentReactStoresReaction(t *testing.T) {
	f := newAgentRoom(t)
	family := f.chatMember(t, "Family")
	f.chatMember(t, "Work")
	target := f.postFromChat(t, family, "thanks!")

	for _, emoji := range []string{"👍", " ❤️ ", "🇧🇷", "1️⃣", "👩‍👩‍👧‍👦"} {
		r := testkit.Decode[dto.Message](f.rest().Post(reactPath(target.ID), dto.ReactRequest{Emoji: emoji}).RequireStatus(http.StatusCreated))
		require.Equal(t, dto.MessageKindReaction, r.Kind, emoji)
		require.Equal(t, dto.AuthorKindAgent, r.AuthorKind)
		require.Equal(t, f.agentID, r.AuthorID)
		require.Equal(t, target.ID, r.ReplyTo)
		require.Empty(t, r.Recipients)
		require.Equal(t, strings.TrimSpace(emoji), r.Text)
	}
}

// REACT-02: reactions to messages that did not come from a chat answer 422 "can only react
// to chat messages" and store nothing.
func TestAgentReactRefusesNonChatMessages(t *testing.T) {
	f := newAgentRoom(t)
	f.chatMember(t, "Family")
	fromUser := f.post(f.alice, "hello")
	fromAgent := f.postAsAgent(t, "build is green")
	for _, id := range []string{fromUser.ID, fromAgent.ID} {
		e := f.rest().Post(reactPath(id), dto.ReactRequest{Emoji: "👍"}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Equal(t, "can only react to chat messages", e.Message)
	}
	page := testkit.Decode[dto.Page[dto.Message]](f.alice.Get("/api/v1/rooms/" + f.room.ID + "/messages").RequireStatus(http.StatusOK))
	require.Len(t, page.Items, 2)
}

// REACT-01, REACT-05: the emoji must be one emoji grapheme: empty, words, digits or
// punctuation alone, other symbols, text-style symbols without VS16, ZWJ chains of more than
// four emoji and several emoji answer 422 on emoji.
func TestAgentReactValidatesEmoji(t *testing.T) {
	f := newAgentRoom(t)
	family := f.chatMember(t, "Family")
	target := f.postFromChat(t, family, "thanks!")
	for _, emoji := range []string{"", "  ", "ok", "👍 👍", "👍👍👍👍👍👍👍👍👍👍👍👍👍👍👍👍👍", "1", "!", "<>", "∑", "👍👍", "©", "❤", "👨‍👩‍👧‍👦‍👦"} {
		e := f.rest().Post(reactPath(target.ID), dto.ReactRequest{Emoji: emoji}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Equal(t, map[string]string{"emoji": "must be one emoji"}, e.Fields, "%q", emoji)
	}
}

// REACT-01: unknown messages and messages of rooms without the agent answer 404.
func TestAgentReactUnknownMessage(t *testing.T) {
	f := newAgentRoom(t)
	other := createRoom(f.alice, "Private")
	_, chat := storeChat(t, f.env, f.alice.User.ID, "Elsewhere")
	f.alice.SeedChatMember(other.ID, chat)
	foreign := postFromChatIn(t, f, other.ID, chat, "not for the agent")
	for _, id := range []string{"unknown", foreign} {
		e := f.rest().Post(reactPath(id), dto.ReactRequest{Emoji: "👍"}).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		require.Equal(t, "message not found", e.Message)
	}
}

// TOOL-08: reactions share the agent's 30 per minute budget.
func TestAgentReactRateLimit(t *testing.T) {
	f := newAgentRoom(t)
	family := f.chatMember(t, "Family")
	target := f.postFromChat(t, family, "hi")
	for i := 1; i <= 30; i++ {
		f.rest().Post(agentRoomPath(f.room.ID, "/messages"), dto.PostMessageRequest{Text: fmt.Sprintf("post %d", i)}).RequireStatus(http.StatusCreated)
	}
	f.rest().Post(reactPath(target.ID), dto.ReactRequest{Emoji: "👍"}).RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
}

func postFromChatIn(t *testing.T, f *agentRoom, roomID, chatID, text string) string {
	t.Helper()
	m, err := f.env.App.Rooms.PostFromChat(context.Background(), roomID, rooms.ChatMessage{ChatID: chatID, SenderID: "1", Text: text})
	require.NoError(t, err)
	return m.ID
}
