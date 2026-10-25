package webpush

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/wnexous/partyline/internal/connectors/safehttp"
)

const (
	// pushTimeout bounds one push request, body included.
	pushTimeout = 10 * time.Second
	// maxResponse caps what the client reads from a push service.
	maxResponse = 4096
	// pushTTL is how long the push service keeps an undelivered message (one hour).
	pushTTL = "3600"
)

// Subscription is one browser push subscription. ID is the stored row (the client ignores it),
// Endpoint the browser's capability URL, and P256dh/Auth its base64url key material.
type Subscription struct {
	ID       string
	Endpoint string
	P256dh   string
	Auth     string
}

// Sender sends an encrypted payload to one subscription and returns the push service's status.
type Sender interface {
	Send(ctx context.Context, sub Subscription, payload []byte) (int, error)
}

// Doer performs one HTTP request; *http.Client implements it. It is injectable so tests can use
// an httptest server without the private-network exception.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client is the HTTP push sender (RFC 8291 payloads, RFC 8292 authentication).
type Client struct {
	// VAPID signs every request; it is required.
	VAPID *VAPID
	// Subject is the application server's public URL, the JWT sub claim.
	Subject string
	// Doer performs the requests; nil uses the safe client.
	Doer Doer
}

// NewClient returns a push client. Requests go through safehttp: private, loopback and
// link-local destinations are refused at dial time, redirects are not followed, the response is
// capped at 4 KiB and the request times out after 10 seconds.
func NewClient(vapid *VAPID, subject string) *Client {
	return &Client{
		VAPID:   vapid,
		Subject: subject,
		Doer: safehttp.NewClient(safehttp.Options{
			Timeout:          pushTimeout,
			MaxResponseBytes: maxResponse,
		}),
	}
}

// Send encrypts payload for the subscription and posts it to its push service. It returns the
// HTTP status; a transport failure is returned as an error (the caller never logs it raw).
func (c *Client) Send(ctx context.Context, sub Subscription, payload []byte) (int, error) {
	if c == nil || c.VAPID == nil {
		return 0, errNoVAPIDKey
	}
	if len(payload) > maxPayload {
		return 0, errors.New("webpush: payload too large")
	}
	audience, err := endpointOrigin(sub.Endpoint)
	if err != nil {
		return 0, err
	}
	uaPublic, err := base64.RawURLEncoding.DecodeString(sub.P256dh)
	if err != nil {
		return 0, errInvalidSubscriptionKey
	}
	authSecret, err := base64.RawURLEncoding.DecodeString(sub.Auth)
	if err != nil {
		return 0, errInvalidSubscriptionKey
	}
	body, err := EncryptRandom(payload, uaPublic, authSecret)
	if err != nil {
		return 0, err
	}
	authorization, err := c.VAPID.Authorization(audience, c.Subject, time.Now())
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, errors.New("webpush: cannot build the push request")
	}
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", pushTTL)
	req.Header.Set("Urgency", "normal")
	req.Header.Set("Authorization", authorization)

	doer := c.Doer
	if doer == nil {
		doer = safehttp.NewClient(safehttp.Options{
			Timeout:          pushTimeout,
			MaxResponseBytes: maxResponse,
		})
	}
	resp, err := doer.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	// The body carries no information we use; drain it (bounded) so the connection can be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponse))
	return resp.StatusCode, nil
}

// endpointOrigin returns scheme://host of a push endpoint, the JWT audience.
func endpointOrigin(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("webpush: invalid subscription endpoint")
	}
	return u.Scheme + "://" + u.Host, nil
}
