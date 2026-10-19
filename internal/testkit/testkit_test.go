package testkit_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/testkit"
)

// The kit boots a ready server and logs accounts in with real sessions.
func TestKitBootsAndLogsIn(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)

	env.Anon().Get("/healthz").RequireStatus(http.StatusOK)
	env.Anon().Get("/readyz").RequireStatus(http.StatusOK)

	admin := env.SetupAdmin()
	require.Same(t, admin, env.SetupAdmin())
	p, err := env.App.Users.ValidateSession(ctx, admin.Session)
	require.NoError(t, err)
	require.Equal(t, authn.KindUser, p.Kind)
	require.Equal(t, admin.User.ID, p.UserID)
	require.Equal(t, "admin", p.Role)

	bob := env.NewUser("bob")
	p, err = env.App.Users.ValidateSession(ctx, bob.Session)
	require.NoError(t, err)
	require.Equal(t, bob.User.ID, p.UserID)
	require.Equal(t, "member", p.Role)
}
