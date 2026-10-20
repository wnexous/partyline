package config

import (
	"log/slog"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/connectors/safehttp"
)

func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(nil))
	require.NoError(t, err)
	require.Equal(t, ":8080", cfg.Addr)
	require.Equal(t, "/data", cfg.DataDir)
	require.Equal(t, "http://localhost:8080", cfg.PublicURL.String())
	require.Empty(t, cfg.SecretKey)
	require.Equal(t, int64(25<<20), cfg.MaxUploadBytes)
	require.Equal(t, slog.LevelInfo, cfg.LogLevel)
	require.Equal(t, "json", cfg.LogFormat)
	require.Empty(t, cfg.TrustedProxies)
	require.False(t, cfg.SecureCookies())
	require.False(t, cfg.AllowPrivateNetworks, "CON-13: connectors reach only public addresses by default")
	require.Nil(t, cfg.PrivateNetworkAllowlist, "S6: no private destination is listed by default")
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"PARTYLINE_ADDR":                      "127.0.0.1:9000",
		"PARTYLINE_DATA_DIR":                  "/var/lib/partyline",
		"PARTYLINE_PUBLIC_URL":                "https://chat.example.com",
		"PARTYLINE_SECRET_KEY":                "c2VjcmV0",
		"PARTYLINE_MAX_UPLOAD_MB":             "10",
		"PARTYLINE_LOG_LEVEL":                 "debug",
		"PARTYLINE_LOG_FORMAT":                "text",
		"PARTYLINE_TRUSTED_PROXIES":           "10.0.0.0/8, 192.168.1.1",
		"PARTYLINE_ALLOW_PRIVATE_NETWORKS":    "true",
		"PARTYLINE_PRIVATE_NETWORK_ALLOWLIST": "192.168.1.0/24, localhost:8088",
	}))
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:9000", cfg.Addr)
	require.Equal(t, "/var/lib/partyline", cfg.DataDir)
	require.Equal(t, "https://chat.example.com", cfg.PublicURL.String())
	require.Equal(t, "c2VjcmV0", cfg.SecretKey)
	require.Equal(t, int64(10<<20), cfg.MaxUploadBytes)
	require.Equal(t, slog.LevelDebug, cfg.LogLevel)
	require.Equal(t, "text", cfg.LogFormat)
	require.Equal(t, []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("192.168.1.1/32"),
	}, cfg.TrustedProxies)
	require.True(t, cfg.AllowPrivateNetworks)
	want, err := safehttp.ParseAllowlist("192.168.1.0/24,localhost:8088")
	require.NoError(t, err)
	require.Equal(t, want, cfg.PrivateNetworkAllowlist)
}

func TestLoadInvalidValuesNameTheVariable(t *testing.T) {
	cases := map[string]string{
		"PARTYLINE_PUBLIC_URL":                "not a url",
		"PARTYLINE_MAX_UPLOAD_MB":             "lots",
		"PARTYLINE_TRUSTED_PROXIES":           "10.0.0.0/99",
		"PARTYLINE_LOG_LEVEL":                 "loud",
		"PARTYLINE_LOG_FORMAT":                "xml",
		"PARTYLINE_ALLOW_PRIVATE_NETWORKS":    "maybe",
		"PARTYLINE_PRIVATE_NETWORK_ALLOWLIST": "10.0.0.0/8,not a host",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(env(map[string]string{name: value}))
			require.Error(t, err)
			require.Contains(t, err.Error(), name)
		})
	}
}

func TestLoadRejectsBadPublicURLs(t *testing.T) {
	for _, value := range []string{"ftp://example.com", "example.com", "https://"} {
		_, err := Load(env(map[string]string{"PARTYLINE_PUBLIC_URL": value}))
		require.Error(t, err, value)
		require.Contains(t, err.Error(), "PARTYLINE_PUBLIC_URL")
	}
}

func TestLoadRejectsNonPositiveUpload(t *testing.T) {
	for _, value := range []string{"0", "-5"} {
		_, err := Load(env(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": value}))
		require.Error(t, err, value)
		require.Contains(t, err.Error(), "PARTYLINE_MAX_UPLOAD_MB")
	}
}

func TestSecureCookiesFollowsPublicURLScheme(t *testing.T) {
	cfg, err := Load(env(map[string]string{"PARTYLINE_PUBLIC_URL": "https://chat.example.com"}))
	require.NoError(t, err)
	require.True(t, cfg.SecureCookies())

	cfg, err = Load(env(map[string]string{"PARTYLINE_PUBLIC_URL": "http://chat.example.com"}))
	require.NoError(t, err)
	require.False(t, cfg.SecureCookies())
}
