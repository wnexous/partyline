package agents_test

import (
	"context"
	"database/sql"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// holder is the live session of another process, standing in for a socket.
type holder struct {
	mu     sync.Mutex
	closed int
}

func (h *holder) Close(websocket.StatusCode, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed++
}

func (h *holder) closes() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}

// SESS-10: a connection that attaches while another process already holds the AI is a parallel
// session, not a refusal: it gets the next label, nobody is closed and the AI's app stays the
// principal's.
func TestGatewayAttachesAConnectionThatLostTheRaceAsAParallelSession(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	id := env.NewAI(alice, "builder")
	ctx := context.Background()
	agent, err := env.App.Agents.ByID(ctx, id)
	require.NoError(t, err)
	winner := &holder{}
	var once sync.Once
	agents.SetBeforeAttach(env.App.Gateway, func() {
		once.Do(func() {
			env.App.Presence.AttachLabeled(ctx, agent, winner, "claude", "winning-process-id", "", false)
		})
	})
	sub, cancel := env.App.Bus.Subscribe(64)
	defer cancel()

	f := testkit.FolderOf("builder")
	f.Harness = "opencode"
	conn, resp := alice.Token(dto.TokenKindAgent, "laptop").DialFolder(f)
	require.NotNil(t, conn, "the race is no refusal: %d %s", resp.Status, resp.Body)
	welcome := conn.Welcome(30 * time.Second)
	require.Equal(t, "opencode-2", welcome.Session.Label, "the parallel label: its harness names it (SESS-11)")
	require.Equal(t, id, welcome.Agent.ID)

	env.App.Gateway.Close() // waits for the connection's handler to finish
	require.True(t, env.App.Presence.Online(id), "the principal stays live")
	require.Zero(t, winner.closes(), "nobody is closed")
	after, err := env.App.Agents.ByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "claude", after.Harness, "the parallel session did not relabel the AI")

	var announced []events.AgentPresence
drain:
	for {
		select {
		case e := <-sub:
			if p, ok := e.(events.AgentPresence); ok && p.AgentID == id {
				announced = append(announced, p)
			}
		default:
			break drain
		}
	}
	require.Equal(t, []events.AgentPresence{
		{AgentID: id, OwnerID: alice.User.ID, Online: true},
		{AgentID: id, OwnerID: alice.User.ID, Online: true},
		{AgentID: id, OwnerID: alice.User.ID, Online: true},
	}, announced, "both attaches and the parallel session's detach are announced; the principal never goes offline")
}

// SESS-10, SESS-15: two dials of one folder with different process ids are two live sessions of
// the same AI, and every welcome names the session it became: the principal is "" and the second
// one is labelled.
func TestTwoSessionsOfOneFolderShareTheAI(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	id := env.NewAI(alice, "builder")
	tok := alice.Token(dto.TokenKindAgent, "laptop")

	principal, resp := tok.DialFolder(testkit.FolderOf("builder"))
	require.NotNil(t, principal, "upgrade refused: %d %s", resp.Status, resp.Body)
	first := principal.Welcome(time.Minute)
	require.Equal(t, "", first.Session.Label, "the principal's label")
	require.Equal(t, id, first.Agent.ID)

	second, resp := tok.DialFolder(testkit.FolderOf("builder"))
	require.NotNil(t, second, "upgrade refused: %d %s", resp.Status, resp.Body)
	parallel := second.Welcome(time.Minute)
	require.Equal(t, "claude-2", parallel.Session.Label)
	require.Equal(t, id, parallel.Agent.ID, "one AI for both sessions")
	require.Equal(t, "claude", parallel.Session.Harness)
	require.Equal(t, "claude", first.Session.Harness)

	require.NoError(t, principal.Closed(200*time.Millisecond), "the principal stays open")
	require.NoError(t, second.Closed(200*time.Millisecond), "the parallel session stays open")
	require.True(t, env.App.Presence.Online(id))
}

// SESS-11: a session in a linked worktree is a parallel session of the repository's AI and its
// label is the worktree folder's slug.
func TestAWorktreeSessionGetsTheWorktreesLabel(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	main := testkit.Folder{Machine: testkit.Machine, Path: "/repo", Harness: "claude"}
	principal, resp := tok.DialFolder(main)
	require.NotNil(t, principal, "upgrade refused: %d %s", resp.Status, resp.Body)
	first := principal.Welcome(time.Minute)
	require.Equal(t, "", first.Session.Label)

	worktree := testkit.Folder{Machine: testkit.Machine, Path: "/repo/wt", MainFolder: "/repo", Harness: "pi"}
	session, resp := tok.DialFolder(worktree)
	require.NotNil(t, session, "upgrade refused: %d %s", resp.Status, resp.Body)
	welcome := session.Welcome(time.Minute)
	require.Equal(t, "wt", welcome.Session.Label)
	require.Equal(t, first.Agent.ID, welcome.Agent.ID, "the worktree session is the repository's AI")
	require.Equal(t, "pi", welcome.Session.Harness)
	require.NoError(t, principal.Closed(200*time.Millisecond))
}

