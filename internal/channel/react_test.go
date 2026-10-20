package channel

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// REACT-01: react stores a reaction to the chat message, for that chat only.
func TestToolReact(t *testing.T) {
	e := newChanEnv(t)
	family := e.chatMember("Family")
	e.chatMember("Work")
	target := e.fromChat(family, "5511999990000", "Grandma", "dinner at seven")
	s := e.connected(e.options())

	text, isErr := s.call("react", toolArgs("message_id", target.ID, "emoji", "👍"))
	require.False(t, isErr, text)
	stored := e.messages(e.room.ID)[0]
	require.Equal(t, "reacted (message_id "+stored.ID+")", text)
	require.Equal(t, dto.MessageKindReaction, stored.Kind)
	require.Equal(t, "👍", stored.Text)
	require.Equal(t, target.ID, stored.ReplyTo)
	require.Equal(t, []string{family}, stored.Recipients)
	require.Equal(t, e.agentID, stored.AuthorID)
}

// REACT-02: reacting to a message that did not come from a chat is a tool error.
func TestToolReactNonChatMessage(t *testing.T) {
	e := newChanEnv(t)
	e.chatMember("Family")
	m := e.post("from alice")
	s := e.connected(e.options())
	require.Equal(t, m.ID, s.notification().Meta["message_id"])

	text, isErr := s.call("react", toolArgs("message_id", m.ID, "emoji", "👍"))
	require.True(t, isErr)
	require.Equal(t, "can only react to chat messages", text)
	require.Len(t, e.messages(e.room.ID), 1, "nothing stored")
}

// The instructions name the react tool.
func TestInstructionsNameReact(t *testing.T) {
	require.Contains(t, Instructions, "react (message_id, one emoji)")
}
