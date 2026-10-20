package users

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// NeedsSetup reports whether the instance has no users yet.
func (s *Service) NeedsSetup(ctx context.Context) (bool, error) {
	n, err := s.store.R().CountUsers(ctx)
	if err != nil {
		return false, err
	}
	return n == 0, nil
}

// Setup creates the first account, an admin, when token matches the setup token and there
// are no users (AUTH-01). Once a user exists it returns ErrSetupDone (AUTH-02).
func (s *Service) Setup(ctx context.Context, token string, in AccountInput) (db.User, error) {
	needs, err := s.NeedsSetup(ctx)
	if err != nil {
		return db.User{}, err
	}
	if !needs {
		return db.User{}, ErrSetupDone
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.setupToken)) != 1 {
		return db.User{}, ErrBadSetupToken
	}
	if err := in.validate(); err != nil {
		return db.User{}, err
	}
	hash, err := authn.HashPassword(in.Password)
	if err != nil {
		return db.User{}, err
	}
	u := db.User{
		ID: store.NewID(), Username: in.Username, DisplayName: in.DisplayName,
		PasswordHash: hash, Role: string(dto.RoleAdmin), CreatedAt: ms(s.now()),
	}
	err = s.store.WithTx(ctx, func(q *db.Queries) error {
		n, err := q.CountUsers(ctx)
		if err != nil {
			return err
		}
		if n > 0 {
			return ErrSetupDone
		}
		if err := q.CreateUser(ctx, db.CreateUserParams{
			ID: u.ID, Username: u.Username, DisplayName: u.DisplayName,
			PasswordHash: u.PasswordHash, Role: u.Role, CreatedAt: u.CreatedAt,
		}); err != nil {
			return err
		}
		return s.audit.Record(ctx, q, audit.Entry{
			ActorID: u.ID, Action: audit.ActionSetup, TargetKind: audit.TargetUser, TargetID: u.ID,
			Detail: map[string]any{"username": u.Username},
		})
	})
	if err != nil {
		return db.User{}, err
	}
	return u, nil
}

// LoginInput holds the credentials and the client details recorded on the session.
type LoginInput struct {
	Username  string
	Password  string
	IP        string
	UserAgent string
}

// NewSession is a freshly created session; Secret is the cookie value and is never stored.
type NewSession struct {
	ID        string
	Secret    string
	ExpiresAt time.Time
}

// Login checks credentials and opens a session. Every failure cause returns
// ErrInvalidCredentials (AUTH-04); an IP over the failure limit gets ErrRateLimited (AUTH-05).
func (s *Service) Login(ctx context.Context, in LoginInput) (db.User, NewSession, error) {
	// Reserve the attempt before the slow hash, so concurrent attempts cannot all pass.
	if !s.limiter.Begin(in.IP, s.now()) {
		return db.User{}, NewSession{}, ErrRateLimited
	}
	u, err := s.checkPassword(ctx, in.Username, in.Password)
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		// The reservation stays as the failure. The attempted username is not recorded:
		// people sometimes type a password there.
		if aerr := s.audit.Append(ctx, audit.Entry{Action: audit.ActionLoginFailed, IP: in.IP}); aerr != nil {
			return db.User{}, NewSession{}, aerr
		}
		return db.User{}, NewSession{}, err
	case err != nil:
		s.limiter.Release(in.IP)
		return db.User{}, NewSession{}, err
	}
	// A success returns only its own reservation; earlier failures stay counted (AUTH-05).
	s.limiter.Release(in.IP)
	sess, err := s.createSession(ctx, u.ID, in.IP, in.UserAgent)
	if err != nil {
		return db.User{}, NewSession{}, err
	}
	err = s.audit.Append(ctx, audit.Entry{
		ActorID: u.ID, Action: audit.ActionLogin, TargetKind: audit.TargetSession, TargetID: sess.ID, IP: in.IP,
	})
	if err != nil {
		return db.User{}, NewSession{}, err
	}
	return u, sess, nil
}

// checkPassword returns the active user with this username and password, or
// ErrInvalidCredentials. Unknown users still pay for one hash so timing does not reveal them.
func (s *Service) checkPassword(ctx context.Context, username, password string) (db.User, error) {
	u, err := s.store.R().GetUserByUsername(ctx, username)
	if errors.Is(err, sql.ErrNoRows) {
		_, _ = s.verifyPassword(s.dummyPasswordHash(), password)
		return db.User{}, ErrInvalidCredentials
	}
	if err != nil {
		return db.User{}, err
	}
	ok, err := s.verifyPassword(u.PasswordHash, password)
	if err != nil {
		return db.User{}, fmt.Errorf("verify password: %w", err)
	}
	if !ok || u.DisabledAt.Valid {
		return db.User{}, ErrInvalidCredentials
	}
	return u, nil
}

func (s *Service) dummyPasswordHash() string {
	s.dummyOnce.Do(func() {
		s.dummyHash, _ = authn.HashPassword("partyline-dummy-password")
	})
	return s.dummyHash
}

// CreateSession opens a session for a user that was authenticated by other means
// (setup, invite acceptance).
func (s *Service) CreateSession(ctx context.Context, userID, ip, userAgent string) (NewSession, error) {
	return s.createSession(ctx, userID, ip, userAgent)
}

