package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

func addProject(c *testkit.Client, roomID, projectID string) *testkit.Response {
	return c.Post("/api/v1/rooms/"+roomID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindProject, MemberID: projectID})
}

// PROJ-06: a room manager adds a project they own (kind project, named after the project,
// owned by its owner) or one shared with them (use grant); a project they may not use, or an
// unknown one, is 403; adding the same project twice is 409.
func TestAddProjectMember(t *testing.T) {
	env := testkit.New(t)
	alice, bob, carol := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol")
	_, own := sessionProject(t, env, alice, "atlas")
	room := createRoom(alice, "Ops")

	m := testkit.Decode[dto.RoomMember](addProject(alice, room.ID, own).RequireStatus(http.StatusCreated))
	require.Equal(t, []any{dto.MemberKindProject, own, "Atlas", "alice", true, dto.DeliveryModeAll},
		[]any{m.Kind, m.MemberID, m.Name, m.Owner, m.Outbound, m.Delivery})
	addProject(alice, room.ID, own).RequireError(http.StatusConflict, dto.ErrorCodeConflict)

	_, bobs := sessionProject(t, env, bob, "web")
	_, carols := sessionProject(t, env, carol, "secret")
	bob.Put("/api/v1/projects/"+bobs+"/sharing", dto.Sharing{Mode: dto.VisibilityShared, Users: []dto.SharingUser{
		{UserID: alice.User.ID, Level: dto.LevelUse},
	}}).RequireStatus(http.StatusOK)
	shared := testkit.Decode[dto.RoomMember](addProject(alice, room.ID, bobs).RequireStatus(http.StatusCreated))
	require.Equal(t, []string{"Web", "bob"}, []string{shared.Name, shared.Owner})

	addProject(alice, room.ID, carols).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	addProject(alice, room.ID, "no-such-project").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	members := testkit.Decode[dto.List[dto.RoomMember]](alice.Get("/api/v1/rooms/" + room.ID + "/members").RequireStatus(http.StatusOK)).Items
	require.Len(t, members, 2, "refused projects were not added")
}

// PROJ-06: a room manager who is not the project's owner nor a grantee gets 403, even when
// the project owner can see the room.
func TestAddProjectMemberNeedsUseOnTheProject(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	_, alices := sessionProject(t, env, alice, "atlas")
	room := createRoom(bob, "Bob's room")
	shareRoom(bob, room.ID, dto.SharingUser{UserID: alice.User.ID, Level: dto.LevelView})
	addProject(bob, room.ID, alices).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
}

// PROJ-06 (member settings): the per-room agent settings apply to project members too.
func TestProjectMemberSettings(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	_, project := sessionProject(t, env, alice, "atlas")
	room := createRoom(alice, "Ops")
	m := testkit.Decode[dto.RoomMember](addProject(alice, room.ID, project).RequireStatus(http.StatusCreated))

	got := testkit.Decode[dto.RoomMember](alice.Patch("/api/v1/rooms/"+room.ID+"/members/"+m.ID, dto.UpdateMemberRequest{
		DisplayName: ptr("Atlas bot"), SignOutbound: ptr(false), Delivery: ptr(dto.DeliveryModeMentions),
		Keywords: &[]string{"video"}, Outbound: ptr(false),
	}).RequireStatus(http.StatusOK))
	require.Equal(t, []any{"Atlas bot", false, dto.DeliveryModeMentions, []string{"video"}, false},
		[]any{got.DisplayName, got.SignOutbound, got.Delivery, got.Keywords, got.Outbound})

	e := alice.Patch("/api/v1/rooms/"+room.ID+"/members/"+m.ID, dto.UpdateMemberRequest{Delivery: ptr(dto.DeliveryMode("loud"))}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Equal(t, "must be all, mentions or trusted", e.Fields["delivery"])
}

// PROJ-06: agents see project members in list_rooms (GET /agent/rooms) and room_info.
func TestAgentsSeeProjectMembers(t *testing.T) {
	f := newAgentRoom(t)
	_, project := sessionProject(t, f.env, f.alice, "atlas")
	addProject(f.alice, f.room.ID, project).RequireStatus(http.StatusCreated)
	conn := f.dial(t)
	conn.Welcome(wsWait)
	agent := f.agent.AsAgent("builder")

	want := func(members []dto.RoomMember) {
		t.Helper()
		var found []string
		for _, m := range members {
			if m.Kind == dto.MemberKindProject {
				found = append(found, m.MemberID+"/"+m.Name+"/"+m.Owner)
			}
		}
		require.Equal(t, []string{project + "/Atlas/alice"}, found)
	}
	rooms := testkit.Decode[dto.List[dto.AgentRoom]](agent.Get("/api/v1/agent/rooms").RequireStatus(http.StatusOK)).Items
	require.Len(t, rooms, 1)
	want(rooms[0].Members)
	info := testkit.Decode[dto.RoomInfo](agent.Get("/api/v1/agent/rooms/" + f.room.ID + "/info").RequireStatus(http.StatusOK))
	want(info.Members)
}

// PROJ-06: an unknown member kind is still refused, naming the four kinds.
func TestAddMemberUnknownKind(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	room := createRoom(alice, "Ops")
	e := alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: "team", MemberID: "x"}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Equal(t, "must be agent, chat, user or project", e.Fields["kind"])
}
