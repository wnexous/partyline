package webpush

import (
	"context"
	"crypto/ecdh"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// The push routes' bounds and their test-notification rate (PUSH-04, PUSH-15).
const (
	// maxEndpointLength bounds a stored push endpoint.
	maxEndpointLength = 2048
	// p256dhLength is the length of an uncompressed P-256 point (RFC 8291).
	p256dhLength = 65
	// uncompressedPoint is the first octet of an uncompressed P-256 point.
	uncompressedPoint = 0x04
	// authSecretLength is the length of a Web Push auth secret (RFC 8291).
	authSecretLength = 16
	// testLimit and testWindow are how many test notifications one account may ask for in a
	// rolling minute (PUSH-15).
	testLimit  = 3
	testWindow = time.Minute
)

// The refusals of the push routes that carry a status of their own.
var (
	// ErrNoSubscriptions is the test route of an account without a subscription: 409.
	ErrNoSubscriptions = errors.New("webpush: the account has no push subscription")
	// ErrSubscriptionNotFound is an endpoint that is unknown or belongs to another account: 404.
	ErrSubscriptionNotFound = errors.New("webpush: push subscription not found")
	// ErrRateLimited is more test notifications than testLimit a minute: 429.
	ErrRateLimited = errors.New("webpush: too many test notifications")
	// ErrBusy is a full queue that took none of the test jobs: 503.
	ErrBusy = errors.New("webpush: the push queue is full")
)

// SubscribeInput is one registration of a browser subscription (PUSH-03). SessionID is empty
// for a user API token, which has no session to revoke.
type SubscribeInput struct {
	UserID    string
	SessionID string
	Endpoint  string
	P256dh    string
	Auth      string
}

// TestQueue queues one test notification per subscription and returns how many jobs it queued;
// Notifier implements it (PUSH-15).
type TestQueue interface {
	EnqueueTest(ctx context.Context, subs []db.PushSubscription) int
}

// ServiceDeps configure a Service; Store, VAPID and Queue are required.
type ServiceDeps struct {
	Store *store.Store
	VAPID *VAPID
	Queue TestQueue
	// Now returns the current time; defaults to time.Now.
	Now func() time.Time
}

// Service is the use cases of the push routes: the instance key, subscribe, unsubscribe and the
// test notification. It validates each body itself, so a handler only decodes and answers
// (PUSH-02, PUSH-03, PUSH-04, PUSH-05, PUSH-15).
type Service struct {
	store *store.Store
	vapid *VAPID
	queue TestQueue
	now   func() time.Time
	// testLimiter counts the test asks of each account in a rolling minute (PUSH-15).
	testLimiter *limiter
}

// NewService returns the push routes' service with an empty test-notification limiter.
func NewService(deps ServiceDeps) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Service{
		store:       deps.Store,
		vapid:       deps.VAPID,
		queue:       deps.Queue,
		now:         deps.Now,
		testLimiter: newLimiter(testLimit, testWindow, deps.Now),
	}
}

// Key returns the instance's VAPID public key, base64url without padding (PUSH-02).
func (s *Service) Key() string { return s.vapid.Public }

// Subscribe validates the body and stores one row per endpoint: a second registration of the
// same endpoint updates it and moves it to the caller and the caller's session (PUSH-03).
func (s *Service) Subscribe(ctx context.Context, in SubscribeInput) (db.PushSubscription, error) {
	fields := map[string]string{}
	if reason := endpointProblem(in.Endpoint); reason != "" {
		fields["endpoint"] = reason
	}
	p256dh, keyProblem := decodeP256dh(in.P256dh)
	if keyProblem != "" {
		fields["p256dh"] = keyProblem
	}
	auth, authProblem := decodeAuthSecret(in.Auth)
	if authProblem != "" {
		fields["auth"] = authProblem
	}
	if len(fields) > 0 {
		return db.PushSubscription{}, &validation.Error{Fields: fields}
	}
	return s.store.R().UpsertPushSubscription(ctx, db.UpsertPushSubscriptionParams{
		ID:        store.NewID(),
		UserID:    in.UserID,
		SessionID: sql.NullString{String: in.SessionID, Valid: in.SessionID != ""},
		Endpoint:  in.Endpoint,
		// The stored form is the canonical unpadded base64url the client decodes (PUSH-04).
		P256dh:    base64.RawURLEncoding.EncodeToString(p256dh),
		Auth:      base64.RawURLEncoding.EncodeToString(auth),
		CreatedAt: s.now().UnixMilli(),
	})
}

// Unsubscribe removes the caller's row for endpoint; an endpoint that is unknown or belongs to
// another account is ErrSubscriptionNotFound and nothing is deleted (PUSH-05).
func (s *Service) Unsubscribe(ctx context.Context, userID, endpoint string) error {
	deleted, err := s.store.R().DeletePushSubscription(ctx, db.DeletePushSubscriptionParams{
		Endpoint: endpoint, UserID: userID,
	})
	if err != nil {
		return err
	}
	if deleted == 0 {
		return ErrSubscriptionNotFound
	}
	return nil
}

// Test queues one test notification for every subscription of the account and returns how many
// jobs it queued (PUSH-15). The queue is checked after the subscription list and the limiter, so
// an account without a subscription is 409 and a rate-limited one is 429 whatever the queue
// holds (PUSH-27).
func (s *Service) Test(ctx context.Context, userID string) (int, error) {
	subs, err := s.store.R().ListPushSubscriptionsByUser(ctx, userID)
	if err != nil {
		return 0, err
	}
	if len(subs) == 0 {
		return 0, ErrNoSubscriptions
	}
	if !s.testLimiter.allow(userID) {
		return 0, ErrRateLimited
	}
	queued := s.queue.EnqueueTest(ctx, subs)
	if queued == 0 {
		return 0, ErrBusy
	}
	return queued, nil
}

// endpointProblem returns why endpoint cannot be a push subscription, or "" when it can: an
// absolute https URL with a host and at most maxEndpointLength characters (PUSH-04).
func endpointProblem(endpoint string) string {
	if endpoint == "" {
		return "is required"
	}
	if len(endpoint) > maxEndpointLength {
		return "must have at most 2048 characters"
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "must be an absolute https URL"
	}
	return ""
}

// decodeP256dh decodes a padded or unpadded base64url uncompressed P-256 point; a bad length,
// a wrong first octet or a point off the curve is refused (PUSH-04).
func decodeP256dh(value string) ([]byte, string) {
	key, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(value, "="))
	if err != nil || len(key) != p256dhLength || key[0] != uncompressedPoint {
		return nil, "must be a 65-byte uncompressed P-256 public key"
	}
	if _, err := ecdh.P256().NewPublicKey(key); err != nil {
		return nil, "must be a point on P-256"
	}
	return key, ""
}

// decodeAuthSecret decodes a padded or unpadded base64url 16-byte auth secret (PUSH-04).
func decodeAuthSecret(value string) ([]byte, string) {
	secret, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(value, "="))
	if err != nil || len(secret) != authSecretLength {
		return nil, "must be a base64url 16-byte secret"
	}
	return secret, ""
}
