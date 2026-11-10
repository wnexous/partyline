package agents_test

import (
	"context"
	"database/sql"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/app"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// session_multiaddress_test.go covers a message whose text addresses several sessions of one AI (MA-01..MA-09).

// deleteMessage deletes one of alice's messages in the fixture's room.
func (f *sessionFixture) deleteMessage(t *testing.T, messageID string) {
	t.Helper()
	f.alice.Delete("/api/v1/rooms/" + f.room.ID + "/messages/" + messageID).RequireStatus(http.StatusOK)
}

// deliveryRows reads every delivery row of a message for the fixture's AI, by label.
func (f *sessionFixture) deliveryRows(t *testing.T, messageID string) map[string]db.AgentSessionDelivery {
	t.Helper()
	rows, err := f.env.App.Store.R().ListSessionDeliveries(context.Background(), db.ListSessionDeliveriesParams{
		MessageID: messageID, AgentID: f.agentID,
	})
	require.NoError(t, err)
	out := make(map[string]db.AgentSessionDelivery, len(rows))
	for _, r := range rows {
		out[r.Label] = r
	}
	return out
}

// waitRows waits until the delivery rows of a message satisfy ok.
func (f *sessionFixture) waitRows(t *testing.T, messageID string, ok func(map[string]db.AgentSessionDelivery) bool) map[string]db.AgentSessionDelivery {
	t.Helper()
	var rows map[string]db.AgentSessionDelivery
	require.Eventually(t, func() bool {
		rows = f.deliveryRows(t, messageID)
		return ok(rows)
	}, 5*time.Second, 10*time.Millisecond)
	return rows
}

// dialLabelled opens a session of atlas in its own folder as the process live with the harness session id,
// and returns it with the label the welcome gives it.
func (f *sessionFixture) dialLabelled(t *testing.T, harnessSession, live string) (*testkit.AgentConn, string) {
	t.Helper()
	folder := testkit.FolderOf("atlas")
	folder.HarnessSession = harnessSession
	folder.Live = live
	conn, resp := f.token.DialFolder(folder)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	t.Cleanup(conn.Close)
	return conn, conn.Welcome(time.Minute).Session.Label
}

// requireAddressed reads the next frame of conn, which must be message m itself: not context, no note.
func requireAddressed(t *testing.T, conn *testkit.AgentConn, m dto.Message) {
	t.Helper()
	frame := conn.Message(time.Minute)
	require.Equal(t, m.ID, frame.Message.ID)
	require.False(t, frame.Context, "the message itself, not context")
	require.Empty(t, frame.ForLabel, "a live addressed session gets no note")
}

// MA-01, MA-02, SN-01, SN-04: a message that addresses two live sessions of the AI reaches each of them once,
// as its own message, after the conversation it had not read; each owns its own delivery row. The principal,
// which follows the room, receives one copy.
func TestTwoAddressedLiveSessionsEachReceiveTheMessage(t *testing.T) {
	f := newSessionFixture(t)
	principal, second, third := f.dial(t), f.dial(t), f.dial(t)
	f.settle(t, principal)
	f.settle(t, second)
	f.settle(t, third)

	before := f.post(t, "some context")
	require.Equal(t, before.ID, principal.Message(time.Minute).Message.ID)
	m := f.post(t, "@atlas:claude-2 and @atlas:claude-3, please compare")
	for _, conn := range []*testkit.AgentConn{second, third} {
		context := conn.Message(time.Minute)
		require.Equal(t, before.ID, context.Message.ID, "the conversation it had not read comes first")
		require.True(t, context.Context)
		requireAddressed(t, conn, m)
	}
	copied := principal.Message(time.Minute)
	require.Equal(t, m.ID, copied.Message.ID, "the principal follows the room: one copy")
	require.Empty(t, copied.ForLabel)
	for _, conn := range []*testkit.AgentConn{principal, second, third} {
		conn.ExpectNone(400 * time.Millisecond)
	}

	rows := f.waitRows(t, m.ID, func(rows map[string]db.AgentSessionDelivery) bool { return len(rows) == 3 })
	for _, label := range []string{"claude-2", "claude-3"} {
		require.Zero(t, rows[label].Context, "%s owns the message", label)
		require.False(t, rows[label].FellBackAt.Valid)
	}
	require.EqualValues(t, 1, rows[""].Context, "the principal's row is a copy")
}

// MA-03, MA-04: one live and one missing address. The live session receives the message; the principal
// receives it once, with the note of the missing label, and not also as a copy of its sibling's.
func TestALiveAndAMissingAddressGiveThePrincipalOneFrameWithTheNote(t *testing.T) {
	f := newSessionFixture(t)
	principal, second := f.dial(t), f.dial(t)
	f.settle(t, principal)
	f.settle(t, second)

	m := f.post(t, "@atlas:claude-2 and @atlas:gone, compare")
	requireAddressed(t, second, m)
	frame := principal.Message(time.Minute)
	require.Equal(t, m.ID, frame.Message.ID)
	require.Equal(t, "gone", frame.ForLabel)
	principal.ExpectNone(400 * time.Millisecond)
	second.ExpectNone(200 * time.Millisecond)

	rows := f.waitRows(t, m.ID, func(rows map[string]db.AgentSessionDelivery) bool { return len(rows) == 2 })
	require.True(t, rows["gone"].FellBackAt.Valid, "the missing label's row is born fallen back")
	require.False(t, rows["claude-2"].FellBackAt.Valid, "the live session owns its row")
}

// MA-03: two missing addresses reach a live principal once, with the note of the first one; each missing label
// has its own fallback row, one acknowledgement clears both, and a reconnect brings nothing back.
func TestTwoMissingAddressesReachThePrincipalOnce(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)

	m := f.post(t, "@atlas:api and @atlas:web, compare")
	frame := principal.Message(time.Minute)
	require.Equal(t, m.ID, frame.Message.ID)
	require.Equal(t, "api", frame.ForLabel, "the note names the first missing label")
	principal.ExpectNone(400 * time.Millisecond)
	rows := f.waitRows(t, m.ID, func(rows map[string]db.AgentSessionDelivery) bool { return len(rows) == 2 })
	require.True(t, rows["api"].FellBackAt.Valid)
	require.True(t, rows["web"].FellBackAt.Valid)

	principal.Ack(m.ID)
	f.waitRows(t, m.ID, func(rows map[string]db.AgentSessionDelivery) bool {
		return rows["api"].AckedAt.Valid && rows["web"].AckedAt.Valid
	})
	principal.Close()
	f.waitPrincipalGone(t)
	again := f.dial(t)
	require.Empty(t, messageIDs(t, again.Drain(700*time.Millisecond)), "nothing acknowledged comes back")
}

