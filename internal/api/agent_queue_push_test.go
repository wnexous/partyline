package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// AD-035: GET /agent/pending, the pull path, was removed in v0.12.1. The tests that read an AI's
// undelivered queue through it now observe the same behaviour through the push path: the backlog a
// session receives when it connects and the live push, acknowledged over the WebSocket.

type queueEnv struct {
	env    *testkit.Env
	alice  *testkit.Client
	token  *testkit.Client // agent token acting as the AI of the folder /work/tools-box
	room   dto.Room
	id     string // the agent's id
	member string // its room member id
}

// newQueueEnv registers tools-box over REST (no WebSocket) and makes it a member of a room.
func newQueueEnv(t *testing.T) queueEnv {
	t.Helper()
	env := testkit.New(t)
	alice := env.NewUser("alice")
	token := alice.Token(dto.TokenKindAgent, "laptop")
	info := testkit.Decode[dto.AgentInfo](token.AsAgent("tools-box").Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK))
	room := testkit.Decode[dto.Room](alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "Builds"}).RequireStatus(http.StatusCreated))
	member := testkit.Decode[dto.RoomMember](alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: info.ID}).RequireStatus(http.StatusCreated))
	return queueEnv{env: env, alice: alice, token: token, room: room, id: info.ID, member: member.ID}
}

func (e queueEnv) post(t *testing.T, text string) dto.Message {
	t.Helper()
	return testkit.Decode[dto.Message](e.alice.Post("/api/v1/rooms/"+e.room.ID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
}

// dial opens the AI's agent WebSocket and reads the welcome frame.
func (e queueEnv) dial(t *testing.T) *testkit.AgentConn {
	t.Helper()
	conn, resp := e.token.DialAgent("tools-box")
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	conn.Welcome(wsWait)
	return conn
}

// offline closes the connection and waits until the AI is offline, so its backlog is read again.
func (e queueEnv) offline(t *testing.T, conn *testkit.AgentConn) {
	t.Helper()
	conn.Close()
	require.Eventually(t, func() bool { return !e.env.App.Presence.Online(e.id) }, wsWait, 10*time.Millisecond)
}

// TURN-01, TURN-02: the messages the AI did not receive arrive on connect, oldest first, with the room
// and the trusted flag, and an acknowledged message does not come again.
func TestAgentBacklogReturnsAndAcks(t *testing.T) {
	e := newQueueEnv(t)
	one, two := e.post(t, "one"), e.post(t, "two")

	conn := e.dial(t)
	got := conn.Message(wsWait)
	require.Equal(t, one.ID, got.Message.ID)
	require.Equal(t, "one", got.Message.Text)
	require.Equal(t, dto.RoomRef{ID: e.room.ID, Name: "Builds"}, got.Room)
	require.True(t, got.Trusted, "written by the agent's owner")
	require.Equal(t, two.ID, conn.Message(wsWait).Message.ID)
	conn.Ack(one.ID)
	conn.Ack(two.ID)
	require.Eventually(t, func() bool {
		seq, err := e.env.App.Store.R().GetAgentCursor(t.Context(), db.GetAgentCursorParams{AgentID: e.id, RoomID: e.room.ID})
		return err == nil && seq == two.Seq
	}, wsWait, 10*time.Millisecond, "the acknowledgements moved the cursor")

	e.offline(t, conn)
	again := e.dial(t)
	again.ExpectNone(300 * time.Millisecond)
}

// TURN-01: the room's delivery mode applies; unwanted messages are skipped for good.
func TestAgentBacklogFollowsDeliveryMode(t *testing.T) {
	e := newQueueEnv(t)
	mode, keywords := dto.DeliveryModeMentions, []string{"deploy"}
	e.alice.Patch("/api/v1/rooms/"+e.room.ID+"/members/"+e.member, dto.UpdateMemberRequest{Delivery: &mode, Keywords: &keywords}).RequireStatus(http.StatusOK)
	e.post(t, "hello")
	wanted := e.post(t, "deploy now")

	conn := e.dial(t)
	got := conn.Message(wsWait)
	require.Equal(t, wanted.ID, got.Message.ID)
	conn.ExpectNone(300 * time.Millisecond)
}

// SESS-22, SESS-24: a fallen-back message reaches the principal with its note over the push path; its
// acknowledgement leaves a tombstone, and no later connection receives it again.
func TestAFallbackIsPushedOnce(t *testing.T) {
	e := newQueueEnv(t)
	conn := e.dial(t)
	m := e.post(t, "@tools-box:claude-2 hello")
	got := conn.Message(wsWait)
	require.Equal(t, m.ID, got.Message.ID)
	require.Equal(t, "claude-2", got.ForLabel, "the principal reads the note")
	conn.Ack(m.ID)
	require.Eventually(t, func() bool {
		rows, err := e.env.App.Store.R().ListSessionDeliveriesByLabel(t.Context(), db.ListSessionDeliveriesByLabelParams{AgentID: e.id, Label: "claude-2"})
		return err == nil && len(rows) == 1 && rows[0].AckedAt.Valid
	}, wsWait, 10*time.Millisecond, "the acknowledgement left a tombstone")

	e.offline(t, conn)
	again := e.dial(t)
	again.ExpectNone(300 * time.Millisecond)
}
