package users

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/audit"
)

func (f *fixture) tokenIDs(t *testing.T, userID string) []string {
	t.Helper()
	list, err := f.svc.ListTokens(context.Background(), userID)
	require.NoError(t, err)
	ids := make([]string, len(list))
	for i, tok := range list {
		ids[i] = tok.ID
	}
	return ids
}

// TOKH-03: the retention purge deletes tokens revoked more than 30 days earlier; a token
// revoked 29 days earlier, an active one and an expired but unrevoked one stay.
func TestPurgeRevokedTokensAfter30Days(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)
	uid := f.adminID(t)
	soon := f.clock.Now().Add(time.Hour)

	old, _ := f.token(t, uid, dto.TokenKindUser, nil)
	recent, _ := f.token(t, uid, dto.TokenKindUser, nil)
	active, _ := f.token(t, uid, dto.TokenKindAgent, nil)
	expired, _ := f.token(t, uid, dto.TokenKindUser, &soon)

	require.NoError(t, f.svc.RevokeToken(ctx, uid, old))
	f.clock.Advance(2 * 24 * time.Hour)
	require.NoError(t, f.svc.RevokeToken(ctx, uid, recent))
	f.clock.Advance(29 * 24 * time.Hour) // old: revoked 31 days ago; recent: 29 days ago

	n, err := f.svc.PurgeRevokedTokens(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	require.ElementsMatch(t, []string{recent, active, expired}, f.tokenIDs(t, uid))

	f.clock.Advance(2 * 24 * time.Hour) // recent: 31 days ago
	n, err = f.svc.PurgeRevokedTokens(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	require.ElementsMatch(t, []string{active, expired}, f.tokenIDs(t, uid))
	require.Equal(t, 30*24*time.Hour, RevokedTokenRetention)
}

// TOKH-02: a user deletes their own revoked token, which is audited; an active token is
// ErrTokenActive (409) and stays; another user's or an unknown token is ErrNotFound.
func TestPurgeToken(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)
	uid := f.adminID(t)
	bob, err := f.svc.CreateUser(ctx, AccountInput{Username: "bob", Password: memberPassword}, dto.RoleMember)
	require.NoError(t, err)

	revoked, secret := f.token(t, uid, dto.TokenKindUser, nil)
	active, _ := f.token(t, uid, dto.TokenKindUser, nil)
	require.NoError(t, f.svc.RevokeToken(ctx, uid, revoked))

	require.ErrorIs(t, f.svc.PurgeToken(ctx, uid, active), ErrTokenActive)
	require.ErrorIs(t, f.svc.PurgeToken(ctx, bob.ID, revoked), ErrNotFound)
	require.ErrorIs(t, f.svc.PurgeToken(ctx, uid, "00000000-0000-7000-8000-000000000000"), ErrNotFound)
	require.ElementsMatch(t, []string{revoked, active}, f.tokenIDs(t, uid))

	require.NoError(t, f.svc.PurgeToken(ctx, uid, revoked))
	require.Equal(t, []string{active}, f.tokenIDs(t, uid))
	require.ErrorIs(t, f.svc.PurgeToken(ctx, uid, revoked), ErrNotFound)
	_, err = f.svc.AuthenticateToken(ctx, secret)
	require.ErrorIs(t, err, ErrUnauthenticated)

	entries, _, err := audit.New(f.store, f.clock.Now).List(ctx, "")
	require.NoError(t, err)
	var purges []string
	for _, e := range entries {
		if e.Action == audit.ActionTokenPurge {
			purges = append(purges, e.TargetID)
		}
	}
	require.Equal(t, []string{revoked}, purges)
	require.Equal(t, "token.purge", audit.ActionTokenPurge)
}