// MA-03, SESS-23: while the principal is offline (the AI is live through a parallel session), a message with two
// missing addresses waits in the principal's queue as one message, not given to the parallel session: the
// principal's backlog gives it once with the note of the first label, and one acknowledgement consumes it.
func TestTwoMissingAddressesWaitForAnOfflinePrincipalAsOneMessage(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dial(t)
	f.settle(t, principal)
	f.settle(t, parallel)
	principal.Close()
	f.waitPrincipalGone(t)

	m := f.post(t, "@atlas:api and @atlas:web, compare")
	f.waitRows(t, m.ID, func(rows map[string]db.AgentSessionDelivery) bool {
		return len(rows) == 2 && rows["api"].FellBackAt.Valid && rows["web"].FellBackAt.Valid
	})
	parallel.ExpectNone(400 * time.Millisecond)
	f.flushDispatch(t)

	back := f.dial(t)
	var got []dto.AgentMessageFrame
	for {
		frame := back.Message(time.Minute)
		got = append(got, frame)
		if frame.Message.ID == m.ID {
			break
		}
	}
	require.Equal(t, "api", got[len(got)-1].ForLabel)
	for _, id := range messageIDs(t, back.Drain(700*time.Millisecond)) {
		require.NotEqual(t, m.ID, id, "the message comes once")
	}
	back.Ack(m.ID)
	f.waitRows(t, m.ID, func(rows map[string]db.AgentSessionDelivery) bool {
		return rows["api"].AckedAt.Valid && rows["web"].AckedAt.Valid
	})
	back.Close()
	f.waitPrincipalGone(t)
	again := f.dial(t)
	for _, id := range messageIDs(t, again.Drain(700*time.Millisecond)) {
		require.NotEqual(t, m.ID, id, "one acknowledgement consumed both rows")
	}
}

