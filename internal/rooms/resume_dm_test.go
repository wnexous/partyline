package rooms

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store"
)

// dmAIs plants the direct message between two AIs, created by the principal, and returns its room id.
func (f *fixture) dmAIs(t *testing.T, creator authn.Principal, x, y string) string {
	t.Helper()
	a, b := x, y
	if a > b {
		a, b = b, a
	}
	id := store.NewID()
	f.exec(t, "INSERT INTO rooms (id, owner_id, name, kind, dm_a, dm_b, created_at, updated_at) VALUES (?, ?, '', 'dm', ?, ?, ?, ?)",
		id, creator.UserID, a, b, t0.UnixMilli(), t0.UnixMilli())
	for _, ai := range []string{x, y} {
		f.exec(t, "INSERT INTO room_members (id, room_id, kind, member_id, added_by, created_at) VALUES (?, ?, 'agent', ?, ?, ?)",
			store.NewID(), id, ai, creator.UserID, t0.UnixMilli())
	}
	return id
}

// pingPong makes the two AIs answer each other until the loop guard pauses the room.
func (f *fixture) pingPong(t *testing.T, roomID string, host authn.Principal, x, y string) {
	t.Helper()
	ais := []authn.Principal{agentPrincipal(host, x), agentPrincipal(host, y)}
	for i := 0; i < 25; i++ {
		f.post(t, ais[i%2], roomID, "ping")
	}
	paused := f.count(t, "SELECT agents_paused FROM rooms WHERE id = ?", roomID)
	require.Equal(t, 1, paused, "the guard paused the room after 25 AI messages in a row")
}

// DLV-09, POS-03: a DM between two AIs that the loop guard paused is resumed by the user who created
// them - who manages both and therefore sees the DM, though nobody manages a DM - and by nobody who cannot
// see it. The AIs themselves cannot resume it: the guard is there to stop them from answering each other.
func TestTheCreatorOfTwoAIsResumesTheirPausedDM(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	org := f.orgOf(t, alice)
	colleague := f.colleague(t, org, "colleague", orgs.PermissionMember)
	stranger := f.user(t, "stranger", dto.RoleMember)
	a1, a2 := f.agent(t, alice, "a1"), f.agent(t, alice, "a2")
	room := f.dmAIs(t, alice, a1, a2)
	f.pingPong(t, room, alice, a1, a2)

	got, err := f.svc.Get(ctx, alice, room)
	require.NoError(t, err)
	require.True(t, got.AgentsPaused)
	require.Equal(t, dto.LevelView, got.Level, "the creator only views the DM")
	require.True(t, got.CanResume, "and resumes it")

	// Whoever does not see the DM cannot resume it: it does not exist for them. Neither does an AI.
	_, err = f.svc.Resume(ctx, colleague, room)
	require.ErrorIs(t, err, access.ErrNotFound, "a member of the organization who manages neither AI")
	_, err = f.svc.Resume(ctx, stranger, room)
	require.ErrorIs(t, err, access.ErrNotFound, "a user of another organization")
	_, err = f.svc.Resume(ctx, agentPrincipal(alice, a1), room)
	require.ErrorIs(t, err, access.ErrForbidden, "a participant that is an AI")
	for _, p := range []authn.Principal{colleague, stranger} {
		_, err = f.svc.Get(ctx, p, room)
		require.ErrorIs(t, err, access.ErrNotFound)
	}
	require.Equal(t, 1, f.count(t, "SELECT agents_paused FROM rooms WHERE id = ?", room), "no refusal resumed it")

	sub, cancel := f.bus.Subscribe(8)
	defer cancel()
	resumed, err := f.svc.Resume(ctx, alice, room)
	require.NoError(t, err)
	require.False(t, resumed.AgentsPaused)
	require.Equal(t, 0, f.count(t, "SELECT agent_streak FROM rooms WHERE id = ?", room), "the streak starts again")
	require.Equal(t, []events.Event{events.RoomChanged{RoomID: room}}, drain(sub))

	// The guard works again: the room pauses after another 25 messages, not before.
	f.post(t, agentPrincipal(alice, a1), room, "again")
	require.Equal(t, 0, f.count(t, "SELECT agents_paused FROM rooms WHERE id = ?", room))
}

// DLV-09: the human participant of a DM resumes it, though the level they hold on it is post and not
// manage; someone who is not in it does not, and a group is still resumed by its managers only.
func TestTheParticipantOfADMResumesIt(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.colleague(t, f.orgOf(t, alice), "bob", orgs.PermissionMember)
	atlas := f.agent(t, bob, "atlas") // bob's AI: alice is a participant of the DM, bob manages the AI
	room := f.dmAI(t, alice, atlas)
	ai := agentPrincipal(bob, atlas)
	for i := 0; i < 25; i++ {
		f.post(t, ai, room, "monologue")
	}
	require.Equal(t, 1, f.count(t, "SELECT agents_paused FROM rooms WHERE id = ?", room))

	got, err := f.svc.Get(ctx, alice, room)
	require.NoError(t, err)
	require.Equal(t, dto.LevelPost, got.Level)
	require.True(t, got.CanResume, "a participant resumes")
	got, err = f.svc.Get(ctx, bob, room)
	require.NoError(t, err)
	require.Equal(t, dto.LevelView, got.Level)
	require.True(t, got.CanResume, "so does the manager of the AI")

	listed, err := f.svc.List(ctx, alice)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.True(t, listed[0].CanResume, "the list says it too")

	_, err = f.svc.Resume(ctx, alice, room)
	require.NoError(t, err)
	require.Equal(t, 0, f.count(t, "SELECT agents_paused FROM rooms WHERE id = ?", room))

	// A group stays with its managers: a member who only posts gets 403, and nothing says otherwise.
	group := f.room(t, alice, "ops")
	f.addMember(t, alice, group.ID, dto.MemberKindUser, bob.UserID)
	f.exec(t, "UPDATE rooms SET agents_paused = 1 WHERE id = ?", group.ID)
	in, err := f.svc.Get(ctx, bob, group.ID)
	require.NoError(t, err)
	require.Equal(t, dto.LevelPost, in.Level)
	require.False(t, in.CanResume)
	_, err = f.svc.Resume(ctx, bob, group.ID)
	require.ErrorIs(t, err, access.ErrForbidden)
	mine, err := f.svc.Get(ctx, alice, group.ID)
	require.NoError(t, err)
	require.True(t, mine.CanResume)
	_, err = f.svc.Resume(ctx, alice, group.ID)
	require.NoError(t, err)
}
