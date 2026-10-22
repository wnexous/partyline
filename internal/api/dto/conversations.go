package dto

// ConversationKind tells a group from a direct message. Both are rooms (AD-002, AD-011): a DM
// has exactly two intelligences, keyed by their ids on the room row, and nobody manages it (DM-02).
type ConversationKind string

// Conversation kinds.
const (
	ConversationKindGroup ConversationKind = "group"
	ConversationKindDM    ConversationKind = "dm"
)

// OpenDMRequest is the body of POST /conversations/dm: the intelligence to talk to, by id
// (DM-01). The answer is the DM as a Room: 201 when it was created, 200 when it already existed.
type OpenDMRequest struct {
	With string `json:"with"`
}

// MaxGroupMembers is the most members a new group is created with (LAY-04); the AI that creates
// a group counts as one.
const MaxGroupMembers = 50

// CreateGroupRequest is the body of POST /groups (LAY-04) and of an AI's group creation (AGRP-01).
// Members are the ids of the users and AIs to put in the group, each checked with the add-member
// rules of the group's owner before anything is created; the owner is a member only when listed.
// Repeated ids count once.
type CreateGroupRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Members     []string `json:"members"`
}

// RefusedReason says why a member could not be added to a new group.
type RefusedReason string

// Reasons a member is refused. An AI the owner may not use is not_found as well: it must not be
// told apart from an id that does not exist.
const (
	// RefusedReasonNotFound is an id that is no enabled user and no AI the owner may use.
	RefusedReasonNotFound RefusedReason = "not_found"
	// RefusedReasonPerson is an external person, who joins a group through a bridged chat only.
	RefusedReasonPerson RefusedReason = "person"
)

// RefusedMember is a member of a new group that was refused (422 members_refused).
type RefusedMember struct {
	ID     string        `json:"id"`
	Reason RefusedReason `json:"reason"`
}
