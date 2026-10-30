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

// SN-04 (SESS-24): the principal's acknowledgement of its copy of a message addressed to a sibling consumes
// the message for the AI: when that sibling dies without acknowledging it, the end of its session hands the
// principal no duplicate, live or in its backlog.
func TestAnAckedCopyIsNotHandedBackWhenItsSiblingDies(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dialHarness(t, "pi")
	f.settle(t, principal)
	f.settle(t, parallel)

	addressed := f.post(t, "@atlas:pi-2 for you")
	require.Equal(t, addressed.ID, parallel.Message(time.Minute).Message.ID)
	require.Equal(t, addressed.ID, principal.Message(time.Minute).Message.ID, "the principal follows the room (SN-04)")
	principal.Ack(addressed.ID)
	f.waitReadSeq(t, "principal", addressed.Seq)
	require.True(t, f.waitRow(t, "", addressed.ID).AckedAt.Valid, "the principal's copy is a tombstone")

	// The principal leaves, and the sibling dies without acknowledging the message: nothing may fall back to
	// the principal's queue.
	principal.Close()
	f.waitPrincipalGone(t)
	parallel.Close()
	require.Eventually(t, func() bool {
		rows := f.rows(t, "pi-2")
		return len(rows) == 1 && rows[0].AckedAt.Valid
	}, 5*time.Second, 10*time.Millisecond, "the sibling's row is a tombstone too (SN-04)")

	again := f.dial(t)
	again.ExpectNone(700 * time.Millisecond)
}

// SN-03: a catch-up batch that was not acknowledged comes back. A session that drops between the batch and
// its acknowledgement did not read those messages, so the next notification delivers them again instead of
// leaving them behind the delivery rows of the lost frames.
func TestAContextBatchThatWasNotAckedComesBack(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dialHarness(t, "pi")
	f.settle(t, principal)
	f.settle(t, parallel)

	one, two := f.post(t, "one"), f.post(t, "two")
	require.Equal(t, one.ID, principal.Message(time.Minute).Message.ID)
	require.Equal(t, two.ID, principal.Message(time.Minute).Message.ID)
	first := f.post(t, "@atlas:pi-2 first")
	for _, want := range []string{one.ID, two.ID} {
		frame := parallel.Message(time.Minute)
		require.Equal(t, want, frame.Message.ID)
		require.True(t, frame.Context)
	}
	require.Equal(t, first.ID, parallel.Message(time.Minute).Message.ID)
	// The session drops before acknowledging anything: the AI's cursor and the session's read position stay
	// where they were.
	parallel.Close()

	again := f.dialHarness(t, "pi")
	f.settle(t, again)
	second := f.post(t, "@atlas:pi-2 second")
	for _, want := range []string{one.ID, two.ID, first.ID} {
		frame := again.Message(time.Minute)
		require.Equal(t, want, frame.Message.ID, "the unacknowledged batch comes back as context")
		require.True(t, frame.Context)
	}
	last := again.Message(time.Minute)
	require.Equal(t, second.ID, last.Message.ID)
	require.False(t, last.Context)
}

// SN-09: an edit or a delete of a message an AI's own session wrote follows every session that was given it,
// also the ones that had it only as context, and never the session that wrote it.
func TestAnUpdateOfASelfWrittenMessageFollowsItsContextHolders(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)
	folder, writer := f.dialWorktree(t)
	f.settle(t, writer)

	posted := f.postFrom(t, folder, "a note from the worktree")
	principal.ExpectNone(300 * time.Millisecond) // SESS-38: a plain own message notifies nobody

	// The principal had not read it, so it comes as context of the next mention, with a copy row.
	mention := f.post(t, "@atlas what about the note")
	context := principal.Message(time.Minute)
	require.Equal(t, posted.ID, context.Message.ID)
	require.True(t, context.Context)
	require.Equal(t, mention.ID, principal.Message(time.Minute).Message.ID)

	f.deleteFrom(t, folder, posted.ID)
	deleted := principal.Deleted(time.Minute)
	require.Equal(t, posted.ID, deleted.Message.ID, "the principal had the message as context, so it hears of the delete")
	writer.ExpectNone(400 * time.Millisecond)
}
