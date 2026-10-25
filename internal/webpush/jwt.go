package webpush

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// jwtLifetime is the VAPID claim lifetime; RFC 8292 caps it at 24 hours.
const jwtLifetime = 12 * time.Hour

var errNoVAPIDKey = errors.New("webpush: no VAPID key")

type jwtHeader struct {
	Typ string `json:"typ"`
	Alg string `json:"alg"`
}

type jwtClaims struct {
	Aud string `json:"aud"`
	Exp int64  `json:"exp"`
	Sub string `json:"sub"`
}

// Token signs a VAPID JWT (RFC 8292) for a push service: audience is the endpoint's origin
// (scheme://host), subject the application server's public URL and now the signing time.
func (v *VAPID) Token(audience, subject string, now time.Time) (string, error) {
	if v == nil || v.Private == nil {
		return "", errNoVAPIDKey
	}
	header, err := json.Marshal(jwtHeader{Typ: "JWT", Alg: "ES256"})
	if err != nil {
		return "", fmt.Errorf("webpush: encode VAPID header: %w", err)
	}
	claims, err := json.Marshal(jwtClaims{
		Aud: audience,
		Exp: now.Add(jwtLifetime).Unix(),
		Sub: subject,
	})
	if err != nil {
		return "", fmt.Errorf("webpush: encode VAPID claims: %w", err)
	}

	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, v.Private, digest[:])
	if err != nil {
		return "", fmt.Errorf("webpush: sign VAPID token: %w", err)
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// Authorization returns the VAPID Authorization header value for a push service:
// "vapid t=<jwt>,k=<public key>".
func (v *VAPID) Authorization(audience, subject string, now time.Time) (string, error) {
	token, err := v.Token(audience, subject, now)
	if err != nil {
		return "", err
	}
	return "vapid t=" + token + ",k=" + v.Public, nil
}
