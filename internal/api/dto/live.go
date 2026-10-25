package dto

import "time"

// LiveEventType is the type of a frame on the live WebSocket (GET /live). Every frame is
// {"type": ..., "data": ...}; a user only receives events about resources they can access.
type LiveEventType string

// Live event types.
const (
	LiveEventTypeHello       LiveEventType = "hello"
	LiveEventTypeRoomMessage LiveEventType = "room.message"
	// LiveEventTypeRoomReactions carries the reactions of a message that someone changed (RCT-06).
	LiveEventTypeRoomReactions   LiveEventType = "room.reactions"
	LiveEventTypeRoomChanged     LiveEventType = "room.changed"
	LiveEventTypeRoomDeleted     LiveEventType = "room.deleted"
	LiveEventTypeRoomRead        LiveEventType = "room.read"
	LiveEventTypeMembersChanged  LiveEventType = "room.members"
	LiveEventTypeAccessChanged   LiveEventType = "access.changed"
	LiveEventTypeAgentPresence   LiveEventType = "agent.presence"
	LiveEventTypeConnectorStatus LiveEventType = "connector.status"
	LiveEventTypeChatDiscovered  LiveEventType = "chat.discovered"
	LiveEventTypeIdentityLinked  LiveEventType = "identity.linked"
	// LiveEventTypeIntelligenceChanged carries the new summary of an intelligence whose
	// profile changed.
	LiveEventTypeIntelligenceChanged LiveEventType = "intelligence.changed"
	LiveEventTypeLagged              LiveEventType = "lagged"
	// LiveEventTypeInvitationReceived carries an invitation to an organization sent to the user, or to an AI
	// the user manages (OMEM-01). Its name is the spec's, not the dotted form of the older types.
	LiveEventTypeInvitationReceived LiveEventType = "invitation_received"
	// LiveEventTypeRoomMessageUpdated carries a message that was edited or deleted, as it is now (EDT-06,
	// DLT-06): the client replaces the message it has with the same id.
	LiveEventTypeRoomMessageUpdated LiveEventType = "room.message_updated"
)

//tygo:emit
var _ = `export type LiveEvent =
  | LiveHello
  | LiveRoomMessage
  | LiveRoomMessageUpdated
  | LiveRoomReactions
  | LiveRoomChanged
  | LiveRoomDeleted
  | LiveRoomRead
  | LiveMembersChanged
  | LiveAccessChanged
  | LiveAgentPresence
  | LiveConnectorStatus
  | LiveChatDiscovered
  | LiveIdentityLinked
  | LiveIntelligenceChanged
  | LiveInvitationReceived
  | LiveLagged;
`

// LiveHello is the first frame after the live WebSocket opens.
type LiveHello struct {
	Type LiveEventType `json:"type" tstype:"typeof LiveEventTypeHello"`
	Data HelloData     `json:"data"`
}

// HelloData carries the server time, so clients can correct relative timestamps.
type HelloData struct {
	ServerTime time.Time `json:"server_time"`
}

// LiveRoomMessage reports that a message was stored in a room the user can view (LIVE-01).
type LiveRoomMessage struct {
	Type LiveEventType `json:"type" tstype:"typeof LiveEventTypeRoomMessage"`
	Data Message       `json:"data"`
}

// LiveRoomMessageUpdated reports that a message of a room the user can view was edited or deleted (EDT-06,
// DLT-06). Data is the message as it is now: with EditedAt when it was edited, as a placeholder with DeletedAt
// when it was deleted. It keeps its id and sequence number, so the client puts it where the old one was.
type LiveRoomMessageUpdated struct {
	Type LiveEventType `json:"type" tstype:"typeof LiveEventTypeRoomMessageUpdated"`
	Data Message       `json:"data"`
}

// LiveRoomChanged reports that a room was created or updated.
type LiveRoomChanged struct {
	Type LiveEventType `json:"type" tstype:"typeof LiveEventTypeRoomChanged"`
	Data Room          `json:"data"`
}

// LiveRoomDeleted reports that a room was deleted or the user lost access to it.
type LiveRoomDeleted struct {
	Type LiveEventType `json:"type" tstype:"typeof LiveEventTypeRoomDeleted"`
	Data RoomIDData    `json:"data"`
}

