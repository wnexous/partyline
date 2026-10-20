package dto

import "time"

// FieldType is the input type of a connector config field (AD-007).
type FieldType string

// Connector config field types.
const (
	FieldTypeString     FieldType = "string"
	FieldTypeURL        FieldType = "url"
	FieldTypeSecret     FieldType = "secret"
	FieldTypeBool       FieldType = "bool"
	FieldTypeStringList FieldType = "string_list"
)

// ConnectorField describes one config field; the web app renders forms from it (CON-01).
type ConnectorField struct {
	Name     string    `json:"name"`
	Label    string    `json:"label"`
	Type     FieldType `json:"type"`
	Required bool      `json:"required"`
	Secret   bool      `json:"secret"` // true when Type is secret: never returned in clear (CON-03)
	Help     string    `json:"help,omitempty"`
	Default  any       `json:"default,omitempty" tstype:"unknown"`
}

// ConnectorKind is a supported platform (GET /connector-kinds).
type ConnectorKind struct {
	Name         string           `json:"name"`
	Label        string           `json:"label"`
	Fields       []ConnectorField `json:"fields"`
	CanListChats bool             `json:"can_list_chats"`
}

// ConnectorStatus is the runtime state of a connector.
type ConnectorStatus string

// Connector states.
const (
	ConnectorStatusStopped    ConnectorStatus = "stopped"
	ConnectorStatusConnecting ConnectorStatus = "connecting"
	ConnectorStatusConnected  ConnectorStatus = "connected"
	ConnectorStatusError      ConnectorStatus = "error"
)

// Connector is a configured platform account (GET /connectors). Secret config values are
// returned as SecretMask when set.
type Connector struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Name      string          `json:"name"`
	Owner     User            `json:"owner"`
	MyLevel   Level           `json:"my_level"` // manage for the owner, use for grantees
	Enabled   bool            `json:"enabled"`
	Status    ConnectorStatus `json:"status"`
	LastError string          `json:"last_error,omitempty"`
	Config    map[string]any  `json:"config" tstype:"{ [key: string]: unknown }"`
	// InboundURL is where a webhook connector receives messages
	// (<public URL>/api/v1/hooks/<id>); empty for other kinds.
	InboundURL string `json:"inbound_url,omitempty"`
	// GeneratedSecrets holds the secrets Partyline generated for the connector (the
	// webhook's secret), by field name. Only the create response carries them; afterwards
	// they read as SecretMask like every secret.
	GeneratedSecrets map[string]string `json:"generated_secrets,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// CreateConnectorRequest is the body of POST /connectors. Enabled defaults to true.
type CreateConnectorRequest struct {
	Kind    string         `json:"kind"`
	Name    string         `json:"name"`
	Config  map[string]any `json:"config" tstype:"{ [key: string]: unknown }"`
	Enabled *bool          `json:"enabled,omitempty"`
}

// UpdateConnectorRequest is the body of PATCH /connectors/{id}; absent fields are unchanged and
// a secret sent as SecretMask keeps its stored value.
type UpdateConnectorRequest struct {
	Name    *string        `json:"name,omitempty"`
	Config  map[string]any `json:"config,omitempty" tstype:"{ [key: string]: unknown }"`
	Enabled *bool          `json:"enabled,omitempty"`
}

// ChatKind is the kind of an external chat.
type ChatKind string

// External chat kinds.
const (
	ChatKindGroup   ChatKind = "group"
	ChatKindDM      ChatKind = "dm"
	ChatKindChannel ChatKind = "channel"
)

// Chat is an external conversation seen by a connector (GET /connectors/{id}/chats, GET /chats).
type Chat struct {
	ID            string     `json:"id"`
	ConnectorID   string     `json:"connector_id"`
	ConnectorKind string     `json:"connector_kind"`
	ConnectorName string     `json:"connector_name"`
	ExternalID    string     `json:"external_id"`
	Title         string     `json:"title"`
	Kind          ChatKind   `json:"kind"`
	LastSeenAt    *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// RouteChatRequest is the body of POST /chats/{id}/route; the response is the new private Room
// with the chat and the agent as members (UI-13).
type RouteChatRequest struct {
	AgentID string `json:"agent_id"`
}

// Identity is a chat account verified as belonging to the current user (GET /identities).
type Identity struct {
	ID            string    `json:"id"`
	ConnectorKind string    `json:"connector_kind"`
	ExternalID    string    `json:"external_id"`
	AltExternalID string    `json:"alt_external_id,omitempty"`
	Display       string    `json:"display"`
	CreatedAt     time.Time `json:"created_at"`
}

// PairingCode is returned by POST /identities/pairing; send "!link CODE" or "/link CODE" from
// the chat account before ExpiresAt (IDN-01).
type PairingCode struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

// HookInboundRequest is the body of POST /hooks/{connector_id}, signed with HeaderSignature
// and HeaderTimestamp (HOOK-01). The response is 202 with no body.
type HookInboundRequest struct {
	ChatID     string `json:"chat_id"`
	ChatTitle  string `json:"chat_title,omitempty"`
	SenderID   string `json:"sender_id"`
	SenderName string `json:"sender_name,omitempty"`
	Text       string `json:"text"`
	MessageID  string `json:"message_id,omitempty"`
}

// HookOutboundEvent is the JSON a webhook connector POSTs to its outbound URL (HOOK-03).
type HookOutboundEvent struct {
	Event       string           `json:"event"` // always "message"
	ConnectorID string           `json:"connector_id"`
	ChatID      string           `json:"chat_id"`
	Message     HookMessage      `json:"message"`
	Attachments []HookAttachment `json:"attachments"`
}

// HookMessage is the message inside a HookOutboundEvent.
type HookMessage struct {
	ID                string    `json:"id"`
	RoomID            string    `json:"room_id"`
	Text              string    `json:"text"`
	ReplyToExternalID string    `json:"reply_to_external_id,omitempty"`
	AuthorName        string    `json:"author_name"`
	CreatedAt         time.Time `json:"created_at"`
}

// HookAttachment is a file inside a HookOutboundEvent; URL is a signed link valid for 10 minutes.
type HookAttachment struct {
	Name string `json:"name"`
	Mime string `json:"mime"`
	Size int64  `json:"size"`
	URL  string `json:"url"`
}
