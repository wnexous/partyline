package users

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
)

func (f *fixture) token(t *testing.T, userID string, kind dto.TokenKind, expiresAt *time.Time) (string, string) {
	t.Helper()
	tok, secret, err := f.svc.CreateToken(context.Background(), userID, TokenInput{Kind: kind, Name: "laptop", ExpiresAt: expiresAt})
	require.NoError(t, err)
	return tok.ID, secret
}

func (f *fixture) lastUsedAt(t *testing.T, userID, tokenID string) (int64, bool) {
	t.Helper()
	list, err := f.svc.ListTokens(context.Background(), userID)
	require.NoError(t, err)
	for _, tok := range list {
		if tok.ID == tokenID {
			return tok.LastUsedAt.Int64, tok.LastUsedAt.Valid
		}
	}
	t.Fatalf("token %s not listed", tokenID)
	return 0, false
}

// TOK-01: the secret is shown once with its kind prefix; afterwards only the 12-char prefix.
func TestCreateTokenReturnsSecretOnce(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)
	uid := f.adminID(t)

	for _, kind := range []dto.TokenKind{dto.TokenKindAgent, dto.TokenKindUser} {
		tok, secret, err := f.svc.CreateToken(ctx, uid, TokenInput{Kind: kind, Name: "ci " + string(kind)})
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(secret, "pl_"+string(kind)+"_"), secret)
		require.Equal(t, secret[:12], tok.Prefix)
		require.Len(t, tok.Prefix, 12)
		require.Equal(t, string(kind), tok.Kind)
		require.Equal(t, authn.HashToken(secret), tok.TokenHash)
		require.False(t, tok.ExpiresAt.Valid)
	}

	list, err := f.svc.ListTokens(ctx, uid)
	require.NoError(t, err)
	require.Len(t, list, 2)
	for _, tok := range list {
		require.Len(t, tok.Prefix, 12)
		require.NotContains(t, tok.TokenHash, "pl_")
		require.Len(t, tok.TokenHash, 64)
	}
}

func TestCreateTokenValidatesInput(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)
	uid := f.adminID(t)
	past := f.clock.Now().Add(-time.Second)

	cases := []struct {
		name  string
		in    TokenInput
		field string
	}{
		{"unknown kind", TokenInput{Kind: "root", Name: "x"}, "kind"},
		{"runner kind", TokenInput{Kind: dto.TokenKindRunner, Name: "x"}, "kind"},
		{"empty name", TokenInput{Kind: dto.TokenKindUser, Name: "  "}, "name"},
		{"long name", TokenInput{Kind: dto.TokenKindUser, Name: strings.Repeat("n", 65)}, "name"},
		{"past expiry", TokenInput{Kind: dto.TokenKindUser, Name: "x", ExpiresAt: &past}, "expires_at"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := f.svc.CreateToken(ctx, uid, tc.in)
			var verr *ValidationError
			require.ErrorAs(t, err, &verr)
			require.Contains(t, verr.Fields, tc.field)
		})
	}
	list, err := f.svc.ListTokens(ctx, uid)
	require.NoError(t, err)
	require.Empty(t, list)
}

// The principal carries the token's kind, owner and id.
func TestAuthenticateTokenResolvesPrincipal(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)
	uid := f.adminID(t)

	agentID, agentSecret := f.token(t, uid, dto.TokenKindAgent, nil)
	p, err := f.svc.AuthenticateToken(ctx, agentSecret)
	require.NoError(t, err)
	require.Equal(t, authn.KindAgent, p.Kind)
	require.Equal(t, uid, p.UserID)
	require.Equal(t, agentID, p.TokenID)
	require.Equal(t, "admin", p.Role)

	userID, userSecret := f.token(t, uid, dto.TokenKindUser, nil)
	p, err = f.svc.AuthenticateToken(ctx, userSecret)
	require.NoError(t, err)
	require.Equal(t, authn.KindUser, p.Kind)
	require.Equal(t, userID, p.TokenID)
	require.Empty(t, p.SessionID)
}

// CA-01: a runner token is created by the device approval path and authenticates as a runner
// principal, so the middleware keeps it on the runner routes.
func TestCreateRunnerTokenIsARunnerPrincipal(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)
	uid := f.adminID(t)

	tok, secret, err := f.svc.CreateRunnerToken(ctx, uid, "devbox (runner)")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(secret, "pl_runner_"), secret)
	require.Equal(t, string(dto.TokenKindRunner), tok.Kind)

	p, err := f.svc.AuthenticateToken(ctx, secret)
	require.NoError(t, err)
	require.Equal(t, authn.KindRunner, p.Kind)
	require.Equal(t, uid, p.UserID)
	require.Equal(t, tok.ID, p.TokenID)
}

// TOK-02: revoked, expired, unknown and malformed tokens are unauthenticated.
func TestAuthenticateTokenRejectsInvalid(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)
	uid := f.adminID(t)

	revokedID, revoked := f.token(t, uid, dto.TokenKindUser, nil)
	require.NoError(t, f.svc.RevokeToken(ctx, uid, revokedID))
	_, err := f.svc.AuthenticateToken(ctx, revoked)
	require.ErrorIs(t, err, ErrUnauthenticated)

	exp := f.clock.Now().Add(time.Hour)
	_, expiring := f.token(t, uid, dto.TokenKindAgent, &exp)
	f.clock.Advance(time.Hour - time.Millisecond)
	_, err = f.svc.AuthenticateToken(ctx, expiring)
	require.NoError(t, err)
	f.clock.Advance(time.Millisecond)
	_, err = f.svc.AuthenticateToken(ctx, expiring)
	require.ErrorIs(t, err, ErrUnauthenticated)

	unknown, err := authn.NewToken(authn.TokenUser)
	require.NoError(t, err)
	_, err = f.svc.AuthenticateToken(ctx, unknown)
	require.ErrorIs(t, err, ErrUnauthenticated)

	_, err = f.svc.AuthenticateToken(ctx, "not-a-token")
	require.ErrorIs(t, err, ErrUnauthenticated)
}

