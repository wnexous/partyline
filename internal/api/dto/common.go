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
	// HeaderOrg names the organization a new AI joins (--org, PARTYLINE_ORG): a slug or an id. Without
	// it the AI joins its creator's default organization. An existing AI keeps its home organization
	// and a different one is refused with organization_mismatch (ORG-04, AD-020).
	HeaderOrg = "X-Partyline-Org"
	// HeaderPosition names the position the AI holds in its organization (--position,
	// PARTYLINE_POSITION): the position's name, percent-encoded with url.PathEscape like the folder.
	// Without it a new AI holds none (POS-07, AD-020).
	HeaderPosition = "X-Partyline-Position"
	// HeaderParent names the AI that starts this one (--parent, PARTYLINE_PARENT): an @handle or an id.
	// It counts when the token holder may act for that AI (their own, hosted on their machines);
	// otherwise the token holder is the creator (POS-08, AD-020).
	HeaderParent = "X-Partyline-Parent"
	// HeaderFeatures lists, separated by commas, the optional features the client of the agent
	// WebSocket understands. FeaturePlatform opts in to platform events (CID-06): a session that
	// does not name it gets room messages only, and its platform queue waits (CID-10).
	HeaderFeatures = "X-Partyline-Features"
	// HeaderSignature carries "sha256=<hex HMAC>" on webhook requests in both directions.
	HeaderSignature = "X-Partyline-Signature"
	// HeaderTimestamp carries the unix seconds used in the webhook signature.
	HeaderTimestamp = "X-Partyline-Timestamp"
	// MultipartMessageField is the multipart part holding a PostMessageRequest as JSON.
	MultipartMessageField = "message"
	// MultipartFileField is the multipart part name for each uploaded file.
	MultipartFileField = "file"
)

