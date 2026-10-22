package agents_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/events"
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

	err = conn.Closed(5 * time.Second)
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
