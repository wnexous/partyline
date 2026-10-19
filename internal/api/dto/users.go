package dto

import "time"

// Role is an instance-wide account role. Admins manage accounts only (ACL-08).
type Role string

// Account roles.
const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

// SetupStatus is returned by GET /setup/status.
type SetupStatus struct {
	NeedsSetup bool `json:"needs_setup"`
}

// SetupRequest is the body of POST /setup; Token is the one-time setup token from the log.
type SetupRequest struct {
	Token       string `json:"token"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name,omitempty"`
}

// LoginRequest is the body of POST /auth/login.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Me is the logged-in user (GET /me, response of setup and login).
type Me struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Role        Role      `json:"role"`
	Locale      string    `json:"locale"`
	CreatedAt   time.Time `json:"created_at"`
}

// UpdateMeRequest is the body of PATCH /me; absent fields are unchanged.
type UpdateMeRequest struct {
	DisplayName *string `json:"display_name,omitempty"`
	Locale      *string `json:"locale,omitempty"`
}

// ChangePasswordRequest is the body of POST /me/password.
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// Session is one login session of the current user (GET /me/sessions).
type Session struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	UserAgent  string    `json:"user_agent"`
	IP         string    `json:"ip"`
	Current    bool      `json:"current"`
}

// User is the public view of an account (GET /users directory, owners, authors).
type User struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

// AdminUser is an account as seen by admins (GET /admin/users).
type AdminUser struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	DisplayName string     `json:"display_name"`
	Role        Role       `json:"role"`
	Disabled    bool       `json:"disabled"`
	DisabledAt  *time.Time `json:"disabled_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// UpdateUserRequest is the body of PATCH /admin/users/{id}; absent fields are unchanged.
type UpdateUserRequest struct {
	Role     *Role `json:"role,omitempty"`
	Disabled *bool `json:"disabled,omitempty"`
}

// InviteStatus is the state of an invite.
type InviteStatus string

// Invite states.
const (
	InviteStatusPending InviteStatus = "pending"
	InviteStatusUsed    InviteStatus = "used"
	InviteStatusRevoked InviteStatus = "revoked"
	InviteStatusExpired InviteStatus = "expired"
)

// Invite is a single-use account invitation (GET /admin/invites).
type Invite struct {
	ID        string       `json:"id"`
	Role      Role         `json:"role"`
	Status    InviteStatus `json:"status"`
	CreatedBy User         `json:"created_by"`
	CreatedAt time.Time    `json:"created_at"`
	ExpiresAt time.Time    `json:"expires_at"`
	UsedBy    *User        `json:"used_by,omitempty"`
	UsedAt    *time.Time   `json:"used_at,omitempty"`
}

// CreateInviteRequest is the body of POST /admin/invites.
type CreateInviteRequest struct {
	Role Role `json:"role"`
}

// CreatedInvite is returned once by POST /admin/invites; URL embeds the secret token.
type CreatedInvite struct {
	Invite Invite `json:"invite"`
	URL    string `json:"url"`
}

// InviteInfo is the public view of a valid invite (GET /invites/{token}).
type InviteInfo struct {
	Role      Role      `json:"role"`
	ExpiresAt time.Time `json:"expires_at"`
}

// AcceptInviteRequest is the body of POST /invites/{token}/accept.
type AcceptInviteRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name,omitempty"`
}

// TokenKind is the kind of a personal API token.
type TokenKind string

// API token kinds: agent tokens reach only /agent endpoints (TOK-03).
const (
	TokenKindAgent TokenKind = "agent"
	TokenKindUser  TokenKind = "user"
)

// Token is an API token without its secret (GET /tokens).
type Token struct {
	ID         string     `json:"id"`
	Kind       TokenKind  `json:"kind"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// CreateTokenRequest is the body of POST /tokens; no ExpiresAt means the token never expires.
type CreateTokenRequest struct {
	Kind      TokenKind  `json:"kind"`
	Name      string     `json:"name"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// CreatedToken is returned once by POST /tokens; Secret is never shown again (TOK-01).
type CreatedToken struct {
	Token  Token  `json:"token"`
	Secret string `json:"secret"`
}

// AuditEntry is one security event (GET /admin/audit, newest first, 50 per page).
type AuditEntry struct {
	ID         string         `json:"id"`
	Actor      *User          `json:"actor,omitempty"`
	Action     string         `json:"action"`
	TargetKind string         `json:"target_kind,omitempty"`
	TargetID   string         `json:"target_id,omitempty"`
	Detail     map[string]any `json:"detail,omitempty" tstype:"{ [key: string]: unknown }"`
	IP         string         `json:"ip,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}
