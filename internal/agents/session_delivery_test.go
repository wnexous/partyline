package agents_test

import (
	"context"
	"database/sql"
	"net/http"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// sessionFixture is one AI called atlas in a group, with its agent token.
type sessionFixture struct {
	env     *testkit.Env
	alice   *testkit.Client
	token   *testkit.Client
	agentID string
	room    dto.Room
}

func newSessionFixture(t *testing.T) *sessionFixture {
	t.Helper()
	env := testkit.New(t)
	alice := env.NewUser("alice")
	agentID := env.NewAI(alice, "atlas")
	room := testkit.Decode[dto.Room](alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "Support"}).RequireStatus(http.StatusCreated))
	alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: agentID}).RequireStatus(http.StatusCreated)
	return &sessionFixture{
		env: env, alice: alice, token: alice.Token(dto.TokenKindAgent, "laptop"), agentID: agentID, room: room,
	}
}

// dial opens one session of atlas and reads its welcome frame.
func (f *sessionFixture) dial(t *testing.T) *testkit.AgentConn {
	t.Helper()
	conn, resp := f.token.DialFolder(testkit.FolderOf("atlas"))
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	conn.Welcome(time.Minute)
	return conn
}

// settle lets the connection finish the backlog it reads right after its welcome frame, so a test
// that posts next does not race that read.
func (f *sessionFixture) settle(t *testing.T, conn *testkit.AgentConn) {
	t.Helper()
	conn.ExpectNone(500 * time.Millisecond)
}

func (f *sessionFixture) post(t *testing.T, text string) dto.Message {
	t.Helper()
	return testkit.Decode[dto.Message](f.alice.Post("/api/v1/rooms/"+f.room.ID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
}

func (f *sessionFixture) edit(t *testing.T, messageID, text string) dto.Message {
	t.Helper()
	return testkit.Decode[dto.Message](f.alice.Patch("/api/v1/rooms/"+f.room.ID+"/messages/"+messageID, dto.EditMessageRequest{Text: text}).RequireStatus(http.StatusOK))
}

func (f *sessionFixture) rows(t *testing.T, label string) []db.AgentSessionDelivery {
	t.Helper()
	rows, err := f.env.App.Store.R().ListSessionDeliveriesByLabel(context.Background(), db.ListSessionDeliveriesByLabelParams{AgentID: f.agentID, Label: label})
	require.NoError(t, err)
	return rows
}

// waitRow waits for the single delivery row of label and messageID.
func (f *sessionFixture) waitRow(t *testing.T, label, messageID string) db.AgentSessionDelivery {
	t.Helper()
	var row db.AgentSessionDelivery
	require.Eventually(t, func() bool {
		rows := f.rows(t, label)
		if len(rows) != 1 || rows[0].MessageID != messageID {
			return false
		}
		row = rows[0]
		return true
	}, 5*time.Second, 10*time.Millisecond)
	return row
}

// waitPrincipalGone waits until the principal session of the AI has ended.
func (f *sessionFixture) waitPrincipalGone(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, ok := f.env.App.Presence.Main(f.agentID)
		return !ok
	}, 5*time.Second, 10*time.Millisecond)
}

// SESS-22, SESS-24: a message for a live label is that session's alone; when the session ends
// before acknowledging it, the row falls back and the principal receives the same message once,
// with the note. A late acknowledgement cannot consume it after the fallback.
func TestALabelledMessageFallsBackWhenItsSessionEnds(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dial(t)
	require.Equal(t, "claude-2", f.env.App.Presence.Sessions(f.agentID)[1].Label)
	f.settle(t, principal)
	f.settle(t, parallel)

	m := f.post(t, "@atlas:claude-2 hello")
	require.Equal(t, m.ID, parallel.Message(time.Minute).Message.ID)
	principal.ExpectNone(400 * time.Millisecond)

	row := f.waitRow(t, "claude-2", m.ID)
	require.False(t, row.FellBackAt.Valid, "the live label owns the message")
	parallel.Close()

	fallback := principal.Message(time.Minute)
	require.Equal(t, m.ID, fallback.Message.ID)
	require.Equal(t, "claude-2", fallback.ForLabel)
	row = f.waitRow(t, "claude-2", m.ID)
	require.True(t, row.FellBackAt.Valid, "the end of the session marked the row for the principal")

	// The closed session's late acknowledgement is ignored: the row already belongs to the principal.
	var acked int64
	require.NoError(t, f.env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
		var err error
		acked, err = q.AckSessionDelivery(context.Background(), db.AckSessionDeliveryParams{
			AckedAt: sql.NullInt64{Int64: time.Now().UnixMilli(), Valid: true}, MessageID: m.ID, AgentID: f.agentID, OwnerLive: row.OwnerLive,
		})
		return err
	}))
	require.Zero(t, acked)
	require.False(t, f.waitRow(t, "claude-2", m.ID).AckedAt.Valid)

	principal.Ack(m.ID)
	require.Eventually(t, func() bool {
		rows := f.rows(t, "claude-2")
		return len(rows) == 1 && rows[0].AckedAt.Valid && rows[0].FellBackAt.Valid
	}, 5*time.Second, 10*time.Millisecond, "the principal's acknowledgement consumes the fallback and leaves a tombstone")
}

