package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors/webhook"
	"github.com/wnexous/partyline/internal/testkit"
)

// REACT-01 (webhook): an agent's reaction to a webhook chat message is POSTed to the outbound
// URL as a signed "reaction" event naming the chat, the target's platform id and the emoji.
func TestWebhookOutboundReaction(t *testing.T) {
	e := newHookEnv(t)
	e.post(t, e.secret, time.Now(), payload(t, map[string]any{"chat_id": "deploys", "sender_id": "ci", "text": "hi"})).
		RequireStatus(http.StatusAccepted)
	room := e.roomWith(t, e.chats(t)[0].ID)
	agentID := registerAgent(t, e.env, e.alice, "builder")
	e.alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: agentID}).RequireStatus(http.StatusCreated)
	e.post(t, e.secret, time.Now(), payload(t, map[string]any{"chat_id": "deploys", "sender_id": "ci", "text": "deployed", "message_id": "in-7"})).
		RequireStatus(http.StatusAccepted)
	target := e.messages(t, room.ID)[0]
	require.Equal(t, "in-7", target.ExternalID)

	agent := e.alice.Token(dto.TokenKindAgent, "laptop").WithHeader(dto.HeaderAgent, "builder")
	r := testkit.Decode[dto.Message](agent.Post("/api/v1/agent/messages/"+target.ID+"/react", dto.ReactRequest{Emoji: "🎉"}).RequireStatus(http.StatusCreated))

	call := e.out.next(t)
	ts := call.Header.Get(webhook.TimestampHeader)
	require.Equal(t, webhook.Sign(e.secret, ts, call.Body), call.Header.Get(webhook.SignatureHeader), "valid signature")
	var got map[string]any
	require.NoError(t, json.Unmarshal(call.Body, &got))
	require.Equal(t, map[string]any{
		"event": "reaction", "connector_id": e.conn.ID, "chat_id": "deploys", "target_message_id": "in-7", "emoji": "🎉",
		"message": map[string]any{
			"id": r.ID, "room_id": room.ID, "author_name": "builder", "created_at": r.CreatedAt.Format(time.RFC3339Nano),
		},
	}, got)
	item := e.firstAttempt(t, room.ID, r.ID)
	require.Equal(t, dto.OutboxStatusSent, item.Status)
}
