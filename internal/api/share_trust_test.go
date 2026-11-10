package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/testkit"
)

func refIDs(refs []dto.IntelligenceRef) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.ID
	}
	return out
}

// ST-01, ST-04, ST-05: the members list offers "the same access as me" exactly where the move rule lets the
// viewer put the person in its own position (an admin holding one), the preview says what it gives, and the
// move refuses everyone else: a member without a position, a manager who is not an admin, a guest, and an
// identity of another organization.
func TestShareAccessFlagAndRefusals(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	other := env.OrgRoles("beta")

	// The admin (holding Admin) is offered it for every person but itself; never for the AI.
	got := listMembers(t, r.Admin, r.Org.ID)
	for _, c := range []*testkit.Client{r.Manager, r.Target, r.Peer, r.Member, r.Inviter, r.Guest} {
		require.True(t, got[c.User.ID].CanShareAccess, c.User.Username)
	}
	require.False(t, got[r.Admin.User.ID].CanShareAccess, "not to oneself")
	require.False(t, got[r.TargetAI].CanShareAccess, "not to an AI")
	// Nobody else is offered it.
	for _, viewer := range []*testkit.Client{r.Manager, r.Target, r.Member, r.Guest} {
		for id, m := range listMembers(t, viewer, r.Org.ID) {
			require.False(t, m.CanShareAccess, "%s sees it on %s", viewer.User.Username, id)
		}
	}

	// What it would give the member: everyone below Admin and the AI the manager created, which trusts it.
	preview := testkit.Decode[dto.MovePreview](r.Admin.Get(movePreviewPath(r.Org.ID, r.Member.User.ID) +
		"?position_id=" + r.AdminPosition).RequireStatus(http.StatusOK))
	require.ElementsMatch(t, []string{r.Manager.User.ID, r.Target.User.ID, r.Peer.User.ID, r.TargetAI}, refIDs(preview.WillManage))
	require.Equal(t, []string{r.TargetAI}, refIDs(preview.WillBeTrustedBy))
	require.Equal(t, []dto.IntelligenceRef{}, preview.NotShared, "the admin manages nothing outside its position")
	require.Equal(t, []dto.IntelligenceRef{}, preview.LoseManagement)

	// The refusals of the move into the Admin position.
	path := memberPath(r.Org.ID, r.Inviter.User.ID)
	r.Manager.Patch(path, toPosition(r.AdminPosition)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	r.Manager.Get(movePreviewPath(r.Org.ID, r.Target.User.ID)+"?position_id="+r.ManagerPosition).
		RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	r.Member.Patch(path, toPosition(r.AdminPosition)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	r.Guest.Patch(path, toPosition(r.AdminPosition)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	other.Admin.Patch(path, toPosition(r.AdminPosition)).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	// Another organization's position is not one of this organization.
	r.Admin.Patch(path, toPosition(other.AdminPosition)).RequireStatus(http.StatusUnprocessableEntity)

	// The admin's own move goes through, and the co-holder is not offered it again.
	moved := testkit.Decode[dto.OrgMember](r.Admin.Patch(memberPath(r.Org.ID, r.Member.User.ID),
		toPosition(r.AdminPosition)).RequireStatus(http.StatusOK))
	require.Equal(t, r.AdminPosition, moved.PositionID)
	require.False(t, listMembers(t, r.Admin, r.Org.ID)[r.Member.User.ID].CanShareAccess)
}

// ST-06: the profile of an AI lists who it takes instructions from - its managers in its home organization,
// nearest first - to whoever shares that organization with it, and to nobody else.
func TestProfileListsWhoCanInstructTheAI(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	other := env.OrgRoles("beta")
	// The AI is also a member of beta: beta's admin sees its profile but not acme's chart.
	env.AddMember(other.Org.ID, r.TargetAI, orgs.PermissionMember, "")

	profile := func(c *testkit.Client) dto.IntelligenceProfile {
		t.Helper()
		return testkit.Decode[dto.IntelligenceProfile](c.Get("/api/v1/intelligences/" + r.TargetAI).RequireStatus(http.StatusOK))
	}
	// Its creator (one step), then the holder of the position above the creator's.
	want := []string{r.Manager.User.ID, r.Admin.User.ID}
	for _, c := range []*testkit.Client{r.Admin, r.Manager, r.Member, r.Guest} {
		p := profile(c)
		require.NotNil(t, p.AI.InstructedBy, c.User.Username)
		require.Equal(t, want, refIDs(*p.AI.InstructedBy), c.User.Username)
	}
	require.Nil(t, profile(other.Admin).AI.InstructedBy, "a viewer outside its home organization is not told")

	// A new manager shows up as soon as the chart says so: the member takes the Admin position.
	r.Admin.Patch(memberPath(r.Org.ID, r.Member.User.ID), toPosition(r.AdminPosition)).RequireStatus(http.StatusOK)
	require.ElementsMatch(t, []string{r.Manager.User.ID, r.Admin.User.ID, r.Member.User.ID},
		refIDs(*profile(r.Guest).AI.InstructedBy))
}
