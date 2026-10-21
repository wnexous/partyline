package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// listAgents returns GET /agents of c (with include_archived when archived is set).
func listAgents(t *testing.T, c *testkit.Client, query string) map[string]dto.Agent {
	t.Helper()
	list := testkit.Decode[dto.List[dto.Agent]](c.Get("/api/v1/agents" + query).RequireStatus(http.StatusOK))
	out := map[string]dto.Agent{}
	for _, a := range list.Items {
		out[a.Name] = a
	}
	return out
}

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

	got := testkit.Decode[dto.Agent](f.alice.Patch("/api/v1/agents/"+f.agentID, dto.UpdateAgentRequest{DisplayName: ptr("  Ruthe bot  ")}).RequireStatus(http.StatusOK))
	require.Equal(t, []string{"builder", "Ruthe bot"}, []string{got.Name, got.DisplayName}, "trimmed; the internal name stays")
	require.Equal(t, "Ruthe bot", listedAgent(t, f.alice, f.agentID).DisplayName)
	require.Equal(t, "Ruthe bot", agentMember(t, f.alice, f.room.ID, f.agentID).Name)
	require.Equal(t, "Ruthe bot", timelineAuthor(t, f.alice, f.room.ID, before.ID))
	after := f.postAsAgent(t, "after")
	require.Equal(t, "Ruthe bot", timelineAuthor(t, f.alice, f.room.ID, after.ID))

	f.post(f.alice, "nobody is named here")
	mention := f.post(f.alice, "ping @ruthe bot, please")
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

// PROJ-02, PROJ-03: the daily job archives a temporary agent offline for more than 24 hours,
// never an online one or a fixed one; GET /agents leaves archived agents out unless
// include_archived=true; a reconnect unarchives the agent.
func TestTemporaryAgentArchivedAfterADayOffline(t *testing.T) {
	clock := testkit.NewClock(time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC))
	env := testkit.New(t, testkit.WithClock(clock.Now))
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	ctx := context.Background()

	stale, gone := dialSession(t, tok, sessionHeaders("proj", "claude", "laptop", "", ""))
	_, live := dialSession(t, tok, sessionHeaders("proj", "codex", "laptop", "", ""))
	fixed, named := dialSession(t, tok, sessionHeaders("proj", "pi", "laptop", "keeper", ""))
	stale.Close()
	fixed.Close()
	waitOffline(t, alice, gone.Name)
	waitOffline(t, alice, named.Name)

	clock.Advance(23*time.Hour + 59*time.Minute)
	n, err := env.App.Agents.Archive(ctx)
	require.NoError(t, err)
	require.Zero(t, n, "inside the 24 hours nothing is archived")
	require.Len(t, listAgents(t, alice, ""), 3)

	clock.Advance(2 * time.Minute)
	n, err = env.App.Agents.Archive(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	visible := listAgents(t, alice, "")
	require.NotContains(t, visible, gone.Name, "archived agents are hidden by default")
	require.Equal(t, []bool{false, false}, []bool{visible[live.Name].Archived, visible[named.Name].Archived},
		"an online temporary agent and an offline fixed agent stay")
	all := listAgents(t, alice, "?include_archived=true")
	require.True(t, all[gone.Name].Archived)
	require.Len(t, all, 3)

	_, back := dialSession(t, tok, sessionHeaders("proj", "claude", "laptop", "", ""))
	require.Equal(t, gone.ID, back.ID, "the next session of the project reuses the archived agent")
	requireUnarchived(t, alice, gone.Name, "a reconnect unarchives it")
}

// requireUnarchived asserts that the agent is listed by default and reads archived: false
// with include_archived=true (PROJ-02, PROJ-03), so a still-archived agent cannot pass by
// being absent from the default list.
func requireUnarchived(t *testing.T, c *testkit.Client, name, msg string) {
	t.Helper()
	_, listed := listAgents(t, c, "")[name]
	require.True(t, listed, "%s: listed without include_archived", msg)
	a, ok := listAgents(t, c, "?include_archived=true")[name]
	require.True(t, ok, "%s: listed with include_archived", msg)
	require.False(t, a.Archived, msg)
}

// ID-05: promoting a temporary agent makes it fixed, so it is never archived; fixed: false is
// refused.
func TestPromotedAgentIsNotArchived(t *testing.T) {
	clock := testkit.NewClock(time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC))
	env := testkit.New(t, testkit.WithClock(clock.Now))
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	conn, info := dialSession(t, tok, sessionHeaders("proj", "claude", "laptop", "", ""))
	conn.Close()
	waitOffline(t, alice, info.Name)

	got := testkit.Decode[dto.Agent](alice.Patch("/api/v1/agents/"+info.ID, dto.UpdateAgentRequest{Fixed: ptr(true)}).RequireStatus(http.StatusOK))
	require.Equal(t, []any{dto.AgentKindFixed, "Proj · Claude Code"}, []any{got.Kind, got.DisplayName}, "the readable name stays")
	clock.Advance(48 * time.Hour)
	n, err := env.App.Agents.Archive(context.Background())
	require.NoError(t, err)
	require.Zero(t, n)
	require.False(t, listAgents(t, alice, "?include_archived=true")[info.Name].Archived)

	e := alice.Patch("/api/v1/agents/"+info.ID, dto.UpdateAgentRequest{Fixed: ptr(false)}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "fixed")
}

