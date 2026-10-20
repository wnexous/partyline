package delivery

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// The room used by the table: two agents, three chats (C2 with outbound off) and a user member.
func room(c1, c2, c3 bool) []Member {
	return []Member{
		{Kind: dto.MemberKindAgent, ID: "A1"},
		{Kind: dto.MemberKindChat, ID: "C1", Outbound: c1},
		{Kind: dto.MemberKindUser, ID: "U1"},
		{Kind: dto.MemberKindAgent, ID: "A2"},
		{Kind: dto.MemberKindChat, ID: "C2", Outbound: c2},
		{Kind: dto.MemberKindChat, ID: "C3", Outbound: c3},
	}
}

// DLV-01..05: explicit delivery sets per author kind, recipients and bridge setting, on a room
// with agents A1, A2, user U1 and chats C1 (outbound on), C2 (outbound off), C3 (outbound on).
func TestPlanTable(t *testing.T) {
	members := room(true, false, true)
	cases := []struct {
		name   string
		bridge bool
		msg    Draft
		agents []string
		chats  []string
	}{
		// DLV-02/03: an agent post reaches the other agent and the outbound chats.
		{"agent, no recipients", false, Draft{AuthorKind: dto.AuthorKindAgent, AuthorID: "A1"}, []string{"A2"}, []string{"C1", "C3"}},
		{"agent, no recipients, bridge", true, Draft{AuthorKind: dto.AuthorKindAgent, AuthorID: "A2"}, []string{"A1"}, []string{"C1", "C3"}},
		// DLV-04: explicit recipients reach only the listed chats. A listed listen-only chat
		// (outbound off) is dropped: the API answers 422 for it, and Plan never sends to it
		// even when a caller skips that check.
		{"agent to C2 (listen-only)", false, Draft{AuthorKind: dto.AuthorKindAgent, AuthorID: "A1", Recipients: []string{"C2"}}, []string{"A2"}, nil},
		{"agent to C1+C2", false, Draft{AuthorKind: dto.AuthorKindAgent, AuthorID: "A1", Recipients: []string{"C2", "C1"}}, []string{"A2"}, []string{"C1"}},
		{"user to C2+C3", false, Draft{AuthorKind: dto.AuthorKindUser, AuthorID: "U1", Recipients: []string{"C3", "C2"}}, []string{"A1", "A2"}, []string{"C3"}},
		{"agent to non-member", false, Draft{AuthorKind: dto.AuthorKindAgent, AuthorID: "A1", Recipients: []string{"C9"}}, []string{"A2"}, nil},
		// DLV-02/03 for users: every agent, the outbound chats.
		{"user, no recipients", false, Draft{AuthorKind: dto.AuthorKindUser, AuthorID: "U1"}, []string{"A1", "A2"}, []string{"C1", "C3"}},
		{"non-member user, no recipients", true, Draft{AuthorKind: dto.AuthorKindUser, AuthorID: "U9"}, []string{"A1", "A2"}, []string{"C1", "C3"}},
		{"user to C3", false, Draft{AuthorKind: dto.AuthorKindUser, AuthorID: "U1", Recipients: []string{"C3"}}, []string{"A1", "A2"}, []string{"C3"}},
		// DLV-05: chat posts reach the other outbound chats only when the room bridges chats;
		// C2 (outbound off) receives nothing, not even bridged messages.
		{"chat C1, no bridge", false, Draft{AuthorKind: dto.AuthorKindChat, AuthorID: "C1"}, []string{"A1", "A2"}, nil},
		{"chat C1, bridge", true, Draft{AuthorKind: dto.AuthorKindChat, AuthorID: "C1"}, []string{"A1", "A2"}, []string{"C3"}},
		{"chat C2, bridge", true, Draft{AuthorKind: dto.AuthorKindChat, AuthorID: "C2"}, []string{"A1", "A2"}, []string{"C1", "C3"}},
		{"chat recipients ignored", false, Draft{AuthorKind: dto.AuthorKindChat, AuthorID: "C1", Recipients: []string{"C3"}}, []string{"A1", "A2"}, nil},
		// System messages (loop guard notices) reach agents only.
		{"system", true, Draft{AuthorKind: dto.AuthorKindSystem}, []string{"A1", "A2"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Plan(RoomState{BridgeChats: tc.bridge}, members, tc.msg)
			require.Equal(t, tc.agents, got.AgentIDs, "agents")
			require.Equal(t, tc.chats, got.ChatIDs, "chats")
		})
	}
}

// DLV-01..05 checked as properties over every combination of author, outbound flags,
// recipients and bridge setting.
func TestPlanExhaustive(t *testing.T) {
	authors := []Draft{
		{AuthorKind: dto.AuthorKindAgent, AuthorID: "A1"},
		{AuthorKind: dto.AuthorKindAgent, AuthorID: "A2"},
		{AuthorKind: dto.AuthorKindUser, AuthorID: "U1"},
		{AuthorKind: dto.AuthorKindChat, AuthorID: "C1"},
		{AuthorKind: dto.AuthorKindChat, AuthorID: "C2"},
		{AuthorKind: dto.AuthorKindChat, AuthorID: "C3"},
		{AuthorKind: dto.AuthorKindSystem},
	}
	recipientSets := [][]string{nil, {"C1"}, {"C2"}, {"C1", "C3"}, {"C1", "C2", "C3"}}
	n := 0
	for mask := 0; mask < 8; mask++ {
		members := room(mask&1 != 0, mask&2 != 0, mask&4 != 0)
		for _, author := range authors {
			for _, rcpt := range recipientSets {
				for _, bridge := range []bool{false, true} {
					msg := author
					msg.Recipients = rcpt
					name := fmt.Sprintf("outbound=%03b/%s:%s/rcpt=%v/bridge=%v", mask, msg.AuthorKind, msg.AuthorID, rcpt, bridge)
					got := Plan(RoomState{BridgeChats: bridge}, members, msg)
					checkPlan(t, name, members, msg, bridge, got)
					n++
				}
			}
		}
	}
	require.Equal(t, 8*7*5*2, n)
}

