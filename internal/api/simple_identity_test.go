package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/testkit"
)

// SI-01, SI-02, SI-03: two AIs of the same host see each other although they share no organization: each opens
// a DM with the other, by handle and by id, and the message arrives untrusted, because seeing is not managing.
// Adding the other one to a group still needs membership of the group's organization, and an AI of another host
// in the second organization stays out of reach.
func TestAIsOfTheSameHostSeeEachOther(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	globex := env.OrgRoles("globex")
	env.AddMember(globex.Org.ID, alice.User.ID, orgs.PermissionMember, "")
	atlas := env.NewAI(alice, "atlas")                    // home: alice's personal organization
	helper := env.NewAIIn(alice, globex.Org.ID, "helper") // home: globex, which atlas does not belong to
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	atlasAI, helperAI := tok.AsAgent("atlas"), tok.AsAgent("helper")
	atlasHandle, helperHandle := "@"+profileOf(t, alice, atlas).Handle, "@"+profileOf(t, alice, helper).Handle

	live, resp := tok.DialAgent("helper")
	require.NotNil(t, live, resp)
	defer live.Close()
	live.Welcome(wsWait)

	// SI-01: each reaches the other, by handle and by id.
	sent := testkit.Decode[dto.Message](sendDM(atlasAI, helperHandle, "the build is green").RequireStatus(http.StatusCreated))
	sendDM(atlasAI, helper, "by id").RequireStatus(http.StatusCreated)
	back := testkit.Decode[dto.Message](sendDM(helperAI, atlasHandle, "thanks").RequireStatus(http.StatusCreated))
	require.Equal(t, sent.RoomID, back.RoomID, "one DM between the two")

	// SI-03: seeing is not trust - the message is not marked trusted for helper.
	got := live.Message(wsWait)
	require.Equal(t, sent.ID, got.Message.ID)
	require.False(t, got.Trusted, "an AI of the same host is not a manager")

	// SI-02: a group of atlas's organization still refuses helper, which is not a member of it.
	e := agentGroup(atlasAI, "Ops", helper).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeMembersRefused)
	require.Equal(t, []dto.RefusedMember{{ID: helper, Reason: dto.RefusedReasonNotOrgMember}}, e.Refused)

	// The same-host rule reaches no further: globex's AI of another host stays hidden from atlas.
	sendDM(atlasAI, globex.TargetAI, "psst").RequireError(http.StatusNotFound, dto.ErrorCodeContactNotFound)
}
