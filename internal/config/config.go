// Package config loads the server configuration from PARTYLINE_* environment variables.
package config

import (
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/wnexous/partyline/internal/connectors/safehttp"
	"github.com/wnexous/partyline/internal/memlimit"
	"github.com/wnexous/partyline/internal/runners/spec"
)

// Config is the validated server configuration.
type Config struct {
	Addr           string
	DataDir        string
	PublicURL      *url.URL
	SecretKey      string // base64 key from PARTYLINE_SECRET_KEY; empty means generate and persist one
	MaxUploadBytes int64
	LogLevel       slog.Level
	LogFormat      string // "json" or "text"
	TrustedProxies []netip.Prefix
	// AllowPrivateNetworks lets connectors reach loopback, private and link-local
	// addresses (PARTYLINE_ALLOW_PRIVATE_NETWORKS); off by default (CON-13).
	AllowPrivateNetworks bool
	// PrivateNetworkAllowlist lets connectors reach the listed private destinations while
	// AllowPrivateNetworks is false (PARTYLINE_PRIVATE_NETWORK_ALLOWLIST); nil lists none.
	PrivateNetworkAllowlist *safehttp.Allowlist
	// MemoryLimit is the soft memory limit of the process (PARTYLINE_MEMORY_LIMIT); nil when the
	// variable is not set, and the limit of the container applies (OPS-06).
	MemoryLimit *memlimit.Limit
	// RunnerImages lists the extra image repositories a container AI may use besides the default
	// agent image (PARTYLINE_RUNNER_IMAGES, comma-separated); nil lists none (CA-09).
	RunnerImages []string
}

// ParseRunnerImages reads the extra image repositories a container AI may use from
// PARTYLINE_RUNNER_IMAGES (comma-separated, CA-09). The server and `partyline runner` read the
// same variable with it, so both sides accept the same images.
func ParseRunnerImages(getenv func(string) string) ([]string, error) {
	var images []string
	for _, repo := range strings.Split(getenv("PARTYLINE_RUNNER_IMAGES"), ",") {
		repo = strings.TrimSpace(repo)
		if repo == "" {
			continue
		}
		if !spec.ValidImageRepo(repo) {
			return nil, fmt.Errorf("PARTYLINE_RUNNER_IMAGES: %q is not an image repository (registry/name)", repo)
		}
		images = append(images, repo)
	}
	return images, nil
}

// SecureCookies reports whether cookies must carry the Secure attribute.
func (c *Config) SecureCookies() bool {
	return c.PublicURL.Scheme == "https"
}

// Load reads the configuration through getenv (usually os.Getenv), applying defaults.
// Validation errors name the offending variable.
func Load(getenv func(string) string) (*Config, error) {
	get := func(name, def string) string {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			return v
		}
		return def
	}

	cfg := &Config{
		Addr:      get("PARTYLINE_ADDR", ":8080"),
		DataDir:   get("PARTYLINE_DATA_DIR", "/data"),
		SecretKey: get("PARTYLINE_SECRET_KEY", ""),
		LogFormat: get("PARTYLINE_LOG_FORMAT", "json"),
	}

	u, err := url.Parse(get("PARTYLINE_PUBLIC_URL", "http://localhost:8080"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("PARTYLINE_PUBLIC_URL: must be an absolute http or https URL")
	}
	cfg.PublicURL = u

	mb, err := strconv.ParseInt(get("PARTYLINE_MAX_UPLOAD_MB", "25"), 10, 64)
	if err != nil || mb <= 0 {
		return nil, fmt.Errorf("PARTYLINE_MAX_UPLOAD_MB: must be a positive integer")
	}
	cfg.MaxUploadBytes = mb << 20

	if err := cfg.LogLevel.UnmarshalText([]byte(get("PARTYLINE_LOG_LEVEL", "info"))); err != nil {
		return nil, fmt.Errorf("PARTYLINE_LOG_LEVEL: must be debug, info, warn or error")
	}
	if cfg.LogFormat != "json" && cfg.LogFormat != "text" {
		return nil, fmt.Errorf("PARTYLINE_LOG_FORMAT: must be json or text")
	}

	allow, err := strconv.ParseBool(get("PARTYLINE_ALLOW_PRIVATE_NETWORKS", "false"))
	if err != nil {
		return nil, fmt.Errorf("PARTYLINE_ALLOW_PRIVATE_NETWORKS: must be true or false")
	}
	cfg.AllowPrivateNetworks = allow
	if cfg.PrivateNetworkAllowlist, err = safehttp.ParseAllowlist(get("PARTYLINE_PRIVATE_NETWORK_ALLOWLIST", "")); err != nil {
		return nil, fmt.Errorf("PARTYLINE_PRIVATE_NETWORK_ALLOWLIST: %w (use CIDRs or host[:port], comma-separated)", err)
	}

	if raw := get("PARTYLINE_MEMORY_LIMIT", ""); raw != "" {
		limit, err := memlimit.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("PARTYLINE_MEMORY_LIMIT: %w", err)
		}
		cfg.MemoryLimit = &limit
	}

	cfg.RunnerImages, err = ParseRunnerImages(getenv)
	if err != nil {
		return nil, err
	}

	for _, item := range strings.Split(get("PARTYLINE_TRUSTED_PROXIES", ""), ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		p, err := parsePrefix(item)
		if err != nil {
			return nil, fmt.Errorf("PARTYLINE_TRUSTED_PROXIES: invalid CIDR or IP %q", item)
		}
		cfg.TrustedProxies = append(cfg.TrustedProxies, p)
	}
	return cfg, nil
}

func parsePrefix(s string) (netip.Prefix, error) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		return p.Masked(), err
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}
