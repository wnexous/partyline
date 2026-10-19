// Package users owns accounts: first-run setup, login, sessions, invites, administration and
// API tokens. Authorization of other resources lives in package access.
package users

import (
	"errors"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/store"
)

// Errors returned by the service. The API maps each to one status code.
var (
	ErrSetupDone          = errors.New("setup already done")             // 409
	ErrBadSetupToken      = errors.New("invalid setup token")            // 401
	ErrInvalidCredentials = errors.New("invalid username or password")   // 401, same for every cause (AUTH-04)
	ErrRateLimited        = errors.New("too many failed logins")         // 429
	ErrUnauthenticated    = errors.New("invalid or expired credentials") // 401
	ErrNotFound           = errors.New("not found")                      // 404
)

// ValidationError names each rejected input field and the reason.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	names := make([]string, 0, len(e.Fields))
	for name := range e.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return "invalid " + strings.Join(names, ", ")
}

// SessionTTL is the sliding lifetime of a login session (AUTH-03).
const SessionTTL = 30 * 24 * time.Hour

// Login rate limit (AUTH-05): at most loginMaxFailures failed attempts per IP per window.
const (
	loginMaxFailures = 5
	loginWindow      = time.Minute
)

// touchInterval bounds how often a session's sliding expiry is written.
const touchInterval = time.Minute

// Options tune the service; zero values use the defaults.
type Options struct {
	// Now returns the current time; defaults to time.Now.
	Now func() time.Time
}

// Service implements account use cases over the store.
type Service struct {
	store      *store.Store
	now        func() time.Time
	setupToken string
	limiter    *failureLimiter

	dummyOnce sync.Once
	dummyHash string
}

// NewService returns a Service and generates the one-time setup token, held in memory only.
func NewService(st *store.Store, opts Options) (*Service, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	token, err := authn.NewSessionToken()
	if err != nil {
		return nil, err
	}
	return &Service{
		store:      st,
		now:        opts.Now,
		setupToken: token,
		limiter:    newFailureLimiter(loginMaxFailures, loginWindow),
	}, nil
}

// SetupToken returns the one-time token that authorizes POST /setup while there are no users.
func (s *Service) SetupToken() string { return s.setupToken }

var usernameRE = regexp.MustCompile(dto.UsernamePattern)

// AccountInput is the data needed to create an account.
type AccountInput struct {
	Username    string
	Password    string
	DisplayName string
}

func (in AccountInput) validate() error {
	fields := map[string]string{}
	if !usernameRE.MatchString(in.Username) {
		fields["username"] = "must match " + dto.UsernamePattern
	}
	if f := validatePassword(in.Password); f != "" {
		fields["password"] = f
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func validatePassword(p string) string {
	if utf8.RuneCountInString(p) < dto.PasswordMinLength {
		return "must have at least 10 characters"
	}
	return ""
}

func ms(t time.Time) int64 { return t.UnixMilli() }

// failureLimiter counts failures per key in fixed windows that open at the first failure.
type failureLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	byKey  map[string]*failureWindow
}

type failureWindow struct {
	start time.Time
	count int
}

func newFailureLimiter(maxFailures int, window time.Duration) *failureLimiter {
	return &failureLimiter{max: maxFailures, window: window, byKey: map[string]*failureWindow{}}
}

// Blocked reports whether key reached the failure limit in its current window.
func (l *failureLimiter) Blocked(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.byKey[key]
	return w != nil && now.Sub(w.start) < l.window && w.count >= l.max
}

// Fail records one failure for key.
func (l *failureLimiter) Fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.byKey[key]
	if w == nil || now.Sub(w.start) >= l.window {
		if len(l.byKey) >= 10000 {
			l.pruneLocked(now)
		}
		l.byKey[key] = &failureWindow{start: now, count: 1}
		return
	}
	w.count++
}

func (l *failureLimiter) pruneLocked(now time.Time) {
	for k, w := range l.byKey {
		if now.Sub(w.start) >= l.window {
			delete(l.byKey, k)
		}
	}
}
