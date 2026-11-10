package agents_test

import (
	"context"
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

// openDM opens the owner's DM with the fixture's AI and returns it with a function that posts to it.
func (f *sessionFixture) openDM(t *testing.T) (dto.Room, func(text string) dto.Message) {
	t.Helper()
	dm := testkit.Decode[dto.Room](f.alice.Post("/api/v1/conversations/dm", dto.OpenDMRequest{With: f.agentID}).RequireStatus(http.StatusCreated))
	return dm, func(text string) dto.Message {
		t.Helper()
		return testkit.Decode[dto.Message](f.alice.Post("/api/v1/rooms/"+dm.ID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
	}
}

// dialSession opens a session of atlas that sends a harness session id: the first one the AI sees is pinned
// as its principal, and any other is a parallel session, also while the principal is away (SESS-52).
func (f *sessionFixture) dialSession(t *testing.T, harnessSession string) *testkit.AgentConn {
	t.Helper()
	folder := testkit.FolderOf("atlas")
	folder.HarnessSession = harnessSession
	folder.Live = testkit.NewLiveID()
	conn, resp := f.token.DialFolder(folder)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	conn.Welcome(time.Minute)
	t.Cleanup(conn.Close)
	return conn
}

// waitDeliveryRow waits until the delivery row of label for messageID satisfies ok, and returns it.
func (f *sessionFixture) waitDeliveryRow(t *testing.T, label, messageID string, ok func(db.AgentSessionDelivery) bool) db.AgentSessionDelivery {
	t.Helper()
	var row db.AgentSessionDelivery
	require.Eventually(t, func() bool {
		var err error
		row, err = f.env.App.Store.R().GetSessionDelivery(context.Background(), db.GetSessionDeliveryParams{
			MessageID: messageID, AgentID: f.agentID, Label: label,
		})
		return err == nil && ok(row)
	}, 5*time.Second, 10*time.Millisecond)
	return row
}

// waitStandInAcked waits until the stand-in row of label for messageID is the session's own tombstone: the
// acknowledgement consumed the message for the principal (SN-10).
func (f *sessionFixture) waitStandInAcked(t *testing.T, label, messageID string) {
	t.Helper()
	f.waitDeliveryRow(t, label, messageID, func(row db.AgentSessionDelivery) bool {
		return row.AckedAt.Valid && row.Context == 0
	})
}

// anyRow accepts every delivery row.
func anyRow(db.AgentSessionDelivery) bool { return true }

// flushDispatch waits until the dispatcher, which handles the events of every room in order, has handled
// everything posted so far: another AI in the fixture's room is told of a message posted now. The message
// is a plain group message, so it is only the principal's (and the watcher's).
func (f *sessionFixture) flushDispatch(t *testing.T) {
	t.Helper()
	watcherID := f.env.NewAI(f.alice, "watcher")
	f.alice.Post("/api/v1/rooms/"+f.room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: watcherID}).RequireStatus(http.StatusCreated)
	watcher, resp := f.token.DialFolder(testkit.FolderOf("watcher"))
	require.NotNil(t, watcher, "upgrade refused: %d %s", resp.Status, resp.Body)
	defer watcher.Close()
	watcher.Welcome(time.Minute)
	sync := f.post(t, "sync")
	for {
		if watcher.Message(time.Minute).Message.ID == sync.ID {
			return
		}
	}
}

// messageIDs reads the message frames of a drained connection.
func messageIDs(t *testing.T, frames []testkit.AgentFrame) []string {
	t.Helper()
	var ids []string
	for _, fr := range frames {
		if fr.Type != dto.AgentFrameTypeMessage {
			continue
		}
		var v dto.AgentMessageFrame
		require.NoError(t, json.Unmarshal(fr.Raw, &v))
		ids = append(ids, v.Message.ID)
	}
	return ids
}

// SN-10: the incident. The AI's only live sessions are parallel ones (the principal is offline) and someone
// writes three messages in a new DM: each of them reaches every live parallel session once, live, as the
// message itself and with its trust, instead of waiting for a principal that never connects. The
// acknowledgement of one of them consumes the message for the principal, which gets no duplicate when it
// comes back.
func TestADirectMessageReachesEveryParallelSessionWhileThePrincipalIsOffline(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	second, third := f.dial(t), f.dial(t)
	f.settle(t, principal)
	f.settle(t, second)
	f.settle(t, third)
	principal.Close()
	f.waitPrincipalGone(t)

	dm, post := f.openDM(t)
	var sent []dto.Message
	for _, text := range []string{"hi", "are you there?", "I need help"} {
		m := post(text)
		sent = append(sent, m)
		for _, conn := range []*testkit.AgentConn{second, third} {
			frame := conn.Message(time.Minute)
			require.Equal(t, m.ID, frame.Message.ID, "every live parallel session stands in for the principal")
			require.False(t, frame.Context, "it is the message itself, not context")
			require.True(t, frame.Trusted, "the owner wrote it")
			require.Equal(t, dm.ID, frame.Room.ID)
			require.Equal(t, dto.ConversationKindDM, frame.Room.Kind)
		}
	}
	second.ExpectNone(300 * time.Millisecond)
	third.ExpectNone(300 * time.Millisecond)
	for _, m := range sent {
		require.EqualValues(t, 1, f.waitDeliveryRow(t, "claude-2", m.ID, anyRow).Context, "a stand-in is a copy until it is acknowledged")
		require.False(t, f.waitDeliveryRow(t, "claude-3", m.ID, anyRow).AckedAt.Valid)
	}

	for _, m := range sent {
		second.Ack(m.ID)
	}
	for _, m := range sent {
		f.waitStandInAcked(t, "claude-2", m.ID)
	}

	// The principal comes back: the messages a parallel session answered are not in its queue.
	again := f.dial(t)
	_, ok := f.env.App.Presence.Main(f.agentID)
	require.True(t, ok, "the new session is the principal")
	again.ExpectNone(700 * time.Millisecond)

	// From now on the DM is the principal's again, and a parallel session on mentions only is not told.
	next := post("thanks")
	require.Equal(t, next.ID, again.Message(time.Minute).Message.ID)
	again.ExpectNone(300 * time.Millisecond)
	second.ExpectNone(300 * time.Millisecond)
	third.ExpectNone(300 * time.Millisecond)
}

// SN-10: a stand-in that no parallel session acknowledged is still the principal's: it comes back with its
// backlog, so nothing is lost when the session that was given it ends.
func TestAStandInNobodyAcknowledgedStaysInThePrincipalsQueue(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dial(t)
	f.settle(t, principal)
	f.settle(t, parallel)
	principal.Close()
	f.waitPrincipalGone(t)

	_, post := f.openDM(t)
	m := post("hello")
	require.Equal(t, m.ID, parallel.Message(time.Minute).Message.ID)
	f.waitDeliveryRow(t, "claude-2", m.ID, anyRow)
	parallel.Close()
	f.waitSessionGone(t, "claude-2")

	again := f.dial(t)
	frame := again.Message(time.Minute)
	require.Equal(t, m.ID, frame.Message.ID, "the principal's backlog kept it")
	require.Empty(t, frame.ForLabel, "a stand-in is not a fallback")
	again.ExpectNone(300 * time.Millisecond)
}

// SN-10: in a group, a bare @handle reaches the live parallel sessions while the principal is offline, with
// what they had not read as context; a plain message, and a mention inside code, still wait for the
// principal. The principal's backlog then carries those, and not the mention a parallel session answered.
func TestABareMentionReachesTheParallelSessionsWhileThePrincipalIsOffline(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dial(t)
	f.settle(t, principal)
	f.settle(t, parallel)
	principal.Close()
	f.waitPrincipalGone(t)

	plain := f.post(t, "hello everyone")
	parallel.ExpectNone(400 * time.Millisecond)

	mention := f.post(t, "@atlas can you look at this?")
	context := parallel.Message(time.Minute)
	require.Equal(t, plain.ID, context.Message.ID)
	require.True(t, context.Context, "what it had not read comes as context (SN-03)")
	frame := parallel.Message(time.Minute)
	require.Equal(t, mention.ID, frame.Message.ID)
	require.False(t, frame.Context)
	require.True(t, frame.Trusted)

	code := f.post(t, "the config says `@atlas` there")
	parallel.ExpectNone(400 * time.Millisecond)

	parallel.Ack(mention.ID)
	f.waitStandInAcked(t, "claude-2", mention.ID)

	again := f.dial(t)
	got := messageIDs(t, again.Drain(700*time.Millisecond))
	require.Equal(t, []string{plain.ID, code.ID}, got, "the principal's backlog has what was its own, once")
}

// SN-10: with the principal live nothing changes: it is the one a bare @handle notifies, and a parallel
// session on mentions only is not told.
func TestABareMentionWithALivePrincipalDoesNotReachTheParallelSessions(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dialHarness(t, "pi")
	f.settle(t, principal)
	f.settle(t, parallel)

	mention := f.post(t, "@atlas ping")
	require.Equal(t, mention.ID, principal.Message(time.Minute).Message.ID)
	parallel.ExpectNone(400 * time.Millisecond)
	require.Empty(t, f.rows(t, "pi-2"))
}

// SN-10: a parallel session that connects while the principal is offline receives, in its backlog, the DMs
// waiting for the principal, as the messages themselves. Its acknowledgement consumes them for the
// principal: a later parallel session and the principal coming back are not handed them again, while a
// plain group message still waits for the principal.
func TestAParallelSessionThatConnectsWhileThePrincipalIsOfflineGetsTheWaitingDMs(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dialSession(t, "lead-session")
	f.settle(t, principal)
	principal.Close()
	f.waitPrincipalGone(t)

	dm, post := f.openDM(t)
	one, two := post("hi"), post("still there?")
	plain := f.post(t, "hello everyone")

	parallel := f.dialSession(t, "side-session")
	sessions := f.env.App.Presence.Sessions(f.agentID)
	require.Len(t, sessions, 1)
	require.Equal(t, "claude-2", sessions[0].Label, "the pinned principal is away: the session is a parallel one")
	for _, want := range []dto.Message{one, two} {
		frame := parallel.Message(time.Minute)
		require.Equal(t, want.ID, frame.Message.ID, "oldest first")
		require.False(t, frame.Context)
		require.True(t, frame.Trusted)
		require.Equal(t, dm.ID, frame.Room.ID)
	}
	parallel.ExpectNone(500 * time.Millisecond)

	parallel.Ack(one.ID)
	parallel.Ack(two.ID)
	f.waitStandInAcked(t, "claude-2", one.ID)
	f.waitStandInAcked(t, "claude-2", two.ID)

	later := f.dialSession(t, "third-session")
	later.ExpectNone(500 * time.Millisecond)

	back := f.dialSession(t, "lead-session")
	_, ok := f.env.App.Presence.Main(f.agentID)
	require.True(t, ok, "the pinned session is the principal again")
	require.Equal(t, []string{plain.ID}, messageIDs(t, back.Drain(700*time.Millisecond)),
		"the principal's backlog has the group message only")
}

// SN-10: the cap of an attaching session's stand-ins counts the messages it stands in for, not the
// principal's whole queue: a mention followed by many plain group messages still reaches it.
func TestAMentionBehindManyPlainMessagesReachesAnAttachingParallelSession(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dialSession(t, "lead-session")
	f.settle(t, principal)
	principal.Close()
	f.waitPrincipalGone(t)

	mention := f.post(t, "@atlas please check the report")
	for i := range agents.BacklogLimit + 10 {
		f.post(t, fmt.Sprintf("plain %d", i))
	}

	parallel := f.dialSession(t, "side-session")
	frame := parallel.Message(time.Minute)
	require.Equal(t, mention.ID, frame.Message.ID)
	require.False(t, frame.Context)
	parallel.ExpectNone(500 * time.Millisecond)
}

// SN-10: an attaching session is given the oldest BacklogLimit messages it stands in for; a notice after
// them says how many more wait in the conversation, and those stay in the principal's queue.
func TestAnAttachingParallelSessionIsToldOfTheStandInsBeyondTheCap(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dialSession(t, "lead-session")
	f.settle(t, principal)
	principal.Close()
	f.waitPrincipalGone(t)

	dm, post := f.openDM(t)
	const extra = 5
	var sent []dto.Message
	for i := range agents.BacklogLimit + extra {
		sent = append(sent, post(fmt.Sprintf("dm %d", i)))
	}
	f.flushDispatch(t) // nothing is left for a live push once the session is there

	parallel := f.dialSession(t, "side-session")
	for _, want := range sent[:agents.BacklogLimit] {
		frame := parallel.Message(time.Minute)
		require.Equal(t, want.ID, frame.Message.ID, "the oldest first")
		require.False(t, frame.Context)
	}
	notice := parallel.Notice(time.Minute)
	require.Equal(t, dm.ID, notice.Room.ID)
	require.Equal(t, agents.StandInMoreNotice(extra), notice.Text)
	parallel.ExpectNone(500 * time.Millisecond)
}

// SN-10, SESS-14: a parallel session whose process reconnects while the principal is offline gets its own
// unacknowledged message again from its backlog, and the stand-in after it; the stand-in's context does not
// write the first one a second time.
func TestAReconnectingParallelSessionGetsItsOwnUnackedMessageOnce(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)
	principal.Close()
	f.waitPrincipalGone(t)
	live := testkit.NewLiveID()
	first := f.dialWorktreeAs(t, live)
	f.settle(t, first)

	own := f.post(t, "@atlas:wt this one is yours")
	require.Equal(t, own.ID, first.Message(time.Minute).Message.ID)
	standIn := f.post(t, "@atlas is anyone there?")
	frame := first.Message(time.Minute)
	require.Equal(t, standIn.ID, frame.Message.ID)
	require.False(t, frame.Context)
	first.ExpectNone(300 * time.Millisecond)

	second := f.dialWorktreeAs(t, live)
	var got []string
	for _, fr := range second.Drain(700 * time.Millisecond) {
		if fr.Type != dto.AgentFrameTypeMessage {
			continue
		}
		var v dto.AgentMessageFrame
		require.NoError(t, json.Unmarshal(fr.Raw, &v))
		require.False(t, v.Context, "neither is context: %s", v.Message.Text)
		got = append(got, v.Message.ID)
	}
	require.Equal(t, []string{own.ID, standIn.ID}, got, "each message once, the session's own first")
}

// SN-10, SN-03: in trusted mode the stand-ins and their context are filtered as for any notification: an
// untrusted message is neither a stand-in nor context, live or in the backlog of a session that comes back.
func TestTrustedModeFiltersTheStandInsAndTheirContext(t *testing.T) {
	f := newSessionFixture(t)
	bob := f.env.NewUser("bobby")
	f.addUserMember(t, bob)
	f.setDelivery(t, dto.DeliveryModeTrusted)
	principal := f.dialSession(t, "lead-session")
	f.settle(t, principal)
	principal.Close()
	f.waitPrincipalGone(t)
	postAs := func(c *testkit.Client, text string) dto.Message {
		t.Helper()
		return testkit.Decode[dto.Message](c.Post("/api/v1/rooms/"+f.room.ID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
	}

	parallel := f.dialSession(t, "side-session")
	f.settle(t, parallel)
	postAs(bob, "noise from a stranger")
	postAs(bob, "@atlas a stranger asks")
	owner := f.post(t, "the owner's context")
	mention := f.post(t, "@atlas please look")
	context := parallel.Message(time.Minute)
	require.Equal(t, owner.ID, context.Message.ID, "the untrusted messages are not context")
	require.True(t, context.Context)
	frame := parallel.Message(time.Minute)
	require.Equal(t, mention.ID, frame.Message.ID)
	require.False(t, frame.Context)
	parallel.ExpectNone(300 * time.Millisecond)
	parallel.Ack(mention.ID)
	f.waitStandInAcked(t, "claude-2", mention.ID)
	parallel.Close()
	f.waitSessionGone(t, "claude-2")

	postAs(bob, "more noise")
	postAs(bob, "@atlas the stranger again")
	more := f.post(t, "more from the owner")
	again := f.post(t, "@atlas and again")
	back := f.dialSession(t, "side-session")
	context = back.Message(time.Minute)
	require.Equal(t, more.ID, context.Message.ID)
	require.True(t, context.Context)
	frame = back.Message(time.Minute)
	require.Equal(t, again.ID, frame.Message.ID)
	require.False(t, frame.Context)
	back.ExpectNone(500 * time.Millisecond)
}

// SN-10, DLV-08: while the loop guard holds a room, an attaching parallel session is given nothing of it.
func TestAPausedRoomHasNoStandIns(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dialSession(t, "lead-session")
	f.settle(t, principal)
	principal.Close()
	f.waitPrincipalGone(t)

	f.post(t, "@atlas before the pause")
	require.NoError(t, f.env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
		return q.SetRoomLoopState(context.Background(), db.SetRoomLoopStateParams{AgentsPaused: true, ID: f.room.ID})
	}))
	parallel := f.dialSession(t, "side-session")
	parallel.ExpectNone(700 * time.Millisecond)
}

// SN-10: a stand-in the session did not acknowledge before its connection ended comes back to its next
// connection, and the acknowledgement there consumes it for the principal.
func TestAStandInIsConsumedByTheAckOfTheSessionsNextConnection(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dialSession(t, "lead-session")
	f.settle(t, principal)
	principal.Close()
	f.waitPrincipalGone(t)
	_, post := f.openDM(t)

	first := f.dialSession(t, "side-session")
	f.settle(t, first)
	m := post("hello?")
	require.Equal(t, m.ID, first.Message(time.Minute).Message.ID)
	first.Close()
	f.waitSessionGone(t, "claude-2")

	second := f.dialSession(t, "side-session")
	frame := second.Message(time.Minute)
	require.Equal(t, m.ID, frame.Message.ID, "the unacknowledged stand-in comes back")
	require.False(t, frame.Context)
	second.Ack(m.ID)
	f.waitStandInAcked(t, "claude-2", m.ID)

	back := f.dialSession(t, "lead-session")
	require.Empty(t, messageIDs(t, back.Drain(700*time.Millisecond)), "the principal is not handed it again")
}

// SN-10: an attaching parallel session is given only what it has not read. Messages it already read (its read
// position passed them: a live delivery it acknowledged, a read_unread) are not stand-ins again, though the
// principal's queue still holds them: a reconnect, as after a server restart, must not bring old DMs back as
// new ones.
func TestAnAttachingParallelSessionIsNotGivenWhatItAlreadyRead(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dialSession(t, "lead-session")
	f.settle(t, principal)
	principal.Close()
	f.waitPrincipalGone(t)

	dm, post := f.openDM(t)
	post("hi")
	read := post("still there?")
	// The session read the conversation up to here earlier, without standing in for the principal.
	require.NoError(t, f.env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
		return q.AdvanceSessionReadSeq(context.Background(), db.AdvanceSessionReadSeqParams{
			AgentID: f.agentID, SessionKey: "side-session", RoomID: dm.ID, ReadSeq: read.Seq,
			Notify: agents.NotifyOn, UpdatedAt: time.Now().UnixMilli(),
		})
	}))
	fresh := post("one more thing")
	// Every post is dispatched before the session attaches: a late live push is not what this checks.
	f.flushDispatch(t)

	parallel := f.dialSession(t, "side-session")
	frame := parallel.Message(time.Minute)
	require.Equal(t, fresh.ID, frame.Message.ID, "only the message it had not read")
	require.False(t, frame.Context)
	parallel.ExpectNone(500 * time.Millisecond)
}
