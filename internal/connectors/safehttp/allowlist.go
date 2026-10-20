package safehttp

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// Allowlist names private destinations connectors may reach while private networks stay
// blocked (PARTYLINE_PRIVATE_NETWORK_ALLOWLIST). An entry is a CIDR (any port), an IP
// address or a host name, each optionally with a port ("10.0.0.0/8", "192.168.1.20",
// "[fd00::5]:8088", "localhost:8088", "evolution.lan"). A host name entry matches the name
// in the URL, whatever it resolves to; an address entry matches the address dialed.
type Allowlist struct {
	addrs []addrRule
	hosts []hostRule
}

type addrRule struct {
	prefix netip.Prefix
	port   string // "" matches every port
}

type hostRule struct {
	host string
	port string
}

// ParseAllowlist parses a comma-separated list; an empty list gives nil.
func ParseAllowlist(s string) (*Allowlist, error) {
	a := &Allowlist{}
	for _, entry := range strings.Split(s, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if err := a.add(entry); err != nil {
			return nil, fmt.Errorf("invalid entry %q", entry)
		}
	}
	if len(a.addrs) == 0 && len(a.hosts) == 0 {
		return nil, nil
	}
	return a, nil
}

func (a *Allowlist) add(entry string) error {
	if strings.Contains(entry, "/") {
		p, err := netip.ParsePrefix(entry)
		if err != nil {
			return err
		}
		a.addrs = append(a.addrs, addrRule{prefix: p.Masked()})
		return nil
	}
	if addr, err := netip.ParseAddr(entry); err == nil { // a bare IP, IPv6 included
		a.addrs = append(a.addrs, addrRule{prefix: single(addr)})
		return nil
	}
	host, port := entry, ""
	if h, p, err := net.SplitHostPort(entry); err == nil {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("invalid port")
		}
		host, port = h, strconv.Itoa(n)
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		a.addrs = append(a.addrs, addrRule{prefix: single(addr), port: port})
		return nil
	}
	host = normalizeHost(host)
	if !validHost(host) {
		return fmt.Errorf("invalid host")
	}
	a.hosts = append(a.hosts, hostRule{host: host, port: port})
	return nil
}

func single(addr netip.Addr) netip.Prefix {
	addr = addr.WithZone("").Unmap()
	return netip.PrefixFrom(addr, addr.BitLen())
}

func normalizeHost(h string) string { return strings.TrimSuffix(strings.ToLower(h), ".") }

func validHost(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for _, r := range h {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '.' {
			return false
		}
	}
	return true
}

// allowsAddr reports whether the dialed address and port are listed.
func (a *Allowlist) allowsAddr(addr netip.Addr, port string) bool {
	if a == nil {
		return false
	}
	addr = addr.WithZone("").Unmap()
	for _, r := range a.addrs {
		if r.prefix.Contains(addr) && (r.port == "" || r.port == port) {
			return true
		}
	}
	return false
}

// allowsHost reports whether the host name (as written in the URL) and port are listed.
func (a *Allowlist) allowsHost(host, port string) bool {
	if a == nil {
		return false
	}
	host = normalizeHost(host)
	for _, r := range a.hosts {
		if r.host == host && (r.port == "" || r.port == port) {
			return true
		}
	}
	return false
}
