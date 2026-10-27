package agents_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
		acked, err = q.AckSessionDelivery(context.Background(), db.AckSessionDeliveryParams{MessageID: m.ID, AgentID: f.agentID})
		return err
	}))
	require.Zero(t, acked)
	require.Len(t, f.rows(t, "claude-2"), 1)

	principal.Ack(m.ID)
	require.Eventually(t, func() bool { return len(f.rows(t, "claude-2")) == 0 }, 5*time.Second, 10*time.Millisecond,
		"the principal's acknowledgement consumes the fallback")
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

// SESS-38: a delivery row of the AI's own message is deleted when its session ends before
// acknowledging it, and the principal never receives it as a fallback.
func TestASelfDeliveryIsDeletedWhenItsSessionEnds(t *testing.T) {
	f := newSessionFixture(t)
	principal := f.dial(t)
	f.settle(t, principal)
	_, sibling := f.dialWorktree(t)
	f.settle(t, sibling)

	m := f.postFrom(t, testkit.FolderOf("atlas"), "@atlas:wt do X")
	require.Equal(t, m.ID, sibling.Message(time.Minute).Message.ID)
	require.False(t, f.waitRow(t, "wt", m.ID).FellBackAt.Valid)

	sibling.Close()
	require.Eventually(t, func() bool { return len(f.rows(t, "wt")) == 0 }, 5*time.Second, 10*time.Millisecond,
		"the row of the AI's own message is dropped, not fallen back")
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
