package api_test

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// pushKeyPair returns one valid browser subscription key material: an uncompressed P-256 point
// and a 16-byte auth secret, both base64url without padding.
func pushKeyPair(t *testing.T) (p256dh, auth string) {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	secret := make([]byte, 16)
	_, err = rand.Read(secret)
	require.NoError(t, err)
	return base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		base64.RawURLEncoding.EncodeToString(secret)
}

// subscribePush stores endpoint with fresh keys for c and returns the answer.
func subscribePush(t *testing.T, c *testkit.Client, endpoint string) *testkit.Response {
	t.Helper()
	p256dh, auth := pushKeyPair(t)
	return c.Post("/api/v1/push/subscriptions", dto.PushSubscriptionRequest{
		Endpoint: endpoint, P256dh: p256dh, Auth: auth,
	})
}

// pushSubscriptions lists the stored subscriptions of userID.
func pushSubscriptions(t *testing.T, env *testkit.Env, userID string) []db.PushSubscription {
	t.Helper()
	rows, err := env.App.Store.R().ListPushSubscriptionsByUser(context.Background(), userID)
	require.NoError(t, err)
	return rows
}

// TestPushKey: an anonymous caller gets 401, an agent token 403, and the account's key is the
// 65-byte uncompressed P-256 public point (PUSH-02).
func TestPushKey(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	bob := env.NewUser("bob")

	env.Anon().Get("/api/v1/push/key").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	bob.Token(dto.TokenKindAgent, "agent").Get("/api/v1/push/key").
		RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)

	key := testkit.Decode[dto.PushKey](bob.Get("/api/v1/push/key").RequireStatus(http.StatusOK))
	require.NotEmpty(t, key.PublicKey)
	point, err := base64.RawURLEncoding.DecodeString(key.PublicKey)
	require.NoError(t, err)
	require.Len(t, point, 65)
	require.Equal(t, byte(0x04), point[0])
}

// TestSubscribeStoresAndUpserts: the first POST stores one row for the caller and the same
// endpoint again updates it instead of adding another (PUSH-03).
func TestSubscribeStoresAndUpserts(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	alice := env.NewUser("alice")
	const endpoint = "https://push.example.test/e2e"

	first := testkit.Decode[dto.PushSubscription](subscribePush(t, alice, endpoint).RequireStatus(http.StatusOK))
	require.NotEmpty(t, first.ID)
	require.Equal(t, endpoint, first.Endpoint)
	require.False(t, first.CreatedAt.IsZero())
	require.Nil(t, first.LastOkAt, "nothing was delivered yet")

	nextP256dh, nextAuth := pushKeyPair(t)
	second := testkit.Decode[dto.PushSubscription](alice.Post("/api/v1/push/subscriptions", dto.PushSubscriptionRequest{
		Endpoint: endpoint, P256dh: nextP256dh, Auth: nextAuth,
	}).RequireStatus(http.StatusOK))
	require.Equal(t, first.ID, second.ID, "the endpoint keeps its row")
	require.Equal(t, first.CreatedAt, second.CreatedAt)

	rows := pushSubscriptions(t, env, alice.User.ID)
	require.Len(t, rows, 1)
	require.Equal(t, nextP256dh, rows[0].P256dh, "the keys are the latest ones")
	require.Equal(t, nextAuth, rows[0].Auth)
}