// SESS-22: a label with no live session is the principal's from the start, with the note.
func TestAMessageForANonLiveLabelReachesThePrincipalWithTheNote(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)

	m := f.post(t, "@atlas:wt hi")
	got := principal.Message(time.Minute)
	require.Equal(t, m.ID, got.Message.ID)
	require.Equal(t, "wt", got.ForLabel)
	row := f.waitRow(t, "wt", m.ID)
	require.True(t, row.FellBackAt.Valid, "the row is born fallen back")
}

// SESS-23: when the principal is offline, a fallback waits for it and is not pushed to another
// session of the same AI.
func TestAFallbackWaitsForAnOfflinePrincipal(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dial(t)
	f.settle(t, principal)
	f.settle(t, parallel)
	principal.Close()
	f.waitPrincipalGone(t)

	m := f.post(t, "@atlas:wt hi")
	parallel.ExpectNone(500 * time.Millisecond)
	row := f.waitRow(t, "wt", m.ID)
	require.True(t, row.FellBackAt.Valid)
}

// SESS-23: a plain message stays in the principal's queue; a labelled session is not its reader.
func TestAPlainMessageWaitsForTheOfflinePrincipalOnly(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dial(t)
	f.settle(t, principal)
	f.settle(t, parallel)
	principal.Close()
	f.waitPrincipalGone(t)

	m := f.post(t, "hello")
	parallel.ExpectNone(500 * time.Millisecond)
	require.Empty(t, f.rows(t, "claude-2"), "a plain message has no delivery row")

	parallel.Close()
	again := f.dial(t)
	require.Equal(t, m.ID, again.Message(time.Minute).Message.ID, "the principal's backlog kept it")
}

// SESS-27: an edit after the addressed session ended reaches the principal, which now holds it.
func TestAnEditAfterTheLabelledSessionEndedReachesThePrincipal(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dial(t)
	f.settle(t, principal)
	f.settle(t, parallel)
	m := f.post(t, "@atlas:claude-2 hi")
	require.Equal(t, m.ID, parallel.Message(time.Minute).Message.ID)
	parallel.Close()
	require.Equal(t, m.ID, principal.Message(time.Minute).Message.ID)

	f.edit(t, m.ID, "@atlas:claude-2 updated")
	frame := principal.Edited(time.Minute)
	require.Equal(t, m.ID, frame.Message.ID)
	require.Equal(t, "@atlas:claude-2 updated", frame.Message.Text)

	// The fallback row is still the principal's until it acknowledges it.
	row := f.waitRow(t, "claude-2", m.ID)
	require.True(t, row.FellBackAt.Valid)
}

// SESS-24: once a message fell back to the principal, the label cannot read it again, not even from
// the backlog of a new session with the same label.
func TestAFallenBackMessageIsNeverTheLabelsAgain(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dial(t)
	f.settle(t, principal)
	f.settle(t, parallel)
	m := f.post(t, "@atlas:claude-2 hello")
	require.Equal(t, m.ID, parallel.Message(time.Minute).Message.ID)
	parallel.Close()
	require.Equal(t, m.ID, principal.Message(time.Minute).Message.ID)

	again := f.dial(t)
	again.ExpectNone(500 * time.Millisecond)
	require.Len(t, f.rows(t, "claude-2"), 1, "the row stays the principal's")
}

