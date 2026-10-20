package connectors_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/testkit"
)

// REACT-01: an agent's reaction to a chat message is stored as a reaction message for that
// chat only and reaches the connector's Reactor with the chat's platform id, the target's
// platform id and sender, and the emoji; nothing is sent as text.
func TestAgentReactionReachesReactor(t *testing.T) {
	e := newOutboundEnv(t)
	e.emit(text(groupID, "M1", "my order arrived"))
	msgs := e.messages(t, e.room.ID)
	require.Len(t, msgs, 1)

	r := testkit.Decode[dto.Message](e.agent.Post("/api/v1/agent/messages/"+msgs[0].ID+"/react", dto.ReactRequest{Emoji: "👍"}).RequireStatus(http.StatusCreated))
	require.Equal(t, dto.MessageKindReaction, r.Kind)
	require.Equal(t, "👍", r.Text)
	require.Equal(t, msgs[0].ID, r.ReplyTo)
	require.Equal(t, []string{e.chat}, r.Recipients)
	require.Equal(t, dto.AuthorKindAgent, r.AuthorKind)

	got := e.inst.WaitReact(t)
	require.Equal(t, connectors.Reaction{
		ChatID: groupID, TargetID: "M1", TargetSenderID: senderID, Emoji: "👍",
		MessageID: r.ID, RoomID: e.room.ID, AuthorName: "helper", CreatedAt: r.CreatedAt,
	}, got)
	item := e.outbox(t, r.ID, func(o dto.OutboxItem) bool { return o.Status == dto.OutboxStatusSent })
	require.Equal(t, 1, item.Attempts)
	require.Empty(t, e.inst.Sent(), "a reaction is not sent as a text message")
}

// REACT-01: a platform without reactions fails the delivery with "reactions not supported".
func TestReactionWithoutReactorFails(t *testing.T) {
	e := newOutboundEnv(t)
	e.emit(text(groupID, "M1", "hello"))
	target := e.messages(t, e.room.ID)[0]

	// Restart the connector as a platform without reactions.
	e.fake.WithoutReactions()
	off, on := false, true
	e.alice.Patch("/api/v1/connectors/"+e.conn.ID, dto.UpdateConnectorRequest{Enabled: &off}).RequireStatus(http.StatusOK)
	e.inst.WaitStop(t)
	e.alice.Patch("/api/v1/connectors/"+e.conn.ID, dto.UpdateConnectorRequest{Enabled: &on}).RequireStatus(http.StatusOK)
	require.Eventually(t, func() bool { return e.fake.Lookup(e.conn.ID) != e.inst }, connectortest.Timeout, 10*time.Millisecond)
	inst := e.fake.Lookup(e.conn.ID)
	inst.WaitRun(t)

	r := testkit.Decode[dto.Message](e.agent.Post("/api/v1/agent/messages/"+target.ID+"/react", dto.ReactRequest{Emoji: "❤️"}).RequireStatus(http.StatusCreated))
	item := e.outbox(t, r.ID, func(o dto.OutboxItem) bool { return o.Attempts >= 1 })
	require.Equal(t, "reactions not supported", item.LastError)
	require.Empty(t, inst.Sent())
}

// REACT-01: a target without a platform id in the chat cannot get a platform reaction; the
// delivery fails naming the target instead of reacting to something else.
func TestReactionWithoutTargetPlatformIDFails(t *testing.T) {
	e := newOutboundEnv(t)
	target, err := e.env.App.Rooms.PostFromChat(context.Background(), e.room.ID, rooms.ChatMessage{ChatID: e.chat, SenderID: senderID, Text: "no platform id"})
	require.NoError(t, err)

	r := testkit.Decode[dto.Message](e.agent.Post("/api/v1/agent/messages/"+target.ID+"/react", dto.ReactRequest{Emoji: "👍"}).RequireStatus(http.StatusCreated))
	item := e.outbox(t, r.ID, func(o dto.OutboxItem) bool { return o.Attempts >= 1 })
	require.Equal(t, "reaction target is not a message of this chat", item.LastError)
}