// TestSubscribeValidation refuses a non-https endpoint, a malformed or off-curve point and a
// wrong-length secret with 422 on the field, never echoing the value (PUSH-04).
func TestSubscribeValidation(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	alice := env.NewUser("alice")
	const endpoint = "https://push.example.test/e2e"
	validP256dh, validAuth := pushKeyPair(t)
	zeroPoint := base64.RawURLEncoding.EncodeToString(make([]byte, 65))
	shortPoint := base64.RawURLEncoding.EncodeToString(make([]byte, 64))
	shortAuth := base64.RawURLEncoding.EncodeToString(make([]byte, 15))

	cases := []struct {
		name     string
		endpoint string
		p256dh   string
		auth     string
		field    string
		value    string
	}{
		{"not https", "http://push.example.test/e2e", validP256dh, validAuth, "endpoint", "http://push.example.test/e2e"},
		{"malformed point", endpoint, "not base64url!", validAuth, "p256dh", "not base64url!"},
		{"short point", endpoint, shortPoint, validAuth, "p256dh", shortPoint},
		{"off curve point", endpoint, zeroPoint, validAuth, "p256dh", zeroPoint},
		{"short auth", endpoint, validP256dh, shortAuth, "auth", shortAuth},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := alice.Post("/api/v1/push/subscriptions", dto.PushSubscriptionRequest{
				Endpoint: tc.endpoint, P256dh: tc.p256dh, Auth: tc.auth,
			}).RequireStatus(http.StatusUnprocessableEntity)
			body := testkit.Decode[dto.ErrorBody](resp)
			require.Equal(t, dto.ErrorCodeInvalid, body.Error.Code)
			require.Contains(t, body.Error.Fields, tc.field)
			require.NotContains(t, resp.String(), tc.value, "no value is echoed back")
			require.Empty(t, pushSubscriptions(t, env, alice.User.ID), "nothing is stored")
		})
	}

	// Padded base64url is accepted; the stored value is the canonical unpadded form.
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	secret := make([]byte, 16)
	_, err = rand.Read(secret)
	require.NoError(t, err)
	rawPoint := key.PublicKey().Bytes()
	paddedP256dh := base64.URLEncoding.EncodeToString(rawPoint)
	paddedAuth := base64.URLEncoding.EncodeToString(secret)
	require.Contains(t, paddedP256dh, "=")
	subscribe := alice.Post("/api/v1/push/subscriptions", dto.PushSubscriptionRequest{
		Endpoint: endpoint, P256dh: paddedP256dh, Auth: paddedAuth,
	}).RequireStatus(http.StatusOK)
	require.NotEmpty(t, testkit.Decode[dto.PushSubscription](subscribe).ID)

	rows := pushSubscriptions(t, env, alice.User.ID)
	require.Len(t, rows, 1)
	require.Equal(t, base64.RawURLEncoding.EncodeToString(rawPoint), rows[0].P256dh)
	require.NotContains(t, rows[0].P256dh, "=")
	require.NotContains(t, rows[0].Auth, "=")
}

// TestUnsubscribeOnlyTheOwners: the DELETE body names the endpoint, another account's endpoint
// is 404 and untouched, the owner removes it and a second DELETE is 404 (PUSH-05).
func TestUnsubscribeOnlyTheOwners(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	alice := env.NewUser("alice")
	bob := env.NewUser("bob")
	const endpoint = "https://push.example.test/e2e"
	subscribePush(t, alice, endpoint).RequireStatus(http.StatusOK)

	body := dto.PushUnsubscribeRequest{Endpoint: endpoint}
	bob.Do(http.MethodDelete, "/api/v1/push/subscriptions", body).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Len(t, pushSubscriptions(t, env, alice.User.ID), 1, "another account's DELETE removes nothing")

	alice.Do(http.MethodDelete, "/api/v1/push/subscriptions", body).RequireStatus(http.StatusNoContent)
	require.Empty(t, pushSubscriptions(t, env, alice.User.ID))

	alice.Do(http.MethodDelete, "/api/v1/push/subscriptions", body).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// TestPushRoutesNeedAUser: every push route is 401 anonymous and 403 with an agent token
// (PUSH-02, PUSH-04, PUSH-05, PUSH-15).
func TestPushRoutesNeedAUser(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	bob := env.NewUser("bob")
	agent := bob.Token(dto.TokenKindAgent, "agent")
	p256dh, auth := pushKeyPair(t)
	routes := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"key", http.MethodGet, "/api/v1/push/key", nil},
		{"subscribe", http.MethodPost, "/api/v1/push/subscriptions", dto.PushSubscriptionRequest{
			Endpoint: "https://push.example.test/e2e", P256dh: p256dh, Auth: auth,
		}},
		{"unsubscribe", http.MethodDelete, "/api/v1/push/subscriptions", dto.PushUnsubscribeRequest{
			Endpoint: "https://push.example.test/e2e",
		}},
		{"test", http.MethodPost, "/api/v1/push/test", nil},
	}
	for _, rt := range routes {
		t.Run(rt.name, func(*testing.T) {
			env.Anon().Do(rt.method, rt.path, rt.body).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
			agent.Do(rt.method, rt.path, rt.body).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		})
	}
}

