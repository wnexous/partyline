package dto

import "time"

// PushKey is the answer of GET /push/key: the instance's VAPID public key, base64url without
// padding (PUSH-02).
type PushKey struct {
	PublicKey string `json:"public_key"`
}

// PushSubscriptionRequest is the body of POST /push/subscriptions: the endpoint and the two
// keys a browser's PushSubscription carries (PUSH-03, PUSH-04).
type PushSubscriptionRequest struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
}

// PushSubscription is one stored subscription of the caller. It never carries the auth secret
// back (PUSH-03, PUSH-22).
type PushSubscription struct {
	ID       string `json:"id"`
	Endpoint string `json:"endpoint"`
	// CreatedAt is when the endpoint first registered; a later POST keeps it.
	CreatedAt time.Time `json:"created_at"`
	// LastOkAt is the last 2xx of the push service; absent until one (PUSH-12).
	LastOkAt *time.Time `json:"last_ok_at,omitempty"`
}

// PushUnsubscribeRequest is the body of DELETE /push/subscriptions: the endpoint travels in the
// body because it is a URL with slashes and a long capability token (PUSH-05).
type PushUnsubscribeRequest struct {
	Endpoint string `json:"endpoint"`
}

// PushTest is the answer of POST /push/test: how many test notifications the queue took
// (PUSH-15).
type PushTest struct {
	Queued int `json:"queued"`
}
