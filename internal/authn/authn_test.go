package authn

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHashPasswordUsesArgon2idPHC(t *testing.T) {
	const pw = "correct horse battery"
	hash, err := HashPassword(pw)
	require.NoError(t, err)
	require.NotContains(t, hash, pw)
	require.True(t, strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$"), hash)
	require.Len(t, strings.Split(hash, "$"), 6)

	other, err := HashPassword(pw)
	require.NoError(t, err)
	require.NotEqual(t, hash, other, "each hash uses a fresh salt")
}

func TestVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery")
	require.NoError(t, err)

	ok, err := VerifyPassword(hash, "correct horse battery")
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = VerifyPassword(hash, "wrong horse battery")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	for _, h := range []string{"", "plain", "$argon2i$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA", "$argon2id$v=19$m=x,t=2,p=1$c2FsdA$aGFzaA"} {
		ok, err := VerifyPassword(h, "pw")
		require.Error(t, err, h)
		require.False(t, ok)
	}
}

func TestNewTokenFormat(t *testing.T) {
	for kind, re := range map[TokenKind]string{
		TokenAgent: `^pl_agent_[0-9A-Za-z]{43}$`,
		TokenUser:  `^pl_user_[0-9A-Za-z]{43}$`,
	} {
		secret, err := NewToken(kind)
		require.NoError(t, err)
		require.Regexp(t, regexp.MustCompile(re), secret)

		prefix := TokenPrefix(secret)
		require.Len(t, prefix, 12)
		require.Equal(t, secret[:12], prefix)

		got, ok := ParseTokenKind(secret)
		require.True(t, ok)
		require.Equal(t, kind, got)
	}

	a, err := NewToken(TokenUser)
	require.NoError(t, err)
	b, err := NewToken(TokenUser)
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}

func TestParseTokenKindRejectsForeignStrings(t *testing.T) {
	for _, s := range []string{"", "pl_admin_abc", "Bearer pl_user_x", "pl_user_", "pl_user_not-base62!"} {
		_, ok := ParseTokenKind(s)
		require.False(t, ok, s)
	}
}

func TestHashTokenIsHexSHA256(t *testing.T) {
	secret, err := NewToken(TokenAgent)
	require.NoError(t, err)
	sum := sha256.Sum256([]byte(secret))

	h := HashToken(secret)
	require.Equal(t, hex.EncodeToString(sum[:]), h)
	require.Regexp(t, `^[0-9a-f]{64}$`, h)
	require.NotContains(t, h, secret)
}

func TestNewSessionToken(t *testing.T) {
	a, err := NewSessionToken()
	require.NoError(t, err)
	require.Regexp(t, `^[0-9A-Za-z]{43}$`, a)
	b, err := NewSessionToken()
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}
