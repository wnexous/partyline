package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store"
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
	require.Equal(t, []string{"builder", "Atlas bot"}, []string{got.Name, got.DisplayName}, "trimmed; the internal name stays")
	require.Equal(t, "Atlas bot", listedAgent(t, f.alice, f.agentID).DisplayName)
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
	require.Equal(t, []string{"builder", ""}, []string{a.DisplayName, a.Description}, "nothing applied")

	long := strings.Repeat("é", 64)
	require.Equal(t, long, testkit.Decode[dto.Agent](patch(dto.UpdateAgentRequest{DisplayName: ptr(long)}).RequireStatus(http.StatusOK)).DisplayName)
}

// ID-05 (clients from before folders): every AI is permanent, so promoting one is accepted and
// changes nothing, and fixed: false is refused.
func TestPromotingAnAgentIsAHarmlessNoOp(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	id := env.NewAI(alice, "builder")

	got := testkit.Decode[dto.Agent](alice.Patch("/api/v1/agents/"+id, dto.UpdateAgentRequest{Fixed: ptr(true)}).RequireStatus(http.StatusOK))
	require.Equal(t, []any{dto.AgentKindFixed, "builder", false}, []any{got.Kind, got.DisplayName, got.Archived})

	e := alice.Patch("/api/v1/agents/"+id, dto.UpdateAgentRequest{Fixed: ptr(false)}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "fixed")
}

// PROJ-05: renaming a project changes its name in the project list and keeps the slug; the
// names of its AIs do not follow it. A name has 1 to 64 characters after trimming.
func TestProjectRename(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	agentID, projectID := sessionProject(t, env, alice, "proj")

	projects := testkit.Decode[dto.List[dto.Project]](alice.Get("/api/v1/projects").RequireStatus(http.StatusOK)).Items
	require.Len(t, projects, 1)
	p := projects[0]
	require.Equal(t, []any{projectID, "proj", "Proj", dto.LevelManage, "alice"}, []any{p.ID, p.Slug, p.Name, p.MyLevel, p.Owner.Username})

	got := testkit.Decode[dto.Project](alice.Patch("/api/v1/projects/"+p.ID, dto.UpdateProjectRequest{Name: "  Atlas  "}).RequireStatus(http.StatusOK))
	require.Equal(t, []string{p.ID, "proj", "Atlas"}, []string{got.ID, got.Slug, got.Name})
	a := listedAgent(t, alice, agentID)
	require.Equal(t, dto.ProjectRef{ID: p.ID, Slug: "proj", Name: "Atlas"}, *a.Project)
	require.Equal(t, "proj-claude", a.DisplayName, "the AI keeps its own name")

	for _, bad := range []string{"", "  ", strings.Repeat("x", 65)} {
		e := alice.Patch("/api/v1/projects/"+p.ID, dto.UpdateProjectRequest{Name: bad}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Equal(t, "must have 1 to 64 characters", e.Fields["name"], "%q", bad)
	}
	require.Equal(t, "Atlas", testkit.Decode[dto.List[dto.Project]](alice.Get("/api/v1/projects").RequireStatus(http.StatusOK)).Items[0].Name)
}

// projectSetup creates a project of the owner shared with the viewer (use grant).
func projectSetup(t *testing.T, m *matrix) matrixResource {
	owner := m.as(t, roleOwner)
	id := createProject(t, m.env, owner, "proj-"+store.NewID(), "Proj")
	owner.Put("/api/v1/projects/"+id+"/sharing", dto.Sharing{Mode: dto.VisibilityShared, Users: []dto.SharingUser{
		{UserID: m.clients[roleViewer].User.ID, Level: dto.LevelUse},
	}}).RequireStatus(http.StatusOK)
	return matrixResource{"project": id}
}

// Auth matrix for the agent PATCH fields (ID-04, ID-05) and the project routes (PROJ-05):
// only the owner renames, promotes and shares; the use grantee reads the project and its
// sharing; everyone else gets 404.
func TestAgentIdentityRoutesAuthMatrix(t *testing.T) {
	m := newMatrix(t)
	agent := func(res matrixResource) string { return "/api/v1/agents/" + res["agent"] }
	project := func(res matrixResource, suffix string) string { return "/api/v1/projects/" + res["project"] + suffix }
	m.run(t, agentSetup, []matrixRoute{
		{"rename agent", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Patch(agent(res), dto.UpdateAgentRequest{DisplayName: ptr("Renamed")})
		}, statuses(200, 403, 404, 404, 404)},
		{"promote agent", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Patch(agent(res), dto.UpdateAgentRequest{Fixed: ptr(true)})
		}, statuses(200, 403, 404, 404, 404)},
	})
	m.run(t, projectSetup, []matrixRoute{
		{"rename project", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Patch(project(res, ""), dto.UpdateProjectRequest{Name: "Renamed"})
		}, statuses(200, 403, 404, 404, 404)},
		{"get project sharing", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Get(project(res, "/sharing"))
		}, statuses(200, 200, 404, 404, 404)},
		{"put project sharing", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Put(project(res, "/sharing"), dto.Sharing{Mode: dto.VisibilityPrivate})
		}, statuses(200, 403, 404, 404, 404)},
	})

	// Listing: only owner and grantee see the project, with their level.
	res := projectSetup(t, m)
	wantLevel := map[matrixRole]dto.Level{roleOwner: dto.LevelManage, roleViewer: dto.LevelUse}
	for _, role := range matrixRoles {
		list := testkit.Decode[dto.List[dto.Project]](m.as(t, role).Get("/api/v1/projects").RequireStatus(http.StatusOK))
		var levels []dto.Level
		for _, p := range list.Items {
			if p.ID == res["project"] {
				levels = append(levels, p.MyLevel)
			}
		}
		if want, ok := wantLevel[role]; ok {
			require.Equal(t, []dto.Level{want}, levels, string(role))
		} else {
			require.Empty(t, levels, string(role))
		}
	}
}

