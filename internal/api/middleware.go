// Package api is the HTTP API: middleware, routing and handlers. Handlers call services and
// render DTOs from package dto; authorization happens inside the services.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/avatars"
	"github.com/wnexous/partyline/internal/users"
)

// SessionCookie is the name of the login session cookie.
const SessionCookie = "pl_session"

// agentPathPrefix is the only path prefix agent tokens may reach (TOK-03).
const agentPathPrefix = "/api/v1/agent/"

// DefaultCSP is the Content-Security-Policy of API responses. HTML responses set their own.
const DefaultCSP = "default-src 'none'; frame-ancestors 'none'"

// Authenticator resolves request credentials to a principal. Unknown, expired or revoked
// credentials yield users.ErrUnauthenticated; any other error is an internal failure.
type Authenticator interface {
	// CheckSession also reports whether the session's expiry slid, so the cookie is re-issued.
	CheckSession(ctx context.Context, secret string) (p authn.Principal, renewed bool, err error)
	AuthenticateToken(ctx context.Context, secret string) (authn.Principal, error)
}

// MiddlewareConfig configures the middleware chain.
type MiddlewareConfig struct {
	PublicURL      *url.URL
	TrustedProxies []netip.Prefix
	MaxUploadBytes int64
	Auth           Authenticator
	Logger         *slog.Logger
}

// Middleware wraps next with, outermost first: recover, request id, security headers,
// client IP, body limit, principal resolution, agent-token restriction and the CSRF check.
func Middleware(cfg MiddlewareConfig, next http.Handler) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	origin := cfg.PublicURL.Scheme + "://" + cfg.PublicURL.Host

	h := csrf(origin, next)
	h = agentRestriction(h)
	h = principal(cfg, h)
	h = bodyLimit(cfg.MaxUploadBytes, h)
	h = clientIP(cfg.TrustedProxies, h)
	h = securityHeaders(h)
	h = requestID(h)
	return recoverer(cfg.Logger, h)
}

func recoverer(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				panic(v)
			}
			log.ErrorContext(r.Context(), "handler panic", "panic", v,
				"request_id", httpx.RequestID(r.Context()), "method", r.Method, "path", r.URL.Path)
			httpx.WriteError(w, errors.New("panic"))
		}()
		next.ServeHTTP(w, r)
	})
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 8)
		_, _ = rand.Read(buf)
		id := hex.EncodeToString(buf)
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(httpx.WithRequestID(r.Context(), id)))
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", DefaultCSP)
		next.ServeHTTP(w, r)
	})
}

// clientIP resolves the client address: the peer address, or the right-most untrusted
// X-Forwarded-For entry when the peer is a trusted proxy.
func clientIP(trusted []netip.Prefix, next http.Handler) http.Handler {
	isTrusted := func(a netip.Addr) bool {
		for _, p := range trusted {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ip netip.Addr
		if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
			ip = ap.Addr().Unmap()
		}
		if ip.IsValid() && isTrusted(ip) {
			hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
			for i := len(hops) - 1; i >= 0; i-- {
				a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
				if err != nil {
					break
				}
				ip = a.Unmap()
				if !isTrusted(ip) {
					break
				}
			}
		}
		next.ServeHTTP(w, r.WithContext(httpx.WithClientIP(r.Context(), ip)))
	})
}

// bodyLimit caps request bodies at 1 MiB, or at the upload limit plus 1 MiB of envelope for
// multipart requests (SEC-03). An avatar upload is a raw image: it may use the upload limit
// (at least 2 MiB and one byte), so that a file over 2 MiB reaches the handler and is refused
// as an invalid avatar (PROF-06).
func bodyLimit(maxUpload int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := int64(httpx.MaxJSONBody)
		if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err == nil && mt == "multipart/form-data" {
			limit += maxUpload
		}
		if isAvatarUpload(r) {
			limit = max(maxUpload, avatars.MaxBytes+1)
		}
		if r.ContentLength > limit {
			httpx.WriteError(w, httpx.NewError(http.StatusRequestEntityTooLarge, dto.ErrorCodeTooLarge, "request body too large"))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}

// isAvatarUpload reports whether r uploads an avatar image: PUT /intelligences/{id}/avatar or
// PUT /agent/profile/avatar.
func isAvatarUpload(r *http.Request) bool {
	if r.Method != http.MethodPut {
		return false
	}
	path := r.URL.Path
	return path == agentPathPrefix+"profile/avatar" ||
		(strings.HasPrefix(path, "/api/v1/intelligences/") && strings.HasSuffix(path, "/avatar"))
}

var errUnauthorized = httpx.NewError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized, "invalid or expired credentials")

