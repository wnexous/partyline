// Package users owns accounts: first-run setup, login, sessions, invites, administration and
// API tokens. Authorization of other resources lives in package access.
package users

import (
	"context"
	"errors"
	"net/netip"
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
	// Kick ends the live session of an AI, as a deleted AI's is; DeleteAccount calls it for each AI the
	// deleted user hosted. Optional.
	Kick func(ctx context.Context, agentID string)
	// RemoveContainers removes the containers of the user's runners before the account is deleted
	// (CA-06): their rows hold the runners with ON DELETE RESTRICT, so the account deletion would fail
	// otherwise. It is best effort about the containers on the hosts and returns an error only when the
	// rows could not be removed. Optional.
	RemoveContainers func(ctx context.Context, ownerID string) error
}

// Service implements account use cases over the store.
type Service struct {
	store      *store.Store
	now        func() time.Time
	setupToken string
	limiter    *failureLimiter
	audit      *audit.Log
	bus        *events.Bus
	kick       func(ctx context.Context, agentID string)
	containers func(ctx context.Context, ownerID string) error
	// verifyPassword checks a password against its argon2id hash (authn.VerifyPassword);
	// tests wrap it to count the slow checks a login burst runs.
	verifyPassword func(hash, password string) (bool, error)

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
		kick:       opts.Kick,
		containers: opts.RemoveContainers,

		verifyPassword: authn.VerifyPassword,
	}, nil
}

// sessionsRevoked announces ended sessions so live sockets bound to them close.
func (s *Service) sessionsRevoked(userID string, ids []string) {
	if len(ids) > 0 {
		s.revoked(events.SessionsRevoked{UserID: userID, SessionIDs: ids})
	}
}

// revoked announces ended credentials so live sockets authenticated by them close.
func (s *Service) revoked(e events.SessionsRevoked) {
	if s.bus != nil {
		s.bus.Publish(e)
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
// Keys are client networks (see clientKey).
type failureLimiter struct {
	mu        sync.Mutex
	max       int
	window    time.Duration
	maxKeys   int // hard cap on tracked keys; beyond it only expired windows are dropped
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
// already used its max attempts in the current window, or when key is new and the limiter
// is full of active windows.
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
	if w == nil && !l.makeRoomLocked(now) {
		return false
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

// makeRoomLocked drops expired windows (at most once per window, or when full) and reports
// whether a new key fits under the cap. Active windows are never evicted: evicting them
// would let a flood of new addresses flush an attacker's own failures.
func (l *failureLimiter) makeRoomLocked(now time.Time) bool {
	if now.Sub(l.lastPrune) >= l.window || len(l.byKey) >= l.maxKeys {
		l.lastPrune = now
		for k, w := range l.byKey {
			if now.Sub(w.start) >= l.window {
				delete(l.byKey, k)
			}
		}
	}
	return len(l.byKey) < l.maxKeys
}

// clientKey is the limiter key of a client address: the IPv4 address itself (/32, IPv4-mapped
// IPv6 included) or the IPv6 /64, the smallest prefix one subscriber usually gets. Anything
// that does not parse is used as is.
func clientKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.WithZone("").Unmap()
	if addr.Is4() {
		return addr.String()
	}
	return netip.PrefixFrom(addr, 64).Masked().String()
}
