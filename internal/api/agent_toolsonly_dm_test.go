package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// DM-05, TURN-01: an AI whose session holds no socket (a tools-only harness such as Antigravity)
// talks to an AI with a live principal in a DM: the message it writes - the first one and the reply
// to a message it was sent - reaches the principal over its socket,
// tools-only AI at its next pending read.
func TestToolsOnlyAIsDMReachesTheLivePrincipal(t *testing.T) {
	f := newAIEnv(t)
	agy := testkit.Folder{Machine: testkit.Machine, Path: "/work/agy", Harness: "antigravity"}
	agyTok := f.aliceTok.AsFolder(agy)
	nexous, resp := f.aliceTok.DialAgent("nexous")
	require.NotNil(t, nexous, "upgrade refused: %d %s", resp.Status, resp.Body)
	nexousID := nexous.Welcome(wsWait).Agent.ID

	first := testkit.Decode[dto.Message](sendDM(agyTok, nexousID, "hello from agy").RequireStatus(http.StatusCreated))
	got := nexous.Message(wsWait)
	require.Equal(t, first.ID, got.Message.ID, "the tools-only AI's DM reaches the principal's socket")
	require.Equal(t, dto.ConversationKindDM, got.Room.Kind)
	nexous.Ack(first.ID)

	// The principal answers; the tools-only AI reads it at its next pending call.
	answer := testkit.Decode[dto.Message](f.aliceTok.AsAgent("nexous").
		Post("/api/v1/agent/messages/"+first.ID+"/reply", dto.ReplyRequest{Text: "welcome"}).RequireStatus(http.StatusCreated))
	pending := testkit.Decode[dto.List[dto.AgentMessage]](agyTok.Get("/api/v1/agent/pending").RequireStatus(http.StatusOK))
	require.Len(t, pending.Items, 1)
	require.Equal(t, answer.ID, pending.Items[0].Message.ID)

	// The tools-only AI replies to that message with the reply tool; the principal hears it.
	reply := testkit.Decode[dto.Message](agyTok.
		Post("/api/v1/agent/messages/"+answer.ID+"/reply", dto.ReplyRequest{Text: "thanks"}).RequireStatus(http.StatusCreated))
	back := nexous.Message(wsWait)
	require.Equal(t, reply.ID, back.Message.ID, "the reply reaches the principal")
}

// SESS-23: when the AI's principal has left and only a parallel session is live, a DM from a tools-only AI is
// not pushed to that parallel session (it is the principal's message); it waits in the principal's queue and
// the next principal receives it. This is how a reply can be "only in history" for a person whose open session
// is not the principal.
func TestToolsOnlyAIsDMWaitsForThePrincipalWhenOnlyAParallelSessionIsLive(t *testing.T) {
	f := newAIEnv(t)
	agyTok := f.aliceTok.AsFolder(testkit.Folder{Machine: testkit.Machine, Path: "/work/agy", Harness: "antigravity"})
	principal, resp := f.aliceTok.DialAgent("nexous")
	require.NotNil(t, principal, "upgrade refused: %d %s", resp.Status, resp.Body)
	nexousID := principal.Welcome(wsWait).Agent.ID
	parallel, resp := f.aliceTok.DialAgent("nexous")
	require.NotNil(t, parallel, "upgrade refused: %d %s", resp.Status, resp.Body)
	require.NotEmpty(t, parallel.Welcome(wsWait).Session.Label, "the second process is a labelled session")
	principal.Close()
	require.Eventually(t, func() bool {
		s := testkit.Decode[dto.SessionState](f.aliceTok.AsAgent("nexous").Get("/api/v1/agent/session").RequireStatus(http.StatusOK))
		return len(s.Sessions) == 1 && s.Sessions[0].Label != ""
	}, wsWait, 20*time.Millisecond, "only the parallel session is left")

	dm := testkit.Decode[dto.Message](sendDM(agyTok, nexousID, "are you there?").RequireStatus(http.StatusCreated))
	parallel.ExpectNone(300 * time.Millisecond)

	again, resp := f.aliceTok.DialAgent("nexous")
	require.NotNil(t, again, "upgrade refused: %d %s", resp.Status, resp.Body)
	require.Empty(t, again.Welcome(wsWait).Session.Label, "the principal label is free again")
	require.Equal(t, dm.ID, again.Message(wsWait).Message.ID, "the waiting DM is the new principal's")
}
