package webpush_test

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
	"github.com/wnexous/partyline/internal/webpush"
)

// fakeTestQueue answers with a fixed number of queued jobs and records nothing: the service's
// rate limit and subscription lookup are what the test observes.
type fakeTestQueue struct{ queued int }

func (q fakeTestQueue) EnqueueTest(context.Context, []db.PushSubscription) int { return q.queued }

// TestServiceTestRouteStates drives webpush.Service.Test through every state the route maps to a
// status: ErrBusy becomes 503, ErrNoSubscriptions 409 and ErrRateLimited 429 (PUSH-15, PUSH-27).
func TestServiceTestRouteStates(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	alice := env.NewUser("alice")
	bob := env.NewUser("bob")
	subscribe(t, env, alice.User.ID, "https://push.example.test/e2e")
	clock := testkit.NewClock(time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC))

	newService := func(t *testing.T, queued int) *webpush.Service {
		t.Helper()
		return webpush.NewService(webpush.ServiceDeps{
			Store: env.App.Store, VAPID: env.App.VAPID, Queue: fakeTestQueue{queued: queued}, Now: clock.Now,
		})
	}

	t.Run("the queue is full", func(t *testing.T) {
		_, err := newService(t, 0).Test(context.Background(), alice.User.ID)
		require.ErrorIs(t, err, webpush.ErrBusy)
	})

	t.Run("the queue took every subscription", func(t *testing.T) {
		queued, err := newService(t, 2).Test(context.Background(), alice.User.ID)
		require.NoError(t, err)
		require.Equal(t, 2, queued)
	})

	t.Run("the user has no subscription", func(t *testing.T) {
		_, err := newService(t, 1).Test(context.Background(), bob.User.ID)
		require.ErrorIs(t, err, webpush.ErrNoSubscriptions)
	})

	t.Run("the fourth ask in a minute", func(t *testing.T) {
		service := newService(t, 1)
		for range 3 {
			_, err := service.Test(context.Background(), alice.User.ID)
			require.NoError(t, err)
		}
		_, err := service.Test(context.Background(), alice.User.ID)
		require.ErrorIs(t, err, webpush.ErrRateLimited)
	})

	t.Run("a minute later the limit is open again", func(t *testing.T) {
		service := newService(t, 1)
		for range 3 {
			_, err := service.Test(context.Background(), alice.User.ID)
			require.NoError(t, err)
		}
		clock.Advance(time.Minute)
		_, err := service.Test(context.Background(), alice.User.ID)
		require.NoError(t, err)
	})
}

// serviceSubscriptionInput returns one valid registration of endpoint for userID.
func serviceSubscriptionInput(t *testing.T, userID, endpoint string) webpush.SubscribeInput {
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

// TestSubscribeCapsTheDevicesPerUser: an eleven-device account keeps its ten newest rows and
// loses the oldest; re-registering an existing endpoint at the cap deletes nothing, and another
// account's rows are untouched (PUSH-03, PUSH-27).
func TestSubscribeCapsTheDevicesPerUser(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	alice := env.NewUser("alice")
	bob := env.NewUser("bob")
	clock := testkit.NewClock(time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC))
	service := webpush.NewService(webpush.ServiceDeps{
		Store: env.App.Store, VAPID: env.App.VAPID, Queue: fakeTestQueue{queued: 1}, Now: clock.Now,
	})
	ctx := context.Background()

	bobRow, err := service.Subscribe(ctx, serviceSubscriptionInput(t, bob.User.ID, "https://push.example.test/bob"))
	require.NoError(t, err)

	firstEndpoint := "https://push.example.test/device-0"
	for i := 0; i <= webpush.MaxSubscriptionsPerUser; i++ {
		endpoint := firstEndpoint
		if i > 0 {
			endpoint = fmt.Sprintf("https://push.example.test/device-%d", i)
		}
		_, err := service.Subscribe(ctx, serviceSubscriptionInput(t, alice.User.ID, endpoint))
		require.NoError(t, err)
		clock.Advance(time.Millisecond)
	}

	rows, err := env.App.Store.R().ListPushSubscriptionsByUser(ctx, alice.User.ID)
	require.NoError(t, err)
	require.Len(t, rows, webpush.MaxSubscriptionsPerUser)
	for _, row := range rows {
		require.NotEqual(t, firstEndpoint, row.Endpoint, "the oldest endpoint made room")
	}

	// A re-registration of an existing endpoint updates its row and deletes none.
	before := make(map[string]string, len(rows))
	for _, row := range rows {
		before[row.Endpoint] = row.ID
	}
	_, err = service.Subscribe(ctx, serviceSubscriptionInput(t, alice.User.ID, rows[0].Endpoint))
	require.NoError(t, err)
	rows, err = env.App.Store.R().ListPushSubscriptionsByUser(ctx, alice.User.ID)
	require.NoError(t, err)
	require.Len(t, rows, webpush.MaxSubscriptionsPerUser)
	for _, row := range rows {
		require.Equal(t, before[row.Endpoint], row.ID, "an existing endpoint keeps its row")
	}

	bobRows, err := env.App.Store.R().ListPushSubscriptionsByUser(ctx, bob.User.ID)
	require.NoError(t, err)
	require.Len(t, bobRows, 1)
	require.Equal(t, bobRow.ID, bobRows[0].ID, "another account's row is untouched")
}