// MA-05, SESS-24: an addressed session that ends before acknowledging falls back to the principal, which
// already holds the message through the other label's fallback: it is not given it a second time, live or
// when it reconnects, and its acknowledgement clears both rows.
func TestAnAddressedSessionThatEndsDoesNotRepeatAFallenBackMessage(t *testing.T) {
	f := newSessionFixture(t)
	principal, second := f.dial(t), f.dial(t)
	f.settle(t, principal)
	f.settle(t, second)

	m := f.post(t, "@atlas:claude-2 and @atlas:gone, compare")
	requireAddressed(t, second, m)
	require.Equal(t, "gone", principal.Message(time.Minute).ForLabel)
	second.Close()
	f.waitRows(t, m.ID, func(rows map[string]db.AgentSessionDelivery) bool {
		return rows["claude-2"].FellBackAt.Valid
	})
	principal.ExpectNone(500 * time.Millisecond)

	principal.Close()
	f.waitPrincipalGone(t)
	back := f.dial(t)
	frame := back.Message(time.Minute)
	require.Equal(t, m.ID, frame.Message.ID, "the unacknowledged message comes back once")
	require.Equal(t, "gone", frame.ForLabel, "with the note of the label that fell back first")
	back.ExpectNone(500 * time.Millisecond)
	back.Ack(m.ID)
	f.waitRows(t, m.ID, func(rows map[string]db.AgentSessionDelivery) bool {
		return rows["claude-2"].AckedAt.Valid && rows["gone"].AckedAt.Valid
	})
}

// MA-05, SN-04: the principal holds a copy of a message that addressed two live siblings; when both end
// before acknowledging it, their fallbacks add nothing to it.
func TestTheFallbacksOfTwoSiblingsAddNothingToThePrincipalsCopy(t *testing.T) {
	f := newSessionFixture(t)
	principal, second, third := f.dial(t), f.dial(t), f.dial(t)
	f.settle(t, principal)
	f.settle(t, second)
	f.settle(t, third)

	m := f.post(t, "@atlas:claude-2 and @atlas:claude-3, compare")
	requireAddressed(t, second, m)
	requireAddressed(t, third, m)
	require.Equal(t, m.ID, principal.Message(time.Minute).Message.ID)
	second.Close()
	third.Close()
	f.waitRows(t, m.ID, func(rows map[string]db.AgentSessionDelivery) bool {
		return rows["claude-2"].FellBackAt.Valid && rows["claude-3"].FellBackAt.Valid
	})
	principal.ExpectNone(500 * time.Millisecond)
	principal.Ack(m.ID)
	f.waitRows(t, m.ID, func(rows map[string]db.AgentSessionDelivery) bool {
		return rows["claude-2"].AckedAt.Valid && rows["claude-3"].AckedAt.Valid
	})
}

