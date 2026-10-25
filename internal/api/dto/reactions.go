package dto

// MaxMessageEmoji is the most different emoji one message carries; one more is refused with
// ErrorCodeReactionLimit (RCT-04).
const MaxMessageEmoji = 12

// Reaction is one emoji on a message, with the intelligences that used it (RCT-05). A message
// lists its reactions in the order each emoji was first used.
type Reaction struct {
	Emoji string `json:"emoji"`
	// By are the ids of the intelligences that used the emoji (a user, an AI), in the order they
	// reacted. The viewer's own reaction is the one whose id is theirs.
	By []string `json:"by"`
}

// AddReactionRequest is the body of POST /rooms/{id}/messages/{mid}/reactions.
type AddReactionRequest struct {
	// Emoji is exactly one emoji (validation.OneEmoji), else 422 invalid.
	Emoji string `json:"emoji"`
}

// ReactionsData is what a room.reactions frame carries: the reactions of one message as they are
// now, whole, so that a client replaces what it had and never has to add or subtract (RCT-06).
type ReactionsData struct {
	RoomID    string     `json:"room_id"`
	MessageID string     `json:"message_id"`
	Reactions []Reaction `json:"reactions"`
}

// LiveRoomReactions reports that someone reacted to a message of a room the user can view, or took
// a reaction back (RCT-06).
type LiveRoomReactions struct {
	Type LiveEventType `json:"type" tstype:"typeof LiveEventTypeRoomReactions"`
	Data ReactionsData `json:"data"`
}
