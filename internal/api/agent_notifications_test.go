package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// roomOf returns the calling session's listing of the fixture's room, as the agent API answers it.
func agentRoomOf(t *testing.T, l *liveEnv, f testkit.Folder) dto.AgentRoom {
	t.Helper()
	rooms := testkit.Decode[dto.List[dto.AgentRoom]](l.tok.AsFolder(f).Get("/api/v1/agent/rooms").RequireStatus(http.StatusOK))
	for _, r := range rooms.Items {
		if r.ID == l.room.ID {
			return r
		}
	}
	t.Fatalf("the room is not in the list: %+v", rooms.Items)
	return dto.AgentRoom{}
}

// parallelSession dials one more session of the AI, which gets the next label, and waits until it settled.
func (l *liveEnv) parallelSession(t *testing.T) (*testkit.AgentConn, testkit.Folder) {
	t.Helper()
	f := acme
	f.Live = testkit.NewLiveID()
	conn, resp := l.tok.DialFolder(f)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	welcome := conn.Welcome(wsWait)
	require.NotEmpty(t, welcome.Session.Label, "the second session is a labelled one")
	return conn, f
}

// SN-05, SN-02: list_rooms gives each session its own unread count and setting: the principal follows the
// room and reads what it acknowledges, while a parallel session is notified of mentions only and counts the
// messages it never saw, including the ones its siblings wrote (SN-06) but not its own.
func TestTheAgentAPICountsUnreadPerSession(t *testing.T) {
	l := newLiveEnv(t)
	principalFolder := acme
	principalFolder.Live = testkit.NewLiveID()
	principal, id := l.dial(t, principalFolder)
	l.addMember(t, id)
	parallel, parallelFolder := l.parallelSession(t)

	one, two := l.post(t, "one"), l.post(t, "two")
	require.Equal(t, one.ID, principal.Message(wsWait).Message.ID)
	require.Equal(t, two.ID, principal.Message(wsWait).Message.ID)
	principal.Ack(two.ID)
	parallel.ExpectNone(300 * time.Millisecond)

	require.EqualValues(t, 2, agentRoomOf(t, l, parallelFolder).Unread, "a session that is not notified counts the message")
	require.Equal(t, agents.NotifyMentions, agentRoomOf(t, l, parallelFolder).Notify)
	principalRoom := agentRoomOf(t, l, principalFolder)
	require.Zero(t, principalRoom.Unread, "the principal read what it acknowledged")
	require.Equal(t, agents.NotifyOn, principalRoom.Notify, "the principal follows the room")

	// A message a sibling writes is unread for this session (SN-06), its own is not.
	own := testkit.Decode[dto.Message](l.tok.AsFolder(parallelFolder).Post(agentRoomPath(l.room.ID, "/messages"),
		dto.PostMessageRequest{Text: "from the sibling"}).RequireStatus(http.StatusCreated))
	require.Equal(t, "claude-2", own.AuthorLabel)
	require.EqualValues(t, 2, agentRoomOf(t, l, parallelFolder).Unread)
}

// SN-05, SN-03a: read_unread returns the unread messages oldest first with the pushed framing — trust
// computed for the AI, context false — marks them read and applies the filters of the catch-up: the loop
// guard's paused messages and the session's own are not part of it. In trusted mode an untrusted message is
// not context either.
func TestReadUnreadMarksReadAndAppliesTheCatchUpFilters(t *testing.T) {
	l := newLiveEnv(t)
	principalFolder := acme
	principalFolder.Live = testkit.NewLiveID()
	principal, id := l.dial(t, principalFolder)
	l.addMember(t, id)
	parallel, parallelFolder := l.parallelSession(t)

	one := l.post(t, "one")
	paused := l.post(t, "while paused")
	require.NoError(t, l.env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
		return q.MarkMessageDuringPause(context.Background(), paused.ID)
	}))
	two := l.post(t, "two")
	for range 3 {
		principal.Message(wsWait) // the live copies are the principal's
	}
	parallel.ExpectNone(300 * time.Millisecond)

	url := agentRoomPath(l.room.ID, "/unread")
	unread := testkit.Decode[dto.AgentUnread](l.tok.AsFolder(parallelFolder).Get(url).RequireStatus(http.StatusOK))
	require.Equal(t, l.room.ID, unread.Room.ID)
	require.Len(t, unread.Messages, 2, "the paused message is not context (DLV-08)")
	require.Equal(t, []string{one.ID, two.ID}, []string{unread.Messages[0].Message.ID, unread.Messages[1].Message.ID})
	require.False(t, unread.Messages[0].Context, "a message read on request is no catch-up")
	require.True(t, unread.Messages[0].Trusted, "its author manages the AI")
	require.Zero(t, unread.Skipped)

	require.Zero(t, agentRoomOf(t, l, parallelFolder).Unread, "the read moved the session's position")
	empty := testkit.Decode[dto.AgentUnread](l.tok.AsFolder(parallelFolder).Get(url).RequireStatus(http.StatusOK))
	require.Empty(t, empty.Messages)

	// A trusted-mode AI reads no untrusted message: alice's other AI shares the room but does not manage it.
	other := l.env.NewAI(l.alice, "stranger")
	l.alice.Post("/api/v1/rooms/"+l.room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: other}).RequireStatus(http.StatusCreated)
	mode := dto.DeliveryModeTrusted
	memberOf := func() string {
		for _, m := range testkit.Decode[dto.List[dto.RoomMember]](l.alice.Get("/api/v1/rooms/" + l.room.ID + "/members").RequireStatus(http.StatusOK)).Items {
			if m.Kind == dto.MemberKindAgent {
				return m.ID
			}
		}
		t.Fatal("the AI is not a member")
		return ""
	}
	l.alice.Patch("/api/v1/rooms/"+l.room.ID+"/members/"+memberOf(),
		dto.UpdateMemberRequest{Delivery: &mode}).RequireStatus(http.StatusOK)

	fromAlice := l.post(t, "from alice")
	l.tok.AsAgent("stranger").Post(agentRoomPath(l.room.ID, "/messages"),
		dto.PostMessageRequest{Text: "from the other AI"}).RequireStatus(http.StatusCreated)
	trusted := testkit.Decode[dto.AgentUnread](l.tok.AsFolder(parallelFolder).Get(url).RequireStatus(http.StatusOK))
	require.Len(t, trusted.Messages, 1, "an untrusted message is not context in trusted mode")
	require.Equal(t, fromAlice.ID, trusted.Messages[0].Message.ID)
	require.True(t, trusted.Messages[0].Trusted)
}

