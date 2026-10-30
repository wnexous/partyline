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
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/rooms"
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

// addUserMember puts another user in the fixture's room and its organization, so their messages are
// untrusted for the AI (POS-10).
func (f *sessionFixture) addUserMember(t *testing.T, u *testkit.Client) {
	t.Helper()
	f.env.AddMember(f.alice.PersonalOrg().ID, u.User.ID, orgs.PermissionMember, "")
	f.alice.Post("/api/v1/rooms/"+f.room.ID+"/members",
		dto.AddMemberRequest{Kind: dto.MemberKindUser, MemberID: u.User.ID}).RequireStatus(http.StatusCreated)
}

// setDelivery sets the AI's delivery mode in its room (MEM-03, MEM-04).
func (f *sessionFixture) setDelivery(t *testing.T, mode dto.DeliveryMode) {
	t.Helper()
	m := f.memberRow(t)
	f.alice.Patch("/api/v1/rooms/"+f.room.ID+"/members/"+m.ID, dto.UpdateMemberRequest{Delivery: &mode}).
		RequireStatus(http.StatusOK)
}

// SN-03 (decision): the delivery mode filters the context too. A trusted-mode session never receives an
// untrusted message, not even as context, while a mentions-mode session keeps the whole conversation as
// context: that is what SN-03 is for.
func TestTrustedModeLeavesUntrustedMessagesOutOfContext(t *testing.T) {
	t.Run("trusted mode", func(t *testing.T) {
		f := newSessionFixture(t)
		bob := f.env.NewUser("bobby")
		f.addUserMember(t, bob)
		f.setDelivery(t, dto.DeliveryModeTrusted)
		principal := f.dial(t)
		f.settle(t, principal)

		untrusted := testkit.Decode[dto.Message](bob.Post("/api/v1/rooms/"+f.room.ID+"/messages",
			dto.PostMessageRequest{Text: "from bob"}).RequireStatus(http.StatusCreated))
		trusted := f.post(t, "from alice")
		got := principal.Message(time.Minute)
		require.Equal(t, trusted.ID, got.Message.ID, "the untrusted message is not context in trusted mode")
		require.False(t, got.Context)
		require.True(t, got.Trusted)
		principal.ExpectNone(400 * time.Millisecond)
		require.NotEqual(t, untrusted.ID, got.Message.ID)
	})
	t.Run("mentions mode", func(t *testing.T) {
		f := newSessionFixture(t)
		bob := f.env.NewUser("bobby")
		f.addUserMember(t, bob)
		f.setDelivery(t, dto.DeliveryModeMentions)
		principal := f.dial(t)
		f.settle(t, principal)

		untrusted := testkit.Decode[dto.Message](bob.Post("/api/v1/rooms/"+f.room.ID+"/messages",
			dto.PostMessageRequest{Text: "from bob"}).RequireStatus(http.StatusCreated))
		trusted := f.post(t, "from alice")
		addressed := f.post(t, "@atlas ping")
		for _, want := range []struct {
			id      string
			trusted bool
		}{{untrusted.ID, false}, {trusted.ID, true}} {
			frame := principal.Message(time.Minute)
			require.Equal(t, want.id, frame.Message.ID, "mentions mode keeps the whole context (SN-03)")
			require.True(t, frame.Context)
			require.Equal(t, want.trusted, frame.Trusted)
		}
		require.Equal(t, addressed.ID, principal.Message(time.Minute).Message.ID)
	})
}

