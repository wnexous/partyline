package agents_test

import (
	"context"
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

// SES-04: a connection that passed the check before the upgrade but lost the race to another
// process is closed with 4003 identity_busy. The session that won stays live and keeps its app,
// and nothing is announced.
func TestGatewayClosesAConnectionThatLostTheRace(t *testing.T) {
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
			_, err := env.App.Presence.Attach(ctx, agent, winner, "winning-process-id", false)
			require.NoError(t, err)
		})
	})
	sub, cancel := env.App.Bus.Subscribe(64)
	defer cancel()

	f := testkit.FolderOf("builder")
	f.Harness = "opencode"
	conn, resp := alice.Token(dto.TokenKindAgent, "laptop").DialFolder(f)
	require.NotNil(t, conn, "the check before the upgrade passed: %d %s", resp.Status, resp.Body)

	err = conn.Closed(30 * time.Second)
	var closeErr websocket.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, []any{agents.CloseBusy, "identity_busy"}, []any{closeErr.Code, closeErr.Reason})
	require.Equal(t, websocket.StatusCode(4003), agents.CloseBusy)

	env.App.Gateway.Close() // waits for the loser's handler to finish
	require.True(t, env.App.Presence.Online(id), "the winner stays live")
	require.Zero(t, winner.closes())
	after, err := env.App.Agents.ByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "claude", after.Harness, "the loser did not relabel the AI")

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
	require.Equal(t, []events.AgentPresence{{AgentID: id, OwnerID: alice.User.ID, Online: true}}, announced, "only the winner was announced")
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
	require.True(t, env.App.Gateway.Push(agentID, dto.AgentMessage{Room: dto.RoomRef{ID: room.ID, Name: "Support"}, Message: late}), "queued for the new session")
	taker.ExpectNone(500 * time.Millisecond)

	after := post("after the takeover")
	require.Equal(t, after.ID, taker.Message(time.Minute).Message.ID, "the next message arrives")
}
