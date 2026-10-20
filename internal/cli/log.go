package cli

import (
	"io"
	"log/slog"
	"strings"

	"github.com/wnexous/partyline/internal/config"
)

// redacted replaces the value of every secret-looking log attribute (SEC-05).
const redacted = "[redacted]"

// secretKeys are attribute keys whose values never reach the log: credentials and message text.
var secretKeys = map[string]bool{
	"password": true, "token": true, "secret": true, "text": true,
	"authorization": true, "cookie": true, "api_key": true,
}

// newLogger returns the JSON or text logger configured by PARTYLINE_LOG_*, with redaction.
func newLogger(cfg *config.Config, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: cfg.LogLevel,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if secretKeys[strings.ToLower(a.Key)] {
				return slog.String(a.Key, redacted)
			}
			return a
		},
	}
	if cfg.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}