// SN-03 (decision): the messages the AI's own sessions wrote while the loop guard had paused the room are
// not context. The pause is a loop boundary, not something the next session has to read; what humans wrote
// in the meantime is.
func TestMessagesWrittenWhileTheRoomWasPausedAreNotContext(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dialHarness(t, "pi")
	f.settle(t, principal)
	f.settle(t, parallel)
	folder := testkit.FolderOf("atlas")

	before := f.post(t, "before the burst")
	require.Equal(t, before.ID, principal.Message(time.Minute).Message.ID)

	// The loop guard (DLV-08) pauses the room at the 25th consecutive agent message: that message and the
	// ones that follow are written while the guard holds the room.
	var burst []dto.Message
	for i := range 25 + 3 {
		burst = append(burst, f.postFrom(t, folder, fmt.Sprintf("agent message %d", i)))
	}
	duringThePause := map[string]bool{}
	for _, m := range burst[24:] {
		duringThePause[m.ID] = true
	}
	require.True(t, testkit.Decode[dto.Room](f.alice.Get("/api/v1/rooms/"+f.room.ID).RequireStatus(http.StatusOK)).AgentsPaused,
		"the burst paused the room")

	duringPause := f.post(t, "a human while the room is paused")
	f.alice.Post("/api/v1/rooms/"+f.room.ID+"/resume", nil).RequireStatus(http.StatusOK)
	afterResume := f.post(t, "after the resume")

	addressed := f.post(t, "@atlas:pi-2 what happened")
	var got []string
	for {
		frame := parallel.Message(time.Minute)
		if frame.Message.ID == addressed.ID {
			require.False(t, frame.Context)
			break
		}
		require.True(t, frame.Context)
		require.NotContains(t, duringThePause, frame.Message.ID,
			"an agent message written while the loop guard paused the room is not context")
		got = append(got, frame.Message.ID)
	}
	// The loop guard's own notice is a system message: it is the one message of the pause that is context.
	page := testkit.Decode[dto.Page[dto.Message]](f.alice.Get("/api/v1/rooms/" + f.room.ID + "/messages?limit=50").RequireStatus(http.StatusOK))
	var notice dto.Message
	for _, m := range page.Items {
		if m.Text == rooms.PauseNotice {
			notice = m
		}
	}
	require.NotEmpty(t, notice.ID, "the pause notice is in the room")

	want := []string{before.ID}
	for _, m := range burst[:24] {
		want = append(want, m.ID)
	}
	want = append(want, notice.ID, duringPause.ID, afterResume.ID)
	require.Equal(t, want, got,
		"the human messages and the agent messages of the burst before the pause are the context")
}

// SN-03: an addressed push overrides the copy an earlier catch-up batch left on the session: the row is
// upgraded to the addressed delivery and the frame goes without the context flag (an out-of-order publish).
func TestALateAddressedPushOverridesAContextCopy(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dialHarness(t, "pi")
	f.settle(t, principal)
	f.settle(t, parallel)

	plain := f.post(t, "plain")
	require.Equal(t, plain.ID, principal.Message(time.Minute).Message.ID)
	addressed := f.post(t, "@atlas:pi-2 now")
	context := parallel.Message(time.Minute)
	require.Equal(t, plain.ID, context.Message.ID)
	require.True(t, context.Context)
	require.Equal(t, addressed.ID, parallel.Message(time.Minute).Message.ID)

	// The dispatcher hands the plain message to the sibling late: it is the message itself now, not context.
	require.True(t, f.pushRow(t, plain, "pi-2"))
	frame := parallel.Message(time.Minute)
	require.Equal(t, plain.ID, frame.Message.ID)
	require.False(t, frame.Context, "the addressed push is not a context copy")
	require.Zero(t, f.rowOf(t, "pi-2", plain.ID).Context, "the copy row was upgraded")
}

// SN-04: a text that addresses a sibling and mentions the AI by its bare handle reaches the principal even
// when its setting is mentions only.
func TestABareMentionAlongsideASiblingAddressReachesThePrincipal(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dialHarness(t, "pi")
	f.settle(t, principal)
	f.settle(t, parallel)
	f.setNotify(t, "", agents.NotifyMentions)

	m := f.post(t, "@atlas:pi-2 and @atlas")
	require.Equal(t, m.ID, parallel.Message(time.Minute).Message.ID)
	got := principal.Message(time.Minute)
	require.Equal(t, m.ID, got.Message.ID, "the bare mention names the principal too")
	require.False(t, got.Context)
}

// SN-09: edits and deletes follow the session that was given the message, by its session key: a label that
// was reassigned to another session never receives an update of a message it never saw.
func TestAnEditFollowsTheSessionKeyNotTheReassignedLabel(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)

	// A session with a harness session id has a key that survives its label.
	named := testkit.FolderOf("atlas")
	named.Harness = "pi"
	named.Live = testkit.NewLiveID()
	named.HarnessSession = "pi-session-1"
	first, resp := f.token.DialFolder(named)
	require.NotNil(t, first, "upgrade refused: %d %s", resp.Status, resp.Body)
	first.Welcome(time.Minute)
	require.Equal(t, "pi-2", f.env.App.Presence.Sessions(f.agentID)[1].Label)
	m := f.post(t, "@atlas:pi-2 hello")
	require.Equal(t, m.ID, first.Message(time.Minute).Message.ID)
	require.Equal(t, m.ID, principal.Message(time.Minute).Message.ID, "the principal's copy (SN-04)")
	first.Ack(m.ID)
	f.waitReadSeq(t, "pi-session-1", m.Seq)
	first.Close()
	f.waitSessionGone(t, "pi-2")

	// A new session without a harness session id takes the label back, with a key of its own.
	again := f.dialHarness(t, "pi")
	f.settle(t, again)

	f.edit(t, m.ID, "edited")
	require.Equal(t, m.ID, principal.Edited(time.Minute).Message.ID)
	again.ExpectNone(500 * time.Millisecond)
}

