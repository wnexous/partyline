package rooms

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/events"
)

// burst posts n messages in the room, alternating between the authors, and returns the 1-based number of the
// message that paused the room, 0 for none. Each post must say whether the room is paused after it (LG-02).
func (f *fixture) burst(t *testing.T, roomID string, n int, authors ...authn.Principal) int {
	t.Helper()
	for i := 1; i <= n; i++ {
		before := f.count(t, "SELECT agents_paused FROM rooms WHERE id = ?", roomID) == 1
		m := f.post(t, authors[(i-1)%len(authors)], roomID, "ping")
		after := f.count(t, "SELECT agents_paused FROM rooms WHERE id = ?", roomID) == 1
		require.Equal(t, after, m.DeliveryPaused, "message %d: the post says what the room is (LG-02)", i)
		if after && !before {
			return i
		}
	}
	return 0
}

// LG-01: a DM whose two participants are AIs of the same host pauses at the 200th agent message in a row; a
// DM between AIs of two hosts, a DM between a user and an AI and a group of two AIs of the same host keep 25.
func TestLoopGuardLimitByRoom(t *testing.T) {
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.user(t, "bob", dto.RoleMember)
	a1, a2 := f.agent(t, alice, "a1"), f.agent(t, alice, "a2")
	b1 := f.agent(t, bob, "b1")

	sameHost := f.dmAIs(t, alice, a1, a2)
	sub, cancel := f.bus.Subscribe(1024)
	defer cancel()
	require.Equal(t, delivery.SameHostLoopLimit,
		f.burst(t, sameHost, 250, agentPrincipal(alice, a1), agentPrincipal(alice, a2)), "same-host DM: 200")
	var paused []events.AgentsPaused
	for _, e := range drain(sub) {
		if p, ok := e.(events.AgentsPaused); ok {
			paused = append(paused, p)
		}
	}
	require.Equal(t, []events.AgentsPaused{{RoomID: sameHost, Limit: delivery.SameHostLoopLimit}}, paused)
	require.True(t, f.post(t, agentPrincipal(alice, a2), sameHost, "after").DeliveryPaused)

	twoHosts := f.dmAIs(t, alice, a1, b1)
	require.Equal(t, delivery.DefaultLoopLimit,
		f.burst(t, twoHosts, 50, agentPrincipal(alice, a1), agentPrincipal(bob, b1)), "AIs of two hosts: 25")

	withUser := f.dmAI(t, bob, a1)
	require.Equal(t, delivery.DefaultLoopLimit, f.burst(t, withUser, 50, agentPrincipal(alice, a1)), "a user and an AI: 25")

	group := f.room(t, alice, "ops")
	f.addMember(t, alice, group.ID, dto.MemberKindAgent, a1)
	f.addMember(t, alice, group.ID, dto.MemberKindAgent, a2)
	require.Equal(t, delivery.DefaultLoopLimit,
		f.burst(t, group.ID, 50, agentPrincipal(alice, a1), agentPrincipal(alice, a2)), "a group of one host's AIs: 25")
}

// LG-01: a person's message resets the streak of a same-host DM as it does anywhere (DLV-10), and the quiet gap
// still starts it again: the higher limit changes only how many messages in a row pause the room.
func TestSameHostDMKeepsTheResets(t *testing.T) {
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	a1, a2 := f.agent(t, alice, "a1"), f.agent(t, alice, "a2")
	room := f.dmAIs(t, alice, a1, a2)
	streak := func() int { return f.count(t, "SELECT agent_streak FROM rooms WHERE id = ?", room) }
	require.Zero(t, f.burst(t, room, 150, agentPrincipal(alice, a1), agentPrincipal(alice, a2)))
	require.Equal(t, 150, streak())
	f.clock.now = f.clock.now.Add(delivery.DefaultQuietGap + time.Minute)
	f.post(t, agentPrincipal(alice, a1), room, "after a pause")
	require.Equal(t, 1, streak(), "the quiet gap starts the streak again")
}
