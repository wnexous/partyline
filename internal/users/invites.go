package users

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/handles"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// InviteTTL is how long an invite stays usable (AUTH-06).
const InviteTTL = 7 * 24 * time.Hour

// Invite is an invite as listed for admins, with its computed status.
type Invite struct {
	db.ListInvitesRow
	Status dto.InviteStatus
}

func validRole(r dto.Role) bool { return r == dto.RoleAdmin || r == dto.RoleMember }

// inviteStatus computes the state of an invite at now.
func inviteStatus(inv db.Invite, now time.Time) dto.InviteStatus {
	switch {
	case inv.UsedAt.Valid:
		return dto.InviteStatusUsed
	case inv.RevokedAt.Valid:
		return dto.InviteStatusRevoked
	case ms(now) >= inv.ExpiresAt:
		return dto.InviteStatusExpired
	default:
		return dto.InviteStatusPending
	}
}

// CreateInvite creates a single-use invite and returns it with its secret token, which is
// shown once and stored only as a hash.
func (s *Service) CreateInvite(ctx context.Context, createdBy string, role dto.Role) (db.Invite, string, error) {
	if !validRole(role) {
		return db.Invite{}, "", &ValidationError{Fields: map[string]string{"role": "must be admin or member"}}
	}
	secret, err := authn.NewSessionToken()
	if err != nil {
		return db.Invite{}, "", err
	}
	now := s.now()
	inv := db.Invite{
		ID: store.NewID(), TokenHash: authn.HashToken(secret), Role: string(role),
		CreatedBy: createdBy, CreatedAt: ms(now), ExpiresAt: ms(now.Add(InviteTTL)),
	}
	err = s.store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateInvite(ctx, db.CreateInviteParams{
			ID: inv.ID, TokenHash: inv.TokenHash, Role: inv.Role,
			CreatedBy: inv.CreatedBy, CreatedAt: inv.CreatedAt, ExpiresAt: inv.ExpiresAt,
		}); err != nil {
			return err
		}
		return s.audit.Record(ctx, q, audit.Entry{
			ActorID: createdBy, Action: audit.ActionInviteCreate, TargetKind: audit.TargetInvite, TargetID: inv.ID,
			Detail: map[string]any{"role": inv.Role},
		})
	})
	if err != nil {
		return db.Invite{}, "", err
	}
	return inv, secret, nil
}

// GetInvite returns the pending invite for token: ErrNotFound when unknown, ErrInviteGone
// when used, revoked or expired.
func (s *Service) GetInvite(ctx context.Context, token string) (db.Invite, error) {
	inv, err := s.store.R().GetInviteByTokenHash(ctx, authn.HashToken(token))
	if errors.Is(err, sql.ErrNoRows) {
		return db.Invite{}, ErrNotFound
	}
	if err != nil {
		return db.Invite{}, err
	}
	if inviteStatus(inv, s.now()) != dto.InviteStatusPending {
		return db.Invite{}, ErrInviteGone
	}
	return inv, nil
}

