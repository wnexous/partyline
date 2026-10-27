package api_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

type pendingEnv struct {
	env    *testkit.Env
	alice  *testkit.Client
	agent  *testkit.Client // agent token acting as the AI of the folder /work/codex-box
	room   dto.Room
	id     string // the agent's id
	member string // its room member id
}

// newPendingEnv registers codex-box over REST (no WebSocket) and makes it a member of a room.
func newPendingEnv(t *testing.T) pendingEnv {
	t.Helper()
	env := testkit.New(t)
	alice := env.NewUser("alice")
	agent := alice.Token(dto.TokenKindAgent, "laptop").AsAgent("codex-box")
	info := testkit.Decode[dto.AgentInfo](agent.Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK))
	room := testkit.Decode[dto.Room](alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "Builds"}).RequireStatus(http.StatusCreated))
	member := testkit.Decode[dto.RoomMember](alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: info.ID}).RequireStatus(http.StatusCreated))
	return pendingEnv{env: env, alice: alice, agent: agent, room: room, id: info.ID, member: member.ID}
}

func (e pendingEnv) post(t *testing.T, text string) dto.Message {
	t.Helper()
	return testkit.Decode[dto.Message](e.alice.Post("/api/v1/rooms/"+e.room.ID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
}

func (e pendingEnv) pending(t *testing.T, query string) []dto.AgentMessage {
	t.Helper()
	return testkit.Decode[dto.List[dto.AgentMessage]](e.agent.Get("/api/v1/agent/pending" + query).RequireStatus(http.StatusOK)).Items
}

// TURN-01, TURN-02: GET /agent/pending returns the undelivered messages oldest first, with the
// room and trusted flag, and counts them as delivered.
func TestAgentPendingReturnsAndAcks(t *testing.T) {
	e := newPendingEnv(t)
	require.Equal(t, []dto.AgentMessage{}, e.pending(t, ""))
	first, second := e.post(t, "one"), e.post(t, "two")

	got := e.pending(t, "")
	require.Len(t, got, 2)
	require.Equal(t, first.ID, got[0].Message.ID)
	require.Equal(t, "one", got[0].Message.Text)
	require.Equal(t, second.ID, got[1].Message.ID)
	require.Equal(t, dto.RoomRef{ID: e.room.ID, Name: "Builds"}, got[0].Room)
	require.True(t, got[0].Trusted, "written by the agent's owner")
	require.Empty(t, e.pending(t, ""), "returned messages are delivered")
}

// TURN-01: at most 20 per call, the oldest first; the rest come with the next call.
func TestAgentPendingLimit(t *testing.T) {
	e := newPendingEnv(t)
	var ids []string
	for i := range 23 {
		ids = append(ids, e.post(t, fmt.Sprintf("m%d", i)).ID)
	}
	got := e.pending(t, "")
	require.Len(t, got, 20)
	require.Equal(t, ids[0], got[0].Message.ID)
	require.Equal(t, ids[19], got[19].Message.ID)
	rest := e.pending(t, "")
	require.Len(t, rest, 3)
	require.Equal(t, ids[20], rest[0].Message.ID)
}

// TURN-01: the room's delivery mode applies; unwanted messages are skipped for good.
func TestAgentPendingFollowsDeliveryMode(t *testing.T) {
	e := newPendingEnv(t)
	mode, keywords := dto.DeliveryModeMentions, []string{"deploy"}
	e.alice.Patch("/api/v1/rooms/"+e.room.ID+"/members/"+e.member, dto.UpdateMemberRequest{Delivery: &mode, Keywords: &keywords}).RequireStatus(http.StatusOK)
	e.post(t, "hello")
	wanted := e.post(t, "deploy now")
	got := e.pending(t, "")
	require.Len(t, got, 1)
	require.Equal(t, wanted.ID, got[0].Message.ID)
	require.Empty(t, e.pending(t, ""))
}

// SESS-22, SESS-24: GET /agent/pending returns a fallen-back message with its note and consumes it;
// the second call does not return it again.
func TestAgentPendingReturnsAFallbackOnce(t *testing.T) {
	e := newPendingEnv(t)
	conn, resp := e.agent.DialAgent("codex-box")
	require.NotNil(t, conn, "upgrade refused: %d", resp.Status)
	conn.Welcome(wsWait)
	m := e.post(t, "@codex-box:claude-2 hello")
	got := conn.Message(wsWait)
	require.Equal(t, m.ID, got.Message.ID)
	require.Equal(t, "claude-2", got.ForLabel, "the principal reads the note")

	conn.Close()
	require.Eventually(t, func() bool { return !e.env.App.Presence.Online(e.id) }, 5*time.Second, 10*time.Millisecond)
	first := e.pending(t, "")
	require.Len(t, first, 1)
	require.Equal(t, m.ID, first[0].Message.ID)
	require.Equal(t, "claude-2", first[0].ForLabel)
	require.Empty(t, e.pending(t, ""), "a pending read consumes the fallback")
}

// SESS-24: a delivery row still owned by a live label is not in the principal's pending queue.
func TestAgentPendingSkipsAPendingLabelsMessage(t *testing.T) {
	e := newPendingEnv(t)
	principal, resp := e.agent.DialAgent("codex-box")
	require.NotNil(t, principal, "upgrade refused: %d", resp.Status)
	principal.Welcome(wsWait)
	parallel, resp := e.agent.DialAgent("codex-box")
	require.NotNil(t, parallel, "upgrade refused: %d", resp.Status)
	require.Equal(t, "claude-2", parallel.Welcome(wsWait).Session.Label)

	m := e.post(t, "@codex-box:claude-2 hello")
	require.Equal(t, m.ID, parallel.Message(wsWait).Message.ID)
	a, err := e.env.App.Agents.ByID(context.Background(), e.id)
	require.NoError(t, err)
	msgs, err := e.env.App.Gateway.Pending(context.Background(), a, "", agents.PendingLimit)
	require.NoError(t, err)
	require.Empty(t, msgs, "the live label owns the message")
	principal.ExpectNone(300 * time.Millisecond)
}

// TURN-01: wait=N returns an empty list after N seconds when nothing arrives.
func TestAgentPendingWaitTimesOut(t *testing.T) {
	e := newPendingEnv(t)
	start := time.Now()
	require.Equal(t, []dto.AgentMessage{}, e.pending(t, "?wait=1"))
	elapsed := time.Since(start)
	require.GreaterOrEqual(t, elapsed, time.Second)
	require.Less(t, elapsed, 10*time.Second)
}

// TURN-01: a message posted while waiting is returned before the timeout.
func TestAgentPendingWaitReturnsEarly(t *testing.T) {
	e := newPendingEnv(t)
	go func() {
		time.Sleep(300 * time.Millisecond)
		e.post(t, "arrived")
	}()
	start := time.Now()
	got := e.pending(t, "?wait=30")
	require.Len(t, got, 1)
	require.Equal(t, "arrived", got[0].Message.Text)
	require.Less(t, time.Since(start), 20*time.Second)
}

// TURN-01: wait is 0 to 300 seconds.
func TestAgentPendingRejectsBadWait(t *testing.T) {
	e := newPendingEnv(t)
	for _, q := range []string{"?wait=301", "?wait=-1", "?wait=soon"} {
		err := e.agent.Get("/api/v1/agent/pending"+q).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, err.Fields, "wait", q)
	}
	e.agent.Get("/api/v1/agent/pending?wait=0").RequireStatus(http.StatusOK)
}

// An agent connected over the WebSocket gets its messages pushed: pending answers 409 and
// takes nothing.
func TestAgentPendingRefusesPushedSessions(t *testing.T) {
	e := newPendingEnv(t)
	conn, resp := e.agent.DialAgent("codex-box")
	require.NotNil(t, conn, "upgrade refused: %d", resp.Status)
	conn.Welcome(wsWait)
	err := e.agent.Get("/api/v1/agent/pending").RequireError(http.StatusConflict, dto.ErrorCodeConflict)
	require.Equal(t, "messages are pushed to this agent's session; there is nothing to wait for", err.Message)
}

// Auth matrix: agent tokens only. A folder the server has not seen gets its AI on this first
// call (AID-04) and nothing is pending for it.
func TestAgentPendingAuth(t *testing.T) {
	e := newPendingEnv(t)
	before := len(ownedAIs(t, e.alice))
	fresh := testkit.Decode[dto.List[dto.AgentMessage]](e.alice.Token(dto.TokenKindAgent, "other").AsAgent("never-seen").Get("/api/v1/agent/pending").
		RequireStatus(http.StatusOK))
	require.Empty(t, fresh.Items)
	require.Len(t, ownedAIs(t, e.alice), before+1, "the call created the AI of the unseen folder")
	e.alice.Token(dto.TokenKindUser, "cli").Get("/api/v1/agent/pending").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	e.env.Anon().AsAgent("codex-box").Get("/api/v1/agent/pending").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
}

// worktreePendingEnv is an AI of /repo with a live principal session, a room, and the tools-only
// worktree folder /repo/wt that resolves to the same AI.
type worktreePendingEnv struct {
	env       *testkit.Env
	alice     *testkit.Client
	token     *testkit.Client
	id        string
	room      dto.Room
	principal testkit.Folder
	worktree  testkit.Folder
	conn      *testkit.AgentConn
}

func newWorktreePendingEnv(t *testing.T) *worktreePendingEnv {
	t.Helper()
	env, _ := fakeConnectorEnv(t)
	alice := env.NewUser("alice")
	token := alice.Token(dto.TokenKindAgent, "laptop")
	principal := testkit.Folder{Machine: testkit.Machine, Path: "/repo", Harness: "claude"}
	conn, resp := token.DialFolder(principal)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	welcome := conn.Welcome(wsWait)
	room := testkit.Decode[dto.Room](alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "Repo"}).RequireStatus(http.StatusCreated))
	alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: welcome.Agent.ID}).RequireStatus(http.StatusCreated)
	return &worktreePendingEnv{
		env: env, alice: alice, token: token, id: welcome.Agent.ID, room: room, principal: principal,
		worktree: testkit.Folder{Machine: testkit.Machine, Path: "/repo/wt", MainFolder: "/repo", Harness: "claude"},
		conn:     conn,
	}
}

