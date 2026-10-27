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

// SESS-24: a fallen-back message is delivered by the principal's backlog even when its sequence is
// below the cursor, with its note; the returned sent map still suppresses a late live duplicate.
func TestBacklogDeliversAFallbackBelowTheCursorOnce(t *testing.T) {
	f := newSessionFixture(t)
	ctx := context.Background()
	first, parallel := f.dial(t), f.dial(t)
	f.settle(t, first)
	f.settle(t, parallel)
	first.Close()
	f.waitPrincipalGone(t)

	// The label has no session: the row is born fallen back while the principal is offline.
	fallback := f.post(t, "@atlas:wt one")
	f.waitRow(t, "wt", fallback.ID)

	principal := f.dial(t)
	got := principal.Message(time.Minute)
	require.Equal(t, fallback.ID, got.Message.ID)
	require.Equal(t, "wt", got.ForLabel)

	later := f.post(t, "two")
	require.Equal(t, later.ID, principal.Message(time.Minute).Message.ID)
	principal.Ack(later.ID)
	require.Eventually(t, func() bool {
		seq, err := f.env.App.Store.R().GetAgentCursor(ctx, db.GetAgentCursorParams{AgentID: f.agentID, RoomID: f.room.ID})
		return err == nil && seq == later.Seq
	}, time.Minute, 10*time.Millisecond, "the later acknowledgement moved the cursor past the fallback")

	principal.Close()
	f.waitPrincipalGone(t)
	again := f.dial(t)
	backlogged := again.Message(time.Minute)
	require.Equal(t, fallback.ID, backlogged.Message.ID, "the fallback is below the cursor and still delivered")
	require.Equal(t, "wt", backlogged.ForLabel)

	// A late live push of the same message is dropped by the sent map the backlog returned.
	late, err := f.env.App.Gateway.Message(ctx, fallback.ID)
	require.NoError(t, err)
	require.True(t, f.env.App.Gateway.Push(f.agentID, "", dto.AgentMessage{Room: got.Room, Message: late}))
	again.ExpectNone(500 * time.Millisecond)
}

// SESS-24: a delivery row still owned by a live label is not part of the principal's backlog.
func TestBacklogDoesNotReadAPendingLabelsMessage(t *testing.T) {
	f := newSessionFixture(t)
	principal, parallel := f.dial(t), f.dial(t)
	f.settle(t, principal)
	f.settle(t, parallel)
	m := f.post(t, "@atlas:claude-2 hello")
	require.Equal(t, m.ID, parallel.Message(time.Minute).Message.ID)
	f.waitRow(t, "claude-2", m.ID)

	principal.Close()
	f.waitPrincipalGone(t)
	again := f.dial(t)
	again.ExpectNone(500 * time.Millisecond)
	require.Len(t, f.rows(t, "claude-2"), 1)
}

// AGT-09, S6: a message posted between the backlog count and the backlog list does not
// change the skipped count: the notice counts exactly the messages before the first
// delivered one, the cursor jumps to just before it, and the new message arrives live after
// the backlog.
func TestBacklogSkippedCountIgnoresConcurrentPost(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	ctx := context.Background()
	agentID := env.NewAI(alice, "builder")
	room := testkit.Decode[dto.Room](alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "Support"}).RequireStatus(http.StatusCreated))
	alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: agentID}).RequireStatus(http.StatusCreated)

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
	seq, err := env.App.Store.R().GetAgentCursor(ctx, db.GetAgentCursorParams{AgentID: agentID, RoomID: room.ID})
	require.NoError(t, err)
	require.Equal(t, posted[9].Seq, seq)
}