// MA-06, SESS-37, SESS-38: a session of the AI that addresses two live siblings, itself and a label with no
// live session reaches both siblings, trusted; not itself, and nothing falls back to the principal.
func TestASessionAddressingTwoSiblingsAndItselfReachesTheSiblings(t *testing.T) {
	f := newSessionFixture(t)
	principal, second, third := f.dial(t), f.dial(t), f.dial(t)
	f.settle(t, principal)
	f.settle(t, second)
	f.settle(t, third)
	folder, writer := f.dialWorktree(t)
	f.settle(t, writer)

	m := f.postFrom(t, folder, "@atlas:claude-2 @atlas:claude-3 @atlas:wt @atlas:gone sync up")
	require.Equal(t, "wt", m.AuthorLabel)
	for _, conn := range []*testkit.AgentConn{second, third} {
		frame := conn.Message(time.Minute)
		require.Equal(t, m.ID, frame.Message.ID)
		require.True(t, frame.Trusted, "the AI wrote it: its siblings trust it")
		require.Empty(t, frame.ForLabel)
	}
	for _, conn := range []*testkit.AgentConn{principal, second, third, writer} {
		conn.ExpectNone(400 * time.Millisecond)
	}
	rows := f.deliveryRows(t, m.ID)
	require.Len(t, rows, 2, "a row for each live sibling, none for the writer or the missing label")
	require.Contains(t, rows, "claude-2")
	require.Contains(t, rows, "claude-3")
}

// MA-07, SESS-39, SN-09: an edit that adds a second address reaches the session addressed before and the one
// addressed now, once each, and the principal that holds a copy once.
func TestAnEditThatAddsASecondAddressReachesBothSessions(t *testing.T) {
	f := newSessionFixture(t)
	principal, second, third := f.dial(t), f.dial(t), f.dial(t)
	f.settle(t, principal)
	f.settle(t, second)
	f.settle(t, third)

	m := f.post(t, "@atlas:claude-2 look")
	requireAddressed(t, second, m)
	require.Equal(t, m.ID, principal.Message(time.Minute).Message.ID)
	f.waitRows(t, m.ID, func(rows map[string]db.AgentSessionDelivery) bool { return len(rows) == 2 })

	f.edit(t, m.ID, "@atlas:claude-2 and @atlas:claude-3 look")
	for _, conn := range []*testkit.AgentConn{second, third, principal} {
		frame := conn.Edited(time.Minute)
		require.Equal(t, m.ID, frame.Message.ID)
		require.Equal(t, "@atlas:claude-2 and @atlas:claude-3 look", frame.Message.Text)
	}
	for _, conn := range []*testkit.AgentConn{principal, second, third} {
		conn.ExpectNone(400 * time.Millisecond)
	}
}

// MA-07, SN-09: an edit that makes a plain message address two labels with no live session reaches the
// principal, which may hold it either way, once.
func TestAnEditThatAddsTwoMissingAddressesReachesThePrincipalOnce(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)

	m := f.post(t, "compare these")
	require.Equal(t, m.ID, principal.Message(time.Minute).Message.ID)
	f.edit(t, m.ID, "@atlas:api and @atlas:web, compare these")
	require.Equal(t, m.ID, principal.Edited(time.Minute).Message.ID)
	principal.ExpectNone(400 * time.Millisecond)
}

// MA-07, SN-09: the delete of a message that addressed a live session and two missing labels reaches the live
// session once and the principal, which holds both fallbacks, once.
func TestADeleteOfAMessageWithSeveralAddressesReachesEachSessionOnce(t *testing.T) {
	f := newSessionFixture(t)
	principal, second := f.dial(t), f.dial(t)
	f.settle(t, principal)
	f.settle(t, second)

	m := f.post(t, "@atlas:claude-2, @atlas:api and @atlas:web, compare")
	requireAddressed(t, second, m)
	require.Equal(t, "api", principal.Message(time.Minute).ForLabel)
	f.waitRows(t, m.ID, func(rows map[string]db.AgentSessionDelivery) bool { return len(rows) == 3 })

	f.deleteMessage(t, m.ID)
	require.Equal(t, m.ID, second.Deleted(time.Minute).Message.ID)
	deleted := principal.Deleted(time.Minute)
	require.Equal(t, m.ID, deleted.Message.ID)
	require.Equal(t, "api", deleted.ForLabel)
	principal.ExpectNone(400 * time.Millisecond)
	second.ExpectNone(200 * time.Millisecond)
}

