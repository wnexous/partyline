package api_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// wsWait bounds waits for an agent frame or close; they return as soon as it arrives, so the
// bound only matters on a loaded machine.
const wsWait = time.Minute

// agentRoom is a room of alice with her agent "builder" as a member.
type agentRoom struct {
	env     *testkit.Env
	alice   *testkit.Client
	agent   *testkit.Client // alice's agent token
	agentID string
	room    dto.Room
}

func newAgentRoom(t *testing.T) *agentRoom {
	t.Helper()
	env := testkit.New(t)
	alice := env.NewUser("alice")
	f := &agentRoom{env: env, alice: alice, agent: alice.Token(dto.TokenKindAgent, "laptop")}
	f.agentID = registerAgent(t, env, alice, "builder")
	f.room = createRoom(alice, "Support")
	alice.Post("/api/v1/rooms/"+f.room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: f.agentID}).RequireStatus(http.StatusCreated)
	return f
}

func (f *agentRoom) post(c *testkit.Client, text string) dto.Message {
	c.T.Helper()
	return testkit.Decode[dto.Message](c.Post("/api/v1/rooms/"+f.room.ID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
}

// postAsAgent stores a message authored by the agent itself.
func (f *agentRoom) postAsAgent(t *testing.T, text string) rooms.Message {
	t.Helper()
	p := authn.Principal{Kind: authn.KindAgent, UserID: f.alice.User.ID, AgentID: f.agentID, AgentName: "builder"}
	m, err := f.env.App.Rooms.Post(context.Background(), p, f.room.ID, rooms.PostInput{Text: text})
	require.NoError(t, err)
	return m
}

func (f *agentRoom) dial(t *testing.T) *testkit.AgentConn {
	t.Helper()
	conn, resp := f.agent.DialAgent("builder")
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	return conn
}

func (f *agentRoom) cursor(t *testing.T) int64 {
	t.Helper()
	seq, err := f.env.App.Store.R().GetAgentCursor(context.Background(), dbCursorParams(f.agentID, f.room.ID))
	require.NoError(t, err)
	return seq
}

func closeError(t *testing.T, err error) websocket.CloseError {
	t.Helper()
	var ce websocket.CloseError
	require.True(t, errors.As(err, &ce), "not a close error: %v", err)
	return ce
}

// AGT-01: the first connection registers the agent and the welcome frame identifies it.
func TestAgentWSWelcomeRegistersAgent(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	conn, _ := alice.Token(dto.TokenKindAgent, "laptop").DialAgent("Billing API!")
	require.NotNil(t, conn)
	w := conn.Welcome(wsWait)
	require.Equal(t, "billing-api", w.Agent.Name)
	require.Equal(t, "alice", w.Agent.Owner)

	list := testkit.Decode[dto.List[dto.Agent]](alice.Get("/api/v1/agents").RequireStatus(http.StatusOK))
	require.Len(t, list.Items, 1)
	require.Equal(t, w.Agent.ID, list.Items[0].ID)
	require.True(t, list.Items[0].Online)
}

// Only agent tokens may open the agent WebSocket.
func TestAgentWSRequiresAgentToken(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	for name, c := range map[string]*testkit.Client{"session": alice, "user token": alice.Token(dto.TokenKindUser, "script")} {
		conn, resp := c.DialAgent("builder")
		require.Nil(t, conn, name)
		require.Equal(t, http.StatusForbidden, resp.Status, name)
	}
	conn, resp := env.Anon().DialAgent("builder")
	require.Nil(t, conn)
	require.Equal(t, http.StatusUnauthorized, resp.Status)
}

// AGT-04: a second connection as the same agent closes the first with reason "replaced".
func TestAgentWSSecondConnectionReplacesFirst(t *testing.T) {
	f := newAgentRoom(t)
	first := f.dial(t)
	first.Welcome(wsWait)
	second := f.dial(t)
	second.Welcome(wsWait)

	ce := closeError(t, first.Closed(wsWait))
	require.Equal(t, websocket.StatusCode(4000), ce.Code)
	require.Equal(t, "replaced", ce.Reason)
	require.NoError(t, second.Closed(300*time.Millisecond), "the newer connection stays open")
	require.True(t, f.env.App.Presence.Online(f.agentID))
}

// AGT-08: messages posted while offline arrive after connecting, oldest first; the agent's
// own messages are not delivered back to it (DLV-02).
func TestAgentWSDeliversBacklogInOrder(t *testing.T) {
	f := newAgentRoom(t)
	want := []dto.Message{f.post(f.alice, "one"), f.post(f.alice, "two")}
	f.postAsAgent(t, "my own words")
	want = append(want, f.post(f.alice, "three"))

	conn := f.dial(t)
	w := conn.Welcome(wsWait)
	require.Equal(t, dto.AgentInfo{ID: f.agentID, Name: "builder", Owner: "alice"}, w.Agent)
	for _, m := range want {
		got := conn.Message(wsWait)
		require.Equal(t, dto.RoomRef{ID: f.room.ID, Name: "Support"}, got.Room)
		require.Equal(t, m.ID, got.Message.ID)
		require.Equal(t, m.Seq, got.Message.Seq)
		require.Equal(t, m.Text, got.Message.Text)
		require.True(t, got.Trusted, "the owner's messages are trusted")
	}
	conn.ExpectNone(300 * time.Millisecond)
}

// AGT-09: with 60 messages pending the agent gets a notice with the skipped count, the last
// 50 messages, and the cursor moves past the skipped ones.
func TestAgentWSBacklogCapSkipsOlderMessages(t *testing.T) {
	f := newAgentRoom(t)
	var posted []dto.Message
	for i := 1; i <= 60; i++ {
		posted = append(posted, f.post(f.alice, fmt.Sprintf("message %d", i)))
	}

	conn := f.dial(t)
	conn.Welcome(wsWait)
	n := conn.Notice(wsWait)
	require.Equal(t, dto.RoomRef{ID: f.room.ID, Name: "Support"}, n.Room)
	require.Equal(t, "10 older messages skipped; use the history tool", n.Text)
	for _, m := range posted[10:] {
		require.Equal(t, m.ID, conn.Message(wsWait).Message.ID)
	}
	conn.ExpectNone(300 * time.Millisecond)
	require.Equal(t, posted[9].Seq, f.cursor(t))
}

// AGT-07: an ack advances the cursor (never backwards); a reconnect resumes after it.
func TestAgentWSAckAdvancesCursor(t *testing.T) {
	f := newAgentRoom(t)
	posted := []dto.Message{f.post(f.alice, "one"), f.post(f.alice, "two"), f.post(f.alice, "three")}

	conn := f.dial(t)
	conn.Welcome(wsWait)
	for range posted {
		conn.Message(wsWait)
	}
	conn.Ack(posted[1].ID)
	require.Eventually(t, func() bool {
		seq, err := f.env.App.Store.R().GetAgentCursor(context.Background(), dbCursorParams(f.agentID, f.room.ID))
		return err == nil && seq == posted[1].Seq
	}, wsWait, 20*time.Millisecond)
	conn.Ack(posted[0].ID)
	conn.Close()
	require.Eventually(t, func() bool { return !f.env.App.Presence.Online(f.agentID) }, wsWait, 20*time.Millisecond)
	require.Equal(t, posted[1].Seq, f.cursor(t), "an older ack does not move the cursor back")

	again := f.dial(t)
	again.Welcome(wsWait)
	require.Equal(t, posted[2].ID, again.Message(wsWait).Message.ID)
	again.ExpectNone(300 * time.Millisecond)
}

// AGT-01/AGT-03: the owner sees the agent go online on connect and offline after close.
func TestAgentWSPresenceOnlineThenOffline(t *testing.T) {
	f := newAgentRoom(t)
	live, _ := f.alice.DialLive("")
	require.NotNil(t, live)
	live.Expect(dto.LiveEventTypeHello, nil, wsWait)

	conn := f.dial(t)
	conn.Welcome(wsWait)
	var p dto.AgentPresence
	live.Expect(dto.LiveEventTypeAgentPresence, &p, wsWait)
	require.Equal(t, dto.AgentPresence{AgentID: f.agentID, Online: true}, p)

	conn.Close()
	live.Expect(dto.LiveEventTypeAgentPresence, &p, wsWait)
	require.Equal(t, dto.AgentPresence{AgentID: f.agentID, Online: false}, p)
	agent := testkit.Decode[dto.List[dto.Agent]](f.alice.Get("/api/v1/agents").RequireStatus(http.StatusOK)).Items[0]
	require.False(t, agent.Online)
	require.NotNil(t, agent.LastSeenAt)
}

func dbCursorParams(agentID, roomID string) db.GetAgentCursorParams {
	return db.GetAgentCursorParams{AgentID: agentID, RoomID: roomID}
}

// The agent is always resolved from (token owner, agent name): another user's token with the
// same agent name gets its own agent and none of the first agent's rooms.
func TestAgentWSIdentityScopedToTokenOwner(t *testing.T) {
	f := newAgentRoom(t)
	f.post(f.alice, "for alice's builder only")
	bob := f.env.NewUser("bobby")
	conn, _ := bob.Token(dto.TokenKindAgent, "laptop").DialAgent("builder")
	require.NotNil(t, conn)
	w := conn.Welcome(wsWait)
	require.NotEqual(t, f.agentID, w.Agent.ID)
	require.Equal(t, "bobby", w.Agent.Owner)
	conn.ExpectNone(300 * time.Millisecond)
	require.False(t, f.env.App.Presence.Online(f.agentID))
}

// AGT-07: acks only move the agent's cursor in rooms it is a member of; an ack for a message
// of another room is ignored and leaves no cursor there.
func TestAgentAckIgnoresForeignRooms(t *testing.T) {
	f := newAgentRoom(t)
	other := createRoom(f.alice, "Elsewhere")
	foreign := testkit.Decode[dto.Message](f.alice.Post("/api/v1/rooms/"+other.ID+"/messages",
		dto.PostMessageRequest{Text: "not for the agent"}).RequireStatus(http.StatusCreated))

	conn := f.dial(t)
	conn.Welcome(wsWait)
	own := f.post(f.alice, "for the agent")
	require.Equal(t, own.ID, conn.Message(wsWait).Message.ID)

	// Acks are handled in order: once the second one moved the cursor, the first was handled.
	conn.Ack(foreign.ID)
	conn.Ack(own.ID)
	require.Eventually(t, func() bool {
		seq, err := f.env.App.Store.R().GetAgentCursor(context.Background(), dbCursorParams(f.agentID, f.room.ID))
		return err == nil && seq == own.Seq
	}, wsWait, 20*time.Millisecond)
	_, err := f.env.App.Store.R().GetAgentCursor(context.Background(), dbCursorParams(f.agentID, other.ID))
	require.ErrorIs(t, err, sql.ErrNoRows, "no cursor in a room the agent is not in")
}

// AGT-08: a live push of a message the backlog already delivered is not sent again (the
// push can race the backlog on connect); newer messages still arrive live.
func TestAgentLivePushSkipsBacklogOverlap(t *testing.T) {
	f := newAgentRoom(t)
	m1, m2 := f.post(f.alice, "one"), f.post(f.alice, "two")
	conn := f.dial(t)
	conn.Welcome(wsWait)
	require.Equal(t, m1.ID, conn.Message(wsWait).Message.ID)
	require.Equal(t, m2.ID, conn.Message(wsWait).Message.ID)

	ref := dto.RoomRef{ID: f.room.ID, Name: f.room.Name}
	for _, m := range []dto.Message{m1, m2} {
		require.True(t, f.env.App.Gateway.Push(f.agentID, dto.AgentMessage{Room: ref, Trusted: true, Message: m}))
	}
	m3 := f.post(f.alice, "three") // pushed live after the two duplicates
	require.Equal(t, m3.ID, conn.Message(wsWait).Message.ID, "the duplicates are skipped")
	conn.ExpectNone(500 * time.Millisecond)
}
