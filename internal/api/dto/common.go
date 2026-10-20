// Package dto is the single source of the HTTP and WebSocket contract (AD-003). The web app's
// TypeScript types are generated from this package with tygo; never hand-write them.
//
// Conventions: JSON fields are snake_case, ids are UUIDv7 strings, times are RFC 3339 strings,
// optional fields are pointers or carry omitempty. Every path is under /api/v1.
package dto

// Validation rules shared by the API and the web app forms.
const (
	// UsernamePattern is the regular expression every username must match.
	UsernamePattern = "^[a-z0-9_-]{3,32}$"
	// AgentNamePattern is the regular expression every agent name matches after slugifying
	// (lowercase letters, digits, dot, underscore and hyphen; at most 48 characters).
	AgentNamePattern = "^[a-z0-9][a-z0-9._-]{0,47}$"
	// PasswordMinLength is the minimum password length in characters.
	PasswordMinLength = 10
	// MaxTextLength is the maximum message text length in characters (MSG-02).
	MaxTextLength = 16000
	// DefaultPageSize is the page size of paginated lists when no limit is given.
	DefaultPageSize = 50
	// MaxPageSize is the largest accepted limit for paginated lists.
	MaxPageSize = 100
	// SecretMask replaces secret values in API responses; sending it back keeps the stored secret.
	SecretMask = "********"
)

// HTTP headers and multipart field names that are part of the contract.
const (
	// HeaderAgent names the agent (within the token owner's agents) on agent-token requests.
	HeaderAgent = "X-Partyline-Agent"
	// HeaderAgentMode set to AgentModeUnique on the agent WebSocket asks for a free name: when
	// the named agent is online in another session, the server registers the next free
	// "<name>-N" (N ≥ 2) and the welcome frame carries it (NAME-02). Without it the newest
	// connection replaces the older one (NAME-03).
	HeaderAgentMode = "X-Partyline-Agent-Mode"
	// AgentModeUnique is the HeaderAgentMode value for derived agent names.
	AgentModeUnique = "unique"
	// HeaderSignature carries "sha256=<hex HMAC>" on webhook requests in both directions.
	HeaderSignature = "X-Partyline-Signature"
	// HeaderTimestamp carries the unix seconds used in the webhook signature.
	HeaderTimestamp = "X-Partyline-Timestamp"
	// MultipartMessageField is the multipart part holding a PostMessageRequest as JSON.
	MultipartMessageField = "message"
	// MultipartFileField is the multipart part name for each uploaded file.
	MultipartFileField = "file"
)

// ErrorCode is the machine-readable code of an API error.
type ErrorCode string

// Error codes and the HTTP status they come with.
const (
	ErrorCodeInvalid      ErrorCode = "invalid"      // 400 malformed request, 422 validation
	ErrorCodeUnauthorized ErrorCode = "unauthorized" // 401
	ErrorCodeForbidden    ErrorCode = "forbidden"    // 403
	ErrorCodeNotFound     ErrorCode = "not_found"    // 404, also when the caller cannot view the resource
	ErrorCodeConflict     ErrorCode = "conflict"     // 409
	ErrorCodeGone         ErrorCode = "gone"         // 410, e.g. used or expired invite
	ErrorCodeTooLarge     ErrorCode = "too_large"    // 413
	ErrorCodeRateLimited  ErrorCode = "rate_limited" // 429
	ErrorCodeInternal     ErrorCode = "internal"     // 500
	ErrorCodeUnavailable  ErrorCode = "unavailable"  // 503, or 502 when a connector's platform failed
)

// ErrorBody is the envelope of every error response.
type ErrorBody struct {
	Error Error `json:"error"`
}

// Error describes an API error. Fields maps a request field name to the reason it was rejected.
type Error struct {
	Code    ErrorCode         `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// List wraps an unpaginated collection.
type List[T any] struct {
	Items []T `json:"items"`
}

// Page wraps one page of a paginated collection. Request the next page with the cursor of
// the last item (before_seq for messages, before=<id> for audit entries).
type Page[T any] struct {
	Items   []T  `json:"items"`
	HasMore bool `json:"has_more"`
}

// Level is an access level on a room (view < post < manage) or on an agent or connector (use).
type Level string

// Access levels.
const (
	LevelView   Level = "view"
	LevelPost   Level = "post"
	LevelManage Level = "manage"
	LevelUse    Level = "use"
)

// Visibility is a sharing preset; the server stores it as a set of grants (AD-005).
type Visibility string

// Visibility presets (ACL-07).
const (
	VisibilityPrivate Visibility = "private" // owner only
	VisibilityShared  Visibility = "shared"  // owner plus the listed users
	VisibilityPublic  Visibility = "public"  // every user of the instance
)

// Sharing is the body of GET and PUT /{rooms|agents|connectors}/{id}/sharing.
type Sharing struct {
	Mode  Visibility    `json:"mode"`
	Users []SharingUser `json:"users"`
	// PublicLevel is the level everyone gets when Mode is public: view or post for rooms, use otherwise.
	PublicLevel Level `json:"public_level,omitempty"`
}

// SharingUser is one user grant. Username and DisplayName are filled in responses only.
type SharingUser struct {
	UserID      string `json:"user_id"`
	Level       Level  `json:"level"`
	Username    string `json:"username,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}