// sessionCookie returns the pl_session cookie for secret with a full SessionTTL Max-Age (AUTH-03).
// Secure follows the public URL scheme (plain HTTP is allowed for localhost).
func sessionCookie(secret string, secure bool) *http.Cookie {
	// #nosec G124 -- Secure is set whenever the public URL is https.
	return &http.Cookie{
		Name:     SessionCookie,
		Value:    secret,
		Path:     "/",
		MaxAge:   int(users.SessionTTL / time.Second),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	}
}

// principal resolves "Authorization: Bearer <token>" or the session cookie. Bad bearer
// credentials are rejected with 401; a bad cookie leaves the request anonymous. When the
// session's expiry slides, the cookie is re-issued so the browser keeps it as long.
func principal(cfg MiddlewareConfig, next http.Handler) http.Handler {
	secure := cfg.PublicURL.Scheme == "https"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if authz := r.Header.Get("Authorization"); authz != "" {
			secret, ok := strings.CutPrefix(authz, "Bearer ")
			if !ok {
				httpx.WriteError(w, errUnauthorized)
				return
			}
			p, err := cfg.Auth.AuthenticateToken(ctx, strings.TrimSpace(secret))
			if err != nil {
				if !errors.Is(err, users.ErrUnauthenticated) {
					cfg.Logger.ErrorContext(ctx, "authenticate token", "err", err, "request_id", httpx.RequestID(ctx))
					httpx.WriteError(w, err)
					return
				}
				httpx.WriteError(w, errUnauthorized)
				return
			}
			if p.Kind == authn.KindAgent {
				p.AgentName = r.Header.Get(dto.HeaderAgent)
			}
			next.ServeHTTP(w, r.WithContext(authn.WithPrincipal(ctx, p)))
			return
		}
		if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
			p, renewed, err := cfg.Auth.CheckSession(ctx, c.Value)
			switch {
			case err == nil:
				if renewed {
					http.SetCookie(w, sessionCookie(c.Value, secure))
				}
				r = r.WithContext(authn.WithPrincipal(ctx, p))
			case !errors.Is(err, users.ErrUnauthenticated):
				cfg.Logger.ErrorContext(ctx, "validate session", "err", err, "request_id", httpx.RequestID(ctx))
				httpx.WriteError(w, err)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func agentRestriction(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := authn.PrincipalFrom(r.Context()); ok && p.Kind == authn.KindAgent &&
			!strings.HasPrefix(r.URL.Path, agentPathPrefix) {
			httpx.WriteError(w, httpx.NewError(http.StatusForbidden, dto.ErrorCodeForbidden, "agent tokens can only call agent endpoints"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// csrf rejects state-changing requests that are not bearer-authenticated when they come from
// another origin, or when they ride on the session cookie without proving their origin (SEC-02).
func csrf(origin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		p, authed := authn.PrincipalFrom(r.Context())
		if authed && p.TokenID != "" {
			next.ServeHTTP(w, r)
			return
		}
		site, from := r.Header.Get("Sec-Fetch-Site"), r.Header.Get("Origin")
		foreign := (site != "" && site != "same-origin") || (from != "" && from != origin)
		unproven := authed && p.SessionID != "" && site == "" && from == ""
		if foreign || unproven {
			httpx.WriteError(w, httpx.NewError(http.StatusForbidden, dto.ErrorCodeForbidden, "cross-origin request refused"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
