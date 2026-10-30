package agents_test

import (
	"context"
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

// dialHarness opens one more session of atlas in another harness of the AI's own folder: after the
// principal, the server labels it after its harness (pi-2, SESS-11).
func (f *sessionFixture) dialHarness(t *testing.T, harness string) *testkit.AgentConn {
	t.Helper()
	folder := testkit.FolderOf("atlas")
	folder.Harness = harness
	folder.Live = testkit.NewLiveID()
	conn, resp := f.token.DialFolder(folder)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	conn.Welcome(time.Minute)
	t.Cleanup(conn.Close)
	return conn
}

// unreadOf is how many messages of the fixture's room the session with label has not read (SN-05): its
// own messages and the ones it was given never count.
func (f *sessionFixture) unreadOf(t *testing.T, label string) int64 {
	t.Helper()
	rows, err := f.env.App.Store.R().CountSessionUnreadByRoom(context.Background(), db.CountSessionUnreadByRoomParams{
		AgentID: f.agentID, SessionKey: agents.SessionKey("", label, ""), AuthorLabel: label,
	})
	require.NoError(t, err)
	for _, row := range rows {
		if row.RoomID == f.room.ID {
			return row.Unread
		}
	}
	return 0
}

// SN-03, SN-04: the incident. The owner writes three plain messages in a group where the AI is live as
// principal and as :pi-2 (which follows mentions only); then it addresses :pi-2. pi-2 receives the three
// messages it had not read as context, oldest first, and then the message, while the principal, which
// follows the room, receives the addressed message too.
func TestAMentionOfASessionBringsTheConversationItMissed(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dialHarness(t, "pi")
	f.settle(t, principal)
	f.settle(t, parallel)

	var plain []dto.Message
	for _, text := range []string{"one", "two", "three"} {
		m := f.post(t, text)
		plain = append(plain, m)
		require.Equal(t, m.ID, principal.Message(time.Minute).Message.ID, "the principal follows the room")
	}
	parallel.ExpectNone(400 * time.Millisecond)
	require.EqualValues(t, 3, f.unreadOf(t, "pi-2"), "a session that is not notified only counts the message")

	addressed := f.post(t, "@atlas:pi-2 take this")
	for i, want := range plain {
		frame := parallel.Message(time.Minute)
		require.Equal(t, want.ID, frame.Message.ID, "context frame %d: oldest first", i)
		require.True(t, frame.Context, "earlier conversation is marked as context")
		require.Zero(t, frame.Skipped, "nothing was skipped")
		require.Equal(t, f.room.ID, frame.Room.ID)
	}
	last := parallel.Message(time.Minute)
	require.Equal(t, addressed.ID, last.Message.ID)
	require.False(t, last.Context, "the message itself is not context")
	require.Equal(t, addressed.ID, principal.Message(time.Minute).Message.ID,
		"the principal also receives the message addressed to its sibling (SN-04)")
	// The three context messages and the message itself are unread until the session acknowledges the batch.
	require.EqualValues(t, 4, f.unreadOf(t, "pi-2"))

	parallel.Ack(addressed.ID)
	f.waitReadSeq(t, "label:pi-2", addressed.Seq)
	require.Zero(t, f.unreadOf(t, "pi-2"), "the acknowledgement moved the session's read position (SN-03)")
}

// SN-03: a message that notifies nobody else stays in the principal's session and counts as unread for a
// parallel session; nothing is pushed to it.
func TestAPlainGroupMessageOnlyNotifiesThePrincipal(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dialHarness(t, "pi")
	f.settle(t, principal)
	f.settle(t, parallel)

	m := f.post(t, "hello everyone")
	require.Equal(t, m.ID, principal.Message(time.Minute).Message.ID)
	parallel.ExpectNone(400 * time.Millisecond)
	require.EqualValues(t, 1, f.unreadOf(t, "pi-2"))
	require.Empty(t, f.rows(t, "pi-2"), "a message it is not notified of is not pushed and gets no row")
}

// SN-02, SN-03: a parallel session that follows the room receives every message the delivery mode wants
// live, as a copy that never falls back, and its acknowledgement moves its read position.
func TestASessionThatFollowsTheRoomReceivesPlainMessages(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dialHarness(t, "pi")
	f.settle(t, principal)
	f.settle(t, parallel)
	f.setNotify(t, "pi-2", agents.NotifyOn)

	m := f.post(t, "hello everyone")
	require.Equal(t, m.ID, principal.Message(time.Minute).Message.ID)
	frame := parallel.Message(time.Minute)
	require.Equal(t, m.ID, frame.Message.ID)
	require.False(t, frame.Context)
	require.False(t, f.waitRow(t, "pi-2", m.ID).FellBackAt.Valid, "a copy is not the session's to own")

	parallel.Ack(m.ID)
	f.waitReadSeq(t, "label:pi-2", m.Seq)
}

// SN-02, SN-03: a principal set to mentions only is not pushed a plain message, but a bare @handle still
// reaches it, with the messages it had not read before it as context.
func TestAPrincipalSetToMentionsOnlyIsToldOfWhatNamesIt(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)
	f.setNotify(t, "", agents.NotifyMentions)

	plain := f.post(t, "hello everyone")
	principal.ExpectNone(400 * time.Millisecond)
	require.EqualValues(t, 1, f.unreadOf(t, ""))

	mentioned := f.post(t, "@atlas ping")
	context := principal.Message(time.Minute)
	require.Equal(t, plain.ID, context.Message.ID)
	require.True(t, context.Context, "the earlier message it had not read comes as context")
	frame := principal.Message(time.Minute)
	require.Equal(t, mentioned.ID, frame.Message.ID)
	require.False(t, frame.Context)
}

