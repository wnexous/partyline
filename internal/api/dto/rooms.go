package dto

import "time"

// Room is a conversation of intelligences, a group or a DM (GET /rooms, GET /rooms/{id}).
type Room struct {
	ID string `json:"id"`
	// Kind tells a group from a direct message; a DM has no name and exactly two Participants.
	Kind ConversationKind `json:"kind"`
	// Participants are the two intelligences of a DM, in id order; empty for a group.
	Participants []IntelligenceRef `json:"participants"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	// AvatarURL is /api/v1/rooms/{id}/avatar?v=<sha>, the photo a manager gave a group (GAV-01); absent
	// without one and for a DM, which shows the other party. The hash is a cache key: a new photo has a new URL.
	AvatarURL string `json:"avatar_url,omitempty"`
	// Instructions is free text for the room's agents (INS-01), up to MaxRoomInstructions
	// characters; agents read it with the room_info tool.
	Instructions string `json:"instructions"`
	// OrgID is the organization a group belongs to (ORG-02); absent for a DM.
	OrgID string `json:"org_id,omitempty"`
	// OrgIDs are the organizations the room is filed under, as in Conversation.OrgIDs: a group's
	// organization; for a DM the organizations both participants belong to, else the caller's default.
	OrgIDs []string `json:"org_ids"`
	Owner  User     `json:"owner"`
	// MyLevel is the caller's resolved level on this room (ACL-02).
	MyLevel       Level `json:"my_level"`
	RetentionDays int   `json:"retention_days"`
	// AgentsPaused is set by the loop guard (DLV-08) and cleared by POST /rooms/{id}/resume.
	AgentsPaused bool `json:"agents_paused"`
	// CanResume tells whether the caller may call POST /rooms/{id}/resume (DLV-09): whoever manages a
	// group, and for a DM, which nobody manages, whoever can see it - its human participants and whoever
	// manages an AI participant. MyLevel alone does not say so for a DM.
	CanResume bool  `json:"can_resume"`
	LastSeq   int64 `json:"last_seq"`
	// MemberCount counts the room's members.
	MemberCount int       `json:"member_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CreateRoomRequest is the body of POST /rooms. RetentionDays defaults to 90; 0 keeps forever.
type CreateRoomRequest struct {
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	Instructions  string `json:"instructions,omitempty"`
	RetentionDays *int   `json:"retention_days,omitempty"`
}

// UpdateRoomRequest is the body of PATCH /rooms/{id}; absent fields are unchanged.
type UpdateRoomRequest struct {
	Name          *string `json:"name,omitempty"`
	Description   *string `json:"description,omitempty"`
	Instructions  *string `json:"instructions,omitempty"`
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
	Name     string     `json:"name"`      // agent display name, chat title or user display name
	// Intelligence is the profile summary of an agent or user member: handle, kind and avatar
	// next to the name (PROF-03, PROF-09). Absent for chat members.
	Intelligence *IntelligenceRef `json:"intelligence,omitempty"`
	// People are the people seen in a chat member, the most recently seen first and at most
	// MaxChatPeople, each with its platform (GRP-02). Only GET /rooms/{id}/members fills it;
	// absent for other members and for a chat nobody was seen in.
	People []IntelligenceRef `json:"people,omitempty"`
	// Online is set for agent members: whether the AI has a live session.
	Online *bool `json:"online,omitempty"`
	// Owner is set for agent members: the username of the agent's owner.
	Owner string `json:"owner,omitempty"`
	// ConnectorKind and ChatKind are set for chat members.
	ConnectorKind string   `json:"connector_kind,omitempty"`
	ChatKind      ChatKind `json:"chat_kind,omitempty"`
	// DisplayName, Delivery and Keywords are per-room settings of agent members (MEM-02..05);
	// other kinds carry the defaults ("", all, []).
	// DisplayName replaces the agent name in the timeline when set (MEM-02).
	DisplayName string       `json:"display_name"`
	Delivery    DeliveryMode `json:"delivery"`
	// Keywords count as mentions (on word boundaries) when Delivery is mentions.
	Keywords  []string  `json:"keywords"`
	CreatedAt time.Time `json:"created_at"`
}

// MaxChatPeople is the most people listed for one chat member of a room (GRP-02).
const MaxChatPeople = 200

// AddMemberRequest is the body of POST /rooms/{id}/members: an AI or a user. A chat is refused
// (422 chats_not_members, CID-19).
type AddMemberRequest struct {
	Kind     MemberKind `json:"kind"`
	MemberID string     `json:"member_id"`
}

// UpdateMemberRequest is the body of PATCH /rooms/{id}/members/{mid}; absent fields are
// unchanged. All of them apply to agent members only (422 for other kinds); limits
// MaxMemberDisplayName, MaxMemberKeywords, MaxMemberKeyword.
type UpdateMemberRequest struct {
	DisplayName *string       `json:"display_name,omitempty"`
	Delivery    *DeliveryMode `json:"delivery,omitempty"`
	Keywords    *[]string     `json:"keywords,omitempty"`
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
	// Author is the author's intelligence (PROF-03, GRP-03): the user, the AI, the person of a
	// chat or the user a chat sender is linked to. Its display name is AuthorName, so a name
	// set for an AI in the room wins. For system messages and chat senders without an
	// intelligence the id, kind, role and handle are empty.
	Author IntelligenceRef `json:"author"`
	// AuthorUserID is the user the author resolves to (DLV-06): the user, the agent's owner or
	// the owner of a verified chat identity. Empty for unverified chat senders and system messages.
	AuthorUserID string `json:"author_user_id,omitempty"`
	// SenderID, SenderAltID and SenderName identify the person inside a chat (chat authors only).
	SenderID    string      `json:"sender_id,omitempty"`
	SenderAltID string      `json:"sender_alt_id,omitempty"`
	SenderName  string      `json:"sender_name,omitempty"`
	Kind        MessageKind `json:"kind"`
	Text        string      `json:"text"`
	ReplyTo     string      `json:"reply_to,omitempty"` // message id
	// Recipients are the chats a message posted before the connection proxy named (DLV-04); a
	// message posted since names none, because a room sends to no chat (CID-22).
	Recipients []string `json:"recipients,omitempty"`
	ExternalID string   `json:"external_id,omitempty"`
	// Chat is the chat a chat-authored message came from; absent for other authors (CTX-01).
	Chat        *ChatRef     `json:"chat,omitempty"`
	Attachments []Attachment `json:"attachments"`
	// Reactions are the emoji people reacted with, in the order each was first used; always an array
	// (RCT-05). A live frame carries them too.
	Reactions []Reaction `json:"reactions"`
	CreatedAt time.Time  `json:"created_at"`
	// EditedAt is when the author last changed the text; absent for a message that was never edited (EDT-01).
	EditedAt *time.Time `json:"edited_at,omitempty"`
	// DeletedAt is when the message was deleted; absent otherwise. A deleted message is a placeholder that
	// stays in its place: its text is empty, it has no attachments and no EditedAt, and it keeps its id,
	// sequence number, author, time and reply_to (DLT-01). It has no reactions either.
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// EditMessageRequest is the body of PATCH /rooms/{id}/messages/{mid} and of PATCH /agent/messages/{id}: the new
// text of a message its author wrote. The answer is the message (EDT-01). The same rules as a post apply to the
// text (MSG-02), except that a message with attachments may have none (EDT-04).
type EditMessageRequest struct {
	Text string `json:"text"`
}

// PostMessageRequest is the body of POST /rooms/{id}/messages (JSON), or the JSON of the
// "message" part of a multipart upload whose files are sent as "file" parts.
type PostMessageRequest struct {
	Text    string `json:"text"`
	ReplyTo string `json:"reply_to,omitempty"`
	// Recipients, sent by v0.4 CLIs, is accepted and ignored: a room sends to no chat (CID-22).
	Recipients []string `json:"recipients,omitempty"`
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

// RoomRef names a room inside agent frames and events. In the frames an AI receives, a DM is
// named by the display name of the other participant and marked Kind dm (DM-03); a group
// carries no Kind.
type RoomRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Kind is dm for a direct message; absent for a group.
	Kind ConversationKind `json:"kind,omitempty"`
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
