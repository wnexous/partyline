package agents_test

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// AGT-09, S6: a message posted between the backlog count and the backlog list does not
// change the skipped count: the notice counts exactly the messages before the first
// delivered one, the cursor jumps to just before it, and the new message arrives live after
// the backlog.
func TestBacklogSkippedCountIgnoresConcurrentPost(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	ctx := context.Background()
	agent, err := env.App.Agents.Upsert(ctx, alice.User.ID, "builder")
	require.NoError(t, err)
	room := testkit.Decode[dto.Room](alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "Support"}).RequireStatus(http.StatusCreated))
	alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: agent.ID}).RequireStatus(http.StatusCreated)

	p := authn.Principal{Kind: authn.KindUser, UserID: alice.User.ID, Role: string(dto.RoleMember)}
	post := func(text string) rooms.Message {
		m, err := env.App.Rooms.Post(ctx, p, room.ID, rooms.PostInput{Text: text})
		require.NoError(t, err)
		return m
	}
	var posted []rooms.Message
	for i := 1; i <= 60; i++ {
		posted = append(posted, post(fmt.Sprintf("message %d", i)))
	}
	var late rooms.Message
	var once sync.Once
	agents.SetAfterBacklogCount(env.App.Gateway, func() { once.Do(func() { late = post("posted during the backlog") }) })

	conn, resp := alice.Token(dto.TokenKindAgent, "laptop").DialAgent("builder")
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	conn.Welcome(time.Minute)
	require.Equal(t, "10 older messages skipped; use the history tool", conn.Notice(time.Minute).Text)
	for _, m := range posted[10:] {
		require.Equal(t, m.ID, conn.Message(time.Minute).Message.ID)
	}
	require.NotEmpty(t, late.ID, "the hook posted between the count and the list")
	require.Equal(t, late.ID, conn.Message(time.Minute).Message.ID, "the concurrent post arrives live after the backlog")
	seq, err := env.App.Store.R().GetAgentCursor(ctx, db.GetAgentCursorParams{AgentID: agent.ID, RoomID: room.ID})
	require.NoError(t, err)
	require.Equal(t, posted[9].Seq, seq)
}
