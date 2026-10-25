package app_test

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/testkit"
	"github.com/wnexous/partyline/internal/webpush"
)

// appSubscriptionInput returns one valid registration of endpoint for userID.
func appSubscriptionInput(t *testing.T, userID, endpoint string) webpush.SubscribeInput {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	auth := make([]byte, 16)
	_, err = rand.Read(auth)
	require.NoError(t, err)
	return webpush.SubscribeInput{
		UserID: userID, Endpoint: endpoint,
		P256dh: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		Auth:   base64.RawURLEncoding.EncodeToString(auth),
	}
}

// TestAppStartResetsSubscriptionsForANewVAPIDKey: a missing or corrupt key file makes a new pair
// and Start drops every stored subscription through the store; a restart with the same key keeps
// them (PUSH-26).
func TestAppStartResetsSubscriptionsForANewVAPIDKey(t *testing.T) {
	corrupt := func(t *testing.T, path string) {
		t.Helper()
		require.NoError(t, os.WriteFile(path, []byte("not-base64\n"), 0o600))
	}
	missing := func(t *testing.T, path string) {
		t.Helper()
		require.NoError(t, os.Remove(path))
	}
	cases := []struct {
		name string
		act  func(t *testing.T, path string)
	}{
		{"missing key file", missing},
		{"corrupt key file", corrupt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := testkit.New(t, testkit.WithoutPush())
			alice := env.NewUser("alice")
			_, err := env.App.PushService.Subscribe(context.Background(),
				appSubscriptionInput(t, alice.User.ID, "https://push.example.test/one"))
			require.NoError(t, err)
			before := env.App.VAPID.Public
			tc.act(t, filepath.Join(env.App.Config.DataDir, webpush.VAPIDKeyFile))

			env.Restart()
			require.NotEqual(t, before, env.App.VAPID.Public)
			require.True(t, env.App.VAPID.New)
			rows, err := env.App.Store.R().ListPushSubscriptionsByUser(context.Background(), alice.User.ID)
			require.NoError(t, err)
			require.Empty(t, rows)
		})
	}

	t.Run("valid key keeps the rows", func(t *testing.T) {
		env := testkit.New(t, testkit.WithoutPush())
		alice := env.NewUser("alice")
		_, err := env.App.PushService.Subscribe(context.Background(),
			appSubscriptionInput(t, alice.User.ID, "https://push.example.test/one"))
		require.NoError(t, err)
		before := env.App.VAPID.Public

		env.Restart()
		require.Equal(t, before, env.App.VAPID.Public)
		require.False(t, env.App.VAPID.New)
		rows, err := env.App.Store.R().ListPushSubscriptionsByUser(context.Background(), alice.User.ID)
		require.NoError(t, err)
		require.Len(t, rows, 1)
	})
}
