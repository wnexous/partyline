package agents_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// dialAs opens a session of atlas in its own folder with a harness session id and its own process, and returns
// the identity it dialed with, so a post from it carries its label. principal asks for the principal role
// (--principal).
func (f *sessionFixture) dialAs(t *testing.T, harnessSession string, principal bool) (testkit.Folder, *testkit.AgentConn) {
	t.Helper()
	return f.dialIn(t, testkit.FolderOf("atlas"), harnessSession, principal)
}

// dialIn is dialAs in folder.
func (f *sessionFixture) dialIn(t *testing.T, folder testkit.Folder, harnessSession string, principal bool) (testkit.Folder, *testkit.AgentConn) {
	t.Helper()
	folder.HarnessSession = harnessSession
	folder.Live = testkit.NewLiveID()
	folder.Principal = principal
	conn, resp := f.token.DialFolder(folder)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	conn.Welcome(time.Minute)
	t.Cleanup(conn.Close)
	return folder, conn
}

// postIn posts in a room as the session of folder.
func (f *sessionFixture) postIn(t *testing.T, folder testkit.Folder, roomID, text string) dto.Message {
	t.Helper()
	return testkit.Decode[dto.Message](f.token.AsFolder(folder).
		Post("/api/v1/agent/rooms/"+roomID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
}

// framesUntil reads the frames of conn until the message frame of id, and returns the message frames before
// it. The backlog of a connection is written before any live push, so a message posted after the attach
// tells where the backlog ended.
func framesUntil(t *testing.T, conn *testkit.AgentConn, id string) []dto.AgentMessageFrame {
	t.Helper()
	var out []dto.AgentMessageFrame
	for {
		fr := conn.Next(time.Minute)
		if fr.Type != dto.AgentFrameTypeMessage {
			continue
		}
		var v dto.AgentMessageFrame
		require.NoError(t, json.Unmarshal(fr.Raw, &v))
		if v.Message.ID == id {
			return out
		}
		out = append(out, v)
	}
}

// frameIDs is the message ids of frames.
func frameIDs(frames []dto.AgentMessageFrame) []string {
	ids := make([]string, 0, len(frames))
	for _, fr := range frames {
		ids = append(ids, fr.Message.ID)
	}
	return ids
}

// waitAcked waits until the delivery row of label for messageID is acknowledged, whatever its kind.
func (f *sessionFixture) waitAcked(t *testing.T, label, messageID string) {
	t.Helper()
	f.waitDeliveryRow(t, label, messageID, func(row db.AgentSessionDelivery) bool { return row.AckedAt.Valid })
}

// readByAnother is the incident of SN-11. The principal was given a DM and a mention and left without
// acknowledging them; a parallel session that follows the group got the mention too, as a message of its own,
// and answered it. Then, for days, the parallel session stood in for the principal: it answered what it was
// given as messages of its own, saw other messages only as the context of those (which the channel
// acknowledges like any frame) or through read_unread, and wrote replies. Then it ended, and two messages
// arrived that no session of the AI saw.
type readByAnother struct {
	dm     dto.Room
	postDM func(text string) dto.Message
	// answered are the messages the parallel session was given as messages of its own and acknowledged, in
	// the group, oldest first: the follow copy of the principal's mention and the first stand-in.
	answered []string
	// reply is the parallel session's reply after them, the last message of the gapless run the AI read.
	reply string
	// request is the plain group request the parallel session saw only as context of a later mention.
	request string
	// waiting are the messages the principal still has to be given: the request, the plain message read with
	// read_unread, the principal's DM the parallel session saw only as context, and the two nobody saw.
	waiting []string
	// standIns are the DMs among waiting: what a parallel session attaching with no principal stands in for.
	standIns []string
}

func (f *sessionFixture) readByAnotherSession(t *testing.T) readByAnother {
	t.Helper()
	ctx := context.Background()
	// Another AI of the group tells when the dispatcher handled a message: it handles every room in order.
	watcherID := f.env.NewAI(f.alice, "watcher")
	f.alice.Post("/api/v1/rooms/"+f.room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: watcherID}).RequireStatus(http.StatusCreated)
	watcher, resp := f.token.DialFolder(testkit.FolderOf("watcher"))
	require.NotNil(t, watcher, "upgrade refused: %d %s", resp.Status, resp.Body)
	t.Cleanup(watcher.Close)
	watcher.Welcome(time.Minute)

	lead := f.dialSession(t, "lead-session") // the first identified session is pinned as the principal
	sideFolder, side := f.dialAs(t, "side-session", false)
	f.settle(t, lead)
	f.settle(t, side)
	_, ok := f.env.App.Presence.SessionByLabel(f.agentID, "claude-2")
	require.True(t, ok, "the second session is a parallel one")
	dm, postDM := f.openDM(t)
	setNotify := func(notify string) {
		t.Helper()
		require.NoError(t, f.env.App.Agents.SetSessionNotify(ctx, f.agentID, "side-session", f.room.ID, false, notify))
	}

	// The principal is given a DM and a mention; the parallel session follows the group for a while, so it
	// is given the mention too, as a message of its own, and acknowledges it. The principal leaves without
	// acknowledging either.
	setNotify(agents.NotifyOn)
	d0 := postDM("first question")
	followed := f.post(t, "@atlas can you check the logs?")
	require.ElementsMatch(t, []string{d0.ID, followed.ID}, []string{lead.Message(time.Minute).Message.ID, lead.Message(time.Minute).Message.ID})
	frame := side.Message(time.Minute)
	require.Equal(t, followed.ID, frame.Message.ID)
	require.False(t, frame.Context, "a session that follows the room gets the message itself")
	side.Ack(followed.ID)
	f.waitAcked(t, "claude-2", followed.ID)
	setNotify(agents.NotifyMentions)
	lead.Close()
	f.waitPrincipalGone(t)

	// It stands in for the principal in the group and answers.
	first := f.post(t, "@atlas can you look at the build?")
	require.Empty(t, framesUntil(t, side, first.ID), "it read the group up to here")
	side.Ack(first.ID)
	f.waitStandInAcked(t, "claude-2", first.ID)
	reply := f.postFrom(t, sideFolder, "looking at it now")

	// A plain request, then a mention: the request reaches the parallel session only as context of the
	// mention, and the channel acknowledges both frames.
	request := f.post(t, "please also update the docs")
	ping := f.post(t, "@atlas ping")
	got := framesUntil(t, side, ping.ID)
	require.Equal(t, []string{request.ID}, frameIDs(got))
	require.True(t, got[0].Context)
	side.Ack(request.ID)
	side.Ack(ping.ID)
	f.waitStandInAcked(t, "claude-2", ping.ID)
	f.waitAcked(t, "claude-2", request.ID)
	f.postFrom(t, sideFolder, "pong")

	// A plain message does not notify it; it reads it with read_unread.
	plain := f.post(t, "one more detail")
	views, _, err := f.env.App.Agents.ReadSessionUnread(ctx, f.agentID, f.room.ID,
		agents.CallingSession{Key: "side-session", Label: "claude-2"}, 0)
	require.NoError(t, err)
	var read []string
	for _, v := range views {
		read = append(read, v.ID)
	}
	require.Equal(t, []string{plain.ID}, read, "read_unread gives what it had not read, not its own replies")

	// In the DM it stands in for the principal too, with the principal's unacknowledged DM as context.
	d1 := postDM("hi, are you around?")
	got = framesUntil(t, side, d1.ID)
	require.Equal(t, []string{d0.ID}, frameIDs(got))
	require.True(t, got[0].Context)
	side.Ack(d0.ID)
	side.Ack(d1.ID)
	f.waitStandInAcked(t, "claude-2", d1.ID)
	f.waitAcked(t, "claude-2", d0.ID)
	f.postIn(t, sideFolder, dm.ID, "yes, here")
	side.Close()
	f.waitSessionGone(t, "claude-2")

	// Nobody is live: these two are seen by no session of the AI. Both are dispatched before the test goes
	// on, so what the next session gets of them is its backlog's.
	du := postDM("ping")
	u := f.post(t, "is anyone there?")
	framesUntil(t, watcher, u.ID)
	return readByAnother{
		dm: dm, postDM: postDM,
		answered: []string{followed.ID, first.ID}, reply: reply.ID, request: request.ID,
		waiting:  []string{request.ID, plain.ID, d0.ID, u.ID, du.ID},
		standIns: []string{d0.ID, du.ID},
	}
}

// unreadIDs reads the unread messages of the principal session with key in the room (read_unread) and
// returns their ids.
func (f *sessionFixture) unreadIDs(t *testing.T, key, roomID string) []string {
	t.Helper()
	views, _, err := f.env.App.Agents.ReadSessionUnread(context.Background(), f.agentID, roomID,
		agents.CallingSession{Key: key, IsPrincipal: true}, 0)
	require.NoError(t, err)
	var ids []string
	for _, v := range views {
		ids = append(ids, v.ID)
	}
	return ids
}

// SN-11: the pinned principal comes back after a parallel session answered in its place. Its backlog has
// everything the parallel session was not given as a message of its own (what it saw only as context or
// through read_unread included) and nothing it answered or wrote; its read position starts after the run the
// AI answered or wrote without a gap; and a new message is delivered once.
func TestThePrincipalComingBackIsNotGivenWhatAnotherSessionAnswered(t *testing.T) {
	f := newSessionFixture(t)
	s := f.readByAnotherSession(t)

	back := f.dialSession(t, "lead-session")
	_, ok := f.env.App.Presence.Main(f.agentID)
	require.True(t, ok, "the pinned session is the principal again")
	next := s.postDM("a new question")
	got := framesUntil(t, back, next.ID)
	require.ElementsMatch(t, s.waiting, frameIDs(got), "what no session was given as its own, once")
	for _, fr := range got {
		require.False(t, fr.Context)
	}
	last := s.postDM("and another")
	require.Equal(t, last.ID, back.Message(time.Minute).Message.ID, "the new message came once")

	// The group's unread starts at the request: what the AI answered and wrote before it is read.
	unread := f.unreadIDs(t, "lead-session", f.room.ID)
	require.NotEmpty(t, unread)
	require.Equal(t, s.request, unread[0])
	for _, id := range append(s.answered, s.reply) {
		require.NotContains(t, unread, id)
	}
}

// SN-11, SN-01: a new harness session that takes the principal role (--principal) after a parallel session
// answered in its place gets the same backlog, and starts caught up for its unread count.
func TestANewPrincipalIsNotGivenWhatAnotherSessionAnswered(t *testing.T) {
	f := newSessionFixture(t)
	s := f.readByAnotherSession(t)

	_, back := f.dialAs(t, "restarted-session", true)
	_, ok := f.env.App.Presence.Main(f.agentID)
	require.True(t, ok, "the new session is the principal")
	next := s.postDM("a new question")
	got := framesUntil(t, back, next.ID)
	require.ElementsMatch(t, s.waiting, frameIDs(got), "what no session was given as its own, once")
	last := s.postDM("and another")
	require.Equal(t, last.ID, back.Message(time.Minute).Message.ID, "the new message came once")
	require.Empty(t, f.unreadIDs(t, "restarted-session", f.room.ID), "a new session joins caught up (SN-01)")
}

// SN-11, SN-10: a new parallel session that attaches while the principal is still away stands in for the DMs
// and mentions no session was given as its own: the principal's DM its sibling saw only as context and the
// one nobody saw, but not the mention its sibling answered as a room it followed. A new DM then reaches it
// once.
func TestANewParallelSessionDoesNotStandInForWhatAnotherSessionAnswered(t *testing.T) {
	f := newSessionFixture(t)
	s := f.readByAnotherSession(t)

	// In a worktree, so its label is not the one of the sibling.
	_, other := f.dialIn(t, worktreeFolder(), "other-session", false)
	_, ok := f.env.App.Presence.SessionByLabel(f.agentID, "wt")
	require.True(t, ok)
	_, ok = f.env.App.Presence.Main(f.agentID)
	require.False(t, ok, "the principal is still away")
	next := s.postDM("a new question")
	got := framesUntil(t, other, next.ID)
	require.ElementsMatch(t, s.standIns, frameIDs(got))
	for _, fr := range got {
		require.False(t, fr.Context)
	}
	last := s.postDM("and another")
	require.Equal(t, last.ID, other.Message(time.Minute).Message.ID, "the new message came once")
}

// SN-11: a request posted as a plain group message and followed by a mention reaches the parallel session
// only as context of the mention; it told the AI to answer the mention, so the request is still the
// principal's when it comes back, and the mention it answered is not.
func TestARequestSeenOnlyAsContextStillReachesThePrincipal(t *testing.T) {
	f := newSessionFixture(t)
	lead := f.dialSession(t, "lead-session")
	f.settle(t, lead)
	_, side := f.dialAs(t, "side-session", false)
	f.settle(t, side)
	lead.Close()
	f.waitPrincipalGone(t)

	request := f.post(t, "please rotate the keys today")
	ping := f.post(t, "@atlas ping")
	got := framesUntil(t, side, ping.ID)
	require.Equal(t, []string{request.ID}, frameIDs(got))
	require.True(t, got[0].Context)
	side.Ack(request.ID)
	side.Ack(ping.ID)
	f.waitStandInAcked(t, "claude-2", ping.ID)
	f.waitAcked(t, "claude-2", request.ID)
	side.Close()
	f.waitSessionGone(t, "claude-2")

	back := f.dialSession(t, "lead-session")
	_, post := f.openDM(t)
	next := post("hello")
	require.Equal(t, []string{request.ID}, frameIDs(framesUntil(t, back, next.ID)), "the request is still the principal's")
}
