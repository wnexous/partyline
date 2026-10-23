package delivery

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// The room used by the table: two agents, three chats and a user member.
func room() []Member {
	return []Member{
		{Kind: dto.MemberKindAgent, ID: "A1"},
		{Kind: dto.MemberKindChat, ID: "C1"},
		{Kind: dto.MemberKindUser, ID: "U1"},
		{Kind: dto.MemberKindAgent, ID: "A2"},
		{Kind: dto.MemberKindChat, ID: "C2"},
		{Kind: dto.MemberKindChat, ID: "C3"},
	}
}

// DLV-01, DLV-02 and CID-22: a message is delivered to every agent member but its author, and to
// nothing else: a user reads the room in the app, and a chat is no target (a room sends nothing to a
// platform), whoever wrote the message.
func TestPlanTable(t *testing.T) {
	members := room()
	cases := []struct {
		name   string
		msg    Draft
		agents []string
	}{
		{"agent A1", Draft{AuthorKind: dto.AuthorKindAgent, AuthorID: "A1"}, []string{"A2"}},
		{"agent A2", Draft{AuthorKind: dto.AuthorKindAgent, AuthorID: "A2"}, []string{"A1"}},
		{"user member", Draft{AuthorKind: dto.AuthorKindUser, AuthorID: "U1"}, []string{"A1", "A2"}},
		{"user who is no member", Draft{AuthorKind: dto.AuthorKindUser, AuthorID: "U9"}, []string{"A1", "A2"}},
		// The author is excluded by kind and id: a user whose id equals an agent's id is not that agent.
		{"user with an agent's id", Draft{AuthorKind: dto.AuthorKindUser, AuthorID: "A1"}, []string{"A1", "A2"}},
		{"chat member", Draft{AuthorKind: dto.AuthorKindChat, AuthorID: "C1"}, []string{"A1", "A2"}},
		{"system", Draft{AuthorKind: dto.AuthorKindSystem}, []string{"A1", "A2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.agents, Plan(members, tc.msg))
		})
	}
	require.Empty(t, Plan(nil, Draft{AuthorKind: dto.AuthorKindUser, AuthorID: "U1"}), "a room without agents delivers to nobody")
	require.Empty(t, Plan([]Member{{Kind: dto.MemberKindChat, ID: "C1"}, {Kind: dto.MemberKindUser, ID: "U1"}}, Draft{AuthorKind: dto.AuthorKindUser, AuthorID: "U1"}),
		"chats and users are no targets")
}

// DLV-01, DLV-02 checked as properties over every author: the targets are exactly the agent members
// other than the author, each once, in member order, and never a chat or a user.
func TestPlanExhaustive(t *testing.T) {
	members := room()
	authors := []Draft{
		{AuthorKind: dto.AuthorKindAgent, AuthorID: "A1"},
		{AuthorKind: dto.AuthorKindAgent, AuthorID: "A2"},
		{AuthorKind: dto.AuthorKindAgent, AuthorID: "A9"},
		{AuthorKind: dto.AuthorKindUser, AuthorID: "U1"},
		{AuthorKind: dto.AuthorKindChat, AuthorID: "C1"},
		{AuthorKind: dto.AuthorKindChat, AuthorID: "C2"},
		{AuthorKind: dto.AuthorKindChat, AuthorID: "C3"},
		{AuthorKind: dto.AuthorKindSystem},
	}
	for _, msg := range authors {
		name := fmt.Sprintf("%s:%s", msg.AuthorKind, msg.AuthorID)
		got := Plan(members, msg)
		var want []string
		for _, m := range members {
			isAuthor := string(m.Kind) == string(msg.AuthorKind) && m.ID == msg.AuthorID
			if m.Kind == dto.MemberKindAgent && !isAuthor {
				want = append(want, m.ID)
			}
		}
		require.Equal(t, want, got, name)
	}
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