// LiveRoomRead reports that the user marked a room read, on this socket or another one of
// theirs: the unread count of the room is zero up to Seq (DM-07). Only the reader receives it.
type LiveRoomRead struct {
	Type LiveEventType `json:"type" tstype:"typeof LiveEventTypeRoomRead"`
	Data ReadData      `json:"data"`
}

// ReadData is the read position that holds in a room: the newest message the user has read.
type ReadData struct {
	RoomID string `json:"room_id"`
	Seq    int64  `json:"seq"`
}

// LiveMembersChanged reports that a room's members changed; refetch GET /rooms/{id}/members.
type LiveMembersChanged struct {
	Type LiveEventType `json:"type" tstype:"typeof LiveEventTypeMembersChanged"`
	Data RoomIDData    `json:"data"`
}

// RoomIDData names the room an event is about.
type RoomIDData struct {
	RoomID string `json:"room_id"`
}

// LiveAccessChanged reports that who may see a resource, or what an organization is, changed; refetch it.
type LiveAccessChanged struct {
	Type LiveEventType     `json:"type" tstype:"typeof LiveEventTypeAccessChanged"`
	Data AccessChangedData `json:"data"`
}

// AccessChangedData names the resource whose visibility changed, or the organization that changed.
// An organization's members, and a member who has just left it, receive kind organization whenever
// something about it changes (its members, positions, chart or invitations) and look at it again; a member
// whose own standing in it changed (they joined, or their permission or position did) also receives one
// event per AI and connection of the organization, and one per group they gained or lost.
type AccessChangedData struct {
	ResourceKind string `json:"resource_kind"` // room, agent, connector or organization
	ResourceID   string `json:"resource_id"`
}

// LiveAgentPresence reports that an agent went online or offline (LIVE-04).
type LiveAgentPresence struct {
	Type LiveEventType `json:"type" tstype:"typeof LiveEventTypeAgentPresence"`
	Data AgentPresence `json:"data"`
}

// AgentPresence is the online state of an agent.
type AgentPresence struct {
	AgentID    string     `json:"agent_id"`
	Online     bool       `json:"online"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

// LiveConnectorStatus reports that a connector changed status (LIVE-04, CON-04).
type LiveConnectorStatus struct {
	Type LiveEventType       `json:"type" tstype:"typeof LiveEventTypeConnectorStatus"`
	Data ConnectorStatusData `json:"data"`
}

// ConnectorStatusData is the new status of a connector; LastError has secrets redacted.
type ConnectorStatusData struct {
	ConnectorID string          `json:"connector_id"`
	Status      ConnectorStatus `json:"status"`
	LastError   string          `json:"last_error,omitempty"`
}

// LiveChatDiscovered reports that a connector saw a new chat or refreshed one.
type LiveChatDiscovered struct {
	Type LiveEventType `json:"type" tstype:"typeof LiveEventTypeChatDiscovered"`
	Data Chat          `json:"data"`
}

// LiveIdentityLinked reports that a chat identity was linked to the user (UI-18).
type LiveIdentityLinked struct {
	Type LiveEventType `json:"type" tstype:"typeof LiveEventTypeIdentityLinked"`
	Data Identity      `json:"data"`
}

// LiveIntelligenceChanged reports that an intelligence the user can see changed its profile
// (name, handle, bio or avatar); Data is its new summary.
type LiveIntelligenceChanged struct {
	Type LiveEventType   `json:"type" tstype:"typeof LiveEventTypeIntelligenceChanged"`
	Data IntelligenceRef `json:"data"`
}

// LiveInvitationReceived reports that the user, or an AI the user manages, was invited to an organization,
// or invited again with other terms (OMEM-01). Data is the pending invitation, as GET /invitations lists it.
type LiveInvitationReceived struct {
	Type LiveEventType `json:"type" tstype:"typeof LiveEventTypeInvitationReceived"`
	Data Invitation    `json:"data"`
}

// LiveLagged reports that the server dropped events for this connection; refetch visible data.
type LiveLagged struct {
	Type LiveEventType `json:"type" tstype:"typeof LiveEventTypeLagged"`
}