// ID-05: promoting an archived agent unarchives it.
func TestPromotingAnArchivedAgentUnarchivesIt(t *testing.T) {
	clock := testkit.NewClock(time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC))
	env := testkit.New(t, testkit.WithClock(clock.Now))
	alice := env.NewUser("alice")
	conn, info := dialSession(t, alice.Token(dto.TokenKindAgent, "laptop"), sessionHeaders("proj", "claude", "laptop", "", ""))
	conn.Close()
	waitOffline(t, alice, info.Name)
	clock.Advance(25 * time.Hour)
	_, err := env.App.Agents.Archive(context.Background())
	require.NoError(t, err)
	require.True(t, listAgents(t, alice, "?include_archived=true")[info.Name].Archived)

	got := testkit.Decode[dto.Agent](alice.Patch("/api/v1/agents/"+info.ID, dto.UpdateAgentRequest{Fixed: ptr(true)}).RequireStatus(http.StatusOK))
	require.Equal(t, []any{dto.AgentKindFixed, false}, []any{got.Kind, got.Archived}, "the PATCH answer")
	requireUnarchived(t, alice, info.Name, "promotion clears archived_at")
}

// PROJ-05: renaming a project changes its name everywhere (the project list, its temporary
// agents' readable names, the timeline) and keeps the slug; later sessions keep the new name.
// A name has 1 to 64 characters after trimming.
func TestProjectRename(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	_, one := dialSession(t, tok, sessionHeaders("proj", "claude", "laptop", "", ""))
	_, two := dialSession(t, tok, sessionHeaders("proj", "claude", "laptop", "", ""))
	room := createRoom(alice, "Ops")
	alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: one.ID}).RequireStatus(http.StatusCreated)
	msg := testkit.Decode[dto.Message](tok.WithHeader(dto.HeaderAgent, one.Name).
		Post("/api/v1/agent/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "hi"}).RequireStatus(http.StatusCreated))
	require.Equal(t, "Proj · Claude Code", msg.AuthorName)

	projects := testkit.Decode[dto.List[dto.Project]](alice.Get("/api/v1/projects").RequireStatus(http.StatusOK)).Items
	require.Len(t, projects, 1)
	p := projects[0]
	require.Equal(t, []any{"proj", "Proj", dto.LevelManage, "alice"}, []any{p.Slug, p.Name, p.MyLevel, p.Owner.Username})

	got := testkit.Decode[dto.Project](alice.Patch("/api/v1/projects/"+p.ID, dto.UpdateProjectRequest{Name: "  Ruthe  "}).RequireStatus(http.StatusOK))
	require.Equal(t, []string{p.ID, "proj", "Ruthe"}, []string{got.ID, got.Slug, got.Name})
	agents := listAgents(t, alice, "")
	require.Equal(t, []string{"Ruthe · Claude Code", "Ruthe · Claude Code #2"}, []string{agents[one.Name].DisplayName, agents[two.Name].DisplayName})
	require.Equal(t, dto.ProjectRef{ID: p.ID, Slug: "proj", Name: "Ruthe"}, *agents[one.Name].Project)
	require.Equal(t, "Ruthe · Claude Code", timelineAuthor(t, alice, room.ID, msg.ID))

	_, three := dialSession(t, tok, sessionHeaders("proj", "claude", "laptop", "", ""))
	require.Equal(t, "Ruthe · Claude Code #3", three.DisplayName, "a session does not rename the project back")

	for _, bad := range []string{"", "  ", strings.Repeat("x", 65)} {
		e := alice.Patch("/api/v1/projects/"+p.ID, dto.UpdateProjectRequest{Name: bad}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Equal(t, "must have 1 to 64 characters", e.Fields["name"], "%q", bad)
	}
	require.Equal(t, "Ruthe", testkit.Decode[dto.List[dto.Project]](alice.Get("/api/v1/projects").RequireStatus(http.StatusOK)).Items[0].Name)
}

// projectSetup creates a project of the owner shared with the viewer (use grant).
func projectSetup(t *testing.T, m *matrix) matrixResource {
	owner := m.as(t, roleOwner)
	ctx := context.Background()
	var id string
	require.NoError(t, m.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		id = store.NewID()
		return q.CreateProjectIfMissing(ctx, db.CreateProjectIfMissingParams{ID: id, OwnerID: owner.User.ID, Slug: "proj-" + id, Name: "Proj", CreatedAt: 1})
	}))
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

// ID-04, PROJ-05: renaming an agent, or the project of a temporary agent, tells live clients
// that the members of the agent's rooms changed (their names did).
func TestRenamesAnnounceMemberChanges(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	_, info := dialSession(t, tok, sessionHeaders("proj", "claude", "laptop", "", ""))
	room := createRoom(alice, "Ops")
	alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: info.ID}).RequireStatus(http.StatusCreated)
	live, resp := alice.DialLive(env.Origin)
	require.NotNil(t, live, "live refused: %d", resp.Status)

	var ref dto.RoomIDData
	alice.Patch("/api/v1/agents/"+info.ID, dto.UpdateAgentRequest{DisplayName: ptr("Bot")}).RequireStatus(http.StatusOK)
	live.Expect(dto.LiveEventTypeMembersChanged, &ref, liveWait)
	require.Equal(t, room.ID, ref.RoomID)

	project := listedAgent(t, alice, info.ID).Project
	alice.Patch("/api/v1/projects/"+project.ID, dto.UpdateProjectRequest{Name: "Ruthe"}).RequireStatus(http.StatusOK)
	live.Expect(dto.LiveEventTypeMembersChanged, &ref, liveWait)
	require.Equal(t, room.ID, ref.RoomID)
}
