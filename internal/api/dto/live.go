package dto

import "time"

// LiveEventType is the type of a frame on the live WebSocket (GET /live). Every frame is
// {"type": ..., "data": ...}; a user only receives events about resources they can access.
type LiveEventType string

// Live event types.
const (
	LiveEventTypeHello           LiveEventType = "hello"
	LiveEventTypeRoomMessage     LiveEventType = "room.message"
	LiveEventTypeRoomChanged     LiveEventType = "room.changed"
	LiveEventTypeRoomDeleted     LiveEventType = "room.deleted"
	LiveEventTypeMembersChanged  LiveEventType = "room.members"
	LiveEventTypeAccessChanged   LiveEventType = "access.changed"
	LiveEventTypeAgentPresence   LiveEventType = "agent.presence"
	LiveEventTypeConnectorStatus LiveEventType = "connector.status"
	LiveEventTypeChatDiscovered  LiveEventType = "chat.discovered"
	LiveEventTypeOutboxStatus    LiveEventType = "outbox.status"
	LiveEventTypeIdentityLinked  LiveEventType = "identity.linked"
	LiveEventTypeLagged          LiveEventType = "lagged"
)

//tygo:emit
var _ = `export type LiveEvent =
  | LiveHello
  | LiveRoomMessage
  | LiveRoomChanged
  | LiveRoomDeleted
  | LiveMembersChanged
  | LiveAccessChanged
  | LiveAgentPresence
  | LiveConnectorStatus
  | LiveChatDiscovered
  | LiveOutboxStatus
  | LiveIdentityLinked
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

// LiveMembersChanged reports that a room's members changed; refetch GET /rooms/{id}/members.
type LiveMembersChanged struct {
	Type LiveEventType `json:"type" tstype:"typeof LiveEventTypeMembersChanged"`
	Data RoomIDData    `json:"data"`
}

// RoomIDData names the room an event is about.
type RoomIDData struct {
	RoomID string `json:"room_id"`
}

// LiveAccessChanged reports that sharing of a resource changed; refetch it.
type LiveAccessChanged struct {
	Type LiveEventType     `json:"type" tstype:"typeof LiveEventTypeAccessChanged"`
	Data AccessChangedData `json:"data"`
}

// AccessChangedData names the resource whose grants changed.
type AccessChangedData struct {
	ResourceKind string `json:"resource_kind"` // room, agent or connector
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

// LiveOutboxStatus reports that an outbound delivery changed status (LIVE-04).
type LiveOutboxStatus struct {
	Type LiveEventType `json:"type" tstype:"typeof LiveEventTypeOutboxStatus"`
	Data OutboxUpdate  `json:"data"`
}

// OutboxUpdate is an outbox item with the message and room it belongs to.
type OutboxUpdate struct {
	RoomID     string `json:"room_id"`
	MessageID  string `json:"message_id"`
	OutboxItem `tstype:",extends"`
}

// LiveIdentityLinked reports that a chat identity was linked to the user (UI-18).
type LiveIdentityLinked struct {
	Type LiveEventType `json:"type" tstype:"typeof LiveEventTypeIdentityLinked"`
	Data Identity      `json:"data"`
}

// LiveLagged reports that the server dropped events for this connection; refetch visible data.
type LiveLagged struct {
	Type LiveEventType `json:"type" tstype:"typeof LiveEventTypeLagged"`
}