// SN-04: the principal receives a message that addresses a live sibling only while it follows the room;
// with the setting at mentions only it hears nothing of it and keeps it unread.
func TestAMentionOfASiblingDoesNotReachAPrincipalSetToMentionsOnly(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dialHarness(t, "pi")
	f.settle(t, principal)
	f.settle(t, parallel)
	f.setNotify(t, "", agents.NotifyMentions)

	addressed := f.post(t, "@atlas:pi-2 for you")
	require.Equal(t, addressed.ID, parallel.Message(time.Minute).Message.ID)
	principal.ExpectNone(400 * time.Millisecond)
	require.EqualValues(t, 1, f.unreadOf(t, ""), "it only counts as unread for the principal")
}

// SN-03: a catch-up batch keeps the newest 50 unread messages and says on its first frame how many
// earlier ones it left out.
func TestTheCatchUpBatchKeepsTheNewestFiftyAndCountsTheRest(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dialHarness(t, "pi")
	f.settle(t, principal)
	f.settle(t, parallel)

	const posted = 55
	for i := range posted {
		m := f.post(t, fmt.Sprintf("message %d", i))
		require.Equal(t, m.ID, principal.Message(time.Minute).Message.ID)
	}
	require.EqualValues(t, posted, f.unreadOf(t, "pi-2"))

	addressed := f.post(t, "@atlas:pi-2 what did I miss")
	first := parallel.Message(time.Minute)
	require.True(t, first.Context)
	require.EqualValues(t, posted-agents.BacklogLimit, first.Skipped, "the batch says how many earlier messages it left out")
	require.Equal(t, "message 5", first.Message.Text, "the batch starts at the newest 50")
	for range agents.BacklogLimit - 1 {
		frame := parallel.Message(time.Minute)
		require.True(t, frame.Context)
		require.Zero(t, frame.Skipped, "only the first frame carries the count")
	}
	last := parallel.Message(time.Minute)
	require.Equal(t, addressed.ID, last.Message.ID)
	require.False(t, last.Context)
}

// SN-03: a message already pushed to a session is not pushed again, and an acknowledgement moves the read
// position forward only.
func TestAPushedMessageIsNotPushedTwiceAndAcksOnlyMoveForward(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dialHarness(t, "pi")
	f.settle(t, principal)
	f.settle(t, parallel)

	one := f.post(t, "@atlas:pi-2 one")
	require.Equal(t, one.ID, parallel.Message(time.Minute).Message.ID)
	require.Equal(t, one.ID, principal.Message(time.Minute).Message.ID)

	two := f.post(t, "@atlas:pi-2 two")
	frame := parallel.Message(time.Minute)
	require.Equal(t, two.ID, frame.Message.ID, "the unacknowledged first message is not pushed again")
	require.False(t, frame.Context)
	parallel.ExpectNone(300 * time.Millisecond)

	parallel.Ack(two.ID)
	f.waitReadSeq(t, "label:pi-2", two.Seq)
	parallel.Ack(one.ID) // a late acknowledgement of the older message
	f.waitReadSeq(t, "label:pi-2", two.Seq)
	require.Equal(t, two.Seq, f.stateOf(t, "label:pi-2").ReadSeq, "an acknowledgement never moves a read position back")

	three := f.post(t, "@atlas:pi-2 three")
	require.Equal(t, three.ID, parallel.Message(time.Minute).Message.ID, "no context: everything up to two is read")
	parallel.ExpectNone(300 * time.Millisecond)
}

