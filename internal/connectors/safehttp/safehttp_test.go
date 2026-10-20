package safehttp

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// CON-13: loopback, private, link-local, CGNAT, multicast and unspecified addresses are
// blocked, also in their IPv4-mapped and NAT64 forms; public addresses are not.
func TestBlocked(t *testing.T) {
	for _, s := range []string{
		"127.0.0.1", "127.8.9.10", "::1", // loopback
		"10.1.2.3", "172.16.0.1", "172.31.255.255", "192.168.1.1", "fc00::1", "fd12:3456::1", // private
		"169.254.169.254", "fe80::1", "fe80::1%eth0", // link-local
		"100.64.0.1", "100.127.255.254", // CGNAT
		"224.0.0.1", "239.255.255.250", "ff02::1", // multicast
		"0.0.0.0", "::", "0.1.2.3", // unspecified, "this network"
		"255.255.255.255",
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "64:ff9b::7f00:1", "64:ff9b::a9fe:a9fe",
	} {
		require.True(t, Blocked(netip.MustParseAddr(s)), s)
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "100.63.255.255", "100.128.0.1", "172.32.0.1", "2001:4860:4860::8888", "64:ff9b::808:808", "::ffff:8.8.8.8"} {
		require.False(t, Blocked(netip.MustParseAddr(s)), s)
	}
	require.True(t, Blocked(netip.Addr{}), "invalid address")
}

func newServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// CON-13: the check runs on the dialed address, so a loopback IP and a name that resolves
// to it are both refused, by request clients and stream (WebSocket) clients; the escape
// hatch allows them.
func TestClientRefusesPrivateDestinations(t *testing.T) {
	var hits atomic.Int32
	srv := newServer(t, func(http.ResponseWriter, *http.Request) { hits.Add(1) })
	byName := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)

	for _, c := range []*http.Client{NewClient(Options{}), NewStreamClient(false, nil)} {
		for _, u := range []string{srv.URL, byName} {
			_, err := c.Get(u)
			require.ErrorIs(t, err, ErrBlockedAddress, u)
		}
	}
	require.Zero(t, hits.Load(), "no request reached the server")

	resp, err := NewClient(Options{AllowPrivate: true}).Get(srv.URL)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp, err = NewStreamClient(true, nil).Get(byName)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, int32(2), hits.Load())
}

// CON-13: redirects are not followed; the 3xx answer comes back as is.
func TestClientDoesNotFollowRedirects(t *testing.T) {
	var followed atomic.Bool
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/target" {
			followed.Store(true)
			return
		}
		http.Redirect(w, r, "/target", http.StatusFound)
	})
	for _, c := range []*http.Client{NewClient(Options{AllowPrivate: true}), NewStreamClient(true, nil)} {
		resp, err := c.Get(srv.URL + "/start")
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusFound, resp.StatusCode)
	}
	require.False(t, followed.Load())
}

// CON-13: a response body over the limit fails to read; one at the limit reads whole.
func TestClientBoundsResponses(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 1024+len(r.URL.Query().Get("extra"))))
	})
	c := NewClient(Options{AllowPrivate: true, MaxResponseBytes: 1024})

	resp, err := c.Get(srv.URL)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Len(t, body, 1024)
	require.NoError(t, resp.Body.Close())

	resp, err = c.Get(srv.URL + "?extra=1")
	require.NoError(t, err)
	_, err = io.ReadAll(resp.Body)
	require.ErrorIs(t, err, ErrResponseTooLarge)
	require.NoError(t, resp.Body.Close())
}

// CON-13: Options.Timeout bounds a call to a server that never answers; the default body
// limit applies when none is set.
func TestClientTimeoutAndDefaultLimit(t *testing.T) {
	require.Equal(t, int64(DefaultMaxResponse), NewClient(Options{}).Transport.(capTransport).max)
	srv := newServer(t, func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	_, err := NewClient(Options{AllowPrivate: true, Timeout: 100 * time.Millisecond}).Get(srv.URL)
	var ne net.Error
	require.ErrorAs(t, err, &ne)
	require.True(t, ne.Timeout())
}
