package api_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

type pendingEnv struct {
	env    *testkit.Env
	alice  *testkit.Client
	agent  *testkit.Client // agent token, X-Partyline-Agent: codex-box
	room   dto.Room
	id     string // the agent's id
	member string // its room member id
}

// newPendingEnv registers codex-box over REST (no WebSocket) and makes it a member of a room.
func newPendingEnv(t *testing.T) pendingEnv {
	t.Helper()
	env := testkit.New(t)
	alice := env.NewUser("alice")
	agent := alice.Token(dto.TokenKindAgent, "laptop").WithHeader(dto.HeaderAgent, "codex-box")
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

// Auth matrix: agent tokens of registered agents only.
func TestAgentPendingAuth(t *testing.T) {
	e := newPendingEnv(t)
	e.alice.Token(dto.TokenKindAgent, "other").WithHeader(dto.HeaderAgent, "never-seen").Get("/api/v1/agent/pending").
		RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	e.alice.Token(dto.TokenKindUser, "cli").Get("/api/v1/agent/pending").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	e.env.Anon().WithHeader(dto.HeaderAgent, "codex-box").Get("/api/v1/agent/pending").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
}
