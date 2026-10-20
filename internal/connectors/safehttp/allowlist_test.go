package safehttp

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// S6: PARTYLINE_PRIVATE_NETWORK_ALLOWLIST entries are CIDRs, IPs and host names, each
// optionally with a port; anything else is refused.
func TestParseAllowlist(t *testing.T) {
	empty, err := ParseAllowlist(" , ")
	require.NoError(t, err)
	require.Nil(t, empty)

	a, err := ParseAllowlist("10.0.0.0/8, 192.168.1.20, [fd00::5]:8088, localhost:8088, Evolution.LAN., fd00:1::/64")
	require.NoError(t, err)
	require.Equal(t, []addrRule{
		{prefix: mustPrefix("10.0.0.0/8")},
		{prefix: mustPrefix("192.168.1.20/32")},
		{prefix: mustPrefix("fd00::5/128"), port: "8088"},
		{prefix: mustPrefix("fd00:1::/64")},
	}, a.addrs)
	require.Equal(t, []hostRule{{host: "localhost", port: "8088"}, {host: "evolution.lan"}}, a.hosts)

	for _, bad := range []string{"10.0.0.0/33", "host:0", "host:65536", "host:http", "bad host", "a/b", "[::1]:x", "under_score"} {
		_, err := ParseAllowlist("10.0.0.0/8," + bad)
		require.Error(t, err, bad)
		require.Contains(t, err.Error(), bad)
	}
}

// S6: with private networks blocked, an allowlisted CIDR, address:port or host name:port
// connects; other private destinations, and the listed ones on another port, stay blocked.
func TestAllowlistLetsListedPrivateDestinationsThrough(t *testing.T) {
	var hits atomic.Int32
	srv := newServer(t, func(http.ResponseWriter, *http.Request) { hits.Add(1) })
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	_, port, err := net.SplitHostPort(u.Host)
	require.NoError(t, err)
	byName := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)

	allowed := map[string]string{
		"127.0.0.0/8":          srv.URL,
		"127.0.0.1:" + port:    srv.URL,
		"localhost:" + port:    byName,
		"LOCALHOST":            byName,
		"10.0.0.0/8,127.0.0.1": srv.URL,
	}
	for entry, target := range allowed {
		list, err := ParseAllowlist(entry)
		require.NoError(t, err)
		for _, c := range []*http.Client{NewClient(Options{Allowlist: list}), NewStreamClient(false, list)} {
			resp, err := c.Get(target)
			require.NoError(t, err, entry)
			require.NoError(t, resp.Body.Close())
		}
	}
	require.Equal(t, int32(2*len(allowed)), hits.Load())

	other := "1"
	if port == "1" {
		other = "2"
	}
	blocked := map[string]string{
		"10.0.0.0/8":            srv.URL, // another private range
		"127.0.0.1:" + other:    srv.URL, // the listed address on another port
		"localhost:" + other:    byName,  // the listed name on another port
		"localhost:" + port:     srv.URL, // a name entry does not allow the address itself
		"evolution.lan:" + port: byName,  // another name
	}
	for entry, target := range blocked {
		list, err := ParseAllowlist(entry)
		require.NoError(t, err)
		for _, c := range []*http.Client{NewClient(Options{Allowlist: list}), NewStreamClient(false, list)} {
			_, err := c.Get(target)
			require.ErrorIs(t, err, ErrBlockedAddress, entry)
		}
	}
	require.Equal(t, int32(2*len(allowed)), hits.Load(), "no blocked request reached the server")
}

func mustPrefix(s string) netip.Prefix { return netip.MustParsePrefix(s) }