// worktreeFolder is the fixture AI's session in a linked worktree of its own folder: the AI of the
// repository, labelled after the worktree (SESS-01, SESS-11).
func worktreeFolder() testkit.Folder {
	main := testkit.FolderOf("atlas")
	return testkit.Folder{Machine: main.Machine, Path: "/work/wt", MainFolder: main.Path, Harness: main.Harness}
}

// dialWorktree opens the AI's :wt session and returns the identity it dialed with, live id
// included, so a post from it carries its real author_label (SESS-29).
func (f *sessionFixture) dialWorktree(t *testing.T) (testkit.Folder, *testkit.AgentConn) {
	t.Helper()
	folder := worktreeFolder()
	folder.Live = testkit.NewLiveID()
	conn, resp := f.token.DialFolder(folder)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	conn.Welcome(time.Minute)
	t.Cleanup(conn.Close)
	return folder, conn
}

// postFrom posts in the room as the session of folder; the live id it carries makes the stored
// author_label the one the server resolved.
func (f *sessionFixture) postFrom(t *testing.T, folder testkit.Folder, text string) dto.Message {
	t.Helper()
	return testkit.Decode[dto.Message](f.token.AsFolder(folder).
		Post("/api/v1/agent/rooms/"+f.room.ID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
}

// editFrom edits one of the AI's messages as the session of folder.
func (f *sessionFixture) editFrom(t *testing.T, folder testkit.Folder, messageID, text string) dto.Message {
	t.Helper()
	return testkit.Decode[dto.Message](f.token.AsFolder(folder).
		Patch("/api/v1/agent/messages/"+messageID, dto.EditMessageRequest{Text: text}).RequireStatus(http.StatusOK))
}

// deleteFrom deletes one of the AI's messages as the session of folder.
func (f *sessionFixture) deleteFrom(t *testing.T, folder testkit.Folder, messageID string) {
	t.Helper()
	f.token.AsFolder(folder).Delete("/api/v1/agent/messages/" + messageID).RequireStatus(http.StatusOK)
}

// SESS-37: the AI's principal writes to a live sibling of its own AI: the sibling receives the
// message once with trusted: true and owns a pending delivery row, and the principal receives
// nothing.
func TestASessionsMessageReachesItsLiveSibling(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)
	_, sibling := f.dialWorktree(t)
	f.settle(t, sibling)

	m := f.postFrom(t, testkit.FolderOf("atlas"), "@atlas:wt do X")
	require.Empty(t, m.AuthorLabel, "the principal writes without a label")
	frame := sibling.Message(time.Minute)
	require.Equal(t, m.ID, frame.Message.ID)
	require.True(t, frame.Trusted, "the AI wrote it: the sibling trusts it")
	require.Empty(t, frame.ForLabel, "a message the AI wrote never carries the note")
	principal.ExpectNone(400 * time.Millisecond)

	row := f.waitRow(t, "wt", m.ID)
	require.False(t, row.FellBackAt.Valid, "the live sibling owns the message")
}

// SESS-38: a delivery row of the AI's own message is acknowledged (it stays as a tombstone, never
// deleted) when its session ends before acknowledging it, and the principal never receives it as a
// fallback.
func TestASelfDeliveryIsAcknowledgedWhenItsSessionEnds(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)
	_, sibling := f.dialWorktree(t)
	f.settle(t, sibling)

	m := f.postFrom(t, testkit.FolderOf("atlas"), "@atlas:wt do X")
	require.Equal(t, m.ID, sibling.Message(time.Minute).Message.ID)
	require.False(t, f.waitRow(t, "wt", m.ID).FellBackAt.Valid)

	sibling.Close()
	require.Eventually(t, func() bool {
		rows := f.rows(t, "wt")
		return len(rows) == 1 && rows[0].AckedAt.Valid
	}, 5*time.Second, 10*time.Millisecond, "the row of the AI's own message is acknowledged, not fallen back")
	require.False(t, f.rows(t, "wt")[0].FellBackAt.Valid)
	principal.ExpectNone(400 * time.Millisecond)
}

// SESS-38: a message the AI's session writes reaches no session of that AI when it names no label,
// the writing session's own label, or a label with no live session; no frame and no row either.
func TestASessionsMessageDoesNotComeBackToItsOwnSessions(t *testing.T) {
	cases := []struct{ name, text string }{
		{"no label", "@atlas do Y"},
		{"its own label", "@atlas:wt do Z"},
		{"a label with no live session", "@atlas:gone do W"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSessionFixture(t)
			principal := f.dial(t)
			f.settle(t, principal)
			folder, sibling := f.dialWorktree(t)
			f.settle(t, sibling)

			m := f.postFrom(t, folder, tc.text)
			require.Equal(t, "wt", m.AuthorLabel, "the stored label names the writing session")
			sibling.ExpectNone(400 * time.Millisecond)
			principal.ExpectNone(400 * time.Millisecond)
			require.Empty(t, f.rows(t, "wt"))
		})
	}
}

