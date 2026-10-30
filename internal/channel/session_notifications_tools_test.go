package channel

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
)

// worktreeSession opens the AI's session in a linked worktree: a parallel session with its own label,
// which is notified of mentions only (SESS-01, SN-02).
func (e *chanEnv) worktreeSession() *session {
	e.t.Helper()
	o := e.options()
	o.Folder, o.MainFolder = builderFolder.Folder+"/wt", builderFolder.Folder
	s := e.connected(o)
	require.Eventually(e.t, func() bool {
		for _, live := range e.env.App.Agents.Sessions(e.agentID) {
			if live.Label == "wt" {
				return true
			}
		}
		return false
	}, wait, 10*time.Millisecond, "the worktree session attached")
	return s
}

// roomsOf runs list_rooms and returns the fixture's room.
func (e *chanEnv) roomsOf(s *session) roomListing {
	e.t.Helper()
	text, isErr := s.call("list_rooms", map[string]any{})
	require.False(e.t, isErr, text)
	var out struct {
		Rooms []roomListing `json:"rooms"`
	}
	require.NoError(e.t, json.Unmarshal([]byte(text), &out))
	for _, r := range out.Rooms {
		if r.ID == e.room.ID {
			return r
		}
	}
	e.t.Fatalf("the room is not listed: %+v", out.Rooms)
	return roomListing{}
}

// SN-05, SN-02, SN-03: a parallel session's tools show its own unread count and setting, read_unread
// returns the messages it missed as the pushed ones are framed and marks them read, and
// set_notifications turns the session's notifications on so the next plain message reaches it.
func TestToolsReadUnreadAndSetNotifications(t *testing.T) {
	e := newChanEnv(t)
	principal := e.connected(e.options())
	worktree := e.worktreeSession()

	one, two := e.post("one"), e.post("two")
	require.Equal(t, one.ID, principal.notification().Meta["message_id"])
	require.Equal(t, two.ID, principal.notification().Meta["message_id"])
	worktree.expectNoNotification(300 * time.Millisecond)

	listed := e.roomsOf(worktree)
	require.EqualValues(t, 2, listed.Unread, "a session that is not notified only counts the messages")
	require.Equal(t, agents.NotifyMentions, listed.Notify)

	text, isErr := worktree.call("read_unread", toolArgs("room", "Support"))
	require.False(t, isErr, text)
	var unread struct {
		Messages []map[string]string `json:"messages"`
		Skipped  int64               `json:"skipped"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &unread))
	require.Len(t, unread.Messages, 2)
	require.Equal(t, "one", unread.Messages[0]["content"], "oldest first")
	require.Equal(t, two.ID, unread.Messages[1]["message_id"])
	require.Equal(t, "true", unread.Messages[0]["trusted"], "the framing is the pushed one")
	require.NotContains(t, unread.Messages[0], "context")
	require.Zero(t, unread.Skipped)
	require.Zero(t, e.roomsOf(worktree).Unread, "read_unread marks them read")

	// room_info says the same, and set_notifications changes the session's setting.
	text, isErr = worktree.call("room_info", toolArgs("room", "Support"))
	require.False(t, isErr, text)
	var info struct {
		Unread int64  `json:"unread"`
		Notify string `json:"notify"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &info))
	require.Zero(t, info.Unread)
	require.Equal(t, agents.NotifyMentions, info.Notify)

	text, isErr = worktree.call("set_notifications", toolArgs("room", "Support", "notify", agents.NotifyOn))
	require.False(t, isErr, text)
	var changed struct {
		Notify string `json:"notify"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &changed))
	require.Equal(t, agents.NotifyOn, changed.Notify)

	next := e.post("three")
	require.Equal(t, next.ID, principal.notification().Meta["message_id"])
	require.Equal(t, "three", worktree.notification().Content, "the session follows the room now")

	bad, isErr := worktree.call("set_notifications", toolArgs("room", "Support", "notify", "all"))
	require.True(t, isErr, bad)
	require.Contains(t, bad, "on or mentions")
}
