package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// AD-016, ROOM-01: a member of a group takes their own user row out to leave, without manage, and
// the group leaves their conversations; taking anyone else's row out still needs manage, a
// stranger still cannot tell the group exists, and a manager's removal is unchanged.
func TestMemberLeavesAGroup(t *testing.T) {
	env := testkit.New(t)
	alice, bob, carol := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol")
	dave := env.NewUser("dave") // of no organization of the group's: a stranger
	room := createRoom(alice, "deploys")
	path := "/api/v1/rooms/" + room.ID
	bobMember := addToRoom(t, env, alice, room.ID, bob)
	carolMember := addToRoom(t, env, alice, room.ID, carol)

	// A member takes nobody else out.
	carol.Delete(path+"/members/"+bobMember.ID).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	require.Contains(t, membersOf(t, bob, room.ID), bob.User.ID)
	// A stranger gets the 404 of a room that does not exist for them.
	dave.Delete(path+"/members/"+bobMember.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	// The manager removing someone else is unchanged.
	alice.Delete(path + "/members/" + carolMember.ID).RequireStatus(http.StatusNoContent)
	carol.Get(path).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	// The member leaves: 204, the usual events, and the group is gone from their conversations.
	bobLive := dialLive(t, bob)
	bob.Delete(path + "/members/" + bobMember.ID).RequireStatus(http.StatusNoContent)
	var gone dto.RoomIDData
	bobLive.Expect(dto.LiveEventTypeRoomDeleted, &gone, liveWait)
	require.Equal(t, room.ID, gone.RoomID)
	bob.Get(path).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Empty(t, testkit.Decode[dto.List[dto.Conversation]](bob.Get("/api/v1/conversations").RequireStatus(http.StatusOK)).Items)
	// Leaving again finds no row of theirs to take out: they no longer see the room.
	bob.Delete(path+"/members/"+bobMember.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	// The group stays for its manager.
	require.Equal(t, "deploys", testkit.Decode[dto.Room](alice.Get(path).RequireStatus(http.StatusOK)).Name)
}

// AD-016: a member who is also the group's only manager may leave; the group keeps its
// organization admins, and managing does not come from the member row.
func TestManagerTakesTheirOwnRowOutOfAGroup(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	room := createRoom(alice, "solo")
	path := "/api/v1/rooms/" + room.ID
	own := testkit.Decode[dto.RoomMember](alice.Post(path+"/members",
		dto.AddMemberRequest{Kind: dto.MemberKindUser, MemberID: alice.User.ID}).RequireStatus(http.StatusCreated))

	alice.Delete(path + "/members/" + own.ID).RequireStatus(http.StatusNoContent)

	got := testkit.Decode[dto.Room](alice.Get(path).RequireStatus(http.StatusOK))
	require.Equal(t, dto.LevelManage, got.MyLevel)
	alice.Patch(path, dto.UpdateRoomRequest{Name: ptr("renamed")}).RequireStatus(http.StatusOK)
}

// DM-02: a participant's own row is not theirs to take out either: a DM has exactly its two
// members, so leaving it this way keeps the refusal it always had.
func TestParticipantCannotLeaveADM(t *testing.T) {
	env := testkit.New(t)
	owner, alice := env.NewUser("owner"), env.NewUser("alice")
	atlas := env.NewAI(owner, "atlas")
	shareAI(owner, atlas, alice)
	room := dmWith(alice, atlas, http.StatusCreated)
	path := "/api/v1/rooms/" + room.ID

	own := membersOf(t, alice, room.ID)[alice.User.ID]
	alice.Delete(path+"/members/"+own.ID).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeDMMembersFixed)
	require.Contains(t, membersOf(t, alice, room.ID), alice.User.ID)
}
