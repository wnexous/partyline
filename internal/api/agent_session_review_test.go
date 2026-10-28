package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// closePrincipal ends the principal session of the AI and waits until only the labelled sessions remain.
func (e *worktreePendingEnv) closePrincipal(t *testing.T) {
	t.Helper()
	e.conn.Close()
	require.Eventually(t, func() bool {
		for _, s := range e.env.App.Agents.Sessions(e.id) {
			if s.Label == "" {
				return false
			}
		}
		return true
	}, 5*time.Second, 10*time.Millisecond)
}

// SESS-06: a tools-only process of the AI's own folder is the principal's reader. It is refused only while
// the principal session is live, not while the AI is live only through a worktree session: then it reads
// the queue.
func TestMainFolderToolsOnlyReaderGetsItsQueueWhileOnlyAWorktreeSessionIsLive(t *testing.T) {
	e := newWorktreePendingEnv(t)
	worktree := e.worktree
	worktree.Live = testkit.NewLiveID()
	conn, resp := e.token.DialFolder(worktree)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	require.Equal(t, "wt", conn.Welcome(wsWait).Session.Label)

	e.token.AsFolder(e.principal).Get("/api/v1/agent/pending").RequireError(http.StatusConflict, dto.ErrorCodeConflict)

	e.closePrincipal(t)
	require.True(t, e.env.App.Presence.Online(e.id), "the worktree session keeps the AI online")
	m := e.post(t, "for the main folder")
	got := e.pending(t, e.principal, "?platform=1")
	require.Len(t, got.Items, 1, "the queue is read, not refused")
	require.Equal(t, m.ID, got.Items[0].Message.ID)
}

// SESS-06: a labelled reader that is a live session honours wait: it waits, then answers empty; a tools-only
// worktree process that holds no live session is still answered at once.
func TestLiveWorktreeSessionPendingHonoursWait(t *testing.T) {
	e := newWorktreePendingEnv(t)
	worktree := e.worktree
	worktree.Live = testkit.NewLiveID()
	conn, resp := e.token.DialFolder(worktree)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	require.Equal(t, "wt", conn.Welcome(wsWait).Session.Label)

	start := time.Now()
	got := e.pending(t, worktree, "?platform=1&wait=1")
	require.GreaterOrEqual(t, time.Since(start), 900*time.Millisecond, "a live session waits as asked")
	require.Empty(t, got.Items)
	require.Empty(t, got.Platform)

	start = time.Now()
	e.pending(t, e.worktree, "?platform=1&wait=30") // no live id: a tools-only process
	require.Less(t, time.Since(start), 5*time.Second, "a tools-only worktree process is answered at once")
}

// SESS-19: GET /agent/session is online while any session is live, and its time is the principal's when the
// principal is live, else the earliest live session's.
func TestSessionStateWithOnlyAParallelSession(t *testing.T) {
	l := newLiveEnv(t)
	first, id := l.dial(t, acme)
	l.clock.Advance(time.Hour)
	second, resp := l.tok.DialFolder(acme)
	require.NotNil(t, second, "upgrade refused: %d %s", resp.Status, resp.Body)
	require.Equal(t, "claude-2", second.Welcome(wsWait).Session.Label)

	first.Close()
	require.Eventually(t, func() bool { return len(l.env.App.Agents.Sessions(id)) == 1 }, 5*time.Second, 10*time.Millisecond)
	s := l.session(t)
	require.True(t, s.Online, "a parallel session keeps the AI online")
	require.NotNil(t, s.ConnectedAt)
	require.True(t, l.start.Add(time.Hour).Equal(*s.ConnectedAt), "the time is the live session's")
	require.Len(t, s.Sessions, 1)
}
