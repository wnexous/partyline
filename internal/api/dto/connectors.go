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
	// Generated is true when the server fills the field if it is left empty on create (the
	// webhook secret): forms may leave it empty even when Required. An empty value in a
	// PATCH keeps the stored one.
	Generated bool `json:"generated,omitempty"`
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

// ConnectorRef names a connector where only its identity is needed: the account behind a
// person.
type ConnectorRef struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Connector is a configured platform account (GET /connectors). Secret config values are
// returned as SecretMask when set.
type Connector struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
	// OrgID is the organization the connection belongs to (ORG-02): its members see it and its admins
	// manage it.
	OrgID string `json:"org_id"`
	// Owner is the user who created the connection. It gives nothing (AD-019).
	Owner User `json:"owner"`
	// MyLevel is manage for the admins of the connection's organization and use for its other members.
	MyLevel   Level           `json:"my_level"`
	Enabled   bool            `json:"enabled"`
	Status    ConnectorStatus `json:"status"`
	LastError string          `json:"last_error,omitempty"`
	// Operator is the AI of the connection's organization the platform account belongs to: what arrives
	// on the account goes to that AI's session, and only it sends through the account (CID-01, ORG-07).
	// Absent while nobody operates the connection: it then runs nothing and its status says so (CID-02).
	Operator *IntelligenceRef `json:"operator,omitempty"`
	// Deliver and GroupsMentionsOnly choose what reaches the operator (CID-12, CID-27): the chats
	// of the connection that are delivered, and whether a group message needs to mention the
	// account or reply to it. A new connection delivers everything with mentions-only on.
	Deliver            ConnectorDeliver `json:"deliver"`
	GroupsMentionsOnly bool             `json:"groups_mentions_only"`
	// Queued is how many messages of the connection wait for their AI's session to acknowledge
	// them (CID-23). Their text is never part of any connector view.
	Queued int64 `json:"queued"`
	// AccountLabel is how the platform names the account once connected: "@bot",
	// "+•••• 1234" (ACC-07); absent until then.
	AccountLabel string         `json:"account_label,omitempty"`
	Config       map[string]any `json:"config" tstype:"{ [key: string]: unknown }"`
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

// CreateConnectorRequest is the body of POST /connectors. Enabled defaults to true. OrgID is the id
// or slug of the organization the connection belongs to: the caller must be an admin of it, else 403
// (ORG-03). Absent, it is the caller's default organization.
type CreateConnectorRequest struct {
	OrgID   string         `json:"org_id,omitempty"`
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
	// Deliver and GroupsMentionsOnly change what reaches the operator (CID-12): Deliver must be
	// dms, groups or all (422 on deliver otherwise). Only the admins of the connection's organization
	// may change them.
	Deliver            *ConnectorDeliver `json:"deliver,omitempty"`
	GroupsMentionsOnly *bool             `json:"groups_mentions_only,omitempty"`
	// OperatorID changes the operator (CID-01): the id of an AI whose home is the connection's
	// organization (ORG-07); anything else, an empty string included, is a 422 on operator_id. Only the
	// admins of the organization may change it. Absent leaves it.
	OperatorID *string `json:"operator_id,omitempty"`
}

// ConnectorDeliver selects which chats of a connection reach its operator: direct chats only,
// group chats only (group and channel chats) or all of them (CID-12).
type ConnectorDeliver string

// Delivery choices.
const (
	ConnectorDeliverDMs    ConnectorDeliver = "dms"
	ConnectorDeliverGroups ConnectorDeliver = "groups"
	ConnectorDeliverAll    ConnectorDeliver = "all"
)

// ConnectorNoOperator is the status detail of a connection nobody operates (CID-02): an admin of its
// organization has to choose the AI that operates it before it runs.
const ConnectorNoOperator = "choose the AI that operates this connection"

// PlatformSendStatus is the state of what an AI sent through a connection (CID-18): pending while
// the platform has not taken the message and Partyline retries it, sent once it has, failed when
// the attempts ran out.
type PlatformSendStatus string

// Platform send states.
const (
	PlatformSendStatusPending PlatformSendStatus = "pending"
	PlatformSendStatusSending PlatformSendStatus = "sending"
	PlatformSendStatusSent    PlatformSendStatus = "sent"
	PlatformSendStatusFailed  PlatformSendStatus = "failed"
)

// SendToChatRequest is the body of POST /agent/connections/{id}/chats/{chat}/messages: the text an
// AI writes, as its own account, to a chat of a connection it operates (CID-16). The answer is a
// PlatformSendResult.
type SendToChatRequest struct {
	Text string `json:"text"`
}

// PlatformSendResult is what an AI is told about an answer it sent through its connection (CID-15,
// CID-16): the id of the send and its state. A send the platform took is sent; one it refused or
// did not answer in time is pending, Error says why and Partyline retries it (CID-18).
type PlatformSendResult struct {
	ID     string             `json:"id"`
	Status PlatformSendStatus `json:"status"`
	Error  string             `json:"error,omitempty"`
}

// ChatKind is the kind of an external chat.
type ChatKind string

// External chat kinds.
const (
	ChatKindGroup   ChatKind = "group"
	ChatKindDM      ChatKind = "dm"
	ChatKindChannel ChatKind = "channel"
)

// Chat is an external conversation seen by a connector (GET /connectors/{id}/chats).
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

// ChatRef names the chat a message came from (CTX-01): Platform is the connector kind
// (whatsapp, telegram, discord, webhook).
type ChatRef struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Kind     ChatKind `json:"kind"`
	Platform string   `json:"platform"`
}

// Identity is a chat account verified as belonging to the current user (GET /identities).
// It is trusted only for messages received through ConnectorID, the connector it was
// linked through.
type Identity struct {
	ID            string    `json:"id"`
	ConnectorID   string    `json:"connector_id"`
	ConnectorKind string    `json:"connector_kind"`
	ExternalID    string    `json:"external_id"`
	AltExternalID string    `json:"alt_external_id,omitempty"`
	Display       string    `json:"display"`
	CreatedAt     time.Time `json:"created_at"`
}

// PairingRequest is the body of POST /identities/pairing: the connection the code is for. The user must
// be a member of its organization, and an admin of it for a webhook connection (IDN-01, IDN-05, AD-016).
type PairingRequest struct {
	ConnectorID string `json:"connector_id"`
}

// PairingCode is returned by POST /identities/pairing; send "!link CODE" or "/link CODE" from
// the chat account, through the connector ConnectorID, before ExpiresAt (IDN-01, IDN-02).
type PairingCode struct {
	Code        string    `json:"code"`
	ConnectorID string    `json:"connector_id"`
	ExpiresAt   time.Time `json:"expires_at"`
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
	// MentionsAccount tells that the message mentions the account. A webhook chat is a channel,
	// so while the connection's groups_mentions_only is on a message without it is not
	// delivered (CID-12).
	MentionsAccount bool `json:"mentions_account,omitempty"`
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