// SESS-39: an edit that adds a label to a message the AI wrote reaches the newly addressed live
// sibling once, a delete of it reaches that sibling, and the principal receives neither.
func TestAnEditAndADeleteOfASessionsMessageReachItsLiveSibling(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)
	_, sibling := f.dialWorktree(t)
	f.settle(t, sibling)
	asPrincipal := testkit.FolderOf("atlas")

	m := f.postFrom(t, asPrincipal, "hello")
	sibling.ExpectNone(400 * time.Millisecond)
	principal.ExpectNone(400 * time.Millisecond)

	f.editFrom(t, asPrincipal, m.ID, "@atlas:wt updated")
	frame := sibling.Edited(time.Minute)
	require.Equal(t, m.ID, frame.Message.ID)
	require.Equal(t, "@atlas:wt updated", frame.Message.Text)
	principal.ExpectNone(400 * time.Millisecond)

	f.deleteFrom(t, asPrincipal, m.ID)
	deleted := sibling.Deleted(time.Minute)
	require.Equal(t, m.ID, deleted.Message.ID)
	principal.ExpectNone(400 * time.Millisecond)
}

// dialWorktreeAs opens the AI's :wt session as the process live.
func (f *sessionFixture) dialWorktreeAs(t *testing.T, live string) *testkit.AgentConn {
	t.Helper()
	folder := worktreeFolder()
	folder.Live = live
	conn, resp := f.token.DialFolder(folder)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	conn.Welcome(time.Minute)
	t.Cleanup(conn.Close)
	return conn
}

// rowOf reads the delivery row of a message.
func (f *sessionFixture) rowOf(t *testing.T, messageID string) db.AgentSessionDelivery {
	t.Helper()
	row, err := f.env.App.Store.R().GetSessionDelivery(context.Background(), db.GetSessionDeliveryParams{MessageID: messageID, AgentID: f.agentID})
	require.NoError(t, err)
	return row
}

// waitSessionGone waits until no live session of the AI has the label.
func (f *sessionFixture) waitSessionGone(t *testing.T, label string) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, ok := f.env.App.Presence.SessionByLabel(f.agentID, label)
		return !ok
	}, 5*time.Second, 10*time.Millisecond)
}

// ownPending writes a pending row for a message as if it had been pushed to the connection owner.
func (f *sessionFixture) ownPending(t *testing.T, messageID, label, owner string) {
	t.Helper()
	require.NoError(t, f.env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
		return q.CreateSessionDelivery(context.Background(), db.CreateSessionDeliveryParams{
			MessageID: messageID, AgentID: f.agentID, Label: label, OwnerLive: owner, CreatedAt: time.Now().UnixMilli(),
		})
	}))
}

// SESS-17, SESS-24: a parallel session has no cursor and no backlog. The principal's unacknowledged
// plain message is not delivered to it, and the principal still receives it.
func TestAParallelSessionGetsNoPrincipalBacklog(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)
	m := f.post(t, "plain for the principal")
	require.Equal(t, m.ID, principal.Message(time.Minute).Message.ID)

	_, parallel := f.dialWorktree(t)
	parallel.ExpectNone(700 * time.Millisecond)
	require.Empty(t, f.rows(t, "wt"))

	principal.Close()
	f.waitPrincipalGone(t)
	again := f.dial(t)
	require.Equal(t, m.ID, again.Message(time.Minute).Message.ID, "the principal's queue kept it")
}

// SESS-24: the acknowledgement of a labelled session leaves a tombstone, so the principal's next
// backlog does not contain the message, and nothing is deleted.
func TestALabelledAckKeepsTheMessageOutOfThePrincipalsBacklog(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)
	_, sibling := f.dialWorktree(t)
	f.settle(t, sibling)

	m := f.post(t, "@atlas:wt hello")
	require.Equal(t, m.ID, sibling.Message(time.Minute).Message.ID)
	sibling.Ack(m.ID)
	require.Eventually(t, func() bool { return f.rowOf(t, m.ID).AckedAt.Valid }, 5*time.Second, 10*time.Millisecond)
	row := f.rowOf(t, m.ID)
	require.False(t, row.FellBackAt.Valid)

	principal.Close()
	f.waitPrincipalGone(t)
	again := f.dial(t)
	again.ExpectNone(500 * time.Millisecond)
	require.Len(t, f.rows(t, "wt"), 1, "the tombstone stays")
}

