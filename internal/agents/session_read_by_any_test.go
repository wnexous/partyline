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

// readByAnother is the incident of SN-11: the principal left two messages unacknowledged and went offline
// for days; a parallel session worked meanwhile, read the conversations (as context of the messages it
// stood in for, which the channel acknowledges like any frame, and with read_unread) and answered in them.
// Then it ended, and two messages arrived that no session of the AI has read.
type readByAnother struct {
	dm     dto.Room
	postDM func(text string) dto.Message
	// unread are the two messages no session of the AI read: one in the group, one in the DM.
	unread []string
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

	// The principal is given two messages and leaves without acknowledging them.
	d0 := postDM("first question")
	m0 := f.post(t, "@atlas quick one")
	require.ElementsMatch(t, []string{d0.ID, m0.ID}, []string{lead.Message(time.Minute).Message.ID, lead.Message(time.Minute).Message.ID})
	lead.Close()
	f.waitPrincipalGone(t)

	// The parallel session stands in for it in the group: it reads what it had not read as context, the
	// principal's unacknowledged mention among it, and acknowledges every frame, as the channel does.
	g1, g2 := f.post(t, "status one"), f.post(t, "status two")
	mention := f.post(t, "@atlas can you look at the build?")
	got := framesUntil(t, side, mention.ID)
	require.Equal(t, []string{m0.ID, g1.ID, g2.ID}, frameIDs(got))
	for _, fr := range got {
		require.True(t, fr.Context)
		side.Ack(fr.Message.ID)
	}
	side.Ack(mention.ID)
	f.waitStandInAcked(t, "claude-2", mention.ID)
	for _, id := range []string{m0.ID, g1.ID, g2.ID} {
		f.waitAcked(t, "claude-2", id)
	}
	f.postFrom(t, sideFolder, "looking at it now")
	// A plain message does not notify it; it reads it with read_unread.
	g3 := f.post(t, "one more detail")
	views, _, err := f.env.App.Agents.ReadSessionUnread(ctx, f.agentID, f.room.ID,
		agents.CallingSession{Key: "side-session", Label: "claude-2"}, 0)
	require.NoError(t, err)
	var read []string
	for _, v := range views {
		read = append(read, v.ID)
	}
	require.Equal(t, []string{g3.ID}, read, "read_unread gives what it had not read, not its own reply")

	// In the DM it stands in for the principal too, with the principal's unacknowledged DM as context.
	d1 := postDM("hi, are you around?")
	got = framesUntil(t, side, d1.ID)
	require.Equal(t, []string{d0.ID}, frameIDs(got))
	side.Ack(d0.ID)
	side.Ack(d1.ID)
	f.waitStandInAcked(t, "claude-2", d1.ID)
	f.waitAcked(t, "claude-2", d0.ID)
	f.postIn(t, sideFolder, dm.ID, "yes, here")
	side.Close()
	f.waitSessionGone(t, "claude-2")

	// Nobody is live: these two are read by no session of the AI. Both are dispatched before the test goes
	// on, so what the next session gets of them is its backlog's.
	du := postDM("ping")
	u := f.post(t, "is anyone there?")
	for {
		if watcher.Message(time.Minute).Message.ID == u.ID {
			break
		}
	}
	return readByAnother{dm: dm, postDM: postDM, unread: []string{u.ID, du.ID}}
}

// principalUnread reads the unread messages of the principal session with key in the room (read_unread),
// checks that none of them is one the AI wrote, and returns their ids.
func (f *sessionFixture) principalUnread(t *testing.T, key, roomID string) []string {
	t.Helper()
	views, _, err := f.env.App.Agents.ReadSessionUnread(context.Background(), f.agentID, roomID,
		agents.CallingSession{Key: key, IsPrincipal: true}, 0)
	require.NoError(t, err)
	var ids []string
	for _, v := range views {
		require.False(t, v.AuthorKind == string(dto.AuthorKindAgent) && v.AuthorID == f.agentID,
			"a message the AI wrote is not unread for it: %s", v.Text)
		ids = append(ids, v.ID)
	}
	return ids
}

// SN-11: the pinned principal comes back after a parallel session read and answered the conversations while
// it was away. Its backlog has only the messages no session of the AI read, nothing the AI wrote is unread for
// it, and a new message is delivered once.
func TestThePrincipalComingBackIsNotGivenWhatAnotherSessionRead(t *testing.T) {
	f := newSessionFixture(t)
	s := f.readByAnotherSession(t)

	back := f.dialSession(t, "lead-session")
	_, ok := f.env.App.Presence.Main(f.agentID)
	require.True(t, ok, "the pinned session is the principal again")
	next := s.postDM("a new question")
	got := framesUntil(t, back, next.ID)
	require.ElementsMatch(t, s.unread, frameIDs(got), "only what no session of the AI read")
	for _, fr := range got {
		require.False(t, fr.Context)
	}
	last := s.postDM("and another")
	require.Equal(t, last.ID, back.Message(time.Minute).Message.ID, "the new message came once")

	// Its read position caught up over what the AI read and wrote: the group's unread is the one message
	// nobody read, and the sibling's replies are not unread anywhere (principalUnread checks them).
	require.Equal(t, []string{s.unread[0]}, f.principalUnread(t, "lead-session", f.room.ID))
	f.principalUnread(t, "lead-session", s.dm.ID)
}

// SN-11, SN-01: a new harness session that takes the principal role (--principal) after a parallel session
// read and answered the conversations gets the same backlog, and starts caught up for its unread count.
func TestANewPrincipalIsNotGivenWhatAnotherSessionRead(t *testing.T) {
	f := newSessionFixture(t)
	s := f.readByAnotherSession(t)

	_, back := f.dialAs(t, "restarted-session", true)
	_, ok := f.env.App.Presence.Main(f.agentID)
	require.True(t, ok, "the new session is the principal")
	next := s.postDM("a new question")
	got := framesUntil(t, back, next.ID)
	require.ElementsMatch(t, s.unread, frameIDs(got), "only what no session of the AI read")
	last := s.postDM("and another")
	require.Equal(t, last.ID, back.Message(time.Minute).Message.ID, "the new message came once")
	require.Empty(t, f.principalUnread(t, "restarted-session", f.room.ID), "a new session joins caught up (SN-01)")
}

// SN-11, SN-10: a new parallel session that attaches while the principal is still away stands in only for
// the messages no session of the AI read: not the principal's unacknowledged messages its sibling read and
// answered. A new DM then reaches it once.
func TestANewParallelSessionDoesNotStandInForWhatAnotherSessionRead(t *testing.T) {
	f := newSessionFixture(t)
	s := f.readByAnotherSession(t)

	// In a worktree, so its label is not the one of the sibling that read them.
	folder := worktreeFolder()
	_, other := f.dialIn(t, folder, "other-session", false)
	_, ok := f.env.App.Presence.SessionByLabel(f.agentID, "wt")
	require.True(t, ok)
	_, ok = f.env.App.Presence.Main(f.agentID)
	require.False(t, ok, "the principal is still away")
	next := s.postDM("a new question")
	got := framesUntil(t, other, next.ID)
	require.Equal(t, []string{s.unread[1]}, frameIDs(got), "only the DM nobody read")
	require.False(t, got[0].Context)
	last := s.postDM("and another")
	require.Equal(t, last.ID, other.Message(time.Minute).Message.ID, "the new message came once")
}
