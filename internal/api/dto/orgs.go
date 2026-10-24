package dto

import (
	"encoding/json"
	"time"
)

// Permission is what a membership allows in an organization itself, independent of the position
// the identity holds there (OMEM-04).
type Permission string

// Organization permissions.
const (
	PermissionAdmin  Permission = "admin"
	PermissionMember Permission = "member"
	PermissionGuest  Permission = "guest"
)

// Organization is an organization as one of its members sees it: its address, its avatar and the
// viewer's own permission and position (ORG-01, OMEM-03).
type Organization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	// AvatarURL is /api/v1/orgs/{id}/avatar?v=<sha>; absent without an avatar.
	AvatarURL string `json:"avatar_url,omitempty"`
	// Permission and PositionID are the viewer's membership ("" for no position).
	Permission Permission `json:"permission"`
	PositionID string     `json:"position_id,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// CreateOrganizationRequest is the body of POST /orgs. The creator becomes its admin, holding the
// root position (ORG-01).
type CreateOrganizationRequest struct {
	Name string `json:"name"`
}

// UpdateOrganizationRequest is the body of PATCH /orgs/{org}; absent fields are unchanged. Slug is
// the address, lower-cased, 1-40 characters of a-z, 0-9 and single hyphens between them.
type UpdateOrganizationRequest struct {
	Name *string `json:"name,omitempty"`
	Slug *string `json:"slug,omitempty"`
}

// Position is one node of an organization's chart (POS-01). ParentID is absent for a root.
type Position struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parent_id,omitempty"`
}

// CreatePositionRequest is the body of POST /orgs/{org}/positions (POS-01); ParentID is empty for a
// root position.
type CreatePositionRequest struct {
	Name     string `json:"name"`
	ParentID string `json:"parent_id,omitempty"`
}

// UpdatePositionRequest is the body of PATCH /orgs/{org}/positions/{id}; absent fields are
// unchanged, and an empty ParentID makes the position a root.
type UpdatePositionRequest struct {
	Name     *string `json:"name,omitempty"`
	ParentID *string `json:"parent_id,omitempty"`
}

// UpdateOrgMemberRequest is the body of PATCH /orgs/{org}/members/{iid}. Permission changes the
// member's permission (admin only); PositionID moves it to a position of the organization, or clears
// it with null or "" (POS-04). A field that is absent is left alone.
type UpdateOrgMemberRequest struct {
	Permission *Permission     `json:"permission,omitempty"`
	PositionID json.RawMessage `json:"position_id,omitempty" tstype:"string | null"`
}

// OrgMember is one membership of an organization as the members list shows it (OMEM-01, OMEM-03):
// the identity, its permission and position, and the inviter of a guest.
type OrgMember struct {
	Intelligence IntelligenceRef `json:"intelligence"`
	Permission   Permission      `json:"permission"`
	// PositionID and PositionName are absent when the member holds no position.
	PositionID   string `json:"position_id,omitempty"`
	PositionName string `json:"position_name,omitempty"`
	// InvitedBy is the identity that invited a guest (POS-05); absent for other permissions.
	InvitedBy *IntelligenceRef `json:"invited_by,omitempty"`
	JoinedAt  time.Time        `json:"joined_at"`
}

// CreateInvitationRequest is the body of POST /orgs/{org}/invitations (OMEM-01). Handle is the @handle of
// a user or an AI. Permission is required: an admin gives any, and every other member or guest invites
// guests with no position only. PositionID is a position of the organization.
type CreateInvitationRequest struct {
	Handle     string     `json:"handle"`
	Permission Permission `json:"permission"`
	PositionID string     `json:"position_id,omitempty"`
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
	// Permission and the position are what the invitee becomes on accepting; the position is absent when
	// the invitation carries none.
	Permission   Permission `json:"permission"`
	PositionID   string     `json:"position_id,omitempty"`
	PositionName string     `json:"position_name,omitempty"`
	// InvitedBy is the identity that sent it; for a guest it becomes the guest's inviter (POS-05).
	InvitedBy IntelligenceRef `json:"invited_by"`
	CreatedAt time.Time       `json:"created_at"`
	ExpiresAt time.Time       `json:"expires_at"`
}
