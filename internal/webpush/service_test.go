package webpush_test

import (
	"context"
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