func (e *worktreePendingEnv) post(t *testing.T, text string) dto.Message {
	t.Helper()
	return testkit.Decode[dto.Message](e.alice.Post("/api/v1/rooms/"+e.room.ID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
}

// cursors is the AI's room cursor rows, which a worktree reader must not move.
func (e *worktreePendingEnv) cursors(t *testing.T) []db.ListRoomAgentCursorsRow {
	t.Helper()
	rows, err := e.env.App.Store.R().ListRoomAgentCursors(t.Context(), e.room.ID)
	require.NoError(t, err)
	return rows
}

// pending reads GET /agent/pending as the AI of folder.
func (e *worktreePendingEnv) pending(t *testing.T, folder testkit.Folder, query string) dto.AgentPending {
	t.Helper()
	return testkit.Decode[dto.AgentPending](e.token.AsFolder(folder).Get("/api/v1/agent/pending" + query).RequireStatus(http.StatusOK))
}

// SESS-06: a tools-only process in a linked worktree reads no queue: the empty page moves no
// cursor, deletes no delivery row and takes no platform event or dropped count, and it answers at
// once even with wait=60. The principal's queue is untouched and reads the messages once.
func TestWorktreeToolsOnlyPendingReadsNoQueue(t *testing.T) {
	e := newWorktreePendingEnv(t)

	plain := e.post(t, "one")
	require.Equal(t, plain.ID, e.conn.Message(wsWait).Message.ID)
	addressed := e.post(t, "@repo:wt two")
	fallback := e.conn.Message(wsWait)
	require.Equal(t, addressed.ID, fallback.Message.ID)
	require.Equal(t, "wt", fallback.ForLabel, "no live session holds the worktree label")

	// A platform event and a dropped count also wait in the AI's queues.
	connector := createFakeIdle(e.alice, "wa", false)
	chat, err := connectors.UpsertChat(t.Context(), e.env.App.Store, e.env.App.Bus, connector.ID,
		connectors.ChatInfo{ID: "120363000000000001@g.us", Title: "Team", Kind: "group"}, time.Now(), time.Now())
	require.NoError(t, err)
	eventID, queued, err := e.env.App.Platform.Enqueue(t.Context(), platform.Item{
		AgentID: e.id, ConnectorID: connector.ID, ChatID: chat.ID, ExternalID: "wamid.one",
		SenderID: "5511999990000", SenderName: "Pat", Text: "on the phone",
	})
	require.NoError(t, err)
	require.True(t, queued)
	require.NoError(t, e.env.App.Store.WithTx(t.Context(), func(q *db.Queries) error {
		return q.AddPlatformDropped(t.Context(), db.AddPlatformDroppedParams{N: 2, AgentID: e.id})
	}))

	before := e.cursors(t)
	start := time.Now()
	got := e.pending(t, e.worktree, "?platform=1&wait=60")
	require.Less(t, time.Since(start), 5*time.Second, "an empty page never waits")
	require.Equal(t, dto.AgentPending{Items: []dto.AgentMessage{}, Platform: []dto.PlatformEvent{}, Dropped: 0}, got)
	require.Equal(t, before, e.cursors(t), "no cursor moved")
	rows, err := e.env.App.Store.R().ListSessionDeliveriesByLabel(t.Context(), db.ListSessionDeliveriesByLabelParams{AgentID: e.id, Label: "wt"})
	require.NoError(t, err)
	require.Len(t, rows, 1, "the delivery row is still there")
	require.Equal(t, addressed.ID, rows[0].MessageID)
	require.True(t, rows[0].FellBackAt.Valid)
	unacked, err := e.env.App.Platform.Unacked(t.Context(), e.id, 0, 10)
	require.NoError(t, err)
	require.Len(t, unacked, 1, "no platform event taken")
	require.Equal(t, eventID, unacked[0].ID)
	dropped, err := e.env.App.Store.R().GetPlatformDropped(t.Context(), e.id)
	require.NoError(t, err)
	require.Equal(t, int64(2), dropped, "the dropped count is not taken")

	// The principal's own folder reads the queue as today, once: the person's message, the fallback
	// with its note, the platform event and the count, and nothing on the next call.
	e.conn.Close()
	require.Eventually(t, func() bool { return !e.env.App.Presence.Online(e.id) }, 5*time.Second, 10*time.Millisecond)
	first := e.pending(t, e.principal, "?platform=1")
	require.Len(t, first.Items, 2)
	require.Equal(t, plain.ID, first.Items[0].Message.ID)
	require.Empty(t, first.Items[0].ForLabel)
	require.Equal(t, addressed.ID, first.Items[1].Message.ID)
	require.Equal(t, "wt", first.Items[1].ForLabel)
	require.Len(t, first.Platform, 1)
	require.Equal(t, eventID, first.Platform[0].ID)
	require.Equal(t, int64(2), first.Dropped)
	again := e.pending(t, e.principal, "?platform=1")
	require.Equal(t, dto.AgentPending{Items: []dto.AgentMessage{}, Platform: []dto.PlatformEvent{}, Dropped: 0}, again)
}
