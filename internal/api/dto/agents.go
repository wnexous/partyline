package dto

import "time"

// Agent is a Claude Code session registered by an agent token (GET /agents).
type Agent struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Owner       User       `json:"owner"`
	MyLevel     Level      `json:"my_level"` // manage for the owner, use for grantees
	Online      bool       `json:"online"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// UpdateAgentRequest is the body of PATCH /agents/{id}.
type UpdateAgentRequest struct {
	Description *string `json:"description,omitempty"`
}

// AgentRoom is a room as seen by an agent (GET /agent/rooms, TOOL-09).
type AgentRoom struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Members     []RoomMember `json:"members"`
}

// AgentMessage is a message delivered to an agent. Trusted is computed for the receiving
// agent: true only when the author resolves to the agent's owner (DLV-06, AD-006).
type AgentMessage struct {
	Room    RoomRef `json:"room"`
	Trusted bool    `json:"trusted"`
	Message Message `json:"message"`
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
	AgentFrameTypeWelcome AgentFrameType = "welcome" // server → channel
	AgentFrameTypeMessage AgentFrameType = "message" // server → channel
	AgentFrameTypeNotice  AgentFrameType = "notice"  // server → channel
	AgentFrameTypeAck     AgentFrameType = "ack"     // channel → server
)

// AgentInfo identifies the connected agent in the welcome frame.
type AgentInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Owner string `json:"owner"` // owner's username
}

// AgentWelcomeFrame is the first frame after the agent WebSocket opens.
type AgentWelcomeFrame struct {
	Type  AgentFrameType `json:"type" tstype:"typeof AgentFrameTypeWelcome"`
	Agent AgentInfo      `json:"agent"`
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

// AgentAckFrame confirms a message was handed to Claude Code (AGT-07).
type AgentAckFrame struct {
	Type      AgentFrameType `json:"type" tstype:"typeof AgentFrameTypeAck"`
	MessageID string         `json:"message_id"`
}
