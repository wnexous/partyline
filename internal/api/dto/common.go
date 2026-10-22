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
	// MaxMessageRecipients is the most distinct chats one post may name in recipients
	// (chat_ids in the channel) (ROUTE-10).
	MaxMessageRecipients = 10
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
	// AgentModeUnique is the HeaderAgentMode value for derived agent names (clients from before
	// projects; current clients send HeaderProject instead).
	AgentModeUnique = "unique"
	// AgentModeTemporary is the HeaderAgentMode value of a session that reconnects under the
	// temporary name the server assigned it: the same temporary agent, newest wins (ID-01).
	AgentModeTemporary = "temporary"
	// HeaderProject names the session's project (git root or working directory basename, or
	// --project). With it, a session without HeaderAgent gets a temporary agent named after
	// project and harness, and HeaderAgent names a fixed agent (ID-01, ID-02).
	HeaderProject = "X-Partyline-Project"
	// HeaderHarness is the launcher's harness id (claude, opencode, pi, omp, codex, gemini).
	HeaderHarness = "X-Partyline-Harness"
	// HeaderMachine is the session's hostname.
	HeaderMachine = "X-Partyline-Machine"
	// HeaderFolder is the canonical folder an AI session works in (absolute, symlinks resolved,
	// no trailing separator), percent-encoded with url.PathEscape. Together with the machine and
	// the token's owner it identifies the AI; the agent WebSocket and every agent route that acts
	// as an AI take it (AID-07).
	HeaderFolder = "X-Partyline-Folder"
	// HeaderHome is the canonical home folder of the session's user, percent-encoded like
	// HeaderFolder; the machine tree shows folders under it as "~" (TREE-02).
	HeaderHome = "X-Partyline-Home"
	// HeaderLive is a random id of the process that holds the agent WebSocket
	// ([A-Za-z0-9_-], 16 to 64 characters): the same process reconnecting is not another
	// session of the AI (SES-02).
	HeaderLive = "X-Partyline-Live"
	// HeaderTakeover set to "1" on the agent WebSocket takes over the AI's live session from
	// another process instead of being refused with identity_busy (SES-02).
	HeaderTakeover = "X-Partyline-Takeover"
	// HeaderAdopt names a handle or a v0.3 agent name (--as, PARTYLINE_AGENT): the unlinked AI to
	// link to the session's folder when the folder has no AI yet (MIG-02).
	HeaderAdopt = "X-Partyline-Adopt"
	// HeaderSession is the session key (PARTYLINE_SESSION; 1 to 64 of A-Z a-z 0-9 . _ -) that
	// the channel, listen and the turn-end hook of one harness session share. A session that
	// names no agent gets its own temporary agent back by this key, and a tools-only session
	// (never online) holds its agent against other sessions until it is archived (ID-01).
	// Other values are ignored; without it the server keeps the rule of older clients.
	HeaderSession = "X-Partyline-Session"
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
	ErrorCodeInvalid       ErrorCode = "invalid"        // 400 malformed request, 422 validation
	ErrorCodeUnauthorized  ErrorCode = "unauthorized"   // 401
	ErrorCodeForbidden     ErrorCode = "forbidden"      // 403
	ErrorCodeNotFound      ErrorCode = "not_found"      // 404, also when the caller cannot view the resource
	ErrorCodeConflict      ErrorCode = "conflict"       // 409
	ErrorCodeGone          ErrorCode = "gone"           // 410, e.g. used or expired invite
	ErrorCodeTooLarge      ErrorCode = "too_large"      // 413
	ErrorCodeRateLimited   ErrorCode = "rate_limited"   // 429
	ErrorCodeInternal      ErrorCode = "internal"       // 500
	ErrorCodeUnavailable   ErrorCode = "unavailable"    // 503, or 502 when a connector's platform failed
	ErrorCodeHandleTaken   ErrorCode = "handle_taken"   // 409, the @handle belongs to another intelligence
	ErrorCodeAvatarInvalid ErrorCode = "avatar_invalid" // 422, the avatar is not an acceptable image
	// ErrorCodeFolderRequired is a session that did not say which machine and folder it works in
	// (an old CLI sends none); ErrorCodeFolderInvalid one whose folder cannot be an identity
	// (not absolute or canonical, a NUL, longer than 1024 bytes) (AID-05).
	ErrorCodeFolderRequired ErrorCode = "folder_required" // 422
	ErrorCodeFolderInvalid  ErrorCode = "folder_invalid"  // 422
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