// SESS-24: only the connection that owns a pending row acknowledges it, even when another one holds
// the same label.
func TestAnAckOfAnotherConnectionsRowIsIgnored(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)
	other := testkit.NewLiveID()
	mine := f.dialWorktreeAs(t, testkit.NewLiveID())
	f.settle(t, mine)

	m := f.post(t, "@atlas:wt on a row the new connection owns")
	require.Equal(t, m.ID, mine.Message(time.Minute).Message.ID)
	mine.Ack(m.ID)
	require.Eventually(t, func() bool { return f.rowOf(t, m.ID).AckedAt.Valid }, 5*time.Second, 10*time.Millisecond)

	foreign := f.post(t, "plain, with a row another connection owns")
	require.Equal(t, foreign.ID, principal.Message(time.Minute).Message.ID)
	f.ownPending(t, foreign.ID, "wt", other)
	mine.Ack(foreign.ID)
	mine.ExpectNone(400 * time.Millisecond)
	row := f.rowOf(t, foreign.ID)
	require.False(t, row.AckedAt.Valid, "the ack of a connection that does not own the row changes nothing")
	require.False(t, row.FellBackAt.Valid)
}

// SESS-24: the sweep of a connection that ends is keyed by the connection, not by the label. A row a
// newer connection owns (a different live id, the same label) is not touched by the old one's end, and
// that connection receives it from its own backlog.
func TestTheEndOfAnOldConnectionLeavesTheRowsOfANewOneAlone(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)
	oldLive, newLive := testkit.NewLiveID(), testkit.NewLiveID()
	old := f.dialWorktreeAs(t, oldLive)
	f.settle(t, old)

	r := f.post(t, "@atlas:wt for the connection that comes next")
	require.Equal(t, r.ID, old.Message(time.Minute).Message.ID)
	own := f.post(t, "plain row the old connection does not own")
	require.Equal(t, own.ID, principal.Message(time.Minute).Message.ID)
	f.ownPending(t, own.ID, "wt", newLive)

	old.Close()
	f.waitSessionGone(t, "wt")
	principal.Message(time.Minute) // the old connection's own row fell back
	principal.ExpectNone(400 * time.Millisecond)
	require.False(t, f.rowOf(t, own.ID).FellBackAt.Valid, "the old sweep did not touch the new owner's row")
	require.True(t, f.rowOf(t, r.ID).FellBackAt.Valid, "its own unacknowledged row fell back")

	fresh := f.dialWorktreeAs(t, newLive)
	require.Equal(t, own.ID, fresh.Message(time.Minute).Message.ID, "the new connection receives the row it owns")
}

// SESS-19, SESS-24: ending the AI's sessions (Kick) sweeps each connection it closes: a pending
// labelled row falls back, and the principal receives it with the note on its next connect.
func TestAKickFallsBackThePendingLabelledRows(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)
	_, sibling := f.dialWorktree(t)
	f.settle(t, sibling)
	m := f.post(t, "@atlas:wt hello")
	require.Equal(t, m.ID, sibling.Message(time.Minute).Message.ID)

	f.env.App.Presence.Kick(context.Background(), f.agentID, agents.CloseTakenOver, "ended")
	require.Eventually(t, func() bool { return f.rowOf(t, m.ID).FellBackAt.Valid }, 5*time.Second, 10*time.Millisecond,
		"the row fell back")

	again := f.dial(t)
	got := again.Message(time.Minute)
	require.Equal(t, m.ID, got.Message.ID)
	require.Equal(t, "wt", got.ForLabel)
}