// MA-02, SN-01, SN-10: long-lived sessions addressed by a multi-address message acknowledge it; the server
// restarts and each reconnects - the parallel ones first, while the principal is offline - and none of them is
// given anything it already read or acknowledged. The next message brings no old context either.
func TestAMultiAddressMessageIsNotDeliveredAgainAfterARestart(t *testing.T) {
	f := newSessionFixture(t)
	lead, side1, side2 := testkit.NewLiveID(), testkit.NewLiveID(), testkit.NewLiveID()
	principal, label := f.dialLabelled(t, "lead-session", lead)
	require.Empty(t, label)
	first, label1 := f.dialLabelled(t, "side-one", side1)
	second, label2 := f.dialLabelled(t, "side-two", side2)
	require.NotEmpty(t, label1)
	require.NotEmpty(t, label2)
	f.settle(t, principal)
	f.settle(t, first)
	f.settle(t, second)

	before := f.post(t, "the plan so far")
	require.Equal(t, before.ID, principal.Message(time.Minute).Message.ID)
	principal.Ack(before.ID)
	m := f.post(t, "@atlas:"+label1+" and @atlas:"+label2+", compare")
	for _, conn := range []*testkit.AgentConn{first, second} {
		require.Equal(t, before.ID, conn.Message(time.Minute).Message.ID)
		requireAddressed(t, conn, m)
		conn.Ack(m.ID)
	}
	require.Equal(t, m.ID, principal.Message(time.Minute).Message.ID)
	principal.Ack(m.ID)
	f.waitRows(t, m.ID, func(rows map[string]db.AgentSessionDelivery) bool {
		return len(rows) == 3 && rows[""].AckedAt.Valid && rows[label1].AckedAt.Valid && rows[label2].AckedAt.Valid
	})
	f.waitReadSeq(t, "side-one", m.Seq)
	f.waitReadSeq(t, "side-two", m.Seq)

	f.env.Restart()
	first, got1 := f.dialLabelled(t, "side-one", side1)
	second, got2 := f.dialLabelled(t, "side-two", side2)
	require.Equal(t, label1, got1, "the harness session keeps its label")
	require.Equal(t, label2, got2)
	require.Empty(t, messageIDs(t, first.Drain(700*time.Millisecond)), "nothing it acknowledged comes again")
	require.Empty(t, messageIDs(t, second.Drain(300*time.Millisecond)))
	principal, _ = f.dialLabelled(t, "lead-session", lead)
	require.Empty(t, messageIDs(t, principal.Drain(700*time.Millisecond)))

	next := f.post(t, "@atlas:"+label1+" one more")
	requireAddressed(t, first, next)
	first.ExpectNone(400 * time.Millisecond)
	second.ExpectNone(200 * time.Millisecond)
	require.Equal(t, next.ID, principal.Message(time.Minute).Message.ID)
}

// SESS-21, SESS-22, SN-04: a message with one address is routed exactly as before multi-address delivery: the
// live session owns it and the principal gets a copy; a missing label is one row born fallen back and one
// frame with the note.
func TestASingleAddressIsRoutedAsBefore(t *testing.T) {
	f := newSessionFixture(t)
	principal, second := f.dial(t), f.dial(t)
	f.settle(t, principal)
	f.settle(t, second)

	m := f.post(t, "@atlas:claude-2 hi")
	requireAddressed(t, second, m)
	copied := principal.Message(time.Minute)
	require.Equal(t, m.ID, copied.Message.ID)
	require.Empty(t, copied.ForLabel)
	rows := f.waitRows(t, m.ID, func(rows map[string]db.AgentSessionDelivery) bool { return len(rows) == 2 })
	require.Zero(t, rows["claude-2"].Context)
	require.EqualValues(t, 1, rows[""].Context)

	gone := f.post(t, "@atlas:gone hi")
	frame := principal.Message(time.Minute)
	require.Equal(t, gone.ID, frame.Message.ID)
	require.Equal(t, "gone", frame.ForLabel)
	rows = f.waitRows(t, gone.ID, func(rows map[string]db.AgentSessionDelivery) bool { return len(rows) == 1 })
	require.True(t, rows["gone"].FellBackAt.Valid)
	principal.ExpectNone(400 * time.Millisecond)
	second.ExpectNone(200 * time.Millisecond)
}

