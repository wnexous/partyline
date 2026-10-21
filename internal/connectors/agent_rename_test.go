package connectors_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// ID-04: after the owner renames an agent, its outbound signature is the new name; a member
// display name in the room still wins.
func TestOutboundAuthorFollowsAgentRename(t *testing.T) {
	e := newOutboundEnv(t)
	agents := testkit.Decode[dto.List[dto.Agent]](e.alice.Get("/api/v1/agents").RequireStatus(http.StatusOK)).Items
	require.Len(t, agents, 1)
	e.alice.Patch("/api/v1/agents/"+agents[0].ID, dto.UpdateAgentRequest{DisplayName: ptr("Ruthe bot")}).RequireStatus(http.StatusOK)

	m := e.agentPost(t, e.room.ID, "renamed")
	sent := e.inst.WaitSend(t)
	require.Equal(t, m.ID, sent.MessageID)
	require.Equal(t, "Ruthe bot", sent.AuthorName)

	e.patchAgentMember(t, e.room.ID, dto.UpdateMemberRequest{DisplayName: ptr("Lolo")})
	m = e.agentPost(t, e.room.ID, "member name")
	sent = e.inst.WaitSend(t)
	require.Equal(t, m.ID, sent.MessageID)
	require.Equal(t, "Lolo", sent.AuthorName)
}
