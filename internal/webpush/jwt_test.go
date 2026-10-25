package webpush_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/webpush"
)

const (
	testAudience = "https://push.example.net"
	testSubject  = "https://partyline.example.com"
)

func testVAPID(t *testing.T) *webpush.VAPID {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	point, err := private.PublicKey.ECDH()
	require.NoError(t, err)
	return &webpush.VAPID{
		Private: private,
		Public:  base64.RawURLEncoding.EncodeToString(point.Bytes()),
	}
}

func TestVAPIDJWTCarriesTheHeaderClaimsAndSignature(t *testing.T) {
	vapid := testVAPID(t)
	now := time.Unix(1_700_000_000, 0)

	token, err := vapid.Token(testAudience, testSubject, now)
	require.NoError(t, err)

	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)

	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	require.NoError(t, err)
	require.Equal(t, `{"typ":"JWT","alg":"ES256"}`, string(header))

	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var claims struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}
	require.NoError(t, json.Unmarshal(claimsJSON, &claims))
	require.Equal(t, testAudience, claims.Aud)
	require.Equal(t, testSubject, claims.Sub)
	require.Equal(t, int64(12*time.Hour/time.Second), claims.Exp-now.Unix())

	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	require.NoError(t, err)
	require.Len(t, signature, 64)
	r := new(big.Int).SetBytes(signature[:32])
	s := new(big.Int).SetBytes(signature[32:])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	require.True(t, ecdsa.Verify(&vapid.Private.PublicKey, digest[:], r, s))
}

func TestVAPIDAuthorizationHeader(t *testing.T) {
	vapid := testVAPID(t)

	header, err := vapid.Authorization(testAudience, testSubject, time.Now())
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(header, "vapid t="))

	jwtPart, public, found := strings.Cut(strings.TrimPrefix(header, "vapid t="), ",k=")
	require.True(t, found)
	require.NotEmpty(t, jwtPart)
	require.Equal(t, vapid.Public, public)
	point, err := base64.RawURLEncoding.DecodeString(public)
	require.NoError(t, err)
	require.Len(t, point, 65)
	require.Equal(t, byte(0x04), point[0])
}
