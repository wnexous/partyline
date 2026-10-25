package webpush_test

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/secrets"
	"github.com/wnexous/partyline/internal/testkit"
	"github.com/wnexous/partyline/internal/webpush"
)

func testBox(t *testing.T) *secrets.Box {
	t.Helper()
	key := make([]byte, secrets.KeySize)
	for i := range key {
		key[i] = byte(i + 1)
	}
	box, err := secrets.NewBox(key)
	require.NoError(t, err)
	return box
}

// openSealedVAPID reads the sealed key file and returns the private key it holds.
func openSealedVAPID(t *testing.T, path string, box *secrets.Box) *ecdsa.PrivateKey {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- the test's own temp dir
	require.NoError(t, err)
	sealed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	require.NoError(t, err)
	der, err := box.Open(sealed)
	require.NoError(t, err)
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	require.NoError(t, err)
	priv, ok := parsed.(*ecdsa.PrivateKey)
	require.True(t, ok)
	return priv
}

func TestVAPIDKeyIsGeneratedOnceAndSealed(t *testing.T) {
	dir := t.TempDir()
	box := testBox(t)

	first, err := webpush.LoadOrCreateVAPID(dir, box)
	require.NoError(t, err)
	require.True(t, first.New, "the first call generated the pair")
	second, err := webpush.LoadOrCreateVAPID(dir, box)
	require.NoError(t, err)
	require.False(t, second.New, "the second call reuses the generated key")
	require.Equal(t, first.Public, second.Public, "the second call reuses the generated key")

	path := filepath.Join(dir, webpush.VAPIDKeyFile)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	priv := openSealedVAPID(t, path, box)
	require.Equal(t, elliptic.P256(), priv.Curve)
	pub, err := priv.PublicKey.ECDH()
	require.NoError(t, err)
	require.Equal(t, first.Public, base64.RawURLEncoding.EncodeToString(pub.Bytes()))

	raw, err := os.ReadFile(path) // #nosec G304 -- the test's own temp dir
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)
	require.NotContains(t, string(raw), string(der), "the private key is sealed, never written as DER")
	scalar, err := priv.Bytes()
	require.NoError(t, err)
	require.NotContains(t, string(raw), base64.StdEncoding.EncodeToString(scalar), "the private scalar is not written in the clear")
	require.NotContains(t, string(raw), base64.RawURLEncoding.EncodeToString(scalar), "the private scalar is not written in the clear")
}

func TestVAPIDPublicKeyIsAnUncompressedPoint(t *testing.T) {
	v, err := webpush.LoadOrCreateVAPID(t.TempDir(), testBox(t))
	require.NoError(t, err)
	require.NotContains(t, v.Public, "=", "the public key is base64url without padding")

	point, err := base64.RawURLEncoding.DecodeString(v.Public)
	require.NoError(t, err)
	require.Len(t, point, 65)
	require.Equal(t, byte(0x04), point[0])
	_, err = ecdh.P256().NewPublicKey(point)
	require.NoError(t, err)
}

func TestVAPIDReplacesACorruptFile(t *testing.T) {
	corrupt := map[string]func(t *testing.T, box *secrets.Box) []byte{
		"not base64": func(*testing.T, *secrets.Box) []byte {
			return []byte("not-base64\n")
		},
		"wrong curve": func(t *testing.T, box *secrets.Box) []byte {
			t.Helper()
			key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
			require.NoError(t, err)
			der, err := x509.MarshalPKCS8PrivateKey(key)
			require.NoError(t, err)
			sealed, err := box.Seal(der)
			require.NoError(t, err)
			return []byte(base64.StdEncoding.EncodeToString(sealed) + "\n")
		},
	}
	for name, content := range corrupt {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			box := testBox(t)
			path := filepath.Join(dir, webpush.VAPIDKeyFile)
			before := content(t, box)
			require.NoError(t, os.WriteFile(path, before, 0o600))

			v, err := webpush.LoadOrCreateVAPID(dir, box)
			require.NoError(t, err, "a bad key file is replaced")
			require.True(t, v.New)
			point, err := base64.RawURLEncoding.DecodeString(v.Public)
			require.NoError(t, err)
			require.Len(t, point, 65)

			after, err := os.ReadFile(path) // #nosec G304 -- the test's own temp dir
			require.NoError(t, err)
			require.NotEqual(t, before, after, "the bad file is replaced")
			priv := openSealedVAPID(t, path, box)
			require.Equal(t, elliptic.P256(), priv.Curve)
			pub, err := priv.PublicKey.ECDH()
			require.NoError(t, err)
			require.Equal(t, v.Public, base64.RawURLEncoding.EncodeToString(pub.Bytes()))
		})
	}
}

func TestVAPIDRefusesADirectoryAsTheKeyFile(t *testing.T) {
	dir := t.TempDir()
	box := testBox(t)
	path := filepath.Join(dir, webpush.VAPIDKeyFile)
	require.NoError(t, os.Mkdir(path, 0o700), "a directory where the key file should be cannot be a key")

	v, err := webpush.LoadOrCreateVAPID(dir, box)
	require.Error(t, err)
	require.Nil(t, v)
	require.NotContains(t, err.Error(), dir, "the error names the file, not the full path")
	info, statErr := os.Stat(path)
	require.NoError(t, statErr)
	require.True(t, info.IsDir(), "the directory is not deleted")
}

func TestTheAppLoadsTheVAPIDKey(t *testing.T) {
	env := testkit.New(t)
	require.NotNil(t, env.App.VAPID)
	point, err := base64.RawURLEncoding.DecodeString(env.App.VAPID.Public)
	require.NoError(t, err)
	require.Len(t, point, 65)
	require.Equal(t, byte(0x04), point[0])
	_, err = os.Stat(filepath.Join(env.App.Config.DataDir, webpush.VAPIDKeyFile))
	require.NoError(t, err)

	public := env.App.VAPID.Public
	env.Restart()
	require.Equal(t, public, env.App.VAPID.Public, "a restart reuses the sealed key")
}

func TestTheAppReplacesACorruptVAPIDKey(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	alice := env.NewUser("alice")
	subscribe(t, env, alice.User.ID, "https://push.example.test/one")
	before := env.App.VAPID.Public
	path := filepath.Join(env.App.Config.DataDir, webpush.VAPIDKeyFile)
	require.NoError(t, os.WriteFile(path, []byte("not-base64\n"), 0o600))

	env.Restart()
	require.NotEqual(t, before, env.App.VAPID.Public)
	require.True(t, env.App.VAPID.New)
	point, err := base64.RawURLEncoding.DecodeString(env.App.VAPID.Public)
	require.NoError(t, err)
	require.Len(t, point, 65)
	rows, err := env.App.Store.R().ListPushSubscriptionsByUser(context.Background(), alice.User.ID)
	require.NoError(t, err)
	require.Empty(t, rows, "subscriptions bound to the old key are dropped")
}
