package conversations_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/conversations"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/testkit"
)

// fixture is an app with alice (owner of atlas), bob (owner of orion) and a conversations
// service whose AllowCreate hook counts its calls and denies after the first allowed ones.
type fixture struct {
	env          *testkit.Env
	alice, bob   *testkit.Client
	atlas, orion string
	svc          *conversations.Service
	asked        []string
	allowed      int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	env := testkit.New(t)
	f := &fixture{env: env, alice: env.NewUser("alice"), bob: env.NewUser("bob")}
	f.atlas, f.orion = env.NewAI(f.alice, "atlas"), env.NewAI(f.bob, "orion")
	f.svc = conversations.New(conversations.Deps{
		Store: env.App.Store, Rooms: env.App.Rooms, Intelligences: env.App.Intelligences, Bus: env.App.Bus,
		AllowCreate: func(agentID string) bool {
			f.asked = append(f.asked, agentID)
			if f.allowed == 0 {
				return false
			}
			f.allowed--
			return true
		},
	})
	return f
}

func (f *fixture) user(c *testkit.Client) authn.Principal {
	return authn.Principal{Kind: authn.KindUser, UserID: c.User.ID, Role: string(c.User.Role)}
}

func (f *fixture) ai(owner *testkit.Client, id string) authn.Principal {
	return authn.Principal{Kind: authn.KindAgent, UserID: owner.User.ID, AgentID: id}
}

// DM-04 (service side): an AI opens the DM with its owner, even though they never talked; the room
// belongs to the owner, the AI is an agent member and the owner posts in it.
func TestAIOpensADMWithItsOwner(t *testing.T) {
	f := newFixture(t)
	f.allowed = 10
	ctx := context.Background()

	room, created, err := f.svc.OpenDM(ctx, f.ai(f.alice, f.atlas), f.alice.User.ID)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, f.alice.User.ID, room.OwnerID, "an AI's DM belongs to its owner")
	require.Equal(t, dto.ConversationKindDM, dto.ConversationKind(room.Kind))
	require.Len(t, room.Participants, 2)

	members, err := f.env.App.Rooms.Members(ctx, f.user(f.alice), room.ID)
	require.NoError(t, err)
	kinds := map[string]string{}
	for _, m := range members {
		kinds[m.MemberID] = m.Kind
	}
	require.Equal(t, map[string]string{f.atlas: "agent", f.alice.User.ID: "user"}, kinds)
	f.alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "hi atlas"}).RequireStatus(http.StatusCreated)

	again, created, err := f.svc.OpenDM(ctx, f.user(f.alice), f.atlas)
	require.NoError(t, err)
	require.False(t, created, "the owner reaches the same DM from the other side")
	require.Equal(t, room.ID, again.ID)
}

// DM-04: the target of an AI must be visible to its owner; a user always is, an AI of another owner
// only when shared, and the AI cannot DM itself.
func TestAIDMTargetsAreThoseItsOwnerSees(t *testing.T) {
	f := newFixture(t)
	f.allowed = 10
	ctx := context.Background()
	atlas := f.ai(f.alice, f.atlas)

	_, _, err := f.svc.OpenDM(ctx, atlas, f.atlas)
	require.ErrorIs(t, err, conversations.ErrDMSelf)
	_, _, err = f.svc.OpenDM(ctx, atlas, f.orion)
	require.ErrorIs(t, err, conversations.ErrContactNotFound, "bob's AI is not shared with alice")
	_, _, err = f.svc.OpenDM(ctx, atlas, store.NewID())
	require.ErrorIs(t, err, conversations.ErrContactNotFound)
	_, _, err = f.svc.OpenDM(ctx, atlas, "")
	require.Error(t, err)

	f.bob.Put("/api/v1/agents/"+f.orion+"/sharing", dto.Sharing{
		Mode: dto.VisibilityShared, Users: []dto.SharingUser{{UserID: f.alice.User.ID, Level: dto.LevelUse}},
	}).RequireStatus(http.StatusOK)
	room, created, err := f.svc.OpenDM(ctx, atlas, f.orion)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, f.alice.User.ID, room.OwnerID, "alice owns the DM her AI opened")
	_, created, err = f.svc.OpenDM(ctx, atlas, f.bob.User.ID)
	require.NoError(t, err, "every user of the instance is a contact")
	require.True(t, created)
}

// DM-04, rate limit hook: only an AI that has to create a DM asks AllowCreate; a refusal creates
// nothing; reopening an existing DM and users never ask.
func TestOnlyNewDMsOfAnAICountAgainstTheLimit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	atlas := f.ai(f.alice, f.atlas)

	_, _, err := f.svc.OpenDM(ctx, atlas, f.bob.User.ID)
	require.ErrorIs(t, err, conversations.ErrRateLimited)
	require.Equal(t, []string{f.atlas}, f.asked)
	rooms, err := f.env.App.Rooms.List(ctx, f.user(f.bob))
	require.NoError(t, err)
	require.Empty(t, rooms, "nothing was created")

	f.allowed = 1
	room, created, err := f.svc.OpenDM(ctx, atlas, f.bob.User.ID)
	require.NoError(t, err)
	require.True(t, created)
	require.Len(t, f.asked, 2)

	// The limit is exhausted, yet the existing DM opens (from either side) without asking.
	again, created, err := f.svc.OpenDM(ctx, atlas, f.bob.User.ID)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, room.ID, again.ID)
	_, created, err = f.svc.OpenDM(ctx, f.user(f.bob), f.atlas)
	require.NoError(t, err)
	require.False(t, created)
	require.Len(t, f.asked, 2, "reopening is not counted")

	// A user creating a DM never asks: the limit is for AIs.
	_, created, err = f.svc.OpenDM(ctx, f.user(f.alice), f.bob.User.ID)
	require.NoError(t, err)
	require.True(t, created)
	require.Len(t, f.asked, 2)
}