// SESS-14, SESS-24: the same process reconnecting replaces its socket without a sweep: nothing falls
// back, and the new socket receives the row it still owns again.
func TestTheSameLiveIDReconnectingGetsItsUnackedRowAgain(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)
	live := testkit.NewLiveID()
	first := f.dialWorktreeAs(t, live)
	f.settle(t, first)
	m := f.post(t, "@atlas:wt unacknowledged")
	require.Equal(t, m.ID, first.Message(time.Minute).Message.ID)

	second := f.dialWorktreeAs(t, live)
	require.Equal(t, m.ID, second.Message(time.Minute).Message.ID, "the new socket receives its own unacknowledged row")
	principal.ExpectNone(500 * time.Millisecond)
	row := f.rowOf(t, m.ID)
	require.False(t, row.FellBackAt.Valid, "nothing fell back")
	require.False(t, row.AckedAt.Valid)

	second.Ack(m.ID)
	require.Eventually(t, func() bool { return f.rowOf(t, m.ID).AckedAt.Valid }, 5*time.Second, 10*time.Millisecond)
}

// SESS-27: an edit of a message whose row fell back reaches the principal only, even when a newer
// session holds the same label.
func TestAnEditOfAFallenBackMessageReachesThePrincipalOnly(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)
	old := f.dialWorktreeAs(t, testkit.NewLiveID())
	f.settle(t, old)
	m := f.post(t, "@atlas:wt first")
	require.Equal(t, m.ID, old.Message(time.Minute).Message.ID)
	old.Close()
	require.Equal(t, m.ID, principal.Message(time.Minute).Message.ID)

	newer := f.dialWorktreeAs(t, testkit.NewLiveID())
	f.settle(t, newer)
	f.edit(t, m.ID, "@atlas:wt second")
	frame := principal.Edited(time.Minute)
	require.Equal(t, m.ID, frame.Message.ID)
	require.Equal(t, "wt", frame.ForLabel)
	newer.ExpectNone(500 * time.Millisecond)
}

// SESS-27: an edit of a message a live session owns reaches its owner connection; once the session
// acknowledged it, the edit still reaches the label while it is live.
func TestAnEditFollowsTheOwnerOfThePendingRow(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)
	owner := f.dialWorktreeAs(t, testkit.NewLiveID())
	f.settle(t, owner)
	m := f.post(t, "@atlas:wt first")
	require.Equal(t, m.ID, owner.Message(time.Minute).Message.ID)

	f.edit(t, m.ID, "@atlas:wt second")
	require.Equal(t, m.ID, owner.Edited(time.Minute).Message.ID)
	principal.ExpectNone(400 * time.Millisecond)

	owner.Ack(m.ID)
	require.Eventually(t, func() bool { return f.rowOf(t, m.ID).AckedAt.Valid }, 5*time.Second, 10*time.Millisecond)
	f.edit(t, m.ID, "@atlas:wt third")
	require.Equal(t, m.ID, owner.Edited(time.Minute).Message.ID, "acknowledged by its label: the label is live")
	principal.ExpectNone(400 * time.Millisecond)
}

// SESS-27: an edit of a message the label acknowledged reaches nobody once that label is gone.
func TestAnEditOfAnAcknowledgedMessageReachesNobodyWhenTheLabelIsGone(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)
	owner := f.dialWorktreeAs(t, testkit.NewLiveID())
	f.settle(t, owner)
	m := f.post(t, "@atlas:wt first")
	require.Equal(t, m.ID, owner.Message(time.Minute).Message.ID)
	owner.Ack(m.ID)
	require.Eventually(t, func() bool { return f.rowOf(t, m.ID).AckedAt.Valid }, 5*time.Second, 10*time.Millisecond)
	owner.Close()
	f.waitSessionGone(t, "wt")

	f.edit(t, m.ID, "@atlas:wt second")
	principal.ExpectNone(600 * time.Millisecond)
}

// SESS-38: the gateway never hands a session its own message, whatever the dispatcher asks: a push
// that names the writing session's own label delivers nothing and writes no row.
func TestAPushToTheAuthorsOwnLabelDeliversNothing(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)
	folder, sibling := f.dialWorktree(t)
	f.settle(t, sibling)
	m := f.postFrom(t, folder, "hello")
	require.Equal(t, "wt", m.AuthorLabel)

	msg, err := f.env.App.Gateway.Message(context.Background(), m.ID)
	require.NoError(t, err)
	require.False(t, f.env.App.Gateway.Push(f.agentID, "wt", dto.AgentMessage{Room: dto.RoomRef{ID: f.room.ID, Name: "Support"}, Trusted: true, Message: msg}))
	sibling.ExpectNone(500 * time.Millisecond)
	principal.ExpectNone(200 * time.Millisecond)
	require.Empty(t, f.rows(t, "wt"))
}