// SN-02, SN-03: set_notifications changes the session's setting through the API, and a plain message then
// reaches that session live; turning it back to mentions silences it again.
func TestSetNotificationsChangesTheRouting(t *testing.T) {
	l := newLiveEnv(t)
	principalFolder := acme
	principalFolder.Live = testkit.NewLiveID()
	principal, id := l.dial(t, principalFolder)
	l.addMember(t, id)
	parallel, parallelFolder := l.parallelSession(t)

	plain := l.post(t, "before")
	require.Equal(t, plain.ID, principal.Message(wsWait).Message.ID)
	parallel.ExpectNone(300 * time.Millisecond)

	url := agentRoomPath(l.room.ID, "/notifications")
	answer := testkit.Decode[dto.RoomNotification](l.tok.AsFolder(parallelFolder).
		Put(url, dto.NotifyRequest{Notify: agents.NotifyOn}).RequireStatus(http.StatusOK))
	require.Equal(t, agents.NotifyOn, answer.Notify)
	require.Equal(t, agents.NotifyOn, agentRoomOf(t, l, parallelFolder).Notify)

	next := l.post(t, "after")
	require.Equal(t, next.ID, principal.Message(wsWait).Message.ID)
	context := parallel.Message(wsWait)
	require.Equal(t, plain.ID, context.Message.ID, "the message it missed comes first as context")
	require.True(t, context.Context)
	require.Equal(t, next.ID, parallel.Message(wsWait).Message.ID, "a session notified of everything receives it")

	testkit.Decode[dto.RoomNotification](l.tok.AsFolder(parallelFolder).
		Put(url, dto.NotifyRequest{Notify: agents.NotifyMentions}).RequireStatus(http.StatusOK))
	last := l.post(t, "again")
	require.Equal(t, last.ID, principal.Message(wsWait).Message.ID)
	parallel.ExpectNone(300 * time.Millisecond)
}

