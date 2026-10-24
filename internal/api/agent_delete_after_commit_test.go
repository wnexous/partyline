package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// failingSession is a live session whose socket fails when the server ends it: what it does is beyond the
// server's control, and the server ends it right after the AI's row is deleted.
type failingSession struct{}

func (failingSession) Close(websocket.StatusCode, string) { panic("the socket failed to close") }

// A delete whose row is gone answers 204, whatever happens after the commit (the Members page showed an
// error for an AI that was deleted, and the retry then found nothing: 404). Here the live session of the AI
// fails when it is ended, which happens right after the commit: the request must not turn into a 500, the AI
// is deleted with its DM and its presence, and the next AIs are deleted as usual.
func TestDeletingAnAIAnswers204WhateverItsSessionDoesAfterTheCommit(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	builder, helper, reviewer := env.NewAI(alice, "builder"), env.NewAI(alice, "helper"), env.NewAI(alice, "reviewer")
	alice.ShareAI(builder, bob)
	dm := dmWith(bob, builder, http.StatusCreated)
	aliceLive := dialLive(t, alice)

	agent, err := env.App.Store.R().GetAgent(ctx, builder)
	require.NoError(t, err)
	_, err = env.App.Presence.Attach(ctx, agent, failingSession{}, "process-one", false)
	require.NoError(t, err)
	require.True(t, env.App.Presence.Online(builder))

	alice.Delete(agentDeletePath(builder)).RequireStatus(http.StatusNoContent)
	requireDeletedForGood(t, env, builder)
	require.Zero(t, countRows(t, env, `SELECT COUNT(*) FROM rooms WHERE id = ?`, dm.ID), "its DM went with it")
	require.False(t, env.App.Presence.Online(builder), "and it is no longer online")
	bob.Get("/api/v1/rooms/"+dm.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	// The clients were told in spite of the failed session: the AI went offline, after the frame that said it
	// came online.
	var presence dto.AgentPresence
	for presence.Online || presence.AgentID == "" {
		aliceLive.Expect(dto.LiveEventTypeAgentPresence, &presence, liveWait)
	}
	require.Equal(t, dto.AgentPresence{AgentID: builder, Online: false}, presence)

	// The retry the user makes after an error finds nothing, and the AIs after it are deleted as usual.
	alice.Delete(agentDeletePath(builder)).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	alice.Delete(agentDeletePath(helper)).RequireStatus(http.StatusNoContent)
	alice.Delete(memberPath(alice.PersonalOrg().ID, reviewer)).RequireStatus(http.StatusNoContent)
	requireDeletedForGood(t, env, helper)
	requireDeletedForGood(t, env, reviewer)
}
