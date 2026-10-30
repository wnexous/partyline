package agents_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// memberRow returns the room member row of the fixture's AI.
func (f *sessionFixture) memberRow(t *testing.T) dto.RoomMember {
	t.Helper()
	list := testkit.Decode[dto.List[dto.RoomMember]](f.alice.Get("/api/v1/rooms/" + f.room.ID + "/members").RequireStatus(http.StatusOK))
	for _, m := range list.Items {
		if m.Kind == dto.MemberKindAgent && m.MemberID == f.agentID {
			return m
		}
	}
	require.FailNow(t, "the AI is not a member of the room")
	return dto.RoomMember{}
}

// removeMember takes the fixture's AI out of the room.
func (f *sessionFixture) removeMember(t *testing.T) {
	t.Helper()
	f.alice.Delete("/api/v1/rooms/" + f.room.ID + "/members/" + f.memberRow(t).ID).RequireStatus(http.StatusNoContent)
}

// addMember adds the fixture's AI to the room again.
func (f *sessionFixture) addMember(t *testing.T) {
	t.Helper()
	f.alice.Post("/api/v1/rooms/"+f.room.ID+"/members",
		dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: f.agentID}).RequireStatus(http.StatusCreated)
}

// messageSeq returns the sequence number of a posted message.
func (f *sessionFixture) messageSeq(t *testing.T, messageID string) int64 {
	t.Helper()
	seq, err := f.env.App.Store.R().GetMessageRoomSeq(context.Background(), messageID)
	require.NoError(t, err)
	return seq.Seq
}

// SN-03, SN-04 (ROOM-01): messages posted while the AI was not a member of the room never come back. Its
// read positions and its cursor are forgotten when it is taken out, so neither the catch-up of a mention
// nor the principal's backlog reaches the absence.
func TestMessagesMissedWhileOutOfTheRoomNeverComeBack(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dialHarness(t, "pi")
	f.settle(t, principal)
	f.settle(t, parallel)

	// Both sessions read up to the last message of the membership: the principal through its cursor, the
	// sibling through its read position.
	last := f.post(t, "@atlas:pi-2 hello")
	require.Equal(t, last.ID, parallel.Message(time.Minute).Message.ID)
	require.Equal(t, last.ID, principal.Message(time.Minute).Message.ID)
	parallel.Ack(last.ID)
	f.waitReadSeq(t, "label:pi-2", last.Seq)
	principal.Ack(last.ID)
	f.waitReadSeq(t, "principal", last.Seq)

	f.removeMember(t)

	// The room forgets the AI: its per-session read positions and its queue cursor go with the membership.
	ctx := context.Background()
	_, err := f.env.App.Store.R().GetSessionRoomState(ctx, db.GetSessionRoomStateParams{
		AgentID: f.agentID, SessionKey: agents.SessionKey("", "pi-2", ""), RoomID: f.room.ID,
	})
	require.ErrorIs(t, err, sql.ErrNoRows, "a removed member has no per-session state in the room")
	cursor, err := f.env.App.Store.R().GetAgentCursor(ctx, db.GetAgentCursorParams{AgentID: f.agentID, RoomID: f.room.ID})
	require.NoError(t, err)
	require.Equal(t, last.Seq, cursor, "the queue position was reset to the point the AI left")

	var away []dto.Message
	for i := range 20 {
		away = append(away, f.post(t, fmt.Sprintf("while away %d", i)))
	}
	parallel.ExpectNone(400 * time.Millisecond)
	principal.ExpectNone(400 * time.Millisecond)

	f.addMember(t)
	principal.Close()
	f.waitPrincipalGone(t)

	// A plain message after the re-add is missed while the principal is away; the mention that follows is
	// the context batch of the sibling, which must not carry the absence.
	after := f.post(t, "after the re-add")
	addressed := f.post(t, "@atlas:pi-2 welcome back")
	context := parallel.Message(time.Minute)
	require.Equal(t, after.ID, context.Message.ID, "only the message of the second membership is context")
	require.True(t, context.Context)
	frame := parallel.Message(time.Minute)
	require.Equal(t, addressed.ID, frame.Message.ID)
	require.False(t, frame.Context)
	parallel.ExpectNone(300 * time.Millisecond)

	// The principal's backlog is the second membership too: the absence is not delivered.
	again := f.dial(t)
	absence := make(map[string]bool, len(away))
	for _, m := range away {
		absence[m.ID] = true
	}
	var got []string
	for _, fr := range again.Drain(700 * time.Millisecond) {
		if fr.Type != dto.AgentFrameTypeMessage {
			continue
		}
		var v dto.AgentMessageFrame
		require.NoError(t, json.Unmarshal(fr.Raw, &v))
		require.False(t, absence[v.Message.ID], "the backlog carries a message from the absence: %s", v.Message.Text)
		got = append(got, v.Message.ID)
	}
	require.Contains(t, got, after.ID, "the backlog carries what the principal missed after the re-add")
}
