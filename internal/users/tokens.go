package users

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// maxTokenName is the longest accepted token name in characters.
const maxTokenName = 64

// RevokedTokenRetention is how long a revoked token stays listed before the retention job
// deletes it (TOKH-03); the audit log keeps its record.
const RevokedTokenRetention = 30 * 24 * time.Hour

// ErrTokenActive refuses to delete a token that is not revoked (TOKH-02): revoke it first. 409.
var ErrTokenActive = errors.New("token is not revoked")

// TokenInput describes a new API token; a nil ExpiresAt never expires.
type TokenInput struct {
	Kind      dto.TokenKind
	Name      string
	ExpiresAt *time.Time
}

// CreateToken creates an API token for userID and returns it with its secret, which is shown
// once and stored only as a SHA-256 hash (TOK-01).
func (s *Service) CreateToken(ctx context.Context, userID string, in TokenInput) (db.ApiToken, string, error) {
	var (
		tok    db.ApiToken
		secret string
	)
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		var err error
		tok, secret, err = s.CreateTokenTx(ctx, q, userID, in)
		return err
	})
	if err != nil {
		return db.ApiToken{}, "", err
	}
	return tok, secret, nil
}

// CreateTokenTx is CreateToken inside the caller's write transaction q (the token and its
// audit entry commit with the caller's changes).
func (s *Service) CreateTokenTx(ctx context.Context, q *db.Queries, userID string, in TokenInput) (db.ApiToken, string, error) {
	now := s.now()
	name := strings.TrimSpace(in.Name)
	fields := map[string]string{}
	if in.Kind != dto.TokenKindAgent && in.Kind != dto.TokenKindUser {
		fields["kind"] = "must be agent or user"
	}
	if name == "" || utf8.RuneCountInString(name) > maxTokenName {
		fields["name"] = "must have 1 to 64 characters"
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(now) {
		fields["expires_at"] = "must be in the future"
	}
	if len(fields) > 0 {
		return db.ApiToken{}, "", &ValidationError{Fields: fields}
	}

	secret, err := authn.NewToken(authn.TokenKind(in.Kind))
	if err != nil {
		return db.ApiToken{}, "", err
	}
	tok := db.ApiToken{
		ID: store.NewID(), UserID: userID, Kind: string(in.Kind), Name: name,
		Prefix: authn.TokenPrefix(secret), TokenHash: authn.HashToken(secret), CreatedAt: ms(now),
	}
	if in.ExpiresAt != nil {
		tok.ExpiresAt = sql.NullInt64{Int64: ms(*in.ExpiresAt), Valid: true}
	}
	if err := q.CreateToken(ctx, db.CreateTokenParams{
		ID: tok.ID, UserID: tok.UserID, Kind: tok.Kind, Name: tok.Name, Prefix: tok.Prefix,
		TokenHash: tok.TokenHash, CreatedAt: tok.CreatedAt, ExpiresAt: tok.ExpiresAt,
	}); err != nil {
		return db.ApiToken{}, "", err
	}
	if err := s.audit.Record(ctx, q, audit.Entry{
		Action: audit.ActionTokenCreate, TargetKind: audit.TargetToken, TargetID: tok.ID,
		Detail: map[string]any{"kind": tok.Kind, "name": tok.Name, "prefix": tok.Prefix},
	}); err != nil {
		return db.ApiToken{}, "", err
	}
	return tok, secret, nil
}

// ListTokens returns the user's tokens, newest first. Rows hold only the hash and prefix.
func (s *Service) ListTokens(ctx context.Context, userID string) ([]db.ApiToken, error) {
	return s.store.R().ListTokensByUser(ctx, userID)
}

// RevokeToken revokes one of the user's tokens; ErrNotFound when the user does not own it.
func (s *Service) RevokeToken(ctx context.Context, userID, id string) error {
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		n, err := q.RevokeToken(ctx, db.RevokeTokenParams{
			RevokedAt: sql.NullInt64{Int64: ms(s.now()), Valid: true}, ID: id, UserID: userID,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return s.audit.Record(ctx, q, audit.Entry{Action: audit.ActionTokenRevoke, TargetKind: audit.TargetToken, TargetID: id})
	})
	if err != nil {
		return err
	}
	s.revoked(events.SessionsRevoked{UserID: userID, TokenIDs: []string{id}})
	return nil
}

// PurgeToken deletes one of the user's revoked tokens and audits it (TOKH-02). ErrTokenActive
// when it is not revoked, ErrNotFound when the user does not own it.
func (s *Service) PurgeToken(ctx context.Context, userID, id string) error {
	return s.store.WithTx(ctx, func(q *db.Queries) error {
		n, err := q.DeleteRevokedToken(ctx, db.DeleteRevokedTokenParams{ID: id, UserID: userID})
		if err != nil {
			return err
		}
		if n == 0 {
			tok, err := q.GetToken(ctx, id)
			if errors.Is(err, sql.ErrNoRows) || (err == nil && tok.UserID != userID) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			return ErrTokenActive
		}
		return s.audit.Record(ctx, q, audit.Entry{Action: audit.ActionTokenPurge, TargetKind: audit.TargetToken, TargetID: id})
	})
}

// PurgeRevokedTokens deletes every token revoked more than RevokedTokenRetention ago and
// returns how many it removed (TOKH-03). The daily retention job runs it.
func (s *Service) PurgeRevokedTokens(ctx context.Context) (int64, error) {
	var n int64
	err := s.store.WithTx(ctx, func(q *db.Queries) (err error) {
		n, err = q.DeleteTokensRevokedBefore(ctx, sql.NullInt64{Int64: ms(s.now().Add(-RevokedTokenRetention)), Valid: true})
		return err
	})
	return n, err
}

// AuthenticateToken resolves a bearer secret to its principal: an agent principal for agent
// tokens, a user principal otherwise. Malformed, unknown, revoked or expired tokens and tokens
// of disabled users return ErrUnauthenticated (TOK-02, AUTH-10). last_used_at is written at
// most once per minute (TOK-04).
func (s *Service) AuthenticateToken(ctx context.Context, secret string) (authn.Principal, error) {
	if _, ok := authn.ParseTokenKind(secret); !ok {
		return authn.Principal{}, ErrUnauthenticated
	}
	tok, err := s.store.R().GetTokenByHash(ctx, authn.HashToken(secret))
	if errors.Is(err, sql.ErrNoRows) {
		return authn.Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return authn.Principal{}, err
	}
	now := s.now()
	if tok.RevokedAt.Valid || (tok.ExpiresAt.Valid && ms(now) >= tok.ExpiresAt.Int64) {
		return authn.Principal{}, ErrUnauthenticated
	}
	u, err := s.activeUser(ctx, tok.UserID)
	if err != nil {
		return authn.Principal{}, err
	}
	if !tok.LastUsedAt.Valid || ms(now)-tok.LastUsedAt.Int64 >= touchInterval.Milliseconds() {
		err := s.store.WithTx(ctx, func(q *db.Queries) error {
			return q.TouchToken(ctx, db.TouchTokenParams{
				Now:         sql.NullInt64{Int64: ms(now), Valid: true},
				ID:          tok.ID,
				StaleBefore: sql.NullInt64{Int64: ms(now) - touchInterval.Milliseconds(), Valid: true},
			})
		})
		if err != nil {
			return authn.Principal{}, err
		}
	}
	kind := authn.KindUser
	if tok.Kind == string(dto.TokenKindAgent) {
		kind = authn.KindAgent
	}
	return authn.Principal{Kind: kind, UserID: u.ID, Role: u.Role, TokenID: tok.ID}, nil
}
