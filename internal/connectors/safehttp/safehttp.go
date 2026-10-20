// Package safehttp builds the HTTP clients connectors use to reach platforms (CON-13).
// Connector URLs come from users (api_base, base_url, outbound_url) and media URLs from
// platform payloads, so by default the clients refuse to connect to loopback, private,
// link-local, CGNAT, multicast and unspecified addresses. The check runs on the address
// actually dialed, after DNS resolution, so a hostname that resolves (or rebinds) to such an
// address is refused too. Redirects are never followed and response bodies are bounded.
package safehttp

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// Errors of the clients.
var (
	// ErrBlockedAddress means the destination resolved to a non-public address.
	ErrBlockedAddress = errors.New("destination address not allowed")
	// ErrResponseTooLarge means a response body exceeded the client's limit.
	ErrResponseTooLarge = errors.New("response too large")
)

// DefaultMaxResponse bounds response bodies when Options.MaxResponseBytes is 0: 4 MiB, the
// cap for JSON API answers. Media downloads pass their own limit (S8).
const DefaultMaxResponse = 4 << 20

// Options configure a client.
type Options struct {
	// AllowPrivate disables the address check (PARTYLINE_ALLOW_PRIVATE_NETWORKS=true), for
	// platforms on the local network and for tests.
	AllowPrivate bool
	// Allowlist lets the listed private destinations through while AllowPrivate is false
	// (PARTYLINE_PRIVATE_NETWORK_ALLOWLIST); nil allows none.
	Allowlist *Allowlist
	// Timeout bounds a whole request, body included; 0 leaves it to the request context.
	Timeout time.Duration
	// MaxResponseBytes bounds a response body; 0 means DefaultMaxResponse.
	MaxResponseBytes int64
}

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),          // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),      // CGNAT
	netip.MustParsePrefix("192.0.0.0/24"),       // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"),      // benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),        // reserved, broadcast
	netip.MustParsePrefix("64:ff9b:1::/48"),     // local-use NAT64
	netip.MustParsePrefix("2001:db8::/32"),      // documentation
	netip.MustParsePrefix("100::/64"),           // discard-only
	netip.MustParsePrefix("::ffff:0:0:0/96"),    // IPv4-translated
	netip.MustParsePrefix("2002::/16"),          // 6to4 (may embed any IPv4)
	netip.MustParsePrefix("2001::/32"),          // Teredo (may embed any IPv4)
	netip.MustParsePrefix("fec0::/10"),          // deprecated site-local
	netip.MustParsePrefix("169.254.0.0/16"),     // link-local, cloud metadata
	netip.MustParsePrefix("fe80::/10"),          // link-local
	netip.MustParsePrefix("255.255.255.255/32"), // broadcast
}

// nat64 is the well-known NAT64 prefix; the IPv4 address it embeds is checked.
var nat64 = netip.MustParsePrefix("64:ff9b::/96")

// Blocked reports whether addr is loopback, private, link-local, CGNAT, multicast,
// unspecified or otherwise not a public unicast address.
func Blocked(addr netip.Addr) bool {
	if !addr.IsValid() {
		return true
	}
	addr = addr.WithZone("").Unmap()
	if addr.Is6() && nat64.Contains(addr) {
		b := addr.As16()
		return Blocked(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}))
	}
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsUnspecified() || addr.IsMulticast() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsInterfaceLocalMulticast() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// control refuses connections to blocked addresses that allow does not list; it runs on
// every dialed address, after name resolution.
func control(allow *Allowlist) func(_, address string, _ syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return ErrBlockedAddress
		}
		addr, err := netip.ParseAddr(host)
		if err != nil || (Blocked(addr) && !allow.allowsAddr(addr, port)) {
			return ErrBlockedAddress
		}
		return nil
	}
}

// Transport returns a transport that dials only public addresses, plus those allow lists,
// unless allowPrivate. It ignores proxy settings when checking addresses, since a proxy
// would dial for it.
func Transport(allowPrivate bool, allow *Allowlist) *http.Transport {
	d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	t := &http.Transport{
		DialContext:           d.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	if allowPrivate {
		t.Proxy = http.ProxyFromEnvironment
		return t
	}
	d.Control = control(allow)
	open := &net.Dialer{Timeout: d.Timeout, KeepAlive: d.KeepAlive}
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if host, port, err := net.SplitHostPort(address); err == nil && allow.allowsHost(host, port) {
			return open.DialContext(ctx, network, address) // a listed host name, whatever it resolves to
		}
		return d.DialContext(ctx, network, address)
	}
	return t
}

// NewClient returns a client for request/response calls: checked destinations, no
// redirects (the 3xx answer is returned as is), the timeout and a bounded response body.
func NewClient(o Options) *http.Client {
	limit := o.MaxResponseBytes
	if limit <= 0 {
		limit = DefaultMaxResponse
	}
	return &http.Client{
		Transport:     capTransport{rt: Transport(o.AllowPrivate, o.Allowlist), max: limit},
		Timeout:       o.Timeout,
		CheckRedirect: noRedirects,
	}
}

// NewStreamClient returns a client for WebSocket handshakes: checked destinations and no
// redirects, without a timeout or body limit (the connection outlives the request).
func NewStreamClient(allowPrivate bool, allow *Allowlist) *http.Client {
	return &http.Client{Transport: Transport(allowPrivate, allow), CheckRedirect: noRedirects}
}

func noRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// capTransport bounds response bodies.
type capTransport struct {
	rt  http.RoundTripper
	max int64
}

func (c capTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := c.rt.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	resp.Body = &cappedBody{ReadCloser: resp.Body, left: c.max}
	return resp, nil
}

// cappedBody fails with ErrResponseTooLarge once more than its limit is read.
type cappedBody struct {
	io.ReadCloser
	left int64
}

func (b *cappedBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		var one [1]byte
		n, err := b.ReadCloser.Read(one[:])
		if n > 0 {
			return 0, ErrResponseTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.ReadCloser.Read(p)
	b.left -= int64(n)
	return n, err
}
