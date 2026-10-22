package conversations_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/conversations"
	"github.com/wnexous/partyline/internal/store"
)

func (f *fixture) shareOrion() {
	f.bob.Put("/api/v1/agents/"+f.orion+"/sharing", dto.Sharing{
		Mode: dto.VisibilityShared, Users: []dto.SharingUser{{UserID: f.alice.User.ID, Level: dto.LevelUse}},
	}).RequireStatus(http.StatusOK)
}

// AGRP-01 (service side): an AI creates a group with its owner and another AI its owner may use;
// the group belongs to the owner, the AI itself is a member, and the AI sees it at post level.
func TestAICreatesAGroupOwnedByItsOwner(t *testing.T) {
	f := newFixture(t)
	f.allowed = 10
	f.shareOrion()
	ctx := context.Background()
	atlas := f.ai(f.alice, f.atlas)

	room, err := f.svc.CreateGroup(ctx, atlas, conversations.GroupInput{
		Name: "Ops", Description: "coordination", Members: []string{f.alice.User.ID, f.orion, f.atlas},
	})
	require.NoError(t, err)
	require.Equal(t, f.alice.User.ID, room.OwnerID)
	require.Equal(t, "Ops", room.Name)
	require.Equal(t, "coordination", room.Description)
	require.Equal(t, dto.ConversationKindGroup, dto.ConversationKind(room.Kind))
	require.Equal(t, dto.LevelPost, room.Level, "the AI posts as a member")
	require.EqualValues(t, 3, room.MemberCount, "the AI once, even though it listed itself")

	members, err := f.env.App.Rooms.Members(ctx, f.user(f.alice), room.ID)
	require.NoError(t, err)
	kinds := map[string]string{}
	for _, m := range members {
		kinds[m.MemberID] = m.Kind
	}
	require.Equal(t, map[string]string{f.atlas: "agent", f.orion: "agent", f.alice.User.ID: "user"}, kinds)
	require.Equal(t, f.atlas, members[0].MemberID, "the creating AI is the first member")

	// The owner manages it and sees it listed.
	seen := f.alice.Get("/api/v1/rooms/" + room.ID).RequireStatus(http.StatusOK)
	require.Contains(t, string(seen.Body), `"my_level":"manage"`)
	require.Equal(t, 1, f.asked(), "one new conversation")
}

func (f *fixture) asked() int { return len(f.askedIDs) }

// AGRP-02: a member the owner may not add refuses the AI's group with the whole list and creates
// nothing; a refused request does not use up the AI's allowance.
func TestAIGroupWithARefusedMemberCreatesNothing(t *testing.T) {
	f := newFixture(t)
	f.allowed = 10
	ctx := context.Background()
	atlas := f.ai(f.alice, f.atlas)

	_, err := f.svc.CreateGroup(ctx, atlas, conversations.GroupInput{Name: "Ops", Members: []string{f.alice.User.ID, f.orion}})
	var refused *conversations.MembersRefusedError
	require.True(t, errors.As(err, &refused), "got %v", err)
	require.Equal(t, []dto.RefusedMember{{ID: f.orion, Reason: dto.RefusedReasonNotFound}}, refused.Refused, "bob's AI is not shared with alice")
	rooms, err := f.env.App.Rooms.List(ctx, f.user(f.alice))
	require.NoError(t, err)
	require.Empty(t, rooms)
	require.Zero(t, f.asked(), "nothing was created, so nothing was counted")

	_, err = f.svc.CreateGroup(ctx, atlas, conversations.GroupInput{Name: "Ops", Members: []string{store.NewID()}})
	require.True(t, errors.As(err, &refused))
	_, err = f.svc.CreateGroup(ctx, atlas, conversations.GroupInput{Name: "", Members: []string{f.orion}})
	require.Error(t, err)
	require.False(t, errors.As(err, &refused), "an invalid name is reported before the members are looked at")
	require.Zero(t, f.asked())
}

// DM-04 / AGRP-01 rate limit hook: an AI asks AllowCreate only for a group it creates; a refusal
// creates nothing; users never ask.
func TestOnlyCreatedGroupsOfAnAICountAgainstTheLimit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	atlas := f.ai(f.alice, f.atlas)

	_, err := f.svc.CreateGroup(ctx, atlas, conversations.GroupInput{Name: "Ops", Members: []string{f.alice.User.ID}})
	require.ErrorIs(t, err, conversations.ErrRateLimited)
	require.Equal(t, 1, f.asked())
	rooms, err := f.env.App.Rooms.List(ctx, f.user(f.alice))
	require.NoError(t, err)
	require.Empty(t, rooms, "the limited request created nothing")

	f.allowed = 1
	_, err = f.svc.CreateGroup(ctx, atlas, conversations.GroupInput{Name: "Ops", Members: []string{f.alice.User.ID}})
	require.NoError(t, err)
	require.Equal(t, 2, f.asked())

	_, err = f.svc.CreateGroup(ctx, f.user(f.alice), conversations.GroupInput{Name: "Mine", Members: []string{f.bob.User.ID}})
	require.NoError(t, err)
	require.Equal(t, 2, f.asked(), "users are not limited")
}
