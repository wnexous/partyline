package dto

import "time"

// AgentKind tells whether an agent was named by its owner or by the server.
type AgentKind string

// Agent kinds (ID-01, ID-02).
const (
	// AgentKindFixed is an agent named with --name (or registered before projects existed); it
	// is never archived.
	AgentKindFixed AgentKind = "fixed"
	// AgentKindTemporary is a session named after its project and harness; it is archived after
	// 24 hours offline.
	AgentKindTemporary AgentKind = "temporary"
)

// ProjectRef names the project of an agent (PROJ-01).
type ProjectRef struct {
	ID   string `json:"id"`
	Slug string `json:"slug"` // stable, unique per owner
	Name string `json:"name"` // what the owner reads; renamable (PROJ-05)
}

// Project groups an owner's agents (GET /projects).
type Project struct {
	ID        string    `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	Owner     User      `json:"owner"`
	MyLevel   Level     `json:"my_level"` // manage for the owner, use for grantees
	CreatedAt time.Time `json:"created_at"`
}

// Agent is a coding-agent session registered by an agent token (GET /agents).
type Agent struct {
	ID string `json:"id"`
	// Name is the internal name the agent uses in X-Partyline-Agent.
	Name string `json:"name"`
	// DisplayName is the owner's rename, else "<Project> · <Harness>[ #N]" for temporary
	// agents, else Name (ID-01, ID-04).
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	// Project is absent for agents that belong to no project.
	Project    *ProjectRef `json:"project,omitempty"`
	Harness    string      `json:"harness"` // claude, opencode, pi, omp, codex, gemini or agent
	Machine    string      `json:"machine"` // the session's hostname; empty when unknown
	Kind       AgentKind   `json:"kind"`
	Archived   bool        `json:"archived"`
	Owner      User        `json:"owner"`
	MyLevel    Level       `json:"my_level"` // manage for the owner, use for grantees
	Online     bool        `json:"online"`
	LastSeenAt *time.Time  `json:"last_seen_at,omitempty"`
	CreatedAt  time.Time   `json:"created_at"`
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

// RoomInfo is GET /agent/rooms/{room}/info (INS-02): the room, its instructions, its members
// and the calling agent's own membership (settings included).
type RoomInfo struct {
	Room         RoomRef `json:"room"`
	Instructions string  `json:"instructions"`
	// InstructionsBy is the username of the user who last saved the instructions; empty when
	// unknown (INS-04).
	InstructionsBy string `json:"instructions_by,omitempty"`
	// InstructionsTrusted is true only when that user is the calling agent's owner (INS-04).
	InstructionsTrusted bool `json:"instructions_trusted"`
	// RoomTextTrusted is true only when the calling agent's owner created the room or last
	// changed its name or description (INS-06).
	RoomTextTrusted bool         `json:"room_text_trusted"`
	Members         []RoomMember `json:"members"`
	Me              RoomMember   `json:"me"`
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
	AgentFrameTypeWelcome AgentFrameType = "welcome" // server → channel
	AgentFrameTypeMessage AgentFrameType = "message" // server → channel
	AgentFrameTypeNotice  AgentFrameType = "notice"  // server → channel
	AgentFrameTypeAck     AgentFrameType = "ack"     // channel → server
)

// AgentInfo identifies the connected agent in the welcome frame.
type AgentInfo struct {
	ID string `json:"id"`
	// Name is the agent name to send in X-Partyline-Agent from now on.
	Name string `json:"name"`
	// DisplayName is the name people read (Agent.DisplayName).
	DisplayName string `json:"display_name"`
	Owner       string `json:"owner"` // owner's username
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