// SN-09 (decision): the principal set to mentions only follows the edits and deletes of the messages it has a
// row for, not of the ones it never received.
func TestAMentionsPrincipalFollowsOnlyTheEditsOfMessagesItHas(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)
	f.setNotify(t, "", agents.NotifyMentions)

	plain := f.post(t, "plain")
	principal.ExpectNone(300 * time.Millisecond)
	f.edit(t, plain.ID, "plain edited")
	principal.ExpectNone(500 * time.Millisecond)

	mentioned := f.post(t, "@atlas for you")
	context := principal.Message(time.Minute)
	require.Equal(t, plain.ID, context.Message.ID, "the message it never read comes as context")
	require.True(t, context.Context)
	require.Equal(t, "plain edited", context.Message.Text)
	require.Equal(t, mentioned.ID, principal.Message(time.Minute).Message.ID)
	f.edit(t, mentioned.ID, "for you, edited")
	require.Equal(t, mentioned.ID, principal.Edited(time.Minute).Message.ID)
}

// SN-03: a plain push to an AI whose only session is the principal writes no delivery row: its cursor covers
// the message. With a parallel session live the row is written, because it is what routes an edit back and
// dedupes the message.
func TestAPrincipalOnlyAIGetsNoCopyRowPerPush(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)

	plain := f.post(t, "plain")
	require.Equal(t, plain.ID, principal.Message(time.Minute).Message.ID)
	principal.Ack(plain.ID)
	f.waitReadSeq(t, "principal", plain.Seq)
	require.Empty(t, f.rows(t, ""), "the cursor already covers the only session's message")

	parallel := f.dialHarness(t, "pi")
	f.settle(t, parallel)
	addressed := f.post(t, "plain with a sibling live")
	require.Equal(t, addressed.ID, principal.Message(time.Minute).Message.ID)
	require.Eventually(t, func() bool {
		rows := f.rows(t, "")
		return len(rows) == 1 && rows[0].MessageID == addressed.ID
	}, 5*time.Second, 10*time.Millisecond, "the row routes the edits and the copies (SN-09)")
}

// SN-03: a catch-up batch larger than the free queue sends the newest frames that fit and counts the rest as
// skipped, leaving a slot for the message itself instead of overflowing the queue.
func TestACatchUpBatchKeepsTheNewestThatFitTheQueue(t *testing.T) {
	agents.SetQueueSize(4)
	t.Cleanup(func() { agents.SetQueueSize(agents.QueueSize) })
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dialHarness(t, "pi")
	f.settle(t, principal)
	f.settle(t, parallel)

	var plain []dto.Message
	for i := range 6 {
		m := f.post(t, fmt.Sprintf("plain %d", i))
		require.Equal(t, m.ID, principal.Message(time.Minute).Message.ID)
		plain = append(plain, m)
	}
	addressed := f.post(t, "@atlas:pi-2 now")

	var contexts []dto.AgentMessageFrame
	for {
		frame := parallel.Message(time.Minute)
		if frame.Message.ID == addressed.ID {
			break
		}
		require.True(t, frame.Context)
		contexts = append(contexts, frame)
	}
	require.LessOrEqual(t, len(contexts), 3, "the queue of four holds the message and at most three context frames")
	require.NotEmpty(t, contexts, "the newest unread messages still come")
	require.EqualValues(t, 6-len(contexts), contexts[0].Skipped, "the frames that did not fit are counted as skipped")
	require.Equal(t, plain[len(plain)-len(contexts)].ID, contexts[0].Message.ID, "the batch keeps the newest")
	require.Equal(t, plain[len(plain)-1].ID, contexts[len(contexts)-1].Message.ID)
}