// SESS-13, SESS-15: a takeover dial replaces the principal, closes the old socket with 4002 and
// its welcome names the principal session.
func TestATakeoverDialBecomesThePrincipal(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	id := env.NewAI(alice, "builder")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	first, resp := tok.DialFolder(testkit.FolderOf("builder"))
	require.NotNil(t, first, "upgrade refused: %d %s", resp.Status, resp.Body)
	first.Welcome(time.Minute)

	takeover := testkit.FolderOf("builder")
	takeover.Takeover = true
	second, resp := tok.DialFolder(takeover)
	require.NotNil(t, second, "upgrade refused: %d %s", resp.Status, resp.Body)
	welcome := second.Welcome(time.Minute)
	require.Equal(t, "", welcome.Session.Label, "a takeover is the principal")
	require.Equal(t, id, welcome.Agent.ID)

	var closeErr websocket.CloseError
	require.ErrorAs(t, first.Closed(time.Minute), &closeErr)
	require.Equal(t, agents.CloseTakenOver, closeErr.Code)
	require.Equal(t, "taken_over", closeErr.Reason)
	require.True(t, env.App.Presence.Online(id))
}

// SESS-17: a parallel session shares the AI without relabeling it: its harness and connect time
// stay the principal's.
func TestAParallelSessionDoesNotRelabelTheAI(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	id := env.NewAI(alice, "builder")
	ctx := context.Background()
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	principal, resp := tok.DialFolder(testkit.FolderOf("builder"))
	require.NotNil(t, principal, "upgrade refused: %d %s", resp.Status, resp.Body)
	principal.Welcome(time.Minute)
	before, err := env.App.Agents.ByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "claude", before.Harness)

	parallel := testkit.FolderOf("builder")
	parallel.Harness = "pi"
	second, resp := tok.DialFolder(parallel)
	require.NotNil(t, second, "upgrade refused: %d %s", resp.Status, resp.Body)
	welcome := second.Welcome(time.Minute)
	require.Equal(t, "pi-2", welcome.Session.Label, "the session's harness names a parallel label (SESS-11)")
	require.Equal(t, "pi", welcome.Session.Harness)

	after, err := env.App.Agents.ByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "claude", after.Harness, "a labelled session never writes agents.harness")
	require.Equal(t, before.ConnectedAt, after.ConnectedAt, "nor agents.connected_at")
}

// SESS-17: the AI's cursor follows its principal session: a labelled session's acknowledgement
// of a message moves nothing.
func TestALabelledSessionAckDoesNotMoveTheAICursor(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	id := env.NewAI(alice, "builder")
	ctx := context.Background()
	room := testkit.Decode[dto.Room](alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "Support"}).RequireStatus(http.StatusCreated))
	alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: id}).RequireStatus(http.StatusCreated)
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	principal, resp := tok.DialFolder(testkit.FolderOf("builder"))
	require.NotNil(t, principal, "upgrade refused: %d %s", resp.Status, resp.Body)
	principal.Welcome(time.Minute)
	m := testkit.Decode[dto.Message](alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "for the principal"}).RequireStatus(http.StatusCreated))
	require.Equal(t, m.ID, principal.Message(time.Minute).Message.ID, "the dispatcher pushes it to the principal")

	// The labelled session has no backlog: the principal's unacknowledged message is not its own.
	second, resp := tok.DialFolder(testkit.FolderOf("builder"))
	require.NotNil(t, second, "upgrade refused: %d %s", resp.Status, resp.Body)
	require.Equal(t, "claude-2", second.Welcome(time.Minute).Session.Label)
	second.ExpectNone(400 * time.Millisecond)

	// A message addressed to it reaches it, and its acknowledgement moves nothing of the AI's.
	addressed := testkit.Decode[dto.Message](alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "@builder:claude-2 for the session"}).RequireStatus(http.StatusCreated))
	require.Equal(t, addressed.ID, second.Message(time.Minute).Message.ID)
	second.Ack(addressed.ID)
	second.ExpectNone(300 * time.Millisecond)
	_, err := env.App.Store.R().GetAgentCursor(ctx, db.GetAgentCursorParams{AgentID: id, RoomID: room.ID})
	require.ErrorIs(t, err, sql.ErrNoRows, "a labelled ack leaves no cursor row")
	principal.ExpectNone(300 * time.Millisecond)

	principal.Ack(m.ID)
	require.Eventually(t, func() bool {
		seq, err := env.App.Store.R().GetAgentCursor(ctx, db.GetAgentCursorParams{AgentID: id, RoomID: room.ID})
		return err == nil && seq == m.Seq
	}, time.Minute, 10*time.Millisecond, "the principal's ack moves the cursor")
}

