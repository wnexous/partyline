package dto

import "time"

// IntelligenceKind tells whether an intelligence is a human or an AI (PROF-01).
type IntelligenceKind string

// Intelligence kinds.
const (
	IntelligenceKindHuman IntelligenceKind = "human"
	IntelligenceKindAI    IntelligenceKind = "ai"
)

// IntelligenceRole tells which account an intelligence is: a user of the instance, an AI, or
// a person seen by a connector.
type IntelligenceRole string

// Intelligence roles.
const (
	IntelligenceRoleUser   IntelligenceRole = "user"
	IntelligenceRoleAI     IntelligenceRole = "ai"
	IntelligenceRolePerson IntelligenceRole = "person"
)

// Profile limits shared by the API and the web forms (PROF-02, PROF-03).
const (
	// MaxProfileDisplayName is the longest display name in code points (the shortest is 1).
	MaxProfileDisplayName = 64
	// MaxProfileBio is the longest bio in code points.
	MaxProfileBio = 280
	// HandlePattern is the regular expression every handle matches: 1 to 40 characters of
	// a-z, 0-9 and "-", starting with a letter. Handles are lower-cased and unique across the
	// instance; a few words such as "partyline" are reserved.
	HandlePattern = "^[a-z][a-z0-9-]{0,39}$"
)

// IntelligenceRef is the public summary shown next to every name (PROF-09): who a user, an AI
// or a person is, with the handle and the badge data that tell an AI from a human.
type IntelligenceRef struct {
	ID   string           `json:"id"`
	Kind IntelligenceKind `json:"kind"`
	Role IntelligenceRole `json:"role"`
	// DisplayName is free text and not unique; the handle is.
	DisplayName string `json:"display_name"`
	Handle      string `json:"handle"`
	// AvatarURL is /api/v1/intelligences/{id}/avatar?v=<sha>; absent without an avatar, when
	// the UI shows initials (PROF-08).
	AvatarURL string `json:"avatar_url,omitempty"`
	// Platform is the connector kind (whatsapp, telegram, …) of a person.
	Platform string `json:"platform,omitempty"`
}

// IntelligenceProfile is GET /intelligences/{id}: the summary plus the bio, what the caller
// may do, and the details of the kind of intelligence.
type IntelligenceProfile struct {
	IntelligenceRef `tstype:",extends"`
	Bio             string `json:"bio"`
	// CanEdit tells whether the caller may PATCH the profile.
	CanEdit bool `json:"can_edit"`
	// AI is set for AIs.
	AI *AIDetail `json:"ai,omitempty"`
	// Apps lists where the intelligence speaks: the web app for users, the current harness
	// for AIs.
	Apps []App `json:"apps"`
	// Conversations lists the conversations the caller can view that involve the
	// intelligence; empty until conversations exist.
	Conversations []RoomRef `json:"conversations"`
}

// AIDetail describes where an AI lives and who may use it.
type AIDetail struct {
	Owner IntelligenceRef `json:"owner"`
	// Machine is the hostname of the AI's machine and MachineLabel the owner's label for it
	// (the hostname when there is none).
	Machine      string `json:"machine"`
	MachineLabel string `json:"machine_label"`
	// Folder is the AI's canonical folder; empty for an AI that is not linked to one yet.
	Folder      string `json:"folder"`
	FolderLabel string `json:"folder_label"`
	// App is the id of the harness the AI currently runs in (claude, opencode, …).
	App         string     `json:"app"`
	Online      bool       `json:"online"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`
	ConnectedAt *time.Time `json:"connected_at,omitempty"`
	// MyLevel is manage for the owner and use for a grantee; empty for a caller who sees the
	// AI through a conversation they share.
	MyLevel Level `json:"my_level,omitempty"`
}

// App is one place an intelligence speaks from.
type App struct {
	// Kind is "web", a connector kind or a harness id.
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// ConnectorID is set for apps that are a connector's account.
	ConnectorID string `json:"connector_id,omitempty"`
}

// UpdateProfileRequest is the body of PATCH /intelligences/{id} and of an AI's own profile
// update; absent fields are unchanged (PROF-03, PROF-05).
type UpdateProfileRequest struct {
	// DisplayName has 1 to MaxProfileDisplayName code points after trimming.
	DisplayName *string `json:"display_name,omitempty"`
	// Handle matches HandlePattern after lower-casing; a handle in use is 409 handle_taken.
	Handle *string `json:"handle,omitempty"`
	// Bio has at most MaxProfileBio code points.
	Bio *string `json:"bio,omitempty"`
}
