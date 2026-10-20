package dto

import "time"

// Room is a routing room (GET /rooms, GET /rooms/{id}).
type Room struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Instructions is free text for the room's agents (INS-01), up to MaxRoomInstructions
	// characters; agents read it with the room_info tool.
	Instructions string     `json:"instructions"`
	Owner        User       `json:"owner"`
	Visibility   Visibility `json:"visibility"`
	// MyLevel is the caller's resolved level on this room (ACL-02).
	MyLevel       Level `json:"my_level"`
	BridgeChats   bool  `json:"bridge_chats"`
	RetentionDays int   `json:"retention_days"`
	// AgentsPaused is set by the loop guard (DLV-08) and cleared by POST /rooms/{id}/resume.
	AgentsPaused bool  `json:"agents_paused"`
	LastSeq      int64 `json:"last_seq"`
	// MemberCount counts the room's agents, chats and users.
	MemberCount int       `json:"member_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CreateRoomRequest is the body of POST /rooms. RetentionDays defaults to 90; 0 keeps forever.
type CreateRoomRequest struct {
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	Instructions  string `json:"instructions,omitempty"`
	BridgeChats   bool   `json:"bridge_chats,omitempty"`
	RetentionDays *int   `json:"retention_days,omitempty"`
}

// UpdateRoomRequest is the body of PATCH /rooms/{id}; absent fields are unchanged.
type UpdateRoomRequest struct {
	Name          *string `json:"name,omitempty"`
	Description   *string `json:"description,omitempty"`
	Instructions  *string `json:"instructions,omitempty"`
	BridgeChats   *bool   `json:"bridge_chats,omitempty"`
	RetentionDays *int    `json:"retention_days,omitempty"`
}

// MemberKind is the kind of a room member.
type MemberKind string

// Room member kinds.
const (
	MemberKindAgent MemberKind = "agent"
	MemberKindChat  MemberKind = "chat"
	MemberKindUser  MemberKind = "user"
)

// MaxRoomInstructions is the longest room instructions text in characters (INS-01).
const MaxRoomInstructions = 4000

// DeliveryMode selects which room messages an agent member receives (MEM-03, MEM-04): all
// (the default), mentions ("@<display name>" or "@<agent name>", a reply to one of its
// messages, a keyword on word boundaries, or any message of a DM chat) or trusted (only
// messages trusted for the agent, DLV-06).
type DeliveryMode string

// Agent delivery modes.
const (
	DeliveryModeAll      DeliveryMode = "all"
	DeliveryModeMentions DeliveryMode = "mentions"
	DeliveryModeTrusted  DeliveryMode = "trusted"
)

// MaxMemberDisplayName is the longest agent display name in characters (MEM-05).
const MaxMemberDisplayName = 64

// MaxMemberKeywords is the most keywords of an agent member (MEM-05).
const MaxMemberKeywords = 20

// MaxMemberKeyword is the longest keyword in characters (MEM-05).
const MaxMemberKeyword = 64

// RoomMember is one member of a room (GET /rooms/{id}/members).
type RoomMember struct {
	ID       string     `json:"id"`
	Kind     MemberKind `json:"kind"`
	MemberID string     `json:"member_id"` // agent, chat or user id
	Name     string     `json:"name"`      // agent name, chat title or user display name
	// Outbound tells whether agent and user posts without explicit recipients reach this chat (DLV-03).
	Outbound bool `json:"outbound"`
	// Online is set for agent members.
	Online *bool `json:"online,omitempty"`
	// Owner is set for agent members: the username of the agent's owner.
	Owner string `json:"owner,omitempty"`
	// ConnectorKind and ChatKind are set for chat members.
	ConnectorKind string   `json:"connector_kind,omitempty"`
	ChatKind      ChatKind `json:"chat_kind,omitempty"`
	// DisplayName, SignOutbound, Delivery and Keywords are per-room settings of agent members
	// (MEM-01..05); other kinds carry the defaults ("", true, all, []).
	// DisplayName replaces the agent name in outbound headers and the timeline when set.
	DisplayName string `json:"display_name"`
	// SignOutbound adds the author header to the agent's messages sent to chats.
	SignOutbound bool         `json:"sign_outbound"`
	Delivery     DeliveryMode `json:"delivery"`
	// Keywords count as mentions (on word boundaries) when Delivery is mentions.
	Keywords  []string  `json:"keywords"`
	CreatedAt time.Time `json:"created_at"`
}

// AddMemberRequest is the body of POST /rooms/{id}/members. Outbound defaults to true.
type AddMemberRequest struct {
	Kind     MemberKind `json:"kind"`
	MemberID string     `json:"member_id"`
	Outbound *bool      `json:"outbound,omitempty"`
}

// UpdateMemberRequest is the body of PATCH /rooms/{id}/members/{mid}; absent fields are
// unchanged. DisplayName, SignOutbound, Delivery and Keywords apply to agent members only
// (422 for other kinds); limits MaxMemberDisplayName, MaxMemberKeywords, MaxMemberKeyword.
type UpdateMemberRequest struct {
	Outbound     *bool         `json:"outbound,omitempty"`
	DisplayName  *string       `json:"display_name,omitempty"`
	SignOutbound *bool         `json:"sign_outbound,omitempty"`
	Delivery     *DeliveryMode `json:"delivery,omitempty"`
	Keywords     *[]string     `json:"keywords,omitempty"`
}

// AuthorKind is who wrote a message.
type AuthorKind string

// Message author kinds.
const (
	AuthorKindAgent  AuthorKind = "agent"
	AuthorKindChat   AuthorKind = "chat"
	AuthorKindUser   AuthorKind = "user"
	AuthorKindSystem AuthorKind = "system"
)

// MessageKind classifies message content; non-text kinds mostly come from chat platforms.
type MessageKind string

// Message kinds.
const (
	MessageKindText     MessageKind = "text"
	MessageKindImage    MessageKind = "image"
	MessageKindVideo    MessageKind = "video"
	MessageKindAudio    MessageKind = "audio"
	MessageKindDocument MessageKind = "document"
	MessageKindSticker  MessageKind = "sticker"
	MessageKindReaction MessageKind = "reaction"
	MessageKindEdit     MessageKind = "edit"
	MessageKindRevoke   MessageKind = "revoke"
	MessageKindLocation MessageKind = "location"
	MessageKindContact  MessageKind = "contact"
	MessageKindPoll     MessageKind = "poll"
	MessageKindSystem   MessageKind = "system"
	MessageKindUnknown  MessageKind = "unknown"
)

// Message is a stored room message (GET /rooms/{id}/messages, live room.message).
type Message struct {
	ID         string     `json:"id"`
	RoomID     string     `json:"room_id"`
	Seq        int64      `json:"seq"`
	AuthorKind AuthorKind `json:"author_kind"`
	AuthorID   string     `json:"author_id,omitempty"` // agent id, chat id or user id; empty for system
	AuthorName string     `json:"author_name"`
	// AuthorUserID is the user the author resolves to (DLV-06): the user, the agent's owner or
	// the owner of a verified chat identity. Empty for unverified chat senders and system messages.
	AuthorUserID string `json:"author_user_id,omitempty"`
	// SenderID, SenderAltID and SenderName identify the person inside a chat (chat authors only).
	SenderID    string      `json:"sender_id,omitempty"`
	SenderAltID string      `json:"sender_alt_id,omitempty"`
	SenderName  string      `json:"sender_name,omitempty"`
	Kind        MessageKind `json:"kind"`
	Text        string      `json:"text"`
	ReplyTo     string      `json:"reply_to,omitempty"`   // message id
	Recipients  []string    `json:"recipients,omitempty"` // explicit chat member ids (DLV-04)
	ExternalID  string      `json:"external_id,omitempty"`
	// Chat is the chat a chat-authored message came from; absent for other authors (CTX-01).
	Chat        *ChatRef     `json:"chat,omitempty"`
	Attachments []Attachment `json:"attachments"`
	Outbox      []OutboxItem `json:"outbox,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
}