// AcceptInvite creates an account with the invite's role and marks the invite used (AUTH-07).
func (s *Service) AcceptInvite(ctx context.Context, token string, in AccountInput) (db.User, error) {
	inv, err := s.GetInvite(ctx, token)
	if err != nil {
		return db.User{}, err
	}
	if err := in.validate(); err != nil {
		return db.User{}, err
	}
	hash, err := authn.HashPassword(in.Password)
	if err != nil {
		return db.User{}, err
	}
	var u db.User
	err = s.store.WithTx(ctx, func(q *db.Queries) error {
		now := s.now()
		var err error
		u, err = s.insertUser(ctx, q, in, hash, inv.Role, now)
		if err != nil {
			return err
		}
		n, err := q.MarkInviteUsed(ctx, db.MarkInviteUsedParams{
			UsedBy: sql.NullString{String: u.ID, Valid: true}, UsedAt: sql.NullInt64{Int64: ms(now), Valid: true},
			ID: inv.ID, ExpiresAt: ms(now),
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrInviteGone
		}
		return s.audit.Record(ctx, q, audit.Entry{
			ActorID: u.ID, Action: audit.ActionInviteAccept, TargetKind: audit.TargetInvite, TargetID: inv.ID,
			Detail: map[string]any{"user_id": u.ID, "username": u.Username, "role": u.Role},
		})
	})
	if err != nil {
		return db.User{}, err
	}
	return u, nil
}

// RevokeInvite revokes an invite so it can no longer be used.
func (s *Service) RevokeInvite(ctx context.Context, id string) error {
	return s.store.WithTx(ctx, func(q *db.Queries) error {
		n, err := q.RevokeInvite(ctx, db.RevokeInviteParams{RevokedAt: sql.NullInt64{Int64: ms(s.now()), Valid: true}, ID: id})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return s.audit.Record(ctx, q, audit.Entry{Action: audit.ActionInviteRevoke, TargetKind: audit.TargetInvite, TargetID: id})
	})
}

// ListInvites returns every invite, newest first, with creator and user names.
func (s *Service) ListInvites(ctx context.Context) ([]Invite, error) {
	rows, err := s.store.R().ListInvites(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := make([]Invite, len(rows))
	for i, r := range rows {
		out[i] = Invite{ListInvitesRow: r, Status: inviteStatus(r.Invite, now)}
	}
	return out, nil
}

// CreateUser creates an active account with the given role, applying the account rules.
func (s *Service) CreateUser(ctx context.Context, in AccountInput, role dto.Role) (db.User, error) {
	if err := in.validate(); err != nil {
		return db.User{}, err
	}
	if !validRole(role) {
		return db.User{}, &ValidationError{Fields: map[string]string{"role": "must be admin or member"}}
	}
	hash, err := authn.HashPassword(in.Password)
	if err != nil {
		return db.User{}, err
	}
	var u db.User
	err = s.store.WithTx(ctx, func(q *db.Queries) error {
		var err error
		u, err = s.insertUser(ctx, q, in, hash, string(role), s.now())
		if err != nil {
			return err
		}
		return s.audit.Record(ctx, q, audit.Entry{
			Action: audit.ActionUserCreate, TargetKind: audit.TargetUser, TargetID: u.ID,
			Detail: map[string]any{"username": u.Username, "role": u.Role},
		})
	})
	return u, err
}

// insertUser creates a user row; a taken username (case-insensitive) is ErrUsernameTaken.
func (s *Service) insertUser(ctx context.Context, q *db.Queries, in AccountInput, hash, role string, now time.Time) (db.User, error) {
	_, err := q.GetUserByUsername(ctx, in.Username)
	if err == nil {
		return db.User{}, ErrUsernameTaken
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return db.User{}, err
	}
	u := db.User{
		ID: store.NewID(), Username: in.Username,
		PasswordHash: hash, Role: role, CreatedAt: ms(now),
	}
	return u, s.insertAccount(ctx, q, u, in.DisplayName)
}

// insertAccount creates the user row, its intelligence and its personal organization in the caller's
// transaction, so every account has exactly one of each (PROF-01, AD-021). The intelligence is named by
// the display name, else the username, and its handle derives from the username (PROF-02); the
// organization is named after the intelligence, is the account's default and is audited as personal.
func (s *Service) insertAccount(ctx context.Context, q *db.Queries, u db.User, displayName string) error {
	if err := q.CreateUser(ctx, db.CreateUserParams{
		ID: u.ID, Username: u.Username,
		PasswordHash: u.PasswordHash, Role: u.Role, CreatedAt: u.CreatedAt,
	}); err != nil {
		return err
	}
	profile, err := intelligences.Create(ctx, q, intelligences.NewParams{
		ID: u.ID, Kind: handles.User, DisplayName: displayName, HandleBase: u.Username, Now: time.UnixMilli(u.CreatedAt),
	})
	if err != nil {
		return err
	}
	org, err := orgs.CreatePersonal(ctx, q, orgs.PersonalParams{
		UserID: u.ID, Name: profile.DisplayName, Username: u.Username, Now: time.UnixMilli(u.CreatedAt),
	})
	if err != nil {
		return err
	}
	return s.audit.Record(ctx, q, audit.Entry{
		ActorID: u.ID, Action: audit.ActionOrgCreate, TargetKind: audit.TargetOrganization, TargetID: org.ID,
		Detail: map[string]any{"name": org.Name, "slug": org.Slug, "personal": true},
	})
}

// ListUsers returns every account ordered by username.
func (s *Service) ListUsers(ctx context.Context) ([]db.User, error) {
	return s.store.R().ListUsers(ctx)
}

// UserUpdate holds the admin changes to an account; nil fields are unchanged.
type UserUpdate struct {
	Role     *dto.Role
	Disabled *bool
}

// UpdateUser changes an account's role or disabled state. Disabling revokes every session
// of the user; its tokens stop authenticating while it stays disabled (AUTH-10). The calling
// admin (principal in ctx) cannot demote or disable themselves (ErrSelfAdminChange), and the
// last active admin cannot be demoted or disabled (ErrLastAdmin).
func (s *Service) UpdateUser(ctx context.Context, id string, up UserUpdate) (db.User, error) {
	if up.Role != nil && !validRole(*up.Role) {
		return db.User{}, &ValidationError{Fields: map[string]string{"role": "must be admin or member"}}
	}
	var u db.User
	var ended []string
	var disabledNow bool
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		detail := map[string]any{}
		if up.Role != nil {
			detail["role"] = string(*up.Role)
		}
		if up.Disabled != nil {
			detail["disabled"] = *up.Disabled
		}
		if err := s.audit.Record(ctx, q, audit.Entry{
			Action: audit.ActionUserUpdate, TargetKind: audit.TargetUser, TargetID: id, Detail: detail,
		}); err != nil {
			return err
		}
		var err error
		u, err = q.GetUser(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		demote := up.Role != nil && *up.Role != dto.RoleAdmin
		disable := up.Disabled != nil && *up.Disabled && !u.DisabledAt.Valid
		if u.Role == string(dto.RoleAdmin) && !u.DisabledAt.Valid && (demote || disable) {
			if p, ok := authn.PrincipalFrom(ctx); ok && p.UserID == id {
				return ErrSelfAdminChange
			}
			// The single-writer tx makes this count race-free.
			n, err := q.CountActiveAdmins(ctx)
			if err != nil {
				return err
			}
			if n <= 1 {
				return ErrLastAdmin
			}
		}
		if up.Role != nil {
			u.Role = string(*up.Role)
			if err := q.UpdateUserRole(ctx, db.UpdateUserRoleParams{Role: u.Role, ID: id}); err != nil {
				return err
			}
		}
		if up.Disabled != nil && *up.Disabled != u.DisabledAt.Valid {
			u.DisabledAt = sql.NullInt64{}
			if *up.Disabled {
				u.DisabledAt = sql.NullInt64{Int64: ms(s.now()), Valid: true}
				if ended, err = q.DeleteSessionsByUser(ctx, id); err != nil {
					return err
				}
				disabledNow = true
			}
			return q.SetUserDisabledAt(ctx, db.SetUserDisabledAtParams{DisabledAt: u.DisabledAt, ID: id})
		}
		return nil
	})
	if err != nil {
		return db.User{}, err
	}
	if disabledNow {
		// Ends the user's token sockets too, not only its sessions'.
		s.revoked(events.SessionsRevoked{UserID: id, SessionIDs: ended, UserDisabled: true})
	}
	return u, nil
}
