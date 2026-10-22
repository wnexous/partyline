package connectors_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// mergeAI merges the AI from into the AI into as alice.
func (pc *perChat) mergeAI(t *testing.T, from, into string) {
	t.Helper()
	pc.alice.Post("/api/v1/agents/"+from+"/merge", dto.MergeAgentRequest{Into: into}).RequireStatus(http.StatusOK)
}

// say has an AI of alice post in a room, the way its session would.
func (pc *perChat) say(t *testing.T, ai, room, text string) rooms.Message {
	t.Helper()
	m, err := pc.env.App.Rooms.Post(context.Background(), authn.Principal{Kind: authn.KindAgent, UserID: pc.alice.User.ID, AgentID: ai}, room, rooms.PostInput{Text: text})
	require.NoError(t, err)
	return m
}

// MRG-01 with ACC-01, ACC-02: the AI that operates a connector is merged into another: the
// connector is operated by the survivor, and the person's platform DM goes on in the same room,
// with its chat, the survivor now on the other side.
func TestMergeKeepsAPlatformDMRunning(t *testing.T) {
	pc := newPerChat(t)
	oldID := pc.env.NewLegacyAI(pc.alice, "old")
	pc.patch(t, dto.UpdateConnectorRequest{OperatorID: &oldID})
	pc.emit(dmText(dmA, "A1", "to old"))
	chat := pc.chatID(t, dmA)
	dm := pc.roomsOf(t, chat)
	require.Len(t, dm, 1)

	pc.mergeAI(t, oldID, pc.atlasID)

	require.Equal(t, pc.atlasID, pc.connector(t).Operator.ID)
	require.Equal(t, dm, pc.roomsOf(t, chat), "the chat stays in its DM")
	room := pc.room(t, pc.alice, dm[0])
	var participants []string
	for _, p := range room.Participants {
		participants = append(participants, p.ID)
	}
	require.Contains(t, participants, pc.atlasID)
	require.NotContains(t, participants, oldID)
	kinds := map[dto.MemberKind]dto.RoomMember{}
	for _, m := range pc.members(t, pc.alice, dm[0]) {
		kinds[m.Kind] = m
	}
	require.Equal(t, pc.atlasID, kinds[dto.MemberKindAgent].MemberID)
	require.True(t, kinds[dto.MemberKindChat].Outbound, "replies still go through the chat")
	require.Equal(t, 1, pc.dmRooms(t))

	pc.emit(dmText(dmA, "A2", "to atlas now"))
	for got := pc.atlas.Message(connectortest.Timeout); got.Message.Text != "to atlas now"; got = pc.atlas.Message(connectortest.Timeout) {
		require.Equal(t, dm[0], got.Room.ID)
	}
	reply := testkit.Decode[dto.Message](pc.tok.AsAgent("atlas").Post("/api/v1/agent/rooms/"+dm[0]+"/messages", dto.PostMessageRequest{Text: "on it"}).RequireStatus(http.StatusCreated))
	require.Len(t, reply.Outbox, 1)
	require.Equal(t, chat, reply.Outbox[0].ChatID, "and the survivor's reply is queued for the person's chat")
}

// MRG-01: both AIs had a platform DM with the person, at different times. The merged AI's history is
// appended to the survivor's DM, the person's chat joins it, what was queued for the chat stays
// queued, and the person's next message lands in the one DM left.
func TestMergeAppendsAPlatformDMAndMovesItsChat(t *testing.T) {
	pc := newPerChat(t)
	oldID := pc.env.NewLegacyAI(pc.alice, "old")

	// The person writes to Atlas, then the account moves to old and they write to old, whose reply waits in the outbox.
	pc.emit(dmText(dmA, "A1", "to atlas"))
	chat := pc.chatID(t, dmA)
	atlasDM := pc.atlas.Message(connectortest.Timeout).Room.ID
	pc.patch(t, dto.UpdateConnectorRequest{OperatorID: &oldID})
	pc.emit(dmText(dmA, "A2", "to old"))
	oldDM := pc.roomsOf(t, chat)
	require.Len(t, oldDM, 1)
	require.NotEqual(t, atlasDM, oldDM[0], "two DMs with the person, one per operator")
	waiting := pc.say(t, oldID, oldDM[0], "old's answer")
	require.Len(t, waiting.Outbox, 1)
	require.Equal(t, 2, pc.dmRooms(t))

	pc.mergeAI(t, oldID, pc.atlasID)

	require.Equal(t, 1, pc.dmRooms(t))
	pc.alice.Get("/api/v1/rooms/"+oldDM[0]).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Equal(t, []string{atlasDM}, pc.roomsOf(t, chat), "the chat is in the DM that is left")
	var texts []string
	var answer dto.Message
	for _, m := range pc.messages(t, atlasDM) { // newest first
		texts = append([]string{m.Text}, texts...)
		if m.Text == "old's answer" {
			answer = m
		}
	}
	require.Equal(t, []string{"to atlas", "History merged from @old", "to old", "old's answer"}, texts)
	require.Equal(t, pc.atlasID, answer.Author.ID)
	require.Len(t, answer.Outbox, 1, "what was queued for the chat went with its message")
	require.Equal(t, []any{chat, dto.OutboxStatusPending}, []any{answer.Outbox[0].ChatID, answer.Outbox[0].Status})

	// ... and it can still be sent: the chat is a member of the room the message is in now.
	due, err := pc.env.App.Store.R().ListDueOutbox(context.Background(), db.ListDueOutboxParams{Now: time.Now().Add(time.Hour).UnixMilli(), Limit: 10})
	require.NoError(t, err)
	var dueMessages []string
	for _, d := range due {
		require.Equal(t, atlasDM, d.RoomID)
		dueMessages = append(dueMessages, d.MessageID)
	}
	require.Contains(t, dueMessages, answer.ID)

	pc.emit(dmText(dmA, "A3", "back to atlas"))
	for got := pc.atlas.Message(connectortest.Timeout); got.Message.Text != "back to atlas"; got = pc.atlas.Message(connectortest.Timeout) {
		require.Equal(t, atlasDM, got.Room.ID)
	}
	require.Equal(t, 1, pc.dmRooms(t))
}
