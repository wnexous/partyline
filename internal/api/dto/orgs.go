package dto

import "time"

// Permission is what a membership allows in an organization (OMEM-04, SR2-01): an admin looks after it,
// a member takes part.
type Permission string

// Organization permissions.
const (
	PermissionAdmin  Permission = "admin"
	PermissionMember Permission = "member"
)

// Organization is an organization as one of its members sees it: its address, its avatar and the
// viewer's own permission (ORG-01, OMEM-03).
type Organization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	// AvatarURL is /api/v1/orgs/{id}/avatar?v=<sha>; absent without an avatar.
	AvatarURL string `json:"avatar_url,omitempty"`
	// Permission is the viewer's.
	Permission Permission `json:"permission"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// CreateOrganizationRequest is the body of POST /orgs. The creator becomes its admin (ORG-01).
type CreateOrganizationRequest struct {
	Name string `json:"name"`
}

// UpdateOrganizationRequest is the body of PATCH /orgs/{org}; absent fields are unchanged. Slug is
// the address, lower-cased, 1-40 characters of a-z, 0-9 and single hyphens between them.
type UpdateOrganizationRequest struct {
	Name *string `json:"name,omitempty"`
	Slug *string `json:"slug,omitempty"`
}

// UpdateOrgMemberRequest is the body of PATCH /orgs/{org}/members/{iid}: Permission changes the member's
// permission (admin only). An absent field is left alone.
type UpdateOrgMemberRequest struct {
	Permission *Permission `json:"permission,omitempty"`
}

// DeleteEffect is what deleting a member of an organization does there (POS-06): an AI whose home is the
// organization is deleted for good, while a person, or an AI of another organization, is only removed from it.
type DeleteEffect string

// Delete effects.
const (
	DeleteEffectDelete DeleteEffect = "delete"
	DeleteEffectRemove DeleteEffect = "remove"
)

// OrgMember is one membership of an organization as the members list shows it (OMEM-01, OMEM-03):
// the identity, its permission, and what the viewer may do to it.
type OrgMember struct {
	Intelligence IntelligenceRef `json:"intelligence"`
	Permission   Permission      `json:"permission"`
	JoinedAt     time.Time       `json:"joined_at"`
	// CanDelete says whether the viewer may delete the member in this organization (SR2-12): an admin may
	// take anyone else out, and the host of an AI whose home is the organization may delete it. DeleteEffect
	// says what that does (POS-06).
	CanDelete    bool         `json:"can_delete"`
	DeleteEffect DeleteEffect `json:"delete_effect"`
}

// CreateInvitationRequest is the body of POST /orgs/{org}/invitations (OMEM-01), which only an admin sends.
// Handle is the @handle of a user or an AI; Permission is admin or member.
type CreateInvitationRequest struct {
	Handle     string     `json:"handle"`
	Permission Permission `json:"permission"`
}

// Invitation is a pending invitation to an organization (OMEM-01) as the invitee, whoever manages an
// invited AI, or the organization's admins see it. It expires 14 days after it was sent or sent again
// (OMEM-07).
type Invitation struct {
	ID string `json:"id"`
	// OrgID, OrgName and OrgSlug name the organization.
	OrgID   string `json:"org_id"`
	OrgName string `json:"org_name"`
	OrgSlug string `json:"org_slug"`
	// Invitee is the user or AI invited.
	Invitee IntelligenceRef `json:"invitee"`
	// Permission is what the invitee becomes on accepting.
	Permission Permission `json:"permission"`
	// InvitedBy is the admin that sent it.
	InvitedBy IntelligenceRef `json:"invited_by"`
	CreatedAt time.Time       `json:"created_at"`
	ExpiresAt time.Time       `json:"expires_at"`
}

// AddInstructorRequest is the body of POST /agents/{id}/instructors (SR2-10): the @handle of a user or of an
// AI the host can see.
type AddInstructorRequest struct {
	Handle string `json:"handle"`
}

// AgentInstructor is one identity that instructs an AI besides its host (SR2-06).
type AgentInstructor struct {
	Intelligence IntelligenceRef `json:"intelligence"`
	// AddedAt is when the link was made.
	AddedAt time.Time `json:"added_at"`
}

// AgentInstructors is GET /agents/{id}/instructors (SR2-10): who instructs the AI - its host and its
// instructors, whose messages it treats as trusted (SR2-07). Instructors is always present, oldest first.
type AgentInstructors struct {
	Host        IntelligenceRef   `json:"host"`
	Instructors []AgentInstructor `json:"instructors"`
}
