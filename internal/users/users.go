// Package users owns accounts: first-run setup, login, sessions, invites, administration and
// API tokens. Authorization of other resources lives in package access.
package users

import (
	"errors"
	"regexp"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/validation"
)

// Errors returned by the service. The API maps each to one status code.
var (
	ErrSetupDone          = errors.New("setup already done")              // 409
	ErrBadSetupToken      = errors.New("invalid setup token")             // 401
	ErrInvalidCredentials = errors.New("invalid username or password")    // 401, same for every cause (AUTH-04)
	ErrRateLimited        = errors.New("too many failed logins")          // 429
	ErrUnauthenticated    = errors.New("invalid or expired credentials")  // 401
	ErrNotFound           = errors.New("not found")                       // 404
	ErrInviteGone         = errors.New("invite used, revoked or expired") // 410
	ErrUsernameTaken      = errors.New("username already taken")          // 409
)

// Admin guard errors (409): the instance always keeps one active admin.
var (
	ErrSelfAdminChange = errors.New("admins cannot demote or disable themselves")
	ErrLastAdmin       = errors.New("the last active admin cannot be demoted or disabled")
)

// ValidationError names each rejected input field and the reason.
type ValidationError = validation.Error

// SessionTTL is the sliding lifetime of a login session (AUTH-03).
const SessionTTL = 30 * 24 * time.Hour

// Login rate limit (AUTH-05): at most loginMaxFailures failed attempts per IP per window.
const (
	loginMaxFailures = 5
	loginWindow      = time.Minute
)

// SessionSlideInterval bounds how often a session's sliding expiry moves, and so how often
// its cookie is re-issued.
const SessionSlideInterval = time.Hour

// touchInterval bounds how often a session's last_seen_at is written.
const touchInterval = time.Minute

// Options tune the service; zero values use the defaults.
type Options struct {
	// Now returns the current time; defaults to time.Now.
	Now func() time.Time
	// Bus receives SessionsRevoked events; optional.
	Bus *events.Bus
}

// Service implements account use cases over the store.
type Service struct {
	store      *store.Store
	now        func() time.Time
	setupToken string
	limiter    *failureLimiter
	audit      *audit.Log
	bus        *events.Bus

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
		audit:      audit.New(st, opts.Now),
		bus:        opts.Bus,
	}, nil
}

// sessionsRevoked announces ended sessions so live sockets bound to them close.
func (s *Service) sessionsRevoked(userID string, ids []string) {
	if s.bus != nil && len(ids) > 0 {
		s.bus.Publish(events.SessionsRevoked{UserID: userID, SessionIDs: ids})
	}
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

// failureLimiter counts login attempts per key in fixed windows that open at the first
// attempt. Begin reserves an attempt before the slow password check, so a parallel burst
// cannot run more than max checks (AUTH-05). A failure keeps its reservation; a success or
// an internal error returns it with Release, so a success never clears earlier failures.
type failureLimiter struct {
	mu        sync.Mutex
	max       int
	window    time.Duration
	maxKeys   int // hard cap on tracked keys; the oldest window is evicted beyond it
	lastPrune time.Time
	byKey     map[string]*failureWindow
}

type failureWindow struct {
	start time.Time
	count int
}

// defaultLimiterKeys bounds the limiter's memory under a many-IP flood.
const defaultLimiterKeys = 10000

func newFailureLimiter(maxFailures int, window time.Duration) *failureLimiter {
	return &failureLimiter{max: maxFailures, window: window, maxKeys: defaultLimiterKeys, byKey: map[string]*failureWindow{}}
}

// Begin reserves one attempt for key. It returns false, reserving nothing, when the key
// already used its max attempts in the current window.
func (l *failureLimiter) Begin(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.byKey[key]
	if w != nil && now.Sub(w.start) < l.window {
		if w.count >= l.max {
			return false
		}
		w.count++
		return true
	}
	if w == nil {
		l.makeRoomLocked(now)
	}
	l.byKey[key] = &failureWindow{start: now, count: 1}
	return true
}

// Release returns a reserved attempt that did not fail: a successful login or an internal
// error. The window and the failures it already counts stay.
func (l *failureLimiter) Release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if w := l.byKey[key]; w != nil && w.count > 0 {
		w.count--
	}
}

// makeRoomLocked drops expired windows (at most once per window, or when full) and, when
// the map is still at its cap, evicts the oldest window.
func (l *failureLimiter) makeRoomLocked(now time.Time) {
	if now.Sub(l.lastPrune) >= l.window || len(l.byKey) >= l.maxKeys {
		l.lastPrune = now
		for k, w := range l.byKey {
			if now.Sub(w.start) >= l.window {
				delete(l.byKey, k)
			}
		}
	}
	for len(l.byKey) >= l.maxKeys {
		var oldest string
		var oldestStart time.Time
		for k, w := range l.byKey {
			if oldest == "" || w.start.Before(oldestStart) {
				oldest, oldestStart = k, w.start
			}
		}
		delete(l.byKey, oldest)
	}
}