// TestTestPush: one queued test notification per subscription, 409 without any, 429 on the
// fourth ask in a minute (PUSH-15).
func TestTestPush(t *testing.T) {
	t.Run("two subscriptions", func(t *testing.T) {
		env := testkit.New(t, testkit.WithoutPush())
		alice := env.NewUser("alice")
		subscribePush(t, alice, "https://push.example.test/one").RequireStatus(http.StatusOK)
		subscribePush(t, alice, "https://push.example.test/two").RequireStatus(http.StatusOK)

		got := testkit.Decode[dto.PushTest](alice.Post("/api/v1/push/test", nil).RequireStatus(http.StatusAccepted))
		require.Equal(t, 2, got.Queued)
	})

	t.Run("no subscription", func(t *testing.T) {
		env := testkit.New(t, testkit.WithoutPush())
		alice := env.NewUser("alice")
		alice.Post("/api/v1/push/test", nil).RequireError(http.StatusConflict, dto.ErrorCodeConflict)
	})

	t.Run("fourth ask in a minute", func(t *testing.T) {
		env := testkit.New(t, testkit.WithoutPush())
		alice := env.NewUser("alice")
		subscribePush(t, alice, "https://push.example.test/one").RequireStatus(http.StatusOK)
		for range 3 {
			alice.Post("/api/v1/push/test", nil).RequireStatus(http.StatusAccepted)
		}
		alice.Post("/api/v1/push/test", nil).RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	})
}

// TestTestPushQueueFull is the HTTP-level test of the queue-full sentinel: with a one-slot
// queue and no worker the first test job stays queued and the second is 503 (PUSH-27, L-017).
func TestTestPushQueueFull(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush(), testkit.WithPushQueueSize(1))
	alice := env.NewUser("alice")
	subscribePush(t, alice, "https://push.example.test/one").RequireStatus(http.StatusOK)

	got := testkit.Decode[dto.PushTest](alice.Post("/api/v1/push/test", nil).RequireStatus(http.StatusAccepted))
	require.Equal(t, 1, got.Queued)
	alice.Post("/api/v1/push/test", nil).RequireError(http.StatusServiceUnavailable, dto.ErrorCodeUnavailable)
}

// TestGroupPushSetting: the account starts on mentions, PATCH /me stores the choice, an unknown
// value is 422 on push_groups and an agent token is 403 (PUSH-06).
func TestGroupPushSetting(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	alice := env.NewUser("alice")

	me := testkit.Decode[dto.Me](alice.Get("/api/v1/me").RequireStatus(http.StatusOK))
	require.Equal(t, "mentions", me.PushGroups)

	off := "off"
	me = testkit.Decode[dto.Me](alice.Patch("/api/v1/me", dto.UpdateMeRequest{PushGroups: &off}).RequireStatus(http.StatusOK))
	require.Equal(t, "off", me.PushGroups)
	me = testkit.Decode[dto.Me](alice.Get("/api/v1/me").RequireStatus(http.StatusOK))
	require.Equal(t, "off", me.PushGroups, "the setting persists")

	none := "none"
	resp := alice.Patch("/api/v1/me", dto.UpdateMeRequest{PushGroups: &none}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, resp.Fields, "push_groups")
	me = testkit.Decode[dto.Me](alice.Get("/api/v1/me").RequireStatus(http.StatusOK))
	require.Equal(t, "off", me.PushGroups, "a refused value changes nothing")

	alice.Token(dto.TokenKindAgent, "agent").Patch("/api/v1/me", dto.UpdateMeRequest{PushGroups: &off}).
		RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
}
