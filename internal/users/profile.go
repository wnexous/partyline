package users

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/store/db"
)

// maxDisplayName is the longest accepted display name in characters.
const maxDisplayName = 64

// localeRE accepts BCP 47 style tags such as "en" or "pt-BR".
var localeRE = regexp.MustCompile(`^[a-zA-Z]{2,3}(-[a-zA-Z0-9]{2,8})*$`)

// GetUser returns an account by id.
func (s *Service) GetUser(ctx context.Context, id string) (db.User, error) {
	u, err := s.store.R().GetUser(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return db.User{}, ErrNotFound
	}
	return u, err
}

// ProfileUpdate holds profile changes; nil fields are unchanged.
type ProfileUpdate struct {
	DisplayName *string
	Locale      *string
}

// UpdateProfile changes the user's display name or locale.
func (s *Service) UpdateProfile(ctx context.Context, userID string, up ProfileUpdate) (db.User, error) {
	fields := map[string]string{}
	if up.DisplayName != nil && utf8.RuneCountInString(*up.DisplayName) > maxDisplayName {
		fields["display_name"] = "must have at most 64 characters"
	}
	if up.Locale != nil && *up.Locale != "" && !localeRE.MatchString(*up.Locale) {
		fields["locale"] = "must be a language tag such as en or pt-BR"
	}
	if len(fields) > 0 {
		return db.User{}, &ValidationError{Fields: fields}
	}
	var u db.User
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		var err error
		u, err = q.GetUser(ctx, userID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if up.DisplayName != nil {
			u.DisplayName = *up.DisplayName
		}
		if up.Locale != nil {
			u.Locale = *up.Locale
		}
		return q.UpdateUserProfile(ctx, db.UpdateUserProfileParams{DisplayName: u.DisplayName, Locale: u.Locale, ID: userID})
	})
	if err != nil {
		return db.User{}, err
	}
	return u, nil
}

// ChangePassword replaces the user's password after checking the current one.
func (s *Service) ChangePassword(ctx context.Context, userID, current, next string) error {
	u, err := s.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	ok, err := authn.VerifyPassword(u.PasswordHash, current)
	if err != nil {
		return err
	}
	fields := map[string]string{}
	if !ok {
		fields["current_password"] = "is incorrect"
	}
	if f := validatePassword(next); f != "" {
		fields["new_password"] = f
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	hash, err := authn.HashPassword(next)
	if err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{PasswordHash: hash, ID: userID}); err != nil {
			return err
		}
		return s.audit.Record(ctx, q, audit.Entry{Action: audit.ActionPasswordChange, TargetKind: audit.TargetUser, TargetID: userID})
	})
}

// ListSessions returns the user's unexpired sessions, most recently used first.
func (s *Service) ListSessions(ctx context.Context, userID string) ([]db.Session, error) {
	return s.store.R().ListSessionsByUser(ctx, db.ListSessionsByUserParams{UserID: userID, ExpiresAt: ms(s.now())})
}

// ResetPassword sets a new password for username without the current one (operator CLI) and
// ends all of the user's sessions.
func (s *Service) ResetPassword(ctx context.Context, username, password string) error {
	if f := validatePassword(password); f != "" {
		return &ValidationError{Fields: map[string]string{"password": f}}
	}
	hash, err := authn.HashPassword(password)
	if err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(q *db.Queries) error {
		u, err := q.GetUserByUsername(ctx, username)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := q.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{PasswordHash: hash, ID: u.ID}); err != nil {
			return err
		}
		if err := q.DeleteSessionsByUser(ctx, u.ID); err != nil {
			return err
		}
		return s.audit.Record(ctx, q, audit.Entry{Action: audit.ActionPasswordReset, TargetKind: audit.TargetUser, TargetID: u.ID})
	})
}
