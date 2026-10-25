package dto

import "time"

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
// rules of the group's creator before anything is created: it must exist, be visible to the creator
// and belong to the group's organization. The creator is a member only when listed. Repeated ids
// count once.
type CreateGroupRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Members     []string `json:"members"`
	// OrgID is the organization the group belongs to (ORG-03), by id; the web app sends it. Absent
	// means the creator's default organization (an AI's is its home). A creator who is not a member
	// or admin of it, a guest included, is refused with 403 forbidden, and so is an unknown one.
	OrgID string `json:"org_id,omitempty"`
	// Org is OrgID by slug or id, for an AI's create_group (POST /agent/groups); both routes read
	// either field, OrgID first.
	Org string `json:"org,omitempty"`
}

// RefusedReason says why a member could not be added to a new group.
type RefusedReason string

// Reasons a member is refused. An AI the creator cannot see is not_found as well: it must not be
// told apart from an id that does not exist.
const (
	// RefusedReasonNotFound is an id that is no enabled user and no AI the creator can see.
	RefusedReasonNotFound RefusedReason = "not_found"
	// RefusedReasonPerson is an external person, who joins a group through a bridged chat only.
	RefusedReasonPerson RefusedReason = "person"
	// RefusedReasonNotOrgMember is a user or an AI the creator can see that does not belong to the
	// group's organization: a group is made of the members of its organization (ORG-03).
	RefusedReasonNotOrgMember RefusedReason = "not_org_member"
)

// RefusedMember is a member of a new group that was refused (422 members_refused).
type RefusedMember struct {
	ID     string        `json:"id"`
	Reason RefusedReason `json:"reason"`
}

// Conversation is one row of the messenger's conversation list (GET /conversations, DM-06,
// LAY-01): a group the caller can view or a DM the caller takes part in. The list is newest
// activity first.
type Conversation struct {
	// ID is the room id.
	ID   string           `json:"id"`
	Kind ConversationKind `json:"kind"`
	// Title is the group's name, or the display name of the other participant of a DM.
	Title string `json:"title"`
	// Counterpart is the other participant of a DM; absent for a group.
	Counterpart *IntelligenceRef `json:"counterpart,omitempty"`
	MyLevel     Level            `json:"my_level"`
	// LastMessage is absent while the conversation has no message.
	LastMessage *MessagePreview `json:"last_message,omitempty"`
	// Unread counts the messages after the caller's read position (and after the room's
	// pre-upgrade baseline) that the caller did not write.
	Unread int `json:"unread"`
	// ActivityAt is the time of the last message, else of the room's creation.
	ActivityAt time.Time `json:"activity_at"`
	// MemberNames are up to MaxConversationMemberNames "Name @handle" entries of the users and
	// AIs of a group (the other participant of a DM), for the sidebar search.
	MemberNames []string `json:"member_names"`
	// OrgIDs are the organizations the conversation is filed under (ORG-02): a group's organization;
	// for a DM the organizations both participants are members of, else the default organization of
	// the caller; empty when there is none.
	OrgIDs []string `json:"org_ids"`
	// AvatarURL is the photo of a group, as Room.AvatarURL (GAV-04); absent without one and for a DM, which is
	// drawn with its Counterpart.
	AvatarURL string `json:"avatar_url,omitempty"`
}

// MaxConversationMemberNames bounds Conversation.MemberNames.
const MaxConversationMemberNames = 20

// MaxPreviewLength is the length of a message preview in code points (LAY-01).
const MaxPreviewLength = 80

// MessagePreview is the last message of a conversation.
type MessagePreview struct {
	Seq int64 `json:"seq"`
	// AuthorID is the intelligence that wrote it; empty for system messages and for chat senders
	// without one.
	AuthorID string `json:"author_id"`
	// AuthorName is the author's display name in a group, where the list prefixes it; empty in a
	// DM.
	AuthorName string `json:"author_name"`
	// Text is the first MaxPreviewLength code points of the message, or "[file name]" for a
	// message that is only an attachment.
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

// MarkReadRequest is the body of PUT /rooms/{id}/read: the newest message the caller has seen. An
// absent seq reads everything; a seq past the newest message counts as the newest; an earlier one
// than the current position changes nothing (DM-07).
type MarkReadRequest struct {
	Seq *int64 `json:"seq,omitempty"`
}

// ReadState is the answer of PUT /rooms/{id}/read: the read position that holds and what is
// still unread after it.
type ReadState struct {
	RoomID string `json:"room_id"`
	Seq    int64  `json:"seq"`
	Unread int    `json:"unread"`
}