// PostMessageRequest is the body of POST /rooms/{id}/messages (JSON), or the JSON of the
// "message" part of a multipart upload whose files are sent as "file" parts.
type PostMessageRequest struct {
	Text       string   `json:"text"`
	ReplyTo    string   `json:"reply_to,omitempty"`
	Recipients []string `json:"recipients,omitempty"` // chat member ids; empty = every chat with outbound on
}

// AttachmentStatus is the storage state of an attachment.
type AttachmentStatus string

// Attachment states.
const (
	AttachmentStatusStored      AttachmentStatus = "stored"
	AttachmentStatusTooLarge    AttachmentStatus = "too_large"
	AttachmentStatusUnavailable AttachmentStatus = "unavailable"
)

// Attachment is a file of a message. Download with GET /attachments/{id}
// (agents: GET /agent/attachments/{id}).
type Attachment struct {
	ID     string           `json:"id"`
	Name   string           `json:"name"`
	Mime   string           `json:"mime"`
	Size   int64            `json:"size"`
	Status AttachmentStatus `json:"status"`
}

// OutboxStatus is the state of one outbound delivery to a chat.
type OutboxStatus string

// Outbound delivery states (DLV-07).
const (
	OutboxStatusPending OutboxStatus = "pending"
	OutboxStatusSending OutboxStatus = "sending"
	OutboxStatusSent    OutboxStatus = "sent"
	OutboxStatusFailed  OutboxStatus = "failed"
)

// OutboxItem is the delivery of a message to one chat.
type OutboxItem struct {
	ID        string       `json:"id"`
	ChatID    string       `json:"chat_id"`
	Status    OutboxStatus `json:"status"`
	Attempts  int          `json:"attempts"`
	LastError string       `json:"last_error,omitempty"`
	UpdatedAt time.Time    `json:"updated_at"`
}

// RoomRef names a room inside agent frames and events.
type RoomRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ActivityItem is one entry of GET /activity: a message with the room it was posted in.
type ActivityItem struct {
	Room    RoomRef `json:"room"`
	Message Message `json:"message"`
}

// Activity feed page sizes (GET /activity?limit=).
const (
	DefaultActivityLimit = 20
	MaxActivityLimit     = 50
)