// SN-01, SN-05: a tools-only process that sends the live id of its listen process acts as that session, and
// one that sends a harness session id keeps its own state: read_unread moves that key, never the
// principal's.
func TestAToolsOnlyProcessUsesItsOwnSessionKey(t *testing.T) {
	l := newLiveEnv(t)
	// The AI is a member before its sessions attach, so both join the room caught up.
	a, err := l.env.App.Agents.ResolveFolder(context.Background(), l.alice.User.ID,
		agents.FolderID{Machine: acme.Machine, Folder: acme.Path, Harness: acme.Harness}, false)
	require.NoError(t, err)
	id := a.ID
	l.addMember(t, id)
	principalFolder := acme
	principalFolder.Live = testkit.NewLiveID()
	principalFolder.HarnessSession = "sess-principal"
	principal, _ := l.dial(t, principalFolder)

	parallelFolder := acme
	parallelFolder.Live = testkit.NewLiveID()
	parallelFolder.HarnessSession = "sess-parallel"
	parallel, resp := l.tok.DialFolder(parallelFolder)
	require.NotNil(t, parallel, "upgrade refused: %d %s", resp.Status, resp.Body)
	require.Equal(t, "claude-2", parallel.Welcome(wsWait).Session.Label)

	m := l.post(t, "for later")
	require.Equal(t, m.ID, principal.Message(wsWait).Message.ID)
	parallel.ExpectNone(300 * time.Millisecond)

	tools := testkit.Folder{Machine: acme.Machine, Path: acme.Path, Live: parallelFolder.Live, HarnessSession: "sess-parallel"}
	unread := testkit.Decode[dto.AgentUnread](l.tok.AsFolder(tools).Get(agentRoomPath(l.room.ID, "/unread")).RequireStatus(http.StatusOK))
	require.Len(t, unread.Messages, 1)
	require.Equal(t, m.ID, unread.Messages[0].Message.ID)

	stateOf := func(key string) db.SessionRoomState {
		state, err := l.env.App.Store.R().GetSessionRoomState(context.Background(), db.GetSessionRoomStateParams{
			AgentID: id, SessionKey: key, RoomID: l.room.ID,
		})
		require.NoError(t, err)
		return state
	}
	require.Equal(t, m.Seq, stateOf("sess-parallel").ReadSeq, "the tools-only call moved its own session")
	require.Zero(t, stateOf("sess-principal").ReadSeq, "the principal's position is untouched")
}

// SN-08: whoever manages an AI lists the notification state of each of its sessions per room and changes
// it, and the member card carries the session's setting for the room. Someone who only sees the AI gets
// 403; an AI, session or room that does not exist is 404; an unknown setting is 422.
func TestTheOwnerListsAndChangesSessionNotifications(t *testing.T) {
	l := newLiveEnv(t)
	a, err := l.env.App.Agents.ResolveFolder(context.Background(), l.alice.User.ID,
		agents.FolderID{Machine: acme.Machine, Folder: acme.Path, Harness: acme.Harness}, false)
	require.NoError(t, err)
	id := a.ID
	l.addMember(t, id)
	principalFolder := acme
	principalFolder.Live = testkit.NewLiveID()
	principal, _ := l.dial(t, principalFolder)
	parallel, _ := l.parallelSession(t)

	one := l.post(t, "one")
	require.Equal(t, one.ID, principal.Message(wsWait).Message.ID)
	principal.Ack(one.ID)
	parallel.ExpectNone(300 * time.Millisecond)

	url := "/api/v1/agents/" + id + "/sessions/"
	principalList := testkit.Decode[dto.List[dto.RoomNotification]](l.alice.Get(url + "_principal/notifications").RequireStatus(http.StatusOK))
	require.Len(t, principalList.Items, 1)
	require.Equal(t, []any{l.room.ID, agents.NotifyOn, int64(0)}, []any{principalList.Items[0].RoomID, principalList.Items[0].Notify, principalList.Items[0].Unread})

	parallelList := testkit.Decode[dto.List[dto.RoomNotification]](l.alice.Get(url + "claude-2/notifications").RequireStatus(http.StatusOK))
	require.Len(t, parallelList.Items, 1)
	require.Equal(t, []any{l.room.ID, agents.NotifyMentions, int64(1)}, []any{parallelList.Items[0].RoomID, parallelList.Items[0].Notify, parallelList.Items[0].Unread})

	changed := testkit.Decode[dto.RoomNotification](l.alice.Put(url+"claude-2/notifications/"+l.room.ID,
		dto.NotifyRequest{Notify: agents.NotifyOn}).RequireStatus(http.StatusOK))
	require.Equal(t, agents.NotifyOn, changed.Notify)

	// The member card carries the setting of each session in this room.
	members := testkit.Decode[dto.List[dto.RoomMember]](l.alice.Get("/api/v1/rooms/" + l.room.ID + "/members").RequireStatus(http.StatusOK))
	var parallelSession dto.SessionInfo
	for _, m := range members.Items {
		if m.Kind != dto.MemberKindAgent || m.MemberID != id {
			continue
		}
		for _, s := range m.Sessions {
			if s.Label == "claude-2" {
				parallelSession = s
			}
		}
	}
	require.Equal(t, agents.NotifyOn, parallelSession.Notify, "the member card marks what the owner chose")

	// Seeing the AI is not managing it; what does not exist is 404; what is not a setting is 422.
	bob := l.env.NewUser("bob")
	l.alice.ShareAI(id, bob)
	bob.Get(url+"claude-2/notifications").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	bob.Put(url+"claude-2/notifications/"+l.room.ID, dto.NotifyRequest{Notify: agents.NotifyOn}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	l.alice.Get("/api/v1/agents/nope/sessions/_principal/notifications").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	l.alice.Get(url+"nope/notifications").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	l.alice.Put(url+"claude-2/notifications/nope", dto.NotifyRequest{Notify: agents.NotifyOn}).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	l.alice.Put(url+"claude-2/notifications/"+l.room.ID, dto.NotifyRequest{Notify: "all"}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
}
