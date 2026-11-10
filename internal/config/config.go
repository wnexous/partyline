// Package config loads the server configuration from PARTYLINE_* environment variables.
package config

import (
	"fmt"
	"log/slog"
	"math"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/wnexous/partyline/internal/connectors/safehttp"
	"github.com/wnexous/partyline/internal/memlimit"
	"github.com/wnexous/partyline/internal/settings"
	"github.com/wnexous/partyline/internal/transcribe"
)

// Config is the validated server configuration.
type Config struct {
	Addr      string
	DataDir   string
	PublicURL *url.URL
	SecretKey string // base64 key from PARTYLINE_SECRET_KEY; empty means generate and persist one
	// MaxUploadBytes bounds one request body that carries a file (PARTYLINE_MAX_UPLOAD_MB): a
	// multipart file, and the chunk of an upload session.
	MaxUploadBytes int64
	// MaxFileBytes bounds one file sent through an upload session in chunks (PARTYLINE_MAX_FILE_MB,
	// LF-03).
	MaxFileBytes int64
	// WhatsAppMaxFileBytes bounds a WhatsApp media, sent or received (PARTYLINE_WHATSAPP_MAX_FILE_MB,
	// 512 by default): evolution-go holds a media's base64 whole in its own memory, both ways.
	WhatsAppMaxFileBytes int64
	// DiskReserveBytes is the free space an upload session must leave on the data disk
	// (PARTYLINE_DISK_RESERVE_MB, LF-07).
	DiskReserveBytes int64
	LogLevel         slog.Level
	LogFormat        string // "json" or "text"
	TrustedProxies   []netip.Prefix
	// AllowPrivateNetworks lets connectors reach loopback, private and link-local
	// addresses (PARTYLINE_ALLOW_PRIVATE_NETWORKS); off by default (CON-13).
	AllowPrivateNetworks bool
	// PrivateNetworkAllowlist lets connectors reach the listed private destinations while
	// AllowPrivateNetworks is false (PARTYLINE_PRIVATE_NETWORK_ALLOWLIST); nil lists none.
	PrivateNetworkAllowlist *safehttp.Allowlist
	// MemoryLimit is the soft memory limit of the process (PARTYLINE_MEMORY_LIMIT); nil when the
	// variable is not set, and the limit of the container applies (OPS-06).
	MemoryLimit *memlimit.Limit
	// SkipMigrationBackup turns off the copy of the database written before pending migrations
	// run (PARTYLINE_SKIP_MIGRATION_BACKUP); only an operator who accepted the risk sets it (OPS-07).
	SkipMigrationBackup bool
	// MigrationBackupsKeep is how many pre-migration backups stay in <data dir>/backups
	// (PARTYLINE_MIGRATION_BACKUPS_KEEP); 0 keeps every backup (OPS-07).
	MigrationBackupsKeep int
	// TranscribeURL is the base URL of an OpenAI-compatible transcription service
	// (PARTYLINE_TRANSCRIBE_URL); empty turns server transcription off (TR-01, TR-06).
	TranscribeURL string
	// TranscribeModel is the model asked of that service (PARTYLINE_TRANSCRIBE_MODEL).
	TranscribeModel string
	// AttachmentRetentionDays is the default of the instance setting that deletes room attachments
	// older than this many days (PARTYLINE_ATTACHMENT_RETENTION_DAYS, RET-01); 0 keeps them forever.
	// An instance admin's value, when set, applies instead.
	AttachmentRetentionDays int
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

	mb, err := strconv.ParseInt(get("PARTYLINE_MAX_UPLOAD_MB", "95"), 10, 64)
	if err != nil || mb <= 0 {
		return nil, fmt.Errorf("PARTYLINE_MAX_UPLOAD_MB: must be a positive integer")
	}
	cfg.MaxUploadBytes = mb << 20
	if cfg.MaxFileBytes, err = megabytes(get("PARTYLINE_MAX_FILE_MB", "5120"), false); err != nil {
		return nil, fmt.Errorf("PARTYLINE_MAX_FILE_MB: %w", err)
	}
	if cfg.WhatsAppMaxFileBytes, err = megabytes(get("PARTYLINE_WHATSAPP_MAX_FILE_MB", "512"), false); err != nil {
		return nil, fmt.Errorf("PARTYLINE_WHATSAPP_MAX_FILE_MB: %w", err)
	}
	if cfg.DiskReserveBytes, err = megabytes(get("PARTYLINE_DISK_RESERVE_MB", "2048"), true); err != nil {
		return nil, fmt.Errorf("PARTYLINE_DISK_RESERVE_MB: %w", err)
	}

	days, err := strconv.Atoi(get("PARTYLINE_ATTACHMENT_RETENTION_DAYS", "0"))
	if err != nil || days < 0 || days > settings.MaxAttachmentRetentionDays {
		return nil, fmt.Errorf("PARTYLINE_ATTACHMENT_RETENTION_DAYS: must be an integer from 0 (keep forever) to %d", settings.MaxAttachmentRetentionDays)
	}
	cfg.AttachmentRetentionDays = days

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

	skipBackup, err := strconv.ParseBool(get("PARTYLINE_SKIP_MIGRATION_BACKUP", "false"))
	if err != nil {
		return nil, fmt.Errorf("PARTYLINE_SKIP_MIGRATION_BACKUP: must be true or false")
	}
	cfg.SkipMigrationBackup = skipBackup
	keepBackups, err := strconv.Atoi(get("PARTYLINE_MIGRATION_BACKUPS_KEEP", "5"))
	if err != nil || keepBackups < 0 {
		return nil, fmt.Errorf("PARTYLINE_MIGRATION_BACKUPS_KEEP: must be a non-negative integer (0 keeps every backup)")
	}
	cfg.MigrationBackupsKeep = keepBackups

	if raw := get("PARTYLINE_TRANSCRIBE_URL", ""); raw != "" {
		t, err := url.Parse(raw)
		if err != nil || (t.Scheme != "http" && t.Scheme != "https") || t.Host == "" {
			return nil, fmt.Errorf("PARTYLINE_TRANSCRIBE_URL: must be an absolute http or https URL")
		}
		cfg.TranscribeURL = raw
	}
	cfg.TranscribeModel = get("PARTYLINE_TRANSCRIBE_MODEL", transcribe.DefaultModel)

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

// megabytes parses a size in MiB and returns it in bytes; zero is allowed only when zeroOK.
func megabytes(raw string, zeroOK bool) (int64, error) {
	mb, err := strconv.ParseInt(raw, 10, 64)
	switch {
	case err == nil && mb > math.MaxInt64>>20:
		return 0, fmt.Errorf("too large")
	case err == nil && (mb > 0 || (zeroOK && mb == 0)):
		return mb << 20, nil
	case zeroOK:
		return 0, fmt.Errorf("must be a non-negative integer")
	}
	return 0, fmt.Errorf("must be a positive integer")
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
