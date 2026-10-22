package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// timelineAuthor returns the author name of a message in the room's timeline.
func timelineAuthor(t *testing.T, c *testkit.Client, roomID, messageID string) string {
	t.Helper()
	page := testkit.Decode[dto.Page[dto.Message]](c.Get("/api/v1/rooms/" + roomID + "/messages").RequireStatus(http.StatusOK))
	for _, m := range page.Items {
		if m.ID == messageID {
			return m.AuthorName
		}
	}
	require.Failf(t, "message not in the timeline", "%s", messageID)
	return ""
}

// ID-04: the owner's rename shows everywhere: the agents list, the room's members, the
// timeline (also for older messages), and "@<new name>" mentions reach a mentions-only
// member. A member display name set in the room still wins in that room.
func TestAgentRenameShowsEverywhere(t *testing.T) {
	f := newAgentRoom(t)
	conn := f.dial(t)
	conn.Welcome(wsWait)
	before := f.postAsAgent(t, "before")
	setMember(t, f.alice, f.room.ID, f.agentID, dto.UpdateMemberRequest{Delivery: ptr(dto.DeliveryModeMentions)})

	got := testkit.Decode[dto.Agent](f.alice.Patch("/api/v1/agents/"+f.agentID, dto.UpdateAgentRequest{DisplayName: ptr("  Atlas bot  ")}).RequireStatus(http.StatusOK))
	require.Equal(t, []string{"builder", "Atlas bot"}, []string{got.Intelligence.Handle, got.Intelligence.DisplayName}, "trimmed; the handle stays")
	require.Equal(t, "Atlas bot", listedAgent(t, f.alice, f.agentID).Intelligence.DisplayName)
	require.Equal(t, "Atlas bot", agentMember(t, f.alice, f.room.ID, f.agentID).Name)
	require.Equal(t, "Atlas bot", timelineAuthor(t, f.alice, f.room.ID, before.ID))
	after := f.postAsAgent(t, "after")
	require.Equal(t, "Atlas bot", timelineAuthor(t, f.alice, f.room.ID, after.ID))

	f.post(f.alice, "nobody is named here")
	mention := f.post(f.alice, "ping @atlas bot, please")
	require.Equal(t, mention.ID, conn.Message(wsWait).Message.ID, "only the mention by the new name is pushed")

	setMember(t, f.alice, f.room.ID, f.agentID, dto.UpdateMemberRequest{DisplayName: ptr("Lolo")})
	require.Equal(t, "Lolo", timelineAuthor(t, f.alice, f.room.ID, after.ID), "the room's member display name wins")
}

// ID-04: a display name has 1 to 64 characters after trimming (code points, so 64 two-byte
// letters fit); an invalid PATCH changes none of its fields.
func TestAgentRenameValidation(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	id := registerAgent(t, env, alice, "builder")
	patch := func(req dto.UpdateAgentRequest) *testkit.Response {
		return alice.Patch("/api/v1/agents/"+id, req)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("x", 65)} {
		e := patch(dto.UpdateAgentRequest{DisplayName: ptr(bad), Description: ptr("changed")}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Equal(t, "must have 1 to 64 characters", e.Fields["display_name"], "%q", bad)
	}
	a := listedAgent(t, alice, id)
	require.Equal(t, []string{"builder", ""}, []string{a.Intelligence.DisplayName, a.Description}, "nothing applied")

	long := strings.Repeat("é", 64)
	require.Equal(t, long, testkit.Decode[dto.Agent](patch(dto.UpdateAgentRequest{DisplayName: ptr(long)}).RequireStatus(http.StatusOK)).Intelligence.DisplayName)
}

// ID-05 (clients from before folders): every AI is permanent, so promoting one is accepted and
// changes nothing, and fixed: false is refused.
func TestPromotingAnAgentIsAHarmlessNoOp(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	id := env.NewAI(alice, "builder")

	got := testkit.Decode[dto.Agent](alice.Patch("/api/v1/agents/"+id, dto.UpdateAgentRequest{Fixed: ptr(true)}).RequireStatus(http.StatusOK))
	require.Equal(t, "builder", got.Intelligence.DisplayName)

	e := alice.Patch("/api/v1/agents/"+id, dto.UpdateAgentRequest{Fixed: ptr(false)}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "fixed")
}

// Auth matrix for the agent PATCH fields (ID-04, ID-05): only the owner renames and promotes;
// everyone else gets 403 or 404.
func TestAgentIdentityRoutesAuthMatrix(t *testing.T) {
	m := newMatrix(t)
	agent := func(res matrixResource) string { return "/api/v1/agents/" + res["agent"] }
	m.run(t, agentSetup, []matrixRoute{
		{"rename agent", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Patch(agent(res), dto.UpdateAgentRequest{DisplayName: ptr("Renamed")})
		}, statuses(200, 403, 404, 404, 404)},
		{"promote agent", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Patch(agent(res), dto.UpdateAgentRequest{Fixed: ptr(true)})
		}, statuses(200, 403, 404, 404, 404)},
	})
}

// ID-04: renaming an agent tells live clients that the members of the agent's rooms
// changed (their names did).
func TestRenamesAnnounceMemberChanges(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	id := env.NewAI(alice, "builder")
	room := createRoom(alice, "Ops")
	alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: id}).RequireStatus(http.StatusCreated)
	live, resp := alice.DialLive(env.Origin)
	require.NotNil(t, live, "live refused: %d", resp.Status)

	var ref dto.RoomIDData
	alice.Patch("/api/v1/agents/"+id, dto.UpdateAgentRequest{DisplayName: ptr("Bot")}).RequireStatus(http.StatusOK)
	live.Expect(dto.LiveEventTypeMembersChanged, &ref, liveWait)
	require.Equal(t, room.ID, ref.RoomID)
}
