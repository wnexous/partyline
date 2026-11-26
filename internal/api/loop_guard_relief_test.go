package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/testkit"
)

// framesOf splits the frames a connection got into the ids of its messages and the texts of its notices for the
// room, in order.
func framesOf(t *testing.T, frames []testkit.AgentFrame, roomID string) (messages, notices []string) {
	t.Helper()
	for _, f := range frames {
		switch f.Type {
		case dto.AgentFrameTypeMessage:
			var m dto.AgentMessageFrame
			require.NoError(t, json.Unmarshal(f.Raw, &m))
			messages = append(messages, m.Message.ID)
		case dto.AgentFrameTypeNotice:
			var n dto.AgentNoticeFrame
			require.NoError(t, json.Unmarshal(f.Raw, &n))
			require.Equal(t, roomID, n.Room.ID)
			notices = append(notices, n.Text)
		}
	}
	return messages, notices
}

// drainUntil reads frames until want message frames and the pause notice arrived (or IOTimeout passed), then
// what comes within a short quiet window: a fixed drain missed frames on a slow CI runner.
func drainUntil(t *testing.T, c *testkit.AgentConn, want int) []testkit.AgentFrame {
	t.Helper()
	var out []testkit.AgentFrame
	deadline := time.Now().Add(testkit.IOTimeout)
	for time.Now().Before(deadline) {
		msgs, notices := 0, 0
		for _, f := range out {
			switch f.Type {
			case dto.AgentFrameTypeMessage:
				msgs++
			case dto.AgentFrameTypeNotice:
				notices++
			}
		}
		if msgs >= want && notices >= 1 {
			break
		}
		out = append(out, c.Drain(200*time.Millisecond)...)
	}
	return append(out, c.Drain(200*time.Millisecond)...)
}

// LG-02: an AI's post answers delivery_paused, false while the room delivers and true from the post that pauses
// it on, on every agent post route; a person's post answers the plain message. LG-05: the 25th message still
// reaches the other AI, the 26th does not. LG-03: both live AIs get one notice that the room was paused.
func TestAgentPostsSayWhenTheRoomIsPaused(t *testing.T) {
	f := newTwoAgents(t)
	builder := f.rest()
	helper := f.bob.Token(dto.TokenKindAgent, "laptop").AsAgent("helper")
	path := "/api/v1/agent/rooms/" + f.room.ID + "/messages"
	post := func(c *testkit.Client, text string) (dto.AgentPostedMessage, map[string]any) {
		t.Helper()
		r := c.Post(path, dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated)
		var raw map[string]any
		require.NoError(t, json.Unmarshal(r.Body, &raw))
		return testkit.Decode[dto.AgentPostedMessage](r), raw
	}

	sentBy := map[string][]string{} // message ids each AI posted
	for i := 1; i <= delivery.DefaultLoopLimit; i++ {
		c, who := builder, "builder"
		if i%2 == 0 {
			c, who = helper, "helper"
		}
		m, raw := post(c, "step")
		require.Contains(t, raw, "delivery_paused", "the field is always there")
		require.Equal(t, i == delivery.DefaultLoopLimit, m.DeliveryPaused, "message %d", i)
		sentBy[who] = append(sentBy[who], m.ID)
	}

	got, notices := framesOf(t, drainUntil(t, f.helper, len(sentBy["builder"])), f.room.ID)
	require.Equal(t, sentBy["builder"], got, "helper got every builder message, the 25th included (LG-05)")
	require.Equal(t, []string{delivery.PausedNotice(delivery.DefaultLoopLimit)}, notices)
	got, notices = framesOf(t, drainUntil(t, f.builder, len(sentBy["helper"])), f.room.ID)
	require.Equal(t, sentBy["helper"], got)
	require.Equal(t, []string{delivery.PausedNotice(delivery.DefaultLoopLimit)}, notices)

	// The 26th, a reply and a reaction are posted, say so, and reach no AI.
	m, _ := post(helper, "the 26th")
	require.True(t, m.DeliveryPaused)
	reply := testkit.Decode[dto.AgentPostedMessage](helper.Post("/api/v1/agent/messages/"+sentBy["builder"][0]+"/reply",
		dto.ReplyRequest{Text: "answer"}).RequireStatus(http.StatusCreated))
	require.True(t, reply.DeliveryPaused)
	require.Equal(t, sentBy["builder"][0], reply.ReplyTo)
	f.builder.ExpectNone(500 * time.Millisecond)

	// A person's post is the plain message.
	r := f.alice.Post("/api/v1/rooms/"+f.room.ID+"/messages", dto.PostMessageRequest{Text: "I am here"}).RequireStatus(http.StatusCreated)
	require.NotContains(t, string(r.Body), "delivery_paused")
	f.helper.ExpectNone(300 * time.Millisecond)

	// After a resume the AIs' posts say the room delivers again.
	f.alice.Post("/api/v1/rooms/"+f.room.ID+"/resume", nil).RequireStatus(http.StatusOK)
	m, _ = post(builder, "back")
	require.False(t, m.DeliveryPaused)
}
