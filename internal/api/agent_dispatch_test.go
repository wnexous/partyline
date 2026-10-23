package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// twoAgents extends agentRoom with bob, a poster in the room, and bob's agent "helper",
// shared with alice and added to the room. Both agents are connected.
type twoAgents struct {
	*agentRoom
	bob      *testkit.Client
	helperID string
	builder  *testkit.AgentConn
	helper   *testkit.AgentConn
}

func newTwoAgents(t *testing.T) *twoAgents {
	t.Helper()
	f := &twoAgents{agentRoom: newAgentRoom(t)}
	f.bob = f.env.NewUser("bobby")
	addToRoom(t, f.env, f.alice, f.room.ID, f.bob)
	f.helperID = registerAgent(t, f.env, f.bob, "helper")
	shareAgent(f.bob, f.helperID, f.alice.User.ID)
	f.alice.Post("/api/v1/rooms/"+f.room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: f.helperID}).RequireStatus(http.StatusCreated)

	f.builder = f.dial(t)
	f.builder.Welcome(wsWait)
	var resp *testkit.Response
	f.helper, resp = f.bob.Token(dto.TokenKindAgent, "laptop").DialAgent("helper")
	require.NotNil(t, f.helper, "upgrade refused: %d", resp.Status)
	f.helper.Welcome(wsWait)
	return f
}

// DLV-02, DLV-06: each online agent gets the message, trusted only when the author is its owner.
func TestDispatchPushesWithRecipientRelativeTrust(t *testing.T) {
	f := newTwoAgents(t)

	fromAlice := f.post(f.alice, "from alice")
	got := f.builder.Message(wsWait)
	require.Equal(t, dto.RoomRef{ID: f.room.ID, Name: "Support"}, got.Room)
	require.Equal(t, fromAlice.ID, got.Message.ID)
	require.Equal(t, "from alice", got.Message.Text)
	require.True(t, got.Trusted, "alice's message to alice's agent")
	got = f.helper.Message(wsWait)
	require.Equal(t, fromAlice.ID, got.Message.ID)
	require.False(t, got.Trusted, "alice's message to bob's agent")

	fromBob := f.post(f.bob, "from bob")
	got = f.builder.Message(wsWait)
	require.Equal(t, fromBob.ID, got.Message.ID)
	require.False(t, got.Trusted, "bob's message to alice's agent")
	got = f.helper.Message(wsWait)
	require.Equal(t, fromBob.ID, got.Message.ID)
	require.True(t, got.Trusted, "bob's message to bob's agent")
}

// DLV-02: the author agent never receives its own message; the other agent does.
func TestDispatchSkipsAuthorAgent(t *testing.T) {
	f := newTwoAgents(t)

	own := f.postAsAgent(t, "builder speaking")
	got := f.helper.Message(wsWait)
	require.Equal(t, own.ID, got.Message.ID)
	require.Equal(t, dto.AuthorKindAgent, got.Message.AuthorKind)
	require.Equal(t, f.agentID, got.Message.AuthorID)
	require.False(t, got.Trusted)
	f.builder.ExpectNone(500 * time.Millisecond)
}

// DLV-08: a paused room pushes nothing to agents, live or on reconnect; after a resume the
// pending message arrives with the backlog.
func TestDispatchSkipsPausedRoom(t *testing.T) {
	f := newAgentRoom(t)
	conn := f.dial(t)
	conn.Welcome(wsWait)
	require.NoError(t, f.env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
		return q.SetRoomLoopState(context.Background(), db.SetRoomLoopStateParams{AgentStreak: 25, AgentsPaused: true, ID: f.room.ID})
	}))

	held := f.post(f.alice, "while paused")
	conn.ExpectNone(500 * time.Millisecond)
	conn.Close()
	require.Eventually(t, func() bool { return !f.env.App.Presence.Online(f.agentID) }, wsWait, 20*time.Millisecond)

	again := f.dial(t)
	again.Welcome(wsWait)
	again.ExpectNone(500 * time.Millisecond)
	again.Close()
	require.Eventually(t, func() bool { return !f.env.App.Presence.Online(f.agentID) }, wsWait, 20*time.Millisecond)

	f.alice.Post("/api/v1/rooms/"+f.room.ID+"/resume", nil).RequireStatus(http.StatusOK)
	resumed := f.dial(t)
	resumed.Welcome(wsWait)
	require.Equal(t, held.ID, resumed.Message(wsWait).Message.ID)
}

// ROOM-01 (agent side): an agent removed from a room gets no further deliveries from it,
// neither a live push while connected nor a backlog when it reconnects.
func TestRemovedAgentGetsNoPushAndNoBacklog(t *testing.T) {
	f := newTwoAgents(t)
	members := testkit.Decode[dto.List[dto.RoomMember]](f.alice.Get("/api/v1/rooms/" + f.room.ID + "/members").RequireStatus(http.StatusOK))
	var memberID string
	for _, m := range members.Items {
		if m.Kind == dto.MemberKindAgent && m.MemberID == f.helperID {
			memberID = m.ID
		}
	}
	require.NotEmpty(t, memberID, "helper is a member")
	f.alice.Delete("/api/v1/rooms/" + f.room.ID + "/members/" + memberID).RequireStatus(http.StatusNoContent)

	after := f.post(f.alice, "after the removal")
	require.Equal(t, after.ID, f.builder.Message(wsWait).Message.ID, "the remaining agent gets the message")
	f.helper.ExpectNone(500 * time.Millisecond)

	f.helper.Close()
	require.Eventually(t, func() bool { return !f.env.App.Presence.Online(f.helperID) }, wsWait, 20*time.Millisecond)
	again, resp := f.bob.Token(dto.TokenKindAgent, "laptop-2").DialAgent("helper")
	require.NotNil(t, again, "upgrade refused: %d", resp.Status)
	again.Welcome(wsWait)
	again.ExpectNone(500 * time.Millisecond)
}