// FeaturePlatform is the HeaderFeatures token of a client that understands platform events.
const FeaturePlatform = "platform"

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
	// ErrorCodeLastInstanceAdmin is the last active instance admin deleting their own account: an admin is
	// needed to inherit the organizations they leave without one (409, OUPG-02).
	ErrorCodeLastInstanceAdmin ErrorCode = "last_instance_admin"
	// ErrorCodeLastAdmin is a change that would leave an organization without an admin: the only admin
	// leaving, or the only admin an AI is deleted from (409, OMEM-05). ErrorCodeHomeOrganization is an AI
	// leaving the organization it belongs to: it is deleted there, never removed (409, POS-06).
	ErrorCodeLastAdmin        ErrorCode = "last_admin"
	ErrorCodeHomeOrganization ErrorCode = "home_organization"
	// ErrorCodeAlreadyMember is an invitation to, or an acceptance of a place in, an organization the
	// identity already belongs to (409, OMEM-01).
	ErrorCodeAlreadyMember ErrorCode = "already_member"
	// ErrorCodeNotOperator is an AI that sends through a connection it does not operate: only the
	// operator sends through it (403, CID-03). ErrorCodeUnknownChat is a chat that is not one of the
	// named connection's chats (404, CID-17).
	ErrorCodeNotOperator ErrorCode = "not_operator"
	ErrorCodeUnknownChat ErrorCode = "unknown_chat"
	// ErrorCodeUnsupported is something the AI asked for that cannot be done (422): a file or a reaction
	// for a platform without a media send or reactions (the webhook), a reaction to a message the
	// platform gave no id, or a quote in a room of a message that arrived on one of its connections
	// (CID-22, CID-32, CID-33).
	ErrorCodeUnsupported ErrorCode = "unsupported"
	// ErrorCodeFolderRequired is a session that did not say which machine and folder it works in
	// (an old CLI sends none); ErrorCodeFolderInvalid one whose folder cannot be an identity
	// (not absolute or canonical, a NUL, longer than 1024 bytes) (AID-05).
	ErrorCodeFolderRequired ErrorCode = "folder_required" // 422
	ErrorCodeFolderInvalid  ErrorCode = "folder_invalid"  // 422
	// ErrorCodePositionUnknown is a start (--position) that names a position the organization does not
	// have (422, field position, POS-07).
	ErrorCodePositionUnknown ErrorCode = "position_unknown"
	// ErrorCodeOrganizationMismatch is a start of an existing AI with --org naming an organization other
	// than its home: AIs do not move (409, Details name the home organization, ORG-04).
	ErrorCodeOrganizationMismatch ErrorCode = "organization_mismatch"
	// ErrorCodeIdentityBusy is a connection for an AI that has a live session in another
	// process (409); Details say who, in which app and since when (SES-04).
	ErrorCodeIdentityBusy ErrorCode = "identity_busy" // 409
	// ErrorCodeDMMembersFixed is an attempt to add, change or remove a member of a direct
	// message: it has exactly its two intelligences (422, DM-02).
	ErrorCodeDMMembersFixed ErrorCode = "dm_members_fixed"
	// ErrorCodeDMSelf is a direct message opened with oneself (422, DM-01).
	ErrorCodeDMSelf ErrorCode = "dm_self"
	// ErrorCodeContactNotFound is a direct message with an intelligence that does not exist or
	// that the caller cannot see; the two cases look alike (404, DM-01, DM-04).
	ErrorCodeContactNotFound ErrorCode = "contact_not_found"
	// ErrorCodeNoRouteToPerson is a direct message with an external person that no platform
	// conversation of the sender reaches (422, DM-05).
	ErrorCodeNoRouteToPerson ErrorCode = "no_route_to_person"
	// ErrorCodeHistoryOnly is a post in a direct message with an external person, or an edit of a
	// person's profile: what the connections left before AD-015 is read-only history, and a person
	// is answered on their platform (422, CID-21).
	ErrorCodeHistoryOnly ErrorCode = "history_only"
	// ErrorCodeCounterpartDisabled is a post in a direct message whose other participant is a
	// disabled user: the DM stays readable (403, DM-02).
	ErrorCodeCounterpartDisabled ErrorCode = "counterpart_disabled"
	// ErrorCodeChatsNotMembers is a chat added as a member of a conversation: rooms hold users and
	// AIs only, and a chat reaches its operator AI through its connection (422, CID-19).
	ErrorCodeChatsNotMembers ErrorCode = "chats_not_members"
	// ErrorCodeMembersRefused is a new group with a member its owner may not add; Refused lists
	// them and nothing was created (422, LAY-04, AGRP-02).
	ErrorCodeMembersRefused ErrorCode = "members_refused"
	// ErrorCodeOrganizationRequired is a session or an AI start that names no usable organization: the
	// host belongs to none, or --org names one that does not exist (422, ORG-04).
	ErrorCodeOrganizationRequired ErrorCode = "organization_required"
	// ErrorCodeOrganizationNotEmpty is the refusal to delete an organization that still owns AIs,
	// connections or groups; Details counts them (409, ORG-06).
	ErrorCodeOrganizationNotEmpty ErrorCode = "organization_not_empty"
	// ErrorCodePositionCycle is a parent change that would make a position its own ancestor (409,
	// POS-02).
	ErrorCodePositionCycle ErrorCode = "position_cycle"
	// ErrorCodeIdentityOnline is a merge of an AI that has a live session: it must be offline
	// (409, MRG-02).
	ErrorCodeIdentityOnline ErrorCode = "identity_online"
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
	// Details carries facts about the refusal that are not request fields: for identity_busy the
	// BusyDetail keys, for organization_mismatch the MismatchDetail keys.
	Details map[string]string `json:"details,omitempty"`
	// Refused lists the members of members_refused, in the order they were asked for.
	Refused []RefusedMember `json:"refused,omitempty"`
}

// Keys of Error.Details for identity_busy: the AI's display name, the id and the readable name of
// the app of its live session and when that session connected (RFC 3339, UTC).
const (
	BusyDetailDisplayName = "display_name"
	BusyDetailApp         = "app"
	BusyDetailAppLabel    = "app_label"
	BusyDetailConnectedAt = "connected_at"
)

// Keys of Error.Details for organization_mismatch: the slug (what --org takes) and the name of the
// organization the AI belongs to.
const (
	MismatchDetailOrganization = "organization"
	MismatchDetailName         = "organization_name"
)

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

// Level is an access level on a room (view < post < manage) or on an AI or a connection (use < manage).
type Level string

// Access levels.
const (
	LevelView   Level = "view"
	LevelPost   Level = "post"
	LevelManage Level = "manage"
	LevelUse    Level = "use"
)