func checkPlan(t *testing.T, name string, members []Member, msg Draft, bridge bool, got Targets) {
	t.Helper()
	agents := set(got.AgentIDs)
	chats := set(got.ChatIDs)
	require.Len(t, agents, len(got.AgentIDs), "%s: duplicate agent target", name)
	require.Len(t, chats, len(got.ChatIDs), "%s: duplicate chat target", name)
	listed := set(msg.Recipients)

	for _, m := range members {
		isAuthor := string(m.Kind) == string(msg.AuthorKind) && m.ID == msg.AuthorID
		switch m.Kind {
		case dto.MemberKindAgent:
			// DLV-01, DLV-02: every agent member except the author.
			require.Equal(t, !isAuthor, agents[m.ID], "%s: agent %s", name, m.ID)
		case dto.MemberKindChat:
			var want bool
			switch {
			case isAuthor: // DLV-01
				want = false
			case msg.AuthorKind == dto.AuthorKindChat: // DLV-05
				want = bridge && m.Outbound
			case msg.AuthorKind == dto.AuthorKindSystem:
				want = false
			case len(msg.Recipients) > 0: // DLV-04; listen-only chats never receive
				want = listed[m.ID] && m.Outbound
			default: // DLV-03
				want = m.Outbound
			}
			require.Equal(t, want, chats[m.ID], "%s: chat %s", name, m.ID)
		case dto.MemberKindUser:
			require.False(t, agents[m.ID] || chats[m.ID], "%s: users are not delivery targets", name)
		}
	}
	for id := range chats {
		require.True(t, isMember(members, dto.MemberKindChat, id), "%s: non-member chat %s", name, id)
	}
}

func isMember(members []Member, kind dto.MemberKind, id string) bool {
	for _, m := range members {
		if m.Kind == kind && m.ID == id {
			return true
		}
	}
	return false
}

func set(ids []string) map[string]bool {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// DLV-06: trusted only when the author resolves to the recipient agent's owner.
func TestTrusted(t *testing.T) {
	cases := []struct {
		authorUser, owner string
		want              bool
	}{
		{"u-owner", "u-owner", true},  // the owner, the owner's agents or verified identities
		{"u-other", "u-owner", false}, // a teammate or a teammate's agent
		{"", "u-owner", false},        // unverified chat sender
		{"", "", false},               // never trusted without a resolved author
		{"u-owner", "", false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, Trusted(tc.authorUser, tc.owner), "author=%q owner=%q", tc.authorUser, tc.owner)
	}
}

// DLV-08: the 25th consecutive agent message pauses agent delivery, reported once.
func TestLoopGuardPausesAtTwentyFifthAgentMessage(t *testing.T) {
	var g LoopGuard
	s := LoopState{}
	for i := 1; i <= 24; i++ {
		var paused bool
		s, paused = g.Next(s, dto.AuthorKindAgent)
		require.False(t, paused, "message %d", i)
		require.Equal(t, LoopState{Streak: i}, s)
	}
	s, paused := g.Next(s, dto.AuthorKindAgent)
	require.True(t, paused)
	require.Equal(t, LoopState{Streak: 25, Paused: true}, s)

	s, paused = g.Next(s, dto.AuthorKindAgent)
	require.False(t, paused, "pausedNow is reported once")
	require.True(t, s.Paused)
}

// DLV-10: a user or chat message resets the streak; DLV-09: only a resume unpauses.
func TestLoopGuardResets(t *testing.T) {
	g := LoopGuard{}
	for _, resetter := range []dto.AuthorKind{dto.AuthorKindUser, dto.AuthorKindChat} {
		t.Run(string(resetter), func(t *testing.T) {
			s := LoopState{}
			for i := 0; i < 24; i++ {
				s, _ = g.Next(s, dto.AuthorKindAgent)
			}
			s, paused := g.Next(s, resetter)
			require.False(t, paused)
			require.Equal(t, LoopState{Streak: 0}, s)

			// After the reset, 25 more agent messages are needed to pause.
			for i := 1; i <= 24; i++ {
				s, paused = g.Next(s, dto.AuthorKindAgent)
				require.False(t, paused)
			}
			s, paused = g.Next(s, dto.AuthorKindAgent)
			require.True(t, paused)

			s, _ = g.Next(s, resetter)
			require.Equal(t, LoopState{Streak: 0, Paused: true}, s, "a reset does not resume a paused room")
		})
	}

	// The loop guard's own system notice neither extends nor resets the streak.
	s, paused := g.Next(LoopState{Streak: 7}, dto.AuthorKindSystem)
	require.False(t, paused)
	require.Equal(t, LoopState{Streak: 7}, s)
}

// The limit is configurable; zero means DefaultLoopLimit.
func TestLoopGuardLimit(t *testing.T) {
	require.Equal(t, 25, DefaultLoopLimit)
	g := LoopGuard{Limit: 2}
	s, paused := g.Next(LoopState{}, dto.AuthorKindAgent)
	require.False(t, paused)
	_, paused = g.Next(s, dto.AuthorKindAgent)
	require.True(t, paused)
}