func TestRevokeTokenOfAnotherUserIsNotFound(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)
	bob, err := f.svc.CreateUser(ctx, AccountInput{Username: "bob", Password: memberPassword}, dto.RoleMember)
	require.NoError(t, err)
	id, secret := f.token(t, f.adminID(t), dto.TokenKindUser, nil)

	require.ErrorIs(t, f.svc.RevokeToken(ctx, bob.ID, id), ErrNotFound)
	_, err = f.svc.AuthenticateToken(ctx, secret)
	require.NoError(t, err)
}

// AUTH-10: a disabled user's tokens stop authenticating and work again when re-enabled.
func TestDisabledUserTokensFail(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)
	bob, err := f.svc.CreateUser(ctx, AccountInput{Username: "bob", Password: memberPassword}, dto.RoleMember)
	require.NoError(t, err)
	_, secret := f.token(t, bob.ID, dto.TokenKindAgent, nil)

	disabled := true
	_, err = f.svc.UpdateUser(ctx, bob.ID, UserUpdate{Disabled: &disabled})
	require.NoError(t, err)
	_, err = f.svc.AuthenticateToken(ctx, secret)
	require.ErrorIs(t, err, ErrUnauthenticated)

	enabled := false
	_, err = f.svc.UpdateUser(ctx, bob.ID, UserUpdate{Disabled: &enabled})
	require.NoError(t, err)
	_, err = f.svc.AuthenticateToken(ctx, secret)
	require.NoError(t, err)
}

// TOK-04: last_used_at is written at most once per minute.
func TestAuthenticateTokenUpdatesLastUsedOncePerMinute(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)
	uid := f.adminID(t)
	id, secret := f.token(t, uid, dto.TokenKindUser, nil)

	_, used := f.lastUsedAt(t, uid, id)
	require.False(t, used)

	first := f.clock.Now()
	_, err := f.svc.AuthenticateToken(ctx, secret)
	require.NoError(t, err)
	at, used := f.lastUsedAt(t, uid, id)
	require.True(t, used)
	require.Equal(t, first.UnixMilli(), at)

	f.clock.Advance(59 * time.Second)
	_, err = f.svc.AuthenticateToken(ctx, secret)
	require.NoError(t, err)
	at, _ = f.lastUsedAt(t, uid, id)
	require.Equal(t, first.UnixMilli(), at)

	f.clock.Advance(time.Second)
	_, err = f.svc.AuthenticateToken(ctx, secret)
	require.NoError(t, err)
	at, _ = f.lastUsedAt(t, uid, id)
	require.Equal(t, first.Add(time.Minute).UnixMilli(), at)
}

// AUTH-09/AUTH-10, TOK-02 for live sockets: PrincipalActive follows the session or token
// and the user's state, so the live re-check closes what the next request would reject.
func TestPrincipalActive(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)
	bob, err := f.svc.CreateUser(ctx, AccountInput{Username: "bob", Password: memberPassword}, dto.RoleMember)
	require.NoError(t, err)
	sess, err := f.svc.CreateSession(ctx, bob.ID, "192.0.2.1", "test")
	require.NoError(t, err)
	tokID, secret := f.token(t, bob.ID, dto.TokenKindUser, nil)
	tokP, err := f.svc.AuthenticateToken(ctx, secret)
	require.NoError(t, err)
	sessP := authn.Principal{Kind: authn.KindUser, UserID: bob.ID, SessionID: sess.ID}

	active := func(p authn.Principal) bool {
		t.Helper()
		ok, err := f.svc.PrincipalActive(ctx, p)
		require.NoError(t, err)
		return ok
	}
	require.True(t, active(sessP))
	require.True(t, active(tokP))
	// Another existing, active user: only the owner check can reject these.
	admin, err := f.store.R().GetUserByUsername(ctx, "admin")
	require.NoError(t, err)
	require.True(t, active(authn.Principal{Kind: authn.KindUser, UserID: admin.ID}), "admin is active")
	require.False(t, active(authn.Principal{Kind: authn.KindUser, UserID: admin.ID, SessionID: sess.ID}), "session of another user")
	require.False(t, active(authn.Principal{Kind: authn.KindUser, UserID: admin.ID, TokenID: tokID}), "token of another user")

	disabled := true
	_, err = f.svc.UpdateUser(ctx, bob.ID, UserUpdate{Disabled: &disabled})
	require.NoError(t, err)
	require.False(t, active(tokP), "disabled user")
	require.False(t, active(sessP), "disabling ends the session")
	disabled = false
	_, err = f.svc.UpdateUser(ctx, bob.ID, UserUpdate{Disabled: &disabled})
	require.NoError(t, err)
	require.True(t, active(tokP))

	require.NoError(t, f.svc.RevokeToken(ctx, bob.ID, tokID))
	require.False(t, active(tokP), "revoked token")

	sess, err = f.svc.CreateSession(ctx, bob.ID, "192.0.2.1", "test")
	require.NoError(t, err)
	f.clock.Advance(SessionTTL)
	require.False(t, active(authn.Principal{Kind: authn.KindUser, UserID: bob.ID, SessionID: sess.ID}), "expired session")
}