// SES-05 (L-050): a message the old session already acknowledged does not reach the session that
// took the AI over. The dispatcher can still be handing the message on when the new session
// attaches (the new session's backlog is empty, its cursor already covers the message), so the
// gateway drops every live push at or below the AI's cursor; a later message arrives.
func TestTakeoverDropsALatePushOfAnAcknowledgedMessage(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	agentID := env.NewAI(alice, "builder")
	ctx := context.Background()
	room := testkit.Decode[dto.Room](alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "Support"}).RequireStatus(http.StatusCreated))
	alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: agentID}).RequireStatus(http.StatusCreated)
	post := func(text string) dto.Message {
		t.Helper()
		return testkit.Decode[dto.Message](alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
	}
	token := alice.Token(dto.TokenKindAgent, "laptop")
	folder := testkit.FolderOf("builder")

	holder, resp := token.DialFolder(folder)
	require.NotNil(t, holder, "upgrade refused: %d %s", resp.Status, resp.Body)
	holder.Welcome(time.Minute)
	before := post("before the takeover")
	require.Equal(t, before.ID, holder.Message(time.Minute).Message.ID)
	holder.Ack(before.ID)
	require.Eventually(t, func() bool {
		seq, err := env.App.Store.R().GetAgentCursor(ctx, db.GetAgentCursorParams{AgentID: agentID, RoomID: room.ID})
		return err == nil && seq == before.Seq
	}, time.Minute, 10*time.Millisecond, "the ack is recorded")

	folder.Takeover = true
	taker, resp := token.DialFolder(folder)
	require.NotNil(t, taker, "upgrade refused: %d %s", resp.Status, resp.Body)
	taker.Welcome(time.Minute)
	require.Equal(t, agents.CloseTakenOver, websocket.CloseStatus(holder.Closed(time.Minute)))

	// The dispatcher, late, pushes the acknowledged message to the AI's session: the new one.
	late, err := env.App.Gateway.Message(ctx, before.ID)
	require.NoError(t, err)
	require.True(t, env.App.Gateway.Push(agentID, "", dto.AgentMessage{Room: dto.RoomRef{ID: room.ID, Name: "Support"}, Message: late}), "queued for the new session")
	taker.ExpectNone(500 * time.Millisecond)

	after := post("after the takeover")
	require.Equal(t, after.ID, taker.Message(time.Minute).Message.ID, "the next message arrives")
}

// SES-06: a connection that stops answering pings (a dead network, no close frame) is closed
// after the ping interval plus the ping timeout, and the AI goes offline.
func TestSilentConnectionGoesOffline(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	id := env.NewAI(alice, "builder")
	agents.SetPingTimings(env.App.Gateway, 50*time.Millisecond, 200*time.Millisecond)

	conn := alice.Token(dto.TokenKindAgent, "laptop").DialFolderSilent(testkit.FolderOf("builder"))
	defer func() { _ = conn.CloseNow() }()
	require.Eventually(t, func() bool { return env.App.Presence.Online(id) }, 5*time.Second, 5*time.Millisecond,
		"the session attaches")
	require.Eventually(t, func() bool { return !env.App.Presence.Online(id) }, 5*time.Second, 10*time.Millisecond,
		"a session that stops answering pings goes offline")
}

// SES-06: a healthy client that keeps answering pings stays online through many pings with the
// production pong timeout, and keeps receiving messages.
func TestHealthyConnectionStaysOnlineAcrossPings(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	id := env.NewAI(alice, "builder")
	agents.SetPingInterval(env.App.Gateway, 20*time.Millisecond)

	conn, resp := alice.Token(dto.TokenKindAgent, "laptop").DialFolder(testkit.FolderOf("builder"))
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	require.Eventually(t, func() bool { return env.App.Presence.Online(id) }, 5*time.Second, 5*time.Millisecond,
		"the session attaches")
	deadline := time.Now().Add(500 * time.Millisecond) // about 25 pings
	for time.Now().Before(deadline) {
		require.True(t, env.App.Presence.Online(id), "a client that answers pings stays online")
		time.Sleep(10 * time.Millisecond)
	}
	require.NoError(t, conn.Closed(10*time.Millisecond), "the connection is still open")
}
