package webpush_test

import (
	"crypto/ecdh"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/webpush"
)

// The RFC 8291 section 5 test vector inputs and its expected body, as the design pins them.
const (
	rfcUAKey     = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	rfcAuth      = "BTBZMqHH6r4Tts7J_aSIgg"
	rfcASPrivate = "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"
	rfcSalt      = "DGv6ra1nlYgDCS1FRnbzlw"
	rfcPlaintext = "When I grow up, I want to be a watermelon"
	rfcBody      = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
)

func decodeRawURL(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	require.NoError(t, err)
	return b
}

func rfcASPrivateKey(t *testing.T) *ecdh.PrivateKey {
	t.Helper()
	key, err := ecdh.P256().NewPrivateKey(decodeRawURL(t, rfcASPrivate))
	require.NoError(t, err)
	return key
}

func TestEncryptMatchesRFC8291Section5(t *testing.T) {
	body, err := webpush.Encrypt(
		[]byte(rfcPlaintext),
		decodeRawURL(t, rfcUAKey),
		decodeRawURL(t, rfcAuth),
		rfcASPrivateKey(t),
		decodeRawURL(t, rfcSalt),
	)
	require.NoError(t, err)
	require.Equal(t, rfcBody, base64.RawURLEncoding.EncodeToString(body))
}

func TestEncryptRejectsAnOffCurveOrWrongLengthKey(t *testing.T) {
	auth := decodeRawURL(t, rfcAuth)
	private := rfcASPrivateKey(t)
	salt := decodeRawURL(t, rfcSalt)

	for _, key := range [][]byte{make([]byte, 65), make([]byte, 64)} {
		_, err := webpush.Encrypt([]byte("hello"), key, auth, private, salt)
		require.Error(t, err)
		require.NotContains(t, err.Error(), base64.RawURLEncoding.EncodeToString(key))
		require.NotContains(t, err.Error(), string(key))
	}
}
