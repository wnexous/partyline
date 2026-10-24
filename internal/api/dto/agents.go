package dto

import (
	"fmt"
	"time"
)

// Agent is an AI as the agents list shows it (GET /agents, and GET /agent/agents for the AIs a token's
// user hosts): its profile, owner, home organization, folder, app and presence.
type Agent struct {
	ID string `json:"id"`
	// Intelligence is the AI's profile: the display name people read, its @handle and avatar.
	Intelligence IntelligenceRef `json:"intelligence"`
	Description  string          `json:"description"`
	Harness      string          `json:"harness"` // claude, opencode, pi, omp, codex, gemini or agent
	Machine      string          `json:"machine"` // the session's hostname; empty when unknown
	// Organization is the AI's home organization (ORG-02); absent for an AI that has none.
	Organization *ContactOrg `json:"organization,omitempty"`
	// Folder is the folder the AI works in on its host's computer; empty for an AI without one.
	Folder string `json:"folder"`
	// FolderLabel is Folder as the host's home folder reads it: "~", "~/x" under it, the absolute path
	// elsewhere; empty without a folder.
	FolderLabel string     `json:"folder_label"`
	Owner       User       `json:"owner"`
	MyLevel     Level      `json:"my_level"` // manage for whoever manages the AI in an organization they share, use otherwise
	Online      bool       `json:"online"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// MaxAgentDisplayName is the longest agent display name in characters (ID-04).
const MaxAgentDisplayName = 64

// UpdateAgentRequest is the body of PATCH /agents/{id} (owner only); absent fields are
// unchanged.
type UpdateAgentRequest struct {
	Description *string `json:"description,omitempty"`
	// DisplayName renames the agent everywhere: 1 to MaxAgentDisplayName characters (ID-04).
	DisplayName *string `json:"display_name,omitempty"`
	// Fixed is accepted for clients from before folders: every AI is permanent, so true changes
	// nothing and false is refused (ID-05).
	Fixed *bool `json:"fixed,omitempty"`
}

// AgentRoom is a room as seen by an agent (GET /agent/rooms, TOOL-09).
type AgentRoom struct {
	ID   string           `json:"id"`
	Kind ConversationKind `json:"kind"`
	// Name is the group's name; for a DM it is the display name of the other participant.
	Name        string `json:"name"`
	Description string `json:"description"`
	// With is the other participant of a DM, relative to the AI; absent for a group.
	With    *IntelligenceRef `json:"with,omitempty"`
	Members []RoomMember     `json:"members"`
}

// AgentMessage is a message delivered to an agent. Trusted is computed for the receiving
// agent when the message is delivered: true only when its author manages the agent in the
// agent's home organization (POS-10, AD-018). Its owner, its other AIs and its peers do not.
type AgentMessage struct {
	Room    RoomRef `json:"room"`
	Trusted bool    `json:"trusted"`
	Message Message `json:"message"`
}

// RoomInfo is GET /agent/rooms/{room}/info (INS-02): the room, its instructions, its members
// and the calling agent's own membership (settings included).
type RoomInfo struct {
	Room         RoomRef `json:"room"`
	Instructions string  `json:"instructions"`
	// InstructionsBy is the username of the user who last saved the instructions; empty when
	// unknown (INS-04).
	InstructionsBy string `json:"instructions_by,omitempty"`
	// InstructionsTrusted is true only when that user manages the calling agent in the agent's
	// home organization (INS-04, POS-10).
	InstructionsTrusted bool `json:"instructions_trusted"`
	// RoomTextTrusted is true only when whoever created the room or last changed its name or
	// description manages the calling agent in the agent's home organization (INS-06, POS-10).
	RoomTextTrusted bool         `json:"room_text_trusted"`
	Members         []RoomMember `json:"members"`
	Me              RoomMember   `json:"me"`
}

// MergeAgentRequest is the body of POST /agents/{id}/merge: the AI the one in the path is merged
// into. Both are the caller's; the first is deleted and everything it had moves to the second
// (MRG-01).
type MergeAgentRequest struct {
	Into string `json:"into"`
}

// ReactRequest is the body of POST /agent/messages/{id}/react: one emoji (REACT-01).
type ReactRequest struct {
	Emoji string `json:"emoji"`
}

// ReplyRequest is the body of POST /agent/messages/{id}/reply (TOOL-02).
type ReplyRequest struct {
	Text string `json:"text"`
}

// Agent endpoints reuse other DTOs: POST /agent/rooms/{room}/messages takes PostMessageRequest
// (JSON or multipart) and returns Message; GET /agent/rooms/{room}/messages?before=<message id>&limit=
// returns Page[AgentMessage].

// AgentFrameType is the type of an agent WebSocket frame (GET /agent/ws).
type AgentFrameType string

// Agent WebSocket frame types.
const (
	AgentFrameTypeWelcome     AgentFrameType = "welcome"      // server → channel
	AgentFrameTypeMessage     AgentFrameType = "message"      // server → channel
	AgentFrameTypeNotice      AgentFrameType = "notice"       // server → channel
	AgentFrameTypeAck         AgentFrameType = "ack"          // channel → server
	AgentFrameTypePlatform    AgentFrameType = "platform"     // server → channel, sessions that opted in (FeaturePlatform)
	AgentFrameTypePlatformAck AgentFrameType = "platform_ack" // channel → server
)

// AgentInfo identifies the connected agent in the welcome frame.
type AgentInfo struct {
	ID string `json:"id"`
	// Name is the AI's @handle.
	Name string `json:"name"`
	// DisplayName is the name people read: the AI's profile display name.
	DisplayName string `json:"display_name"`
	Owner       string `json:"owner"` // owner's username
}

// AgentConnection is a connection (an account on a platform) the AI operates, as the welcome
// frame lists it (CID-01): the channel tells the harness which numbers are its own.
type AgentConnection struct {
	ID string `json:"id"`
	// Platform is the connector kind: whatsapp, telegram, discord or webhook.
	Platform string `json:"platform"`
	// AccountLabel names the account (a masked number or a bot name); empty while unknown.
	AccountLabel string `json:"account_label"`
}

// AgentWelcomeFrame is the first frame after the agent WebSocket opens.
type AgentWelcomeFrame struct {
	Type  AgentFrameType `json:"type" tstype:"typeof AgentFrameTypeWelcome"`
	Agent AgentInfo      `json:"agent"`
	// Intelligence is the AI the session is, and Owner its owner: the channel tells the harness
	// who it is and who it works for.
	Intelligence IntelligenceRef `json:"intelligence"`
	Owner        IntelligenceRef `json:"owner"`
	// Connections are the connections the AI operates, by name; always present, empty when it
	// operates none (CID-01).
	Connections []AgentConnection `json:"connections"`
}

// SessionState is GET /agent/session: what the server knows about the AI of the machine and
// folder named in the headers, without creating anything (SES-01). Online tells whether the AI
// has a live session; the launcher asks before taking it over.
type SessionState struct {
	Exists bool `json:"exists"`
	Online bool `json:"online"`
	// Intelligence is the AI; absent when Exists is false.
	Intelligence *IntelligenceRef `json:"intelligence,omitempty"`
	// App is the id of the harness the AI currently runs in; empty when Exists is false.
	App string `json:"app"`
	// ConnectedAt is when the live session connected; absent while the AI is offline.
	ConnectedAt *time.Time `json:"connected_at,omitempty"`
}

// AgentMessageFrame pushes one room message (backlog first, then live).
type AgentMessageFrame struct {
	Type         AgentFrameType `json:"type" tstype:"typeof AgentFrameTypeMessage"`
	AgentMessage `tstype:",extends"`
}

// AgentNoticeFrame tells the agent about skipped backlog (AGT-09) and similar events.
type AgentNoticeFrame struct {
	Type AgentFrameType `json:"type" tstype:"typeof AgentFrameTypeNotice"`
	Room RoomRef        `json:"room"`
	Text string         `json:"text"`
}

// DroppedNotice is the text that tells an AI how many of its platform messages the server dropped, for
// being older than 7 days or over the bound of 5,000 queued (CID-10). The WebSocket notice and the
// output of the harnesses that poll (the turn-end hooks and wait_for_messages) share it.
func DroppedNotice(n int64) string {
	return fmt.Sprintf("%d platform messages were dropped (older than 7 days or over 5,000 queued)", n)
}

// AgentAckFrame confirms a message was handed to Claude Code (AGT-07).
type AgentAckFrame struct {
	Type      AgentFrameType `json:"type" tstype:"typeof AgentFrameTypeAck"`
	MessageID string         `json:"message_id"`
}

// PlatformEvent is a message that arrived on a connection the AI operates, as its session
// receives it (CID-06): never a message of a Partyline conversation. ID is what the AI answers
// with (reply); ChatID names the chat for send_message together with ConnectionID. A chat of
// another connection, a message of the account itself (FromSelf) and a sender whose account is not
// linked to a user who manages the AI in its home organization are never trusted (CID-13, CID-14,
// POS-10).
type PlatformEvent struct {
	ID           string `json:"id"`
	Platform     string `json:"platform"` // whatsapp, telegram, discord or webhook
	ConnectionID string `json:"connection_id"`
	// ChatID is Partyline's id of the chat, not the platform's.
	ChatID     string   `json:"chat_id"`
	ChatTitle  string   `json:"chat_title"`
	ChatKind   ChatKind `json:"chat_kind"`
	SenderName string   `json:"sender_name"`
	SenderID   string   `json:"sender_id"`
	// Trusted is true only when the sender's account is linked, through this connection, to a user
	// who manages the AI in its home organization (POS-10, AD-018). It is decided when the message
	// arrives.
	Trusted bool `json:"trusted"`
	// FromSelf is true for a message written from the connection's own account on the platform
	// itself (typed on its device), which Partyline did not send.
	FromSelf bool        `json:"from_self"`
	Kind     MessageKind `json:"kind"`
	Text     string      `json:"text"`
	// Attachments are downloadable until 24 hours after the session acknowledged the event.
	Attachments []Attachment `json:"attachments"`
	ReceivedAt  time.Time    `json:"received_at"`
}

// AgentPlatformFrame pushes one platform event to a session that opted in with FeaturePlatform:
// the AI's queued messages first, oldest first, then live. At most 50 are in flight; the session
// acknowledges each with an AgentPlatformAckFrame, which deletes its text (CID-08, CID-09).
type AgentPlatformFrame struct {
	Type  AgentFrameType `json:"type" tstype:"typeof AgentFrameTypePlatform"`
	Event PlatformEvent  `json:"event"`
}

// AgentPlatformAckFrame confirms a platform event was handed to the harness (CID-09).
type AgentPlatformAckFrame struct {
	Type   AgentFrameType `json:"type" tstype:"typeof AgentFrameTypePlatformAck"`
	ItemID string         `json:"item_id"`
}

// AgentPending is the answer of GET /agent/pending?platform=1 (CID-06): the undelivered room
// messages, exactly as the call without the parameter returns them (a List of AgentMessage), and
// the AI's queued platform events, oldest first, at most 20 per call like the room messages. Both
// lists count as delivered once returned: the platform events lose their text (CID-09).
type AgentPending struct {
	Items    []AgentMessage  `json:"items"`
	Platform []PlatformEvent `json:"platform"`
	// Dropped is how many of the AI's platform messages the server dropped since it last told the AI,
	// for being older than 7 days or over the bound of 5,000 (CID-10): the WebSocket's notice for the
	// harnesses that poll (DroppedNotice words it). The call that returns it resets it, so it comes
	// once, and it ends a wait like a message does. Always present; 0 when nothing was dropped.
	Dropped int64 `json:"dropped"`
}

// AgentDMRequest is the body of POST /agent/dms (DM-04): the AI writes to an intelligence its
// owner can see, opening the DM when there is none. To is the target's "@handle", handle or id;
// ChatIDs, sent by v0.4 CLIs, are accepted and ignored: a DM sends nothing to a chat (CID-22).
type AgentDMRequest struct {
	To      string   `json:"to"`
	Text    string   `json:"text"`
	ChatIDs []string `json:"chat_ids,omitempty"`
}

// CreatedGroup is the answer of POST /agent/groups (AGRP-01): the new group's room id and name, and
// the organization it belongs to (ORG-02). Its request is a CreateGroupRequest whose members are
// "@handle", handle or id.
type CreatedGroup struct {
	Room    string `json:"room"`
	Name    string `json:"name"`
	OrgID   string `json:"org_id"`
	OrgSlug string `json:"org_slug"`
	OrgName string `json:"org_name"`
}
