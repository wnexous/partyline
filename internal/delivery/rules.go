// Package delivery decides who receives a room message and runs the agent dispatcher. The rules
// in this file are pure: Plan (DLV-01, DLV-02), Trusted (DLV-06) and LoopGuard (DLV-08, DLV-10).
// A room delivers to intelligences only: chats are never members of a conversation and a room
// sends nothing to a platform (CID-22).
package delivery

import "github.com/wnexous/partyline/internal/api/dto"

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

// Trusted reports whether a message is trusted by an agent (DLV-06, AD-006): only when the
// author resolves to a user (authorUserID, filled at insert) who is the agent's owner.
func Trusted(authorUserID, recipientOwnerID string) bool {
	return authorUserID != "" && authorUserID == recipientOwnerID
}

// DefaultLoopLimit is the number of consecutive agent messages that pauses agent delivery.
const DefaultLoopLimit = 25

// LoopState is a room's loop guard state.
type LoopState struct {
	// Streak counts consecutive agent-authored messages.
	Streak int
	// Paused stops delivery to agents until a manager resumes the room (DLV-09).
	Paused bool
}

// LoopGuard stops agent ping-pong in a room.
type LoopGuard struct {
	// Limit is the streak that pauses agent delivery; DefaultLoopLimit when zero.
	Limit int
}

// Next returns the state after a message by author. An agent message extends the streak and
// pauses the room when it reaches the limit, reporting pausedNow once (DLV-08); a user or chat
// message resets the streak (DLV-10) but does not resume a paused room. System messages leave
// the state unchanged.
func (g LoopGuard) Next(s LoopState, author dto.AuthorKind) (next LoopState, pausedNow bool) {
	limit := g.Limit
	if limit <= 0 {
		limit = DefaultLoopLimit
	}
	switch author {
	case dto.AuthorKindAgent:
		s.Streak++
		if s.Streak >= limit && !s.Paused {
			s.Paused = true
			return s, true
		}
	case dto.AuthorKindUser, dto.AuthorKindChat:
		s.Streak = 0
	}
	return s, false
}