// ID-04, PROJ-05: renaming an agent tells live clients that the members of the agent's rooms
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

// PROJ-05: renaming a project tells the live viewers of every room that has the project as a
// member (kind project) that the room's members changed, so they show the new name; users who
// cannot view the room hear nothing.
func TestProjectRenameRefreshesItsRoomsLive(t *testing.T) {
	env := testkit.New(t)
	alice, bob, carol := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol")
	_, projectID := sessionProject(t, env, alice, "proj")
	room := createRoom(alice, "Ops")
	addProject(alice, room.ID, projectID).RequireStatus(http.StatusCreated)
	shareRoom(alice, room.ID, dto.SharingUser{UserID: bob.User.ID, Level: dto.LevelPost})
	aliceLive, bobLive, carolLive := dialLive(t, alice), dialLive(t, bob), dialLive(t, carol)
	bobLive.ExpectNone(quiet, dto.LiveEventTypeAgentPresence)

	alice.Patch("/api/v1/projects/"+projectID, dto.UpdateProjectRequest{Name: "Atlas"}).RequireStatus(http.StatusOK)
	var ref dto.RoomIDData
	bobLive.Expect(dto.LiveEventTypeMembersChanged, &ref, liveWait)
	require.Equal(t, room.ID, ref.RoomID)
	aliceLive.Expect(dto.LiveEventTypeMembersChanged, &ref, liveWait)
	require.Equal(t, room.ID, ref.RoomID)
	carolLive.ExpectNone(quiet)

	var names []string
	for _, m := range testkit.Decode[dto.List[dto.RoomMember]](bob.Get("/api/v1/rooms/" + room.ID + "/members").RequireStatus(http.StatusOK)).Items {
		if m.Kind == dto.MemberKindProject {
			names = append(names, m.Name)
		}
	}
	require.Equal(t, []string{"Atlas"}, names)
}
