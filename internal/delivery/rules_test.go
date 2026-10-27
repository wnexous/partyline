package delivery

import (
	"fmt"
	"testing"
	"time"

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

// loopGuardStart is the time of the first message in the loop guard tests.
var loopGuardStart = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// DLV-08: the 25th consecutive agent message within the quiet gap pauses agent delivery, reported
// once; the guard itself never resumes it (DLV-09).
func TestLoopGuardPausesAtTwentyFifthAgentMessage(t *testing.T) {
	var g LoopGuard
	s := LoopState{}
	previous := time.Time{}
	for i := 1; i <= 24; i++ {
		now := loopGuardStart.Add(time.Duration(i) * time.Second)
		var paused bool
		s, paused = g.Next(s, dto.AuthorKindAgent, previous, now)
		require.False(t, paused, "message %d", i)
		require.Equal(t, LoopState{Streak: i}, s)
		previous = now
	}
	now := loopGuardStart.Add(25 * time.Second)
	s, paused := g.Next(s, dto.AuthorKindAgent, previous, now)
	require.True(t, paused)
	require.Equal(t, LoopState{Streak: 25, Paused: true}, s)

	s, paused = g.Next(s, dto.AuthorKindAgent, now, now.Add(time.Second))
	require.False(t, paused, "pausedNow is reported once")
	require.True(t, s.Paused)
}

// DLV-08: a slow conversation is no ping-pong. 25 agent messages, each posted more than the quiet
// gap after the previous one, never pause the room: every message starts its own burst.
func TestLoopGuardQuietGapStartsTheStreakAgain(t *testing.T) {
	var g LoopGuard
	require.Equal(t, 10*time.Minute, DefaultQuietGap)
	previous := time.Time{}
	now := loopGuardStart
	var s LoopState
	var paused bool
	for i := 1; i <= 25; i++ {
		s, paused = g.Next(s, dto.AuthorKindAgent, previous, now)
		require.False(t, paused, "message %d", i)
		require.Equal(t, LoopState{Streak: 1}, s, "message %d starts its own burst", i)
		previous = now
		now = now.Add(11 * time.Minute)
	}
}

// DLV-08: the gap must be longer than the quiet gap. Exactly 10 minutes still counts as the same
// burst, so a boundary case keeps counting rather than silently restarting.
func TestLoopGuardQuietGapBoundary(t *testing.T) {
	var g LoopGuard
	now := loopGuardStart.Add(5 * time.Minute)
	s, paused := g.Next(LoopState{Streak: 5}, dto.AuthorKindAgent, now.Add(-DefaultQuietGap), now)
	require.False(t, paused)
	require.Equal(t, LoopState{Streak: 6}, s, "a gap of exactly QuietGap continues the burst")

	s, paused = g.Next(s, dto.AuthorKindAgent, now, now.Add(DefaultQuietGap+time.Second))
	require.False(t, paused)
	require.Equal(t, LoopState{Streak: 1}, s, "a longer gap starts the streak again")
}

// DLV-08: 20 rapid agent messages, a quiet gap and 10 more rapid ones are two bursts of 20 and 10,
// not a run of 30 that would pause the room.
func TestLoopGuardBurstAfterQuietGap(t *testing.T) {
	var g LoopGuard
	s := LoopState{}
	previous := time.Time{}
	now := loopGuardStart
	post := func() (LoopState, bool) {
		next, paused := g.Next(s, dto.AuthorKindAgent, previous, now)
		previous, s = now, next
		return s, paused
	}
	for i := 0; i < 20; i++ {
		_, paused := post()
		require.False(t, paused)
		now = now.Add(time.Second)
	}
	require.Equal(t, LoopState{Streak: 20}, s)

	now = now.Add(15 * time.Minute)
	for i := 0; i < 10; i++ {
		_, paused := post()
		require.False(t, paused, "message %d of the second burst", i+1)
		now = now.Add(time.Second)
	}
	require.Equal(t, LoopState{Streak: 10}, s, "the quiet gap started the streak again")
}

// DLV-09: a quiet gap only restarts the counter. A paused room stays paused until a resume.
func TestLoopGuardPausedStaysPausedAfterGap(t *testing.T) {
	var g LoopGuard
	previous := loopGuardStart
	now := previous.Add(15 * time.Minute)
	s, paused := g.Next(LoopState{Streak: 25, Paused: true}, dto.AuthorKindAgent, previous, now)
	require.False(t, paused, "an already paused room posts no second notice")
	require.True(t, s.Paused, "the gap does not resume the room")
	require.Equal(t, LoopState{Streak: 1, Paused: true}, s, "the gap restarts the counter but keeps the pause")
}

// DLV-10: a user or chat message resets the streak; DLV-09: only a resume unpauses.
func TestLoopGuardResets(t *testing.T) {
	g := LoopGuard{}
	for _, resetter := range []dto.AuthorKind{dto.AuthorKindUser, dto.AuthorKindChat} {
		t.Run(string(resetter), func(t *testing.T) {
			s := LoopState{}
			previous := time.Time{}
			now := loopGuardStart
			for i := 0; i < 24; i++ {
				s, _ = g.Next(s, dto.AuthorKindAgent, previous, now)
				previous = now
				now = now.Add(time.Second)
			}
			s, paused := g.Next(s, resetter, previous, now)
			require.False(t, paused)
			require.Equal(t, LoopState{Streak: 0}, s)
			previous = now

			// After the reset, 25 more agent messages within the quiet gap are needed to pause.
			for i := 1; i <= 24; i++ {
				now = now.Add(time.Second)
				s, paused = g.Next(s, dto.AuthorKindAgent, previous, now)
				require.False(t, paused)
				previous = now
			}
			now = now.Add(time.Second)
			s, paused = g.Next(s, dto.AuthorKindAgent, previous, now)
			require.True(t, paused)

			s, _ = g.Next(s, resetter, now, now.Add(15*time.Minute))
			require.Equal(t, LoopState{Streak: 0, Paused: true}, s, "a reset does not resume a paused room")
		})
	}

	// The loop guard's own system notice neither extends nor resets the streak.
	s, paused := g.Next(LoopState{Streak: 7}, dto.AuthorKindSystem, loopGuardStart, loopGuardStart.Add(time.Second))
	require.False(t, paused)
	require.Equal(t, LoopState{Streak: 7}, s)
}

// The limit is configurable; zero means DefaultLoopLimit.
func TestLoopGuardLimit(t *testing.T) {
	require.Equal(t, 25, DefaultLoopLimit)
	g := LoopGuard{Limit: 2}
	now := loopGuardStart
	s, paused := g.Next(LoopState{}, dto.AuthorKindAgent, time.Time{}, now)
	require.False(t, paused)
	_, paused = g.Next(s, dto.AuthorKindAgent, now, now.Add(time.Second))
	require.True(t, paused)
}

// The quiet gap is configurable; zero means DefaultQuietGap.
func TestLoopGuardQuietGap(t *testing.T) {
	g := LoopGuard{Limit: 3, QuietGap: time.Minute}
	now := loopGuardStart
	s, paused := g.Next(LoopState{Streak: 2}, dto.AuthorKindAgent, now, now.Add(2*time.Minute))
	require.False(t, paused)
	require.Equal(t, LoopState{Streak: 1}, s, "a custom quiet gap starts the streak again")
}