// MA-09: an AI's eleventh distinct address in one text is plain text: it routes nothing, so its label has no
// row and nothing falls back for it.
func TestTheEleventhAddressOfAnAIRoutesNothing(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)

	text := ""
	for i := 1; i <= 11; i++ {
		text += "@atlas:s" + string(rune('a'+i-1)) + " "
	}
	m := f.post(t, text)
	frame := principal.Message(time.Minute)
	require.Equal(t, m.ID, frame.Message.ID)
	require.Equal(t, "sa", frame.ForLabel)
	principal.ExpectNone(400 * time.Millisecond)
	rows := f.waitRows(t, m.ID, func(rows map[string]db.AgentSessionDelivery) bool { return len(rows) == 10 })
	require.NotContains(t, rows, "sk", "the eleventh address is plain text")
}

// MA-05, SN-04: the principal acknowledges the note of a message before its live addressed sibling has been
// pushed. Its acknowledgement consumes the sibling's row too, because that row was written with the fallback
// rows: the sibling still receives its message, and when it ends without acknowledging it the principal is
// not given the message it already acknowledged a second time.
func TestAnAckOfTheNoteBeforeTheSiblingsPushLeavesNothingToFallBack(t *testing.T) {
	f := newSessionFixture(t)
	principal, second := f.dial(t), f.dial(t)
	f.settle(t, principal)
	f.settle(t, second)

	reached, release := make(chan struct{}), make(chan struct{})
	var once, released sync.Once
	t.Cleanup(func() { released.Do(func() { close(release) }) })
	agents.SetAfterNoteRoute(f.env.App.Gateway, func() {
		once.Do(func() {
			close(reached)
			<-release
		})
	})

	m := f.post(t, "@atlas:claude-2 and @atlas:gone, compare")
	<-reached
	require.Equal(t, "gone", principal.Message(time.Minute).ForLabel)
	principal.Ack(m.ID)
	f.waitRows(t, m.ID, func(rows map[string]db.AgentSessionDelivery) bool {
		return rows["gone"].AckedAt.Valid && rows["claude-2"].AckedAt.Valid
	})
	released.Do(func() { close(release) })

	requireAddressed(t, second, m)
	second.Close()
	f.waitSessionGone(t, "claude-2")
	principal.ExpectNone(500 * time.Millisecond)
	row := f.rowOf(t, "claude-2", m.ID)
	require.True(t, row.AckedAt.Valid)
	require.False(t, row.FellBackAt.Valid, "an acknowledged row never falls back")
}

// MA-02: when the fallback rows of a message's missing labels cannot be written, its live addressed session
// still receives it, through its own route.
func TestALiveAddressedSessionReceivesTheMessageWhenTheFallbackWriteFails(t *testing.T) {
	f := newSessionFixture(t)
	principal, second := f.dial(t), f.dial(t)
	f.settle(t, principal)
	f.settle(t, second)
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(f.env.App.Config.DataDir, app.DatabaseFile)+"?_pragma=busy_timeout(5000)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	_, err = raw.ExecContext(context.Background(),
		"CREATE TRIGGER forced_failure BEFORE INSERT ON agent_session_deliveries WHEN NEW.label = 'gone' BEGIN SELECT RAISE(ABORT, 'forced failure'); END")
	require.NoError(t, err)

	m := f.post(t, "@atlas:claude-2 and @atlas:gone, compare")
	requireAddressed(t, second, m)
	row := f.waitRow(t, "claude-2", m.ID)
	require.False(t, row.FellBackAt.Valid, "the live session owns its row")
	require.NotContains(t, f.deliveryRows(t, m.ID), "gone")
}

