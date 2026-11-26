// Package delivery decides who receives a room message and runs the agent dispatcher. The rules
// in this file are pure: Plan (DLV-01, DLV-02) and LoopGuard (DLV-08, DLV-10, LG-01). Trust (SR2-07) is in trust.go.
// A room delivers to intelligences only: chats are never members of a conversation and a room
// sends nothing to a platform (CID-22).
package delivery

import (
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
)

// Member is a room member as the delivery rules see it.
type Member struct {
	Kind dto.MemberKind
	// ID is the agent, chat or user id (room_members.member_id).
	ID string
}

// Draft is the message being delivered.
type Draft struct {
	AuthorKind dto.AuthorKind
	// AuthorID is the authoring agent, chat or user id; empty for system messages.
	AuthorID string
}

// Plan returns the agents a message is delivered to, in member order: every agent member except the
// author itself (DLV-01, DLV-02). Users read the room in the app and chats are never targets: a
// room sends nothing to a platform (CID-22).
func Plan(members []Member, msg Draft) []string {
	var agentIDs []string
	for _, m := range members {
		if m.Kind != dto.MemberKindAgent {
			continue
		}
		if msg.AuthorID != "" && string(msg.AuthorKind) == string(m.Kind) && m.ID == msg.AuthorID {
			continue
		}
		agentIDs = append(agentIDs, m.ID)
	}
	return agentIDs
}

// DefaultLoopLimit is the number of consecutive agent messages that pauses agent delivery.
const DefaultLoopLimit = 25

// SameHostLoopLimit is the number of consecutive agent messages that pauses agent delivery in a DM whose two
// participants are AIs of the same host (LG-01): two AIs of one person working together in their DM exchange
// many messages in a row on purpose, and a real loop between them still stops.
const SameHostLoopLimit = 200

// DefaultQuietGap is the gap that ends a burst and restarts the streak; 10 minutes.
const DefaultQuietGap = 10 * time.Minute

// LoopState is a room's loop guard state.
type LoopState struct {
	// Streak counts consecutive agent-authored messages.
	Streak int
	// Paused stops delivery to agents until someone who may resumes the room (DLV-09): a group's manager, or
	// whoever can see a DM as a user.
	Paused bool
}

// LoopGuard stops agent ping-pong in a room.
type LoopGuard struct {
	// Limit is the streak that pauses agent delivery; DefaultLoopLimit when zero.
	Limit int
	// SameHostLimit is the Limit that For gives a DM between two AIs of the same host (LG-01);
	// SameHostLoopLimit when zero.
	SameHostLimit int
	// QuietGap is the gap that ends a burst: an agent message posted more than this long after
	// the room's previous message starts the streak again from 0; DefaultQuietGap when zero.
	QuietGap time.Duration
	// Off never pauses a room: the operator turned the guard off (PARTYLINE_LOOP_GUARD=off, LG-06).
	Off bool
}

// For returns the guard to apply in a room. The caller decides what the room is: sameHostDM is true for a
// DM whose two participants are AIs of the same host, which gets SameHostLimit as its Limit (LG-01). Every
// other room, groups included, keeps Limit. The quiet gap is the same for both.
func (g LoopGuard) For(sameHostDM bool) LoopGuard {
	if sameHostDM {
		g.Limit = g.SameHostLimit
		if g.Limit <= 0 {
			g.Limit = SameHostLoopLimit
		}
	}
	return g
}

// EffectiveLimit is the streak that pauses agent delivery under this guard: Limit, or DefaultLoopLimit when
// it is zero.
func (g LoopGuard) EffectiveLimit() int {
	if g.Limit <= 0 {
		return DefaultLoopLimit
	}
	return g.Limit
}

// Next returns the state after a message by author posted at now, whose previous message in the
// room was posted at previous (zero for the first message). An agent message posted more than
// QuietGap after the previous one starts the streak again from 0 before counting this message
// (DLV-08), so a slow conversation never pauses; a burst of agent messages within QuietGap
// pauses the room when it reaches the limit, reporting pausedNow once. A user or chat message
// resets the streak (DLV-10) but does not resume a paused room. System messages leave the state
// unchanged.
func (g LoopGuard) Next(s LoopState, author dto.AuthorKind, previous, now time.Time) (next LoopState, pausedNow bool) {
	limit := g.EffectiveLimit()
	gap := g.QuietGap
	if gap <= 0 {
		gap = DefaultQuietGap
	}
	switch author {
	case dto.AuthorKindAgent:
		if !previous.IsZero() && now.Sub(previous) > gap {
			s.Streak = 0
		}
		s.Streak++
		if s.Streak >= limit && !s.Paused && !g.Off {
			s.Paused = true
			return s, true
		}
	case dto.AuthorKindUser, dto.AuthorKindChat:
		s.Streak = 0
	}
	return s, false
}