func (s *Service) createSession(ctx context.Context, userID, ip, userAgent string) (NewSession, error) {
	secret, err := authn.NewSessionToken()
	if err != nil {
		return NewSession{}, err
	}
	now := s.now()
	sess := NewSession{ID: store.NewID(), Secret: secret, ExpiresAt: now.Add(SessionTTL)}
	err = s.store.WithTx(ctx, func(q *db.Queries) error {
		return q.CreateSession(ctx, db.CreateSessionParams{
			ID: sess.ID, TokenHash: authn.HashToken(secret), UserID: userID,
			CreatedAt: ms(now), ExpiresAt: ms(sess.ExpiresAt), LastSeenAt: ms(now),
			UserAgent: userAgent, Ip: ip,
		})
	})
	if err != nil {
		return NewSession{}, err
	}
	return sess, nil
}

// ValidateSession resolves a session cookie value to its user principal; see CheckSession.
func (s *Service) ValidateSession(ctx context.Context, secret string) (authn.Principal, error) {
	p, _, err := s.CheckSession(ctx, secret)
	return p, err
}

// CheckSession resolves a session cookie value to its user principal. Unknown, expired or
// revoked sessions and sessions of disabled users return ErrUnauthenticated.
//
// Use slides the session: when now + SessionTTL is at least SessionSlideInterval past the
// stored expiry, the expiry moves to now + SessionTTL and renewed is true, telling the caller
// to re-issue the cookie with a fresh SessionTTL Max-Age (AUTH-03). Otherwise only
// last_seen_at is written, at most once per minute.
func (s *Service) CheckSession(ctx context.Context, secret string) (p authn.Principal, renewed bool, err error) {
	sess, err := s.store.R().GetSessionByTokenHash(ctx, authn.HashToken(secret))
	if errors.Is(err, sql.ErrNoRows) {
		return authn.Principal{}, false, ErrUnauthenticated
	}
	if err != nil {
		return authn.Principal{}, false, err
	}
	now := s.now()
	if ms(now) >= sess.ExpiresAt {
		return authn.Principal{}, false, ErrUnauthenticated
	}
	u, err := s.activeUser(ctx, sess.UserID)
	if err != nil {
		return authn.Principal{}, false, err
	}
	expires := sess.ExpiresAt
	if next := ms(now.Add(SessionTTL)); next-expires >= SessionSlideInterval.Milliseconds() {
		expires, renewed = next, true
	}
	if renewed || ms(now)-sess.LastSeenAt >= touchInterval.Milliseconds() {
		err := s.store.WithTx(ctx, func(q *db.Queries) error {
			return q.TouchSession(ctx, db.TouchSessionParams{LastSeenAt: ms(now), ExpiresAt: expires, ID: sess.ID})
		})
		if err != nil {
			return authn.Principal{}, false, err
		}
	}
	return authn.Principal{Kind: authn.KindUser, UserID: u.ID, Role: u.Role, SessionID: sess.ID}, renewed, nil
}

// activeUser returns the user or ErrUnauthenticated when it is missing or disabled.
func (s *Service) activeUser(ctx context.Context, id string) (db.User, error) {
	u, err := s.store.R().GetUser(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return db.User{}, ErrUnauthenticated
	}
	if err != nil {
		return db.User{}, err
	}
	if u.DisabledAt.Valid {
		return db.User{}, ErrUnauthenticated
	}
	return u, nil
}

// RevokeSession deletes one of the user's sessions (logout, AUTH-09). It returns
// ErrNotFound when the session does not belong to the user.
func (s *Service) RevokeSession(ctx context.Context, userID, sessionID string) error {
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		n, err := q.DeleteUserSession(ctx, db.DeleteUserSessionParams{ID: sessionID, UserID: userID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		action := audit.ActionSessionRevoke
		if p, ok := authn.PrincipalFrom(ctx); ok && p.SessionID == sessionID {
			action = audit.ActionLogout
		}
		return s.audit.Record(ctx, q, audit.Entry{Action: action, TargetKind: audit.TargetSession, TargetID: sessionID})
	})
	if err != nil {
		return err
	}
	s.sessionsRevoked(userID, []string{sessionID})
	return nil
}

// PrincipalActive reports whether p still authenticates: its session (or token) exists and
// has not expired or been revoked, and its user is active. Live sockets re-check it.
func (s *Service) PrincipalActive(ctx context.Context, p authn.Principal) (bool, error) {
	now := ms(s.now())
	switch {
	case p.SessionID != "":
		sess, err := s.store.R().GetSession(ctx, p.SessionID)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if sess.UserID != p.UserID || now >= sess.ExpiresAt {
			return false, nil
		}
	case p.TokenID != "":
		tok, err := s.store.R().GetToken(ctx, p.TokenID)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if tok.UserID != p.UserID || tok.RevokedAt.Valid || (tok.ExpiresAt.Valid && now >= tok.ExpiresAt.Int64) {
			return false, nil
		}
	}
	_, err := s.activeUser(ctx, p.UserID)
	if errors.Is(err, ErrUnauthenticated) {
		return false, nil
	}
	return err == nil, err
}
