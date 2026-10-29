package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// DM-05: an AI whose session holds no socket (a tools-only call, no WebSocket) talks to an AI with
// a live principal in a DM: the message it writes reaches the principal over its socket. The pull half
// of this test (the principal's answer read at the tools-only AI's next pending call) went with
// GET /agent/pending (AD-035): with no socket an AI receives nothing.
func TestToolsOnlyAIsDMReachesTheLivePrincipal(t *testing.T) {
	f := newAIEnv(t)
	tools := testkit.Folder{Machine: testkit.Machine, Path: "/work/tools", Harness: "omp"}
	toolsTok := f.aliceTok.AsFolder(tools)
	atlas, resp := f.aliceTok.DialAgent("atlas")
	require.NotNil(t, atlas, "upgrade refused: %d %s", resp.Status, resp.Body)
	atlasID := atlas.Welcome(wsWait).Agent.ID

	first := testkit.Decode[dto.Message](sendDM(toolsTok, atlasID, "hello from tools").RequireStatus(http.StatusCreated))
	got := atlas.Message(wsWait)
	require.Equal(t, first.ID, got.Message.ID, "the tools-only AI's DM reaches the principal's socket")
	require.Equal(t, dto.ConversationKindDM, got.Room.Kind)
	atlas.Ack(first.ID)
}

// SESS-23: when the AI's principal has left and only a parallel session is live, a DM from a tools-only AI is
// not pushed to that parallel session (it is the principal's message); it waits in the principal's queue and
// the next principal receives it. This is how a reply can be "only in history" for a person whose open session
// is not the principal.
func TestToolsOnlyAIsDMWaitsForThePrincipalWhenOnlyAParallelSessionIsLive(t *testing.T) {
	f := newAIEnv(t)
	toolsTok := f.aliceTok.AsFolder(testkit.Folder{Machine: testkit.Machine, Path: "/work/tools", Harness: "omp"})
	principal, resp := f.aliceTok.DialAgent("atlas")
	require.NotNil(t, principal, "upgrade refused: %d %s", resp.Status, resp.Body)
	atlasID := principal.Welcome(wsWait).Agent.ID
	parallel, resp := f.aliceTok.DialAgent("atlas")
	require.NotNil(t, parallel, "upgrade refused: %d %s", resp.Status, resp.Body)
	require.NotEmpty(t, parallel.Welcome(wsWait).Session.Label, "the second process is a labelled session")
	principal.Close()
	require.Eventually(t, func() bool {
		s := testkit.Decode[dto.SessionState](f.aliceTok.AsAgent("atlas").Get("/api/v1/agent/session").RequireStatus(http.StatusOK))
		return len(s.Sessions) == 1 && s.Sessions[0].Label != ""
	}, wsWait, 20*time.Millisecond, "only the parallel session is left")

	dm := testkit.Decode[dto.Message](sendDM(toolsTok, atlasID, "are you there?").RequireStatus(http.StatusCreated))
	parallel.ExpectNone(300 * time.Millisecond)

	again, resp := f.aliceTok.DialAgent("atlas")
	require.NotNil(t, again, "upgrade refused: %d %s", resp.Status, resp.Body)
	require.Empty(t, again.Welcome(wsWait).Session.Label, "the principal label is free again")
	require.Equal(t, dm.ID, again.Message(wsWait).Message.ID, "the waiting DM is the new principal's")
}