// pending reads the principal's queue the way a tools-only reader does.
func (f *sessionFixture) pending(t *testing.T) []dto.AgentMessage {
	t.Helper()
	a, err := f.env.App.Store.R().GetAgent(context.Background(), f.agentID)
	require.NoError(t, err)
	out, err := f.env.App.Gateway.Pending(context.Background(), a, "", 20)
	require.NoError(t, err)
	return out
}

func (f *sessionFixture) cursor(t *testing.T) (int64, error) {
	t.Helper()
	return f.env.App.Store.R().GetAgentCursor(context.Background(), db.GetAgentCursorParams{AgentID: f.agentID, RoomID: f.room.ID})
}

// SESS-24: a Pending call that returns only a fallback row never moves the principal's cursor, whether
// the fallback is below it (it was jumped over by a later acknowledgement) or above everything the
// cursor stream holds.
func TestPendingWithOnlyAFallbackKeepsTheCursor(t *testing.T) {
	t.Run("below the cursor", func(t *testing.T) {
		f := newSessionFixture(t)
		principal := f.dial(t)
		f.settle(t, principal)
		fallback := f.post(t, "@atlas:wt addressed to no live session")
		require.Equal(t, fallback.ID, principal.Message(time.Minute).Message.ID)
		later := f.post(t, "plain, acknowledged first")
		require.Equal(t, later.ID, principal.Message(time.Minute).Message.ID)
		principal.Ack(later.ID)
		require.Eventually(t, func() bool {
			seq, err := f.cursor(t)
			return err == nil && seq == later.Seq
		}, 5*time.Second, 10*time.Millisecond)
		principal.Close()
		f.waitPrincipalGone(t)

		got := f.pending(t)
		require.Len(t, got, 1)
		require.Equal(t, fallback.ID, got[0].Message.ID)
		require.Equal(t, "wt", got[0].ForLabel)
		seq, err := f.cursor(t)
		require.NoError(t, err)
		require.Equal(t, later.Seq, seq, "the cursor did not move")
		require.True(t, f.rowOf(t, fallback.ID).AckedAt.Valid, "the read consumed the fallback and left a tombstone")
		require.Empty(t, f.pending(t))
	})
	t.Run("above the cursor stream", func(t *testing.T) {
		f := newSessionFixture(t)
		principal := f.dial(t)
		f.settle(t, principal)
		fallback := f.post(t, "@atlas:wt addressed to no live session")
		require.Equal(t, fallback.ID, principal.Message(time.Minute).Message.ID)
		principal.Close()
		f.waitPrincipalGone(t)

		got := f.pending(t)
		require.Len(t, got, 1)
		require.Equal(t, fallback.ID, got[0].Message.ID)
		_, err := f.cursor(t)
		require.ErrorIs(t, err, sql.ErrNoRows, "a fallback's sequence never becomes the cursor")
	})
}

// dialPrincipalAs opens the AI's principal session as the process live.
func (f *sessionFixture) dialPrincipalAs(t *testing.T, live string) *testkit.AgentConn {
	t.Helper()
	folder := testkit.FolderOf("atlas")
	folder.Live = live
	conn, resp := f.token.DialFolder(folder)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	conn.Welcome(time.Minute)
	t.Cleanup(conn.Close)
	return conn
}

// pushRow runs the dispatcher's push of a stored message towards a label.
func (f *sessionFixture) pushRow(t *testing.T, m dto.Message, label string) bool {
	t.Helper()
	msg, err := f.env.App.Gateway.Message(context.Background(), m.ID)
	require.NoError(t, err)
	return f.env.App.Gateway.Push(f.agentID, label, dto.AgentMessage{Room: dto.RoomRef{ID: f.room.ID, Name: "Support"}, Trusted: true, Message: msg})
}

// SESS-14, SESS-24: a row belongs to the connection that holds its label. A process that was labelled and
// is now the principal has the same live id as the row's owner, but it is not the session the row was
// pushed to: the row falls back, and the principal receives it with the note instead of as a plain message.
func TestAPrincipalWithTheOwnersLiveIDIsNotGivenALabelledRow(t *testing.T) {
	f := newSessionFixture(t)
	live := testkit.NewLiveID()
	principal := f.dialPrincipalAs(t, live)
	f.settle(t, principal)
	m := f.post(t, "plain, then claimed by a label this process no longer holds")
	require.Equal(t, m.ID, principal.Message(time.Minute).Message.ID)
	f.ownPending(t, m.ID, "wt", live)

	f.pushRow(t, m, "wt")
	got := principal.Message(time.Minute)
	require.Equal(t, m.ID, got.Message.ID)
	require.Equal(t, "wt", got.ForLabel, "the principal receives the row with the note")
	require.Eventually(t, func() bool { return f.rowOf(t, m.ID).FellBackAt.Valid }, 5*time.Second, 10*time.Millisecond,
		"the row fell back instead of staying pending")
}

