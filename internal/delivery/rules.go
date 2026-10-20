// Package delivery decides who receives a room message and runs the delivery workers.
// The rules in this file are pure: Plan (DLV-01..05), Trusted (DLV-06) and LoopGuard
// (DLV-08, DLV-10).
package delivery

import "github.com/wnexous/partyline/internal/api/dto"

// Member is a room member as the delivery rules see it.
type Member struct {
	Kind dto.MemberKind
	// ID is the agent, chat or user id (room_members.member_id).
	ID string
	// Outbound tells whether agent and user posts without explicit recipients (DLV-03) and
	// bridged chat posts (DLV-05) reach this chat.
	Outbound bool
}

// RoomState holds the room settings the rules depend on.
type RoomState struct {
	// BridgeChats relays chat-authored messages to the other chats (DLV-05).
	BridgeChats bool
}

// Draft is the message being delivered.
type Draft struct {
	AuthorKind dto.AuthorKind
	// AuthorID is the authoring agent, chat or user id; empty for system messages.
	AuthorID string
	// Recipients are explicit chat ids (DLV-04); only agent and user authors set them.
	Recipients []string
}

// Targets are the members a message is delivered to.
type Targets struct {
	// AgentIDs receive the message over the agent channel.
	AgentIDs []string
	// ChatIDs get an outbox entry each.
	ChatIDs []string
}

// Plan returns the delivery targets of msg, in member order:
//   - never the author itself (DLV-01);
//   - every other agent member (DLV-02);
//   - agent and user posts: the listed chats with Outbound on when Recipients is set (DLV-04),
//     otherwise every chat with Outbound on (DLV-03);
//   - chat posts: every other chat with Outbound on when the room bridges chats, else none
//     (DLV-05);
//   - system posts: no chats.
func Plan(room RoomState, members []Member, msg Draft) Targets {
	listed := make(map[string]bool, len(msg.Recipients))
	for _, id := range msg.Recipients {
		listed[id] = true
	}
	isAuthor := func(m Member) bool {
		return msg.AuthorID != "" && string(m.Kind) == string(msg.AuthorKind) && m.ID == msg.AuthorID
	}
	var t Targets
	for _, m := range members {
		if isAuthor(m) {
			continue
		}
		switch m.Kind {
		case dto.MemberKindAgent:
			t.AgentIDs = append(t.AgentIDs, m.ID)
		case dto.MemberKindChat:
			if chatReceives(room, m, msg, listed) {
				t.ChatIDs = append(t.ChatIDs, m.ID)
			}
		}
	}
	return t
}

func chatReceives(room RoomState, m Member, msg Draft, listed map[string]bool) bool {
	switch msg.AuthorKind {
	case dto.AuthorKindAgent, dto.AuthorKindUser:
		if len(msg.Recipients) > 0 {
			// A listen-only chat never receives, even when listed (the API rejects that with
			// 422; this guards callers that skip the check).
			return listed[m.ID] && m.Outbound
		}
		return m.Outbound
	case dto.AuthorKindChat:
		return room.BridgeChats && m.Outbound
	default:
		return false
	}
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
