package connectors_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// ID-04: after the owner renames an agent, its outbound signature is the new name; a member
// display name in the room still wins.
func TestOutboundAuthorFollowsAgentRename(t *testing.T) {
	e := newOutboundEnv(t)
	e.alice.Patch("/api/v1/agents/"+e.agentByHandle(t, "helper"), dto.UpdateAgentRequest{DisplayName: ptr("Atlas bot")}).RequireStatus(http.StatusOK)

	m := e.agentPost(t, e.room.ID, "renamed")
	sent := e.inst.WaitSend(t)
	require.Equal(t, m.ID, sent.MessageID)
	require.Equal(t, "Atlas bot", sent.AuthorName)

	e.patchAgentMember(t, e.room.ID, dto.UpdateMemberRequest{DisplayName: ptr("Lolo")})
	m = e.agentPost(t, e.room.ID, "member name")
	sent = e.inst.WaitSend(t)
	require.Equal(t, m.ID, sent.MessageID)
	require.Equal(t, "Lolo", sent.AuthorName)
}

// AID-06, GRP-04: renamed through its profile, by its owner or by the AI itself, an AI signs
// its outbound messages with the new display name; the name set for it in the room still wins.
func TestOutboundAuthorFollowsProfileRename(t *testing.T) {
	e := newOutboundEnv(t)
	e.alice.Patch("/api/v1/intelligences/"+e.agentByHandle(t, "helper"), dto.UpdateProfileRequest{DisplayName: ptr("Orion Bot")}).RequireStatus(http.StatusOK)

	m := e.agentPost(t, e.room.ID, "renamed by the owner")
	sent := e.inst.WaitSend(t)
	require.Equal(t, m.ID, sent.MessageID)
	require.Equal(t, "Orion Bot", sent.AuthorName)

	e.agent.Patch("/api/v1/agent/profile", dto.UpdateProfileRequest{DisplayName: ptr("Orion Prime")}).RequireStatus(http.StatusOK)
	m = e.agentPost(t, e.room.ID, "renamed by the AI itself")
	sent = e.inst.WaitSend(t)
	require.Equal(t, m.ID, sent.MessageID)
	require.Equal(t, "Orion Prime", sent.AuthorName)

	e.patchAgentMember(t, e.room.ID, dto.UpdateMemberRequest{DisplayName: ptr("Lolo")})
	m = e.agentPost(t, e.room.ID, "member name")
	sent = e.inst.WaitSend(t)
	require.Equal(t, m.ID, sent.MessageID)
	require.Equal(t, "Lolo", sent.AuthorName)
}
