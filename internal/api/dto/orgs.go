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

// DeleteEffect is what deleting a member of an organization does there (POS-06): an AI whose home is the
// organization is deleted for good, while a person, or an AI of another organization, is only removed from it.
type DeleteEffect string

// Delete effects.
const (
	DeleteEffectDelete DeleteEffect = "delete"
	DeleteEffectRemove DeleteEffect = "remove"
)

// OrgMember is one membership of an organization as the members list shows it (OMEM-01, OMEM-03):
// the identity, its permission and position, the inviter of a guest, and what the viewer may do to it.
type OrgMember struct {
	Intelligence IntelligenceRef `json:"intelligence"`
	Permission   Permission      `json:"permission"`
	// PositionID and PositionName are absent when the member holds no position.
	PositionID   string `json:"position_id,omitempty"`
	PositionName string `json:"position_name,omitempty"`
	// InvitedBy is the identity that invited a guest (POS-05); absent for other permissions.
	InvitedBy *IntelligenceRef `json:"invited_by,omitempty"`
	JoinedAt  time.Time        `json:"joined_at"`
	// CanDelete says whether the viewer may delete the member in this organization (POS-05), and DeleteEffect
	// what that does (POS-06). CanMove says whether the viewer may move the member to another position
	// (POS-04): an admin may move anyone; anyone else a member it manages, to a position below its own.
	CanDelete    bool         `json:"can_delete"`
	DeleteEffect DeleteEffect `json:"delete_effect"`
	CanMove      bool         `json:"can_move"`
}

// MovePreview is what moving a member to another position would change about who manages it (POS-04):
// the identities that manage it now and would stop doing so. An empty list means nobody loses the
// management.
type MovePreview struct {
	LoseManagement []IntelligenceRef `json:"lose_management"`
}

// OrgChart is an organization's org chart as one of its members, guests included, sees it (CHART-01..03).
// Every member appears once: in the Holders of its position, in Members, or in Unassigned.
type OrgChart struct {
	Viewer ChartViewer `json:"viewer"`
	// Positions is every position of the organization as a flat list in tree order - a parent before its
	// children, siblings by name - which ParentID links into the tree.
	Positions []ChartPosition `json:"positions"`
	// Members are the members with no position that hang under another member: a guest under its inviter,
	// an AI under its creator (Under names it). A member Under names may be a holder, or itself one of
	// Members or Unassigned.
	Members []ChartMember `json:"members"`
	// Unassigned are the members with no position and nobody to hang under.
	Unassigned []ChartMember `json:"unassigned"`
}

// ChartViewer is what the chart says about the person looking at it.
type ChartViewer struct {
	// Admin tells whether the viewer is an admin of the organization, who may edit its positions (CHART-03).
	Admin bool `json:"admin"`
}

// ChartPosition is one position with the members that hold it, ordered by name.
type ChartPosition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// ParentID is absent for a root.
	ParentID string        `json:"parent_id,omitempty"`
	Holders  []ChartMember `json:"holders"`
}

// ChartMember is a member as the chart draws it, with what the viewer may do to it (CHART-03).
type ChartMember struct {
	Intelligence IntelligenceRef `json:"intelligence"`
	// Permission carries the admin and guest badges.
	Permission Permission `json:"permission"`
	// Online is true for an AI that has a live session; people have no presence and are always false.
	Online bool `json:"online"`
	// Under is the id of the member this one hangs under; set only in Chart.Members.
	Under string `json:"under,omitempty"`
	// CanDelete, DeleteEffect and CanMove are the viewer's rights over the member, as in OrgMember.
	CanDelete    bool         `json:"can_delete"`
	DeleteEffect DeleteEffect `json:"delete_effect"`
	CanMove      bool         `json:"can_move"`
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
