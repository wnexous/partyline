package secrets

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/config"
)

func randomKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, KeySize)
	_, err := rand.Read(k)
	require.NoError(t, err)
	return k
}

func newBox(t *testing.T, key []byte) *Box {
	t.Helper()
	b, err := NewBox(key)
	require.NoError(t, err)
	return b
}

func TestSealOpenRoundTrip(t *testing.T) {
	b := newBox(t, randomKey(t))
	plain := []byte(`{"bot_token":"123456:synthetic-token"}`)

	sealed, err := b.Seal(plain)
	require.NoError(t, err)
	require.False(t, bytes.Contains(sealed, plain), "ciphertext must not contain the plaintext")

	got, err := b.Open(sealed)
	require.NoError(t, err)
	require.Equal(t, plain, got)
}

func TestSealUsesRandomNonce(t *testing.T) {
	b := newBox(t, randomKey(t))
	a, err := b.Seal([]byte("same"))
	require.NoError(t, err)
	c, err := b.Seal([]byte("same"))
	require.NoError(t, err)
	require.NotEqual(t, a, c)
}

func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	b := newBox(t, randomKey(t))
	sealed, err := b.Seal([]byte("secret"))
	require.NoError(t, err)

	sealed[len(sealed)-1] ^= 0x01
	_, err = b.Open(sealed)
	require.Error(t, err)

	_, err = b.Open(sealed[:4])
	require.Error(t, err, "truncated input must fail")
}

func TestOpenRejectsWrongKey(t *testing.T) {
	sealed, err := newBox(t, randomKey(t)).Seal([]byte("secret"))
	require.NoError(t, err)
	_, err = newBox(t, randomKey(t)).Open(sealed)
	require.Error(t, err)
}

func TestNewBoxRejectsShortKey(t *testing.T) {
	_, err := NewBox(make([]byte, 16))
	require.Error(t, err)
}

func TestLoadOrCreateKeyUsesEnvKey(t *testing.T) {
	key := randomKey(t)
	cfg := &config.Config{DataDir: t.TempDir(), SecretKey: base64.StdEncoding.EncodeToString(key)}

	got, err := LoadOrCreateKey(cfg)
	require.NoError(t, err)
	require.Equal(t, key, got)
	require.NoFileExists(t, filepath.Join(cfg.DataDir, KeyFile))
}

func TestLoadOrCreateKeyRejectsInvalidEnvKey(t *testing.T) {
	for _, v := range []string{"not base64!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		_, err := LoadOrCreateKey(&config.Config{DataDir: t.TempDir(), SecretKey: v})
		require.Error(t, err)
		require.Contains(t, err.Error(), "PARTYLINE_SECRET_KEY")
	}
}

func TestLoadOrCreateKeyPersistsGeneratedKey(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir()}

	first, err := LoadOrCreateKey(cfg)
	require.NoError(t, err)
	require.Len(t, first, KeySize)

	info, err := os.Stat(filepath.Join(cfg.DataDir, KeyFile))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	second, err := LoadOrCreateKey(cfg)
	require.NoError(t, err)
	require.Equal(t, first, second, "the persisted key is reused")
}

func TestLoadOrCreateKeyRejectsCorruptKeyFile(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir()}
	require.NoError(t, os.WriteFile(filepath.Join(cfg.DataDir, KeyFile), []byte("garbage"), 0o600))
	_, err := LoadOrCreateKey(cfg)
	require.Error(t, err)
}