// SN-06: a message the AI's sessions wrote is context for the other sessions, never a notification, and it
// brings no context batch of its own: SESS-38/SESS-49 stay exactly as they are.
func TestASessionsOwnMessageBringsNoContextBatch(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dialHarness(t, "pi")
	f.settle(t, principal)
	f.settle(t, parallel)

	plain := f.post(t, "plain from alice")
	require.Equal(t, plain.ID, principal.Message(time.Minute).Message.ID)
	require.EqualValues(t, 1, f.unreadOf(t, "pi-2"))

	fromPrincipal := testkit.FolderOf("atlas")
	own := f.postFrom(t, fromPrincipal, "@atlas:pi-2 only this")
	require.Empty(t, own.AuthorLabel)
	frame := parallel.Message(time.Minute)
	require.Equal(t, own.ID, frame.Message.ID, "the addressed sibling receives the message alone")
	require.False(t, frame.Context, "a message the AI wrote brings no context batch")
	parallel.ExpectNone(300 * time.Millisecond)
	// alice's message and the AI's own one are unread for pi-2: a sibling's message is context, not a
	// notification (SN-06).
	require.EqualValues(t, 2, f.unreadOf(t, "pi-2"))
}

// SN-09: an edit or a delete follows every session that was given the message, including one that had it
// only as context of a catch-up batch.
func TestAnEditFollowsTheSessionsThatWereGivenTheMessage(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dialHarness(t, "pi")
	f.settle(t, principal)
	f.settle(t, parallel)

	plain := f.post(t, "before the mention")
	require.Equal(t, plain.ID, principal.Message(time.Minute).Message.ID)
	mention := f.post(t, "@atlas:pi-2 hi")
	require.Equal(t, plain.ID, parallel.Message(time.Minute).Message.ID) // the context
	require.Equal(t, mention.ID, parallel.Message(time.Minute).Message.ID)
	require.Equal(t, mention.ID, principal.Message(time.Minute).Message.ID, "the principal gets the copy (SN-04)")

	f.edit(t, plain.ID, "after the edit")
	require.Equal(t, plain.ID, principal.Edited(time.Minute).Message.ID)
	require.Equal(t, plain.ID, parallel.Edited(time.Minute).Message.ID,
		"the session that had it as context hears of the edit")

}

// SN-03: in a DM the AI is addressed as a whole, so the principal gets the message as today, and a message
// that names a sibling reaches that session with the DM's conversation as context, and the principal too.
func TestADirectMessageAddressesThePrincipalAndASiblingWithContext(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dialHarness(t, "pi")
	f.settle(t, principal)
	f.settle(t, parallel)
	dm := testkit.Decode[dto.Room](f.alice.Post("/api/v1/conversations/dm", dto.OpenDMRequest{With: f.agentID}).RequireStatus(http.StatusCreated))
	post := func(text string) dto.Message {
		t.Helper()
		return testkit.Decode[dto.Message](f.alice.Post("/api/v1/rooms/"+dm.ID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
	}

	hello := post("hello")
	require.Equal(t, hello.ID, principal.Message(time.Minute).Message.ID, "a DM goes to the principal as today")
	parallel.ExpectNone(400 * time.Millisecond)

	addressed := post("@atlas:pi-2 and you")
	context := parallel.Message(time.Minute)
	require.Equal(t, hello.ID, context.Message.ID)
	require.True(t, context.Context)
	require.Equal(t, dto.ConversationKindDM, context.Room.Kind, "the context keeps the DM's name")
	frame := parallel.Message(time.Minute)
	require.Equal(t, addressed.ID, frame.Message.ID)
	require.Equal(t, dto.ConversationKindDM, frame.Room.Kind)
	require.Equal(t, addressed.ID, principal.Message(time.Minute).Message.ID)
}