// SESS-14, SESS-24: a connection of the owning process that attaches while the release looks for the
// owner receives the row, and the principal does not get it as a fallback too.
func TestAReconnectDuringTheReleaseDoesNotDeliverTwice(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)
	m := f.post(t, "plain, claimed by a session that is not attached yet")
	require.Equal(t, m.ID, principal.Message(time.Minute).Message.ID)
	live := testkit.NewLiveID()
	f.ownPending(t, m.ID, "wt", live)

	var sibling *testkit.AgentConn
	agents.SetAfterOwnerMiss(f.env.App.Gateway, func() { sibling = f.dialWorktreeAs(t, live) })
	f.pushRow(t, m, "wt")
	require.NotNil(t, sibling, "the owner was missing when the push looked for it")

	require.Equal(t, m.ID, sibling.Message(time.Minute).Message.ID, "the reconnected owner receives its row")
	sibling.ExpectNone(400 * time.Millisecond)
	principal.ExpectNone(400 * time.Millisecond)
	require.False(t, f.rowOf(t, m.ID).FellBackAt.Valid, "the release found the owner attached and swept nothing")
}

// A connection's label is set before it is visible, and the Kick sweep and the owner lookup read it
// from other goroutines: a message pushed to a session the moment it becomes visible finds it as the
// owner of its row, and kicking it at once does not race (run with -race).
func TestAConnectionsLabelIsSetBeforeItIsVisible(t *testing.T) {
	f := newSessionFixture(t)
	for i := 0; i < 40; i++ {
		folder := worktreeFolder()
		folder.Live = testkit.NewLiveID()
		ws := f.token.DialFolderSilent(folder)
		deadline := time.Now().Add(5 * time.Second)
		for ; time.Now().Before(deadline); runtime.Gosched() { // spin: the Kick must land in the attach's tail
			if _, ok := f.env.App.Presence.SessionByLabel(f.agentID, "wt"); ok {
				break
			}
		}
		m := f.post(t, "@atlas:wt as soon as you are there")
		var row db.AgentSessionDelivery
		require.Eventually(t, func() bool {
			var err error
			row, err = f.env.App.Store.R().GetSessionDelivery(context.Background(), db.GetSessionDeliveryParams{MessageID: m.ID, AgentID: f.agentID})
			return err == nil
		}, 5*time.Second, time.Millisecond)
		require.False(t, row.FellBackAt.Valid, "the session owns the row it was visible for")
		f.env.App.Presence.Kick(context.Background(), f.agentID, agents.CloseTakenOver, "ended")
		_ = ws.CloseNow()
		f.waitSessionGone(t, "wt")
	}
}

// SESS-24: acknowledging a fallback never moves the principal's cursor to the fallback's sequence: a
// plain message below it that was not acknowledged is still the principal's.
func TestAFallbackAckDoesNotMoveTheCursorPastAnUnackedMessage(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)
	plain := f.post(t, "plain, delivered but not acknowledged")
	require.Equal(t, plain.ID, principal.Message(time.Minute).Message.ID)
	fallback := f.post(t, "@atlas:wt addressed to no live session")
	require.Equal(t, fallback.ID, principal.Message(time.Minute).Message.ID)
	require.Greater(t, fallback.Seq, plain.Seq)

	principal.Ack(fallback.ID)
	require.Eventually(t, func() bool { return f.rowOf(t, fallback.ID).AckedAt.Valid }, 5*time.Second, 10*time.Millisecond)
	_, err := f.cursor(t)
	require.ErrorIs(t, err, sql.ErrNoRows, "the cursor did not jump over the unacknowledged plain message")

	principal.Close()
	f.waitPrincipalGone(t)
	again := f.dial(t)
	require.Equal(t, plain.ID, again.Message(time.Minute).Message.ID, "the plain message is still pending")
}
