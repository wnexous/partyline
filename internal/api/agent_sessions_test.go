package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// dialAs connects f as a session of the agent token's AI and returns the connection and the
// welcome frame it got.
func dialAs(t *testing.T, l *liveEnv, f testkit.Folder) (*testkit.AgentConn, dto.AgentWelcomeFrame) {
	t.Helper()
	conn, resp := l.tok.DialFolder(f)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	return conn, conn.Welcome(wsWait)
}

// postAs posts a message through the agent route as the AI of f.
func postAs(t *testing.T, l *liveEnv, f testkit.Folder, text string) dto.Message {
	t.Helper()
	return testkit.Decode[dto.Message](
		l.tok.AsFolder(f).Post(agentRoomPath(l.room.ID, "/messages"), dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
}

// aiOf creates the AI of f for alice when it does not exist yet and adds it to the room.
func (l *liveEnv) aiOf(t *testing.T, f testkit.Folder) string {
	t.Helper()
	a, err := l.env.App.Agents.ResolveFolder(context.Background(), l.alice.User.ID,
		agents.FolderID{Machine: f.Machine, Folder: f.Path, Harness: f.Harness}, false)
	require.NoError(t, err)
	l.addMember(t, a.ID)
	return a.ID
}

// messageOf returns the message with id from a history page.
func messageOf(t *testing.T, page dto.Page[dto.Message], id string) dto.Message {
	t.Helper()
	for _, m := range page.Items {
		if m.ID == id {
			return m
		}
	}
	require.FailNow(t, "message not in the history", id)
	return dto.Message{}
}

// SESS-28, SESS-29, SESS-30, SESS-31: a message an AI posts is the AI's, and it records the label
// of the session it came from: the live session its X-Partyline-Live header names, else the
// worktree folder it names, else "" for the principal. The text never decides the label, and an
// edit keeps it.
func TestAMessageRecordsTheSessionThatWroteIt(t *testing.T) {
	l := newLiveEnv(t)

	principalFolder := acme
	principalFolder.Live = testkit.NewLiveID()
	_, principalWelcome := dialAs(t, l, principalFolder)
	require.Empty(t, principalWelcome.Session.Label, "the first connection is the principal")
	id := principalWelcome.Agent.ID
	l.addMember(t, id)

	parallelFolder := acme
	parallelFolder.Live = testkit.NewLiveID()
	_, parallelWelcome := dialAs(t, l, parallelFolder)
	require.Equal(t, "claude-2", parallelWelcome.Session.Label)
	require.Equal(t, id, parallelWelcome.Agent.ID, "one AI for both sessions")

	live, resp := l.alice.DialLive("")
	require.NotNil(t, live, "live refused: %d %s", resp.Status, resp.Body)

	fromParallel := postAs(t, l, parallelFolder, "the parallel session writes")
	require.Equal(t, "claude-2", fromParallel.AuthorLabel)
	require.Equal(t, id, fromParallel.AuthorID, "the message is the AI's")

	var frame dto.Message
	live.Expect(dto.LiveEventTypeRoomMessage, &frame, wsWait)
	require.Equal(t, fromParallel.ID, frame.ID)
	require.Equal(t, "claude-2", frame.AuthorLabel, "the live frame carries the label")

	history := testkit.Decode[dto.Page[dto.Message]](l.alice.Get("/api/v1/rooms/" + l.room.ID + "/messages").RequireStatus(http.StatusOK))
	require.Equal(t, "claude-2", messageOf(t, history, fromParallel.ID).AuthorLabel, "the history carries the label")

	// The principal, a process that is not connected, a person and the text itself: no label.
	fromPrincipal := postAs(t, l, principalFolder, "the principal writes")
	require.Empty(t, fromPrincipal.AuthorLabel)
	fromText := postAs(t, l, principalFolder, "@me:fake a session in the text")
	require.Empty(t, fromText.AuthorLabel, "the label never comes from the text")
	stranger := acme
	stranger.Live = testkit.NewLiveID()
	fromStranger := postAs(t, l, stranger, "a process that is not connected")
	require.Empty(t, fromStranger.AuthorLabel, "an unknown live id in the AI's own folder is the principal")
	fromPerson := l.post(t, "a person writes")
	require.Empty(t, fromPerson.AuthorLabel, "a person's message has no session")

	// A tools-only process in a worktree sends no live id: its folder names the session (D10).
	repoID := l.aiOf(t, testkit.Folder{Machine: "devbox", Path: "/repo", Harness: "claude"})
	worktree := testkit.Folder{Machine: "devbox", Path: "/repo/wt", MainFolder: "/repo", Harness: "claude"}
	fromWorktree := postAs(t, l, worktree, "the worktree writes")
	require.Equal(t, "wt", fromWorktree.AuthorLabel)
	require.Equal(t, repoID, fromWorktree.AuthorID, "both folders are the AI of the repository")

	// The two other agent post paths record it too: a reply and a reaction.
	target := l.post(t, "a question")
	reply := testkit.Decode[dto.Message](l.tok.AsFolder(parallelFolder).
		Post("/api/v1/agent/messages/"+target.ID+"/reply", dto.ReplyRequest{Text: "the answer"}).RequireStatus(http.StatusCreated))
	require.Equal(t, "claude-2", reply.AuthorLabel)

	_, chat := storeChat(t, l.env, l.alice.User.ID, "tg")
	l.alice.SeedChatMember(l.room.ID, chat)
	chatMessage, err := l.env.App.Rooms.PostFromChat(context.Background(), l.room.ID,
		rooms.ChatMessage{ChatID: chat, SenderID: "5511999990000", Text: "on the platform"})
	require.NoError(t, err)
	reaction := testkit.Decode[dto.Message](l.tok.AsFolder(parallelFolder).
		Post("/api/v1/agent/messages/"+chatMessage.ID+"/react", dto.ReactRequest{Emoji: "👍"}).RequireStatus(http.StatusCreated))
	require.Equal(t, "claude-2", reaction.AuthorLabel)

	// An edit changes the text, never the session that wrote the message (SESS-31).
	edited := testkit.Decode[dto.Message](l.tok.AsFolder(parallelFolder).
		Patch("/api/v1/agent/messages/"+fromParallel.ID, dto.EditMessageRequest{Text: "edited"}).RequireStatus(http.StatusOK))
	require.Equal(t, "edited", edited.Text)
	require.Equal(t, "claude-2", edited.AuthorLabel, "the label stays after an edit")
	require.Equal(t, "claude-2", messageOf(t,
		testkit.Decode[dto.Page[dto.Message]](l.alice.Get("/api/v1/rooms/"+l.room.ID+"/messages").RequireStatus(http.StatusOK)),
		fromParallel.ID).AuthorLabel, "and in the history")
}

// SESS-32: an AI's profile lists its live sessions, the principal first, each with the harness and
// start time of the connection that holds it; the list refreshes as sessions attach and detach and
// is an empty array, never null, while the AI is offline.
func TestAProfileListsTheAIsLiveSessions(t *testing.T) {
	l := newLiveEnv(t)
	principalFolder := acme
	principalFolder.Live = testkit.NewLiveID()
	principalConn, principalWelcome := dialAs(t, l, principalFolder)
	id := principalWelcome.Agent.ID
	l.addMember(t, id)

	l.clock.Advance(time.Hour)
	parallelFolder := acme
	parallelFolder.Path = acme.Path + "/claude-2" // a worktree whose label is the preferred one
	parallelFolder.MainFolder = acme.Path
	parallelFolder.Harness = "pi"
	parallelFolder.Live = testkit.NewLiveID()
	parallelConn, parallelWelcome := dialAs(t, l, parallelFolder)
	require.Equal(t, "claude-2", parallelWelcome.Session.Label)
	require.Equal(t, id, parallelWelcome.Agent.ID, "one AI for both sessions")

	profile := profileOf(t, l.alice, id)
	require.NotNil(t, profile.AI)
	require.True(t, profile.AI.Online)
	require.Equal(t, []dto.SessionInfo{
		{Label: "", Harness: "claude", ConnectedAt: l.start},
		{Label: "claude-2", Harness: "pi", ConnectedAt: l.start.Add(time.Hour)},
	}, profile.AI.Sessions, "the principal first, then the parallel session")

	parallelConn.Close()
	require.Eventually(t, func() bool {
		p := profileOf(t, l.alice, id)
		return p.AI != nil && len(p.AI.Sessions) == 1 && p.AI.Sessions[0].Label == ""
	}, wsWait, 20*time.Millisecond, "closing the parallel session leaves the principal")

	principalConn.Close()
	var sessions []dto.SessionInfo
	require.Eventually(t, func() bool {
		p := profileOf(t, l.alice, id)
		if p.AI == nil || p.AI.Online {
			return false
		}
		sessions = p.AI.Sessions
		return true
	}, wsWait, 20*time.Millisecond, "the AI is offline once its last session ends")
	require.NotNil(t, sessions, "an empty array, not null")
	require.Empty(t, sessions)
}

// SESS-31: a message stored before this feature keeps the migration's default, "".
func TestAMessageStoredBeforeTheFeatureHasNoLabel(t *testing.T) {
	l := newLiveEnv(t)
	_, welcome := dialAs(t, l, acme)
	id := welcome.Agent.ID
	l.addMember(t, id)

	old := store.NewID()
	ctx := context.Background()
	require.NoError(t, l.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		room, err := q.NextRoomSeq(ctx, db.NextRoomSeqParams{UpdatedAt: l.start.UnixMilli(), ID: l.room.ID})
		if err != nil {
			return err
		}
		return q.CreateMessage(ctx, db.CreateMessageParams{
			ID: old, RoomID: l.room.ID, Seq: room.NextSeq - 1, AuthorKind: string(dto.AuthorKindAgent),
			AuthorID: id, AuthorIid: id, Kind: string(dto.MessageKindText), Text: "written before the feature",
			RecipientsJson: "[]", CreatedAt: l.start.UnixMilli(),
		})
	}))

	history := testkit.Decode[dto.Page[dto.Message]](l.alice.Get("/api/v1/rooms/" + l.room.ID + "/messages").RequireStatus(http.StatusOK))
	require.Empty(t, messageOf(t, history, old).AuthorLabel)
}
