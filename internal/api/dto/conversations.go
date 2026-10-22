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
