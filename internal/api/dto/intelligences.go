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
	// Person is set for a person, for the users who own or may use its connector (PEOP-03,
	// PEOP-04); a user who sees the person through a conversation only gets the summary.
	Person *PersonDetail `json:"person,omitempty"`
	// Apps lists where the intelligence speaks: the web app for users, the current harness
	// for AIs, the platform account for a person.
	Apps []App `json:"apps"`
	// Conversations lists the conversations the caller can view that involve a person (the
	// rooms with a chat where it was seen, or where it wrote), the most recently active first
	// (PEOP-03); empty for the other kinds.
	Conversations []RoomRef `json:"conversations"`
	// Memberships lists, by organization name, the organizations the caller shares with a user or an AI
	// (all of its own when the caller is the profile), with its permission and position in each and what
	// the caller may do about it (OMEM-03, CHART-02). Empty for a person.
	Memberships []ProfileMembership `json:"memberships"`
	// CanEndSession tells whether the caller may end the AI's live session: it manages the AI in an
	// organization they share (POS-04). Always false for users and people.
	CanEndSession bool `json:"can_end_session"`
}

// ProfileMembership is the place of an intelligence in one organization, as a profile shows it.
type ProfileMembership struct {
	OrgID   string `json:"org_id"`
	OrgName string `json:"org_name"`
	OrgSlug string `json:"org_slug"`
	// Permission is admin, member or guest.
	Permission string `json:"permission"`
	// PositionID and PositionName are absent for a member with no position.
	PositionID   string `json:"position_id,omitempty"`
	PositionName string `json:"position_name,omitempty"`
	// CanDelete tells whether the caller may delete the intelligence in this organization (POS-05).
	CanDelete bool `json:"can_delete"`
	// DeleteEffect is what deleting it here does (POS-06): "delete" removes an AI for good, because this is its
	// home organization; "remove" only takes the identity out of this organization.
	DeleteEffect string `json:"delete_effect"`
}

// PersonDetail describes the platform account behind a person (PEOP-03).
type PersonDetail struct {
	Connector ConnectorRef `json:"connector"`
	// ExternalID is the account's id on the platform.
	ExternalID string `json:"external_id"`
	// LinkedUser is the user whose verified account it is (AD-008, PEOP-02); messages of the
	// account are authored by that user.
	LinkedUser *IntelligenceRef `json:"linked_user,omitempty"`
	// NameLocked tells that a user chose the name, which platform name changes no longer
	// replace (PEOP-05, PROF-11).
	NameLocked bool `json:"name_locked"`
}

// AIDetail describes where an AI lives and how the caller stands to it.
type AIDetail struct {
	// Owner is the user who hosts the AI: whose computer and token run it. It grants nothing (AD-019).
	Owner IntelligenceRef `json:"owner"`
	// Folder is the AI's canonical folder; empty for an AI that is not linked to one yet.
	Folder      string `json:"folder"`
	FolderLabel string `json:"folder_label"`
	// App is the id of the harness the AI currently runs in (claude, opencode, …).
	App         string     `json:"app"`
	Online      bool       `json:"online"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`
	ConnectedAt *time.Time `json:"connected_at,omitempty"`
	// MyLevel is manage for whoever manages the AI in an organization they share with it (and the AI itself)
	// and use for whoever only sees it.
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
