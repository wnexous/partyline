package api_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// projectCursor reads the project's cursor in the room (0 when it has none yet).
func (f *projectRoom) projectCursor(t *testing.T) int64 {
	t.Helper()
	seq, err := f.env.App.Store.R().GetProjectCursor(context.Background(), db.GetProjectCursorParams{ProjectID: f.project, RoomID: f.room.ID})
	if err != nil {
		return 0
	}
	return seq
}

// closeSession closes a session and waits until the server sees it offline.
func (f *projectRoom) closeSession(t *testing.T, conn *testkit.AgentConn, info dto.AgentInfo) {
	t.Helper()
	conn.Close()
	f.waitOffline(t, info.ID)
}

// PROJ-08: messages posted while no session of the project is online wait; the next session
// receives them as backlog, oldest first, and its ack moves the project's cursor, so the
// session after it does not receive them again.
func TestProjectBacklogReachesTheNextSession(t *testing.T) {
	f := newProjectRoom(t)
	first, one := f.session(t)
	f.closeSession(t, first, one)

	posted := []dto.Message{f.post(f.alice, "while away"), f.post(f.alice, "still away")}
	second, two := f.session(t)
	for _, m := range posted {
		got := second.Message(wsWait)
		require.Equal(t, dto.RoomRef{ID: f.room.ID, Name: "Ops"}, got.Room)
		require.Equal(t, m.ID, got.Message.ID)
		require.True(t, got.Trusted, "the project owner's message")
	}
	second.ExpectNone(300 * time.Millisecond)
	second.Ack(posted[1].ID)
	require.Eventually(t, func() bool { return f.projectCursor(t) == posted[1].Seq }, wsWait, 20*time.Millisecond)
	f.closeSession(t, second, two)

	third, _ := f.session(t)
	third.ExpectNone(500 * time.Millisecond)
	later := f.post(f.alice, "welcome back")
	require.Equal(t, later.ID, third.Message(wsWait).Message.ID)
}

// PROJ-08: a message the active session received live and acknowledged is not delivered
// again to the session that replaces it after a restart.
func TestProjectRestartDoesNotRepeatAcknowledgedMessages(t *testing.T) {
	f := newProjectRoom(t)
	conn, info := f.session(t)
	m := f.post(f.alice, "live")
	require.Equal(t, m.ID, conn.Message(wsWait).Message.ID)
	conn.Ack(m.ID)
	require.Eventually(t, func() bool { return f.projectCursor(t) == m.Seq }, wsWait, 20*time.Millisecond)
	f.closeSession(t, conn, info)

	again, _ := f.session(t)
	again.ExpectNone(500 * time.Millisecond)
}

// PROJ-08: an ack from an active session that is also a member of the room itself moves the
// project's cursor too, so the next session of the project does not get the message again.
func TestProjectAckFromAMemberSessionMovesTheProjectCursor(t *testing.T) {
	f := newProjectRoom(t)
	conn, info := f.session(t)
	f.addAgent(t, info.ID)
	m := f.post(f.alice, "as a member")
	require.Equal(t, m.ID, conn.Message(wsWait).Message.ID)
	conn.Ack(m.ID)
	require.Eventually(t, func() bool { return f.projectCursor(t) == m.Seq }, wsWait, 20*time.Millisecond)
	require.Equal(t, m.Seq, func() int64 {
		seq, err := f.env.App.Store.R().GetAgentCursor(context.Background(), dbCursorParams(info.ID, f.room.ID))
		require.NoError(t, err)
		return seq
	}(), "the member's own cursor moves as well")
	f.closeSession(t, conn, info)

	next, _ := f.session(t)
	next.ExpectNone(500 * time.Millisecond)
}

// PROJ-08 (AGT-08, AGT-09): with more than 50 messages waiting the next session gets the
// skipped-count notice and the last 50, and the project's cursor moves past the skipped ones.
func TestProjectBacklogCapSkipsOlderMessages(t *testing.T) {
	f := newProjectRoom(t)
	first, one := f.session(t)
	f.closeSession(t, first, one)
	var posted []dto.Message
	for i := 1; i <= 55; i++ {
		posted = append(posted, f.post(f.alice, fmt.Sprintf("message %d", i)))
	}

	conn, _ := f.session(t)
	n := conn.Notice(wsWait)
	require.Equal(t, dto.RoomRef{ID: f.room.ID, Name: "Ops"}, n.Room)
	require.Equal(t, "5 older messages skipped; use the history tool", n.Text)
	for _, m := range posted[5:] {
		require.Equal(t, m.ID, conn.Message(wsWait).Message.ID)
	}
	conn.ExpectNone(300 * time.Millisecond)
	require.Equal(t, posted[4].Seq, f.projectCursor(t))
}

// PROJ-08, PROJ-09: the project's backlog leaves out what its own sessions posted: a new
// session (another agent of the project) gets the unacknowledged user message but not the
// post of the session that was active before it.
func TestProjectBacklogLeavesOutTheProjectsOwnPosts(t *testing.T) {
	f := newProjectRoom(t)
	conn, info := f.session(t)
	f.rest(info).Post(agentRoomPath(f.room.ID, "/messages"), dto.PostMessageRequest{Text: "from the session"}).RequireStatus(http.StatusCreated)
	user := f.post(f.alice, "from alice")
	require.Equal(t, user.ID, conn.Message(wsWait).Message.ID, "delivered live, not acknowledged")

	next, nextInfo := f.session(t)
	require.NotEqual(t, info.ID, nextInfo.ID)
	require.Equal(t, user.ID, next.Message(wsWait).Message.ID)
	next.ExpectNone(300 * time.Millisecond)
}