// MA-03, MA-04, SN-02: with the principal set to mentions only, a message that addresses a live sibling and a
// missing label still reaches the principal once, with the note, and the sibling as its own message.
func TestANoteAndALiveSiblingReachAPrincipalSetToMentionsOnly(t *testing.T) {
	f := newSessionFixture(t)
	principal, second := f.dial(t), f.dial(t)
	f.settle(t, principal)
	f.settle(t, second)
	f.setNotify(t, "", agents.NotifyMentions)

	m := f.post(t, "@atlas:claude-2 and @atlas:gone, compare")
	requireAddressed(t, second, m)
	frame := principal.Message(time.Minute)
	require.Equal(t, m.ID, frame.Message.ID)
	require.Equal(t, "gone", frame.ForLabel)
	principal.ExpectNone(400 * time.Millisecond)
	second.ExpectNone(200 * time.Millisecond)
}

// MA-02, SN-10: while the principal is offline, a message that addresses two live sessions and also names the
// AI by its bare @handle reaches the addressed sessions only: it is theirs, so no other live parallel session
// stands in for the principal - in a group and in a DM.
func TestAddressesWithABareMentionWhileThePrincipalIsOfflineReachOnlyTheAddressedSessions(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	second, third, fourth := f.dial(t), f.dial(t), f.dial(t)
	for _, conn := range []*testkit.AgentConn{principal, second, third, fourth} {
		f.settle(t, conn)
	}
	principal.Close()
	f.waitPrincipalGone(t)
	_, postDM := f.openDM(t)

	for _, post := range []func(string) dto.Message{
		func(text string) dto.Message { return f.post(t, text) },
		postDM,
	} {
		m := post("@atlas, @atlas:claude-2 and @atlas:claude-3, please look")
		requireAddressed(t, second, m)
		requireAddressed(t, third, m)
		fourth.ExpectNone(400 * time.Millisecond)
		second.ExpectNone(200 * time.Millisecond)
		third.ExpectNone(200 * time.Millisecond)
		rows := f.deliveryRows(t, m.ID)
		require.NotContains(t, rows, "claude-4", "nobody stands in for the principal")
		require.Zero(t, rows["claude-2"].Context)
		require.Zero(t, rows["claude-3"].Context)
	}
}

// MA-05, SN-04: the principal's acknowledgement of the note consumes the message for the whole AI, so the live
// addressed sibling's pending row becomes a tombstone too. That is the rule of a principal's copy (SN-04): when
// the sibling ends without acknowledging, nothing falls back to the principal, and when its process reconnects
// the message is not given to it again.
func TestThePrincipalsAckOfTheNoteConsumesALivePendingSibling(t *testing.T) {
	f := newSessionFixture(t)
	side := testkit.NewLiveID()
	principal, label := f.dialLabelled(t, "lead-session", testkit.NewLiveID())
	require.Empty(t, label)
	sibling, siblingLabel := f.dialLabelled(t, "side-session", side)
	require.NotEmpty(t, siblingLabel)
	f.settle(t, principal)
	f.settle(t, sibling)

	m := f.post(t, "@atlas:"+siblingLabel+" and @atlas:gone, compare")
	requireAddressed(t, sibling, m)
	require.Equal(t, "gone", principal.Message(time.Minute).ForLabel)
	require.False(t, f.rowOf(t, siblingLabel, m.ID).AckedAt.Valid, "the sibling has not acknowledged it")

	principal.Ack(m.ID)
	f.waitRows(t, m.ID, func(rows map[string]db.AgentSessionDelivery) bool {
		return rows["gone"].AckedAt.Valid && rows[siblingLabel].AckedAt.Valid
	})
	sibling.Close()
	f.waitSessionGone(t, siblingLabel)
	principal.ExpectNone(500 * time.Millisecond)
	require.False(t, f.rowOf(t, siblingLabel, m.ID).FellBackAt.Valid, "a tombstone never falls back")

	back, again := f.dialLabelled(t, "side-session", side)
	require.Equal(t, siblingLabel, again)
	require.Empty(t, messageIDs(t, back.Drain(700*time.Millisecond)), "the consumed message is not given again")
}
