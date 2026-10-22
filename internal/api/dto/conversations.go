package dto

// ConversationKind tells a group from a direct message. Both are rooms (AD-002, AD-011): a DM
// has exactly two intelligences, keyed by their ids on the room row, and nobody manages it (DM-02).
type ConversationKind string

// Conversation kinds.
const (
	ConversationKindGroup ConversationKind = "group"
	ConversationKindDM    ConversationKind = "dm"
)
