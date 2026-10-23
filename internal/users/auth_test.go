package users

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/store"
)

// fakeClock is a manually advanced clock shared by the service under test.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type fixture struct {
	svc   *Service
	store *store.Store
	clock *fakeClock
	path  string // the database file
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "partyline.db")
	st, err := store.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(context.Background()))
	clock := &fakeClock{now: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	svc, err := NewService(st, Options{Now: clock.Now})
	require.NoError(t, err)
	return &fixture{svc: svc, store: st, clock: clock, path: path}
}

const adminPassword = "correct-horse-battery"

func (f *fixture) setupAdmin(t *testing.T) {
	t.Helper()
	_, err := f.svc.Setup(context.Background(), f.svc.SetupToken(), AccountInput{Username: "admin", Password: adminPassword})
	require.NoError(t, err)
}

// displayName is the display name of the account's intelligence: the one name an account has
// (PROF-03).
func (f *fixture) displayName(t *testing.T, id string) string {
	t.Helper()
	i, err := f.store.R().GetIntelligence(context.Background(), id)
	require.NoError(t, err)
	return i.DisplayName
}

func (f *fixture) userCount(t *testing.T) int64 {
	t.Helper()
	n, err := f.store.R().CountUsers(context.Background())
	require.NoError(t, err)
	return n
}

func login(f *fixture, ip, username, password string) (NewSession, error) {
	_, sess, err := f.svc.Login(context.Background(), LoginInput{Username: username, Password: password, IP: ip})
	return sess, err
}

// AUTH-01: setup is accepted only with the in-memory setup token while there are zero users.
func TestSetupRequiresSetupTokenAndZeroUsers(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	require.NotEmpty(t, f.svc.SetupToken())

	needs, err := f.svc.NeedsSetup(ctx)
	require.NoError(t, err)
	require.True(t, needs)

	_, err = f.svc.Setup(ctx, "not-the-token", AccountInput{Username: "admin", Password: adminPassword})
	require.ErrorIs(t, err, ErrBadSetupToken)
	require.Zero(t, f.userCount(t))

	_, err = f.svc.Setup(ctx, "", AccountInput{Username: "admin", Password: adminPassword})
	require.ErrorIs(t, err, ErrBadSetupToken)
	require.Zero(t, f.userCount(t))

	u, err := f.svc.Setup(ctx, f.svc.SetupToken(), AccountInput{Username: "admin", Password: adminPassword, DisplayName: "Admin"})
	require.NoError(t, err)
	require.Equal(t, "admin", u.Username)
	require.Equal(t, "admin", u.Role)
	require.Equal(t, "Admin", f.displayName(t, u.ID))
	require.EqualValues(t, 1, f.userCount(t))

	needs, err = f.svc.NeedsSetup(ctx)
	require.NoError(t, err)
	require.False(t, needs)
}

// AUTH-02: a second setup, even with the right token, is a conflict and creates nothing.
func TestSetupAfterUserExistsIsConflict(t *testing.T) {
	f := newFixture(t)
	f.setupAdmin(t)

	_, err := f.svc.Setup(context.Background(), f.svc.SetupToken(), AccountInput{Username: "second", Password: adminPassword})
	require.ErrorIs(t, err, ErrSetupDone)
	require.EqualValues(t, 1, f.userCount(t))
}

// AUTH-07 rules applied at setup: username pattern and password length name the field.
func TestSetupValidatesUsernameAndPassword(t *testing.T) {
	f := newFixture(t)

	_, err := f.svc.Setup(context.Background(), f.svc.SetupToken(), AccountInput{Username: "No Spaces", Password: "short"})
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	require.Contains(t, verr.Fields, "username")
	require.Contains(t, verr.Fields, "password")
	require.Zero(t, f.userCount(t))
}

// AUTH-04: unknown user and wrong password fail with the same error value.
func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	f := newFixture(t)
	f.setupAdmin(t)

	_, errUnknown := login(f, "192.0.2.1", "nobody", adminPassword)
	_, errWrong := login(f, "192.0.2.1", "admin", "wrong-password-123")

	require.ErrorIs(t, errUnknown, ErrInvalidCredentials)
	require.ErrorIs(t, errWrong, ErrInvalidCredentials)
	require.Equal(t, errUnknown, errWrong)
}

// AUTH-03 + AUTH-08: login creates a 30-day session whose secret is stored only as sha256.
func TestLoginCreatesThirtyDaySessionStoredAsHash(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)

	u, sess, err := f.svc.Login(ctx, LoginInput{Username: "admin", Password: adminPassword, IP: "192.0.2.1", UserAgent: "test-agent"})
	require.NoError(t, err)
	require.Equal(t, "admin", u.Username)
	require.NotEmpty(t, sess.Secret)
	require.Equal(t, f.clock.Now().Add(30*24*time.Hour), sess.ExpiresAt)

	row, err := f.store.R().GetSessionByTokenHash(ctx, authn.HashToken(sess.Secret))
	require.NoError(t, err)
	require.NotEqual(t, sess.Secret, row.TokenHash)
	require.Equal(t, sess.ID, row.ID)
	require.Equal(t, "192.0.2.1", row.Ip)
	require.Equal(t, "test-agent", row.UserAgent)

	p, err := f.svc.ValidateSession(ctx, sess.Secret)
	require.NoError(t, err)
	require.Equal(t, authn.KindUser, p.Kind)
	require.Equal(t, u.ID, p.UserID)
	require.Equal(t, "admin", p.Role)
	require.Equal(t, sess.ID, p.SessionID)
}

// AUTH-05: after 5 failures from one IP within a minute, further attempts are rate-limited
// for the rest of that minute, even with the right password; other IPs are unaffected.
func TestLoginRateLimitPerIP(t *testing.T) {
	f := newFixture(t)
	f.setupAdmin(t)
	const ip = "192.0.2.7"

	for i := range 5 {
		_, err := login(f, ip, "admin", "wrong-password-123")
		require.ErrorIs(t, err, ErrInvalidCredentials, "attempt %d", i+1)
		f.clock.Advance(5 * time.Second)
	}
	_, err := login(f, ip, "admin", "wrong-password-123")
	require.ErrorIs(t, err, ErrRateLimited)
	_, err = login(f, ip, "admin", adminPassword)
	require.ErrorIs(t, err, ErrRateLimited)

	_, err = login(f, "192.0.2.8", "admin", adminPassword)
	require.NoError(t, err)

	// The window opened at the first failure; 25 s have passed, so 35 s remain.
	f.clock.Advance(34 * time.Second)
	_, err = login(f, ip, "admin", adminPassword)
	require.ErrorIs(t, err, ErrRateLimited)

	f.clock.Advance(1 * time.Second)
	_, err = login(f, ip, "admin", adminPassword)
	require.NoError(t, err)
}

// AUTH-05: a parallel burst of wrong logins from one IP runs at most 5 password checks;
// every other attempt is rate-limited without verifying the password.
func TestLoginRateLimitConcurrentBurst(t *testing.T) {
	f := newFixture(t)
	f.setupAdmin(t)
	var checks atomic.Int32
	verify := f.svc.verifyPassword
	f.svc.verifyPassword = func(hash, password string) (bool, error) {
		checks.Add(1)
		return verify(hash, password)
	}
	var invalid, limited atomic.Int32
	var wg sync.WaitGroup
	for range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := login(f, "192.0.2.50", "admin", "wrong-password-123")
			switch {
			case errors.Is(err, ErrInvalidCredentials):
				invalid.Add(1)
			case errors.Is(err, ErrRateLimited):
				limited.Add(1)
			default:
				t.Errorf("unexpected result: %v", err)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int32(5), checks.Load(), "password verifications in a 30-request burst")
	require.Equal(t, int32(5), invalid.Load())
	require.Equal(t, int32(25), limited.Load())
}

// AUTH-05: a successful login does not reset the IP's failures. The verifier's probe: rounds
// of 4 wrong + 1 correct login from one IP within a minute. The 6th failure is refused with
// 429 and every later attempt, right password included, gets 429 until the minute ends.
func TestLoginSuccessKeepsFailures(t *testing.T) {
	f := newFixture(t)
	f.setupAdmin(t)
	const ip = "192.0.2.9"
	start := f.clock.Now()
	attempt := func(password string) error {
		_, err := login(f, ip, "admin", password)
		f.clock.Advance(2 * time.Second)
		return err
	}

	// Round 1: failures 1-4, then a success that must not clear them.
	for i := range 4 {
		require.ErrorIs(t, attempt("wrong-password-123"), ErrInvalidCredentials, "failure %d", i+1)
	}
	require.NoError(t, attempt(adminPassword))

	// Round 2: the 5th failure is still answered; the 6th is refused.
	require.ErrorIs(t, attempt("wrong-password-123"), ErrInvalidCredentials, "failure 5")
	require.ErrorIs(t, attempt("wrong-password-123"), ErrRateLimited, "failure 6")
	require.ErrorIs(t, attempt("wrong-password-123"), ErrRateLimited)
	require.ErrorIs(t, attempt("wrong-password-123"), ErrRateLimited)
	require.ErrorIs(t, attempt(adminPassword), ErrRateLimited, "right password after the limit")

	// Rounds 3 and 4: everything stays limited.
	for round := 3; round <= 4; round++ {
		for range 4 {
			require.ErrorIs(t, attempt("wrong-password-123"), ErrRateLimited, "round %d", round)
		}
		require.ErrorIs(t, attempt(adminPassword), ErrRateLimited, "round %d", round)
	}

	// The minute opened at the first failure: limited up to its last instant, open after it.
	f.clock.Advance(start.Add(time.Minute - time.Millisecond).Sub(f.clock.Now()))
	_, err := login(f, ip, "admin", adminPassword)
	require.ErrorIs(t, err, ErrRateLimited)
	f.clock.Advance(time.Millisecond)
	_, err = login(f, ip, "admin", adminPassword)
	require.NoError(t, err)
}

// AUTH-05 (DoS bound): the limiter never holds more keys than its cap. When full it drops
// expired windows; with none expired it refuses the new key and never evicts an active
// window, so flooding new addresses cannot flush an attacker's own failures.
func TestFailureLimiterIsBounded(t *testing.T) {
	l := newFailureLimiter(2, time.Minute)
	l.maxKeys = 3
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) time.Time { return now.Add(d) }

	require.True(t, l.Begin("a", at(0)))
	require.True(t, l.Begin("a", at(1*time.Second)), "a: 2 failures, at its limit")
	require.True(t, l.Begin("b", at(10*time.Second)))
	require.True(t, l.Begin("c", at(20*time.Second)))

	// Full with active windows only: new keys are refused, every active window stays.
	for i := range 20 {
		require.False(t, l.Begin(fmt.Sprintf("flood-%d", i), at(30*time.Second)), "new key %d while full", i)
	}
	require.Len(t, l.byKey, 3)
	require.False(t, l.Begin("a", at(31*time.Second)), "a keeps its count")
	require.True(t, l.Begin("b", at(31*time.Second)), "b keeps its window")

	// a's and b's windows have expired (they opened at 0 s and 10 s); c's is active.
	require.True(t, l.Begin("new", at(70*time.Second)))
	require.Len(t, l.byKey, 2)
	require.NotContains(t, l.byKey, "a")
	require.NotContains(t, l.byKey, "b")
	require.Contains(t, l.byKey, "c", "an active window is never evicted")
	require.Contains(t, l.byKey, "new")
}

// AUTH-05 (DoS bound): a full limiter prunes expired windows whenever a new key arrives, not
// only once per window, so new networks are refused only until some window expires.
func TestFullLimiterAcceptsNewKeysOnceAWindowExpires(t *testing.T) {
	l := newFailureLimiter(2, time.Minute)
	l.maxKeys = 3
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) time.Time { return now.Add(d) }

	require.True(t, l.Begin("a", at(0)))
	require.True(t, l.Begin("b", at(10*time.Second)))
	require.True(t, l.Begin("c", at(20*time.Second)))
	// a expired at 60 s: the full limiter makes room for d.
	require.True(t, l.Begin("d", at(61*time.Second)))
	require.Len(t, l.byKey, 3)
	// Full again (b, c, d). b expired at 70 s; e must fit at 75 s even though the last
	// prune ran only 14 s ago.
	require.True(t, l.Begin("e", at(75*time.Second)), "new key refused after a window expired")
	require.NotContains(t, l.byKey, "b")
	require.Contains(t, l.byKey, "c", "an active window is never evicted")
}

// AUTH-05 (DoS bound): the service's limiter allows 5 failures per minute and tracks at most
// 10,000 client networks.
func TestLoginLimiterDefaults(t *testing.T) {
	f := newFixture(t)
	require.Equal(t, 5, f.svc.limiter.max)
	require.Equal(t, time.Minute, f.svc.limiter.window)
	require.Equal(t, 10000, f.svc.limiter.maxKeys)
}

// AUTH-05: IPv6 clients are limited per /64 (one subscriber's prefix), IPv4 clients per address;
// an IPv4-mapped IPv6 address counts as its IPv4 address.
func TestLoginRateLimitPerNetwork(t *testing.T) {
	f := newFixture(t)
	f.setupAdmin(t)
	for i := range 5 {
		_, err := login(f, fmt.Sprintf("2001:db8:1:2::%x", i+1), "admin", "wrong-password-123")
		require.ErrorIs(t, err, ErrInvalidCredentials, "attempt %d", i+1)
	}
	_, err := login(f, "2001:db8:1:2:ffff:ffff:ffff:ffff", "admin", adminPassword)
	require.ErrorIs(t, err, ErrRateLimited, "same /64")
	_, err = login(f, "2001:db8:1:3::1", "admin", adminPassword)
	require.NoError(t, err, "another /64")

	for range 5 {
		_, err := login(f, "192.0.2.20", "admin", "wrong-password-123")
		require.ErrorIs(t, err, ErrInvalidCredentials)
	}
	_, err = login(f, "::ffff:192.0.2.20", "admin", adminPassword)
	require.ErrorIs(t, err, ErrRateLimited, "IPv4-mapped form of the same address")
	_, err = login(f, "192.0.2.21", "admin", adminPassword)
	require.NoError(t, err, "IPv4 addresses are limited one by one")
}

// AUTH-09: a revoked session no longer validates.
func TestRevokedSessionFailsValidation(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)
	sess, err := login(f, "192.0.2.1", "admin", adminPassword)
	require.NoError(t, err)

	p, err := f.svc.ValidateSession(ctx, sess.Secret)
	require.NoError(t, err)
	require.NoError(t, f.svc.RevokeSession(ctx, p.UserID, sess.ID))

	_, err = f.svc.ValidateSession(ctx, sess.Secret)
	require.ErrorIs(t, err, ErrUnauthenticated)
}

// AUTH-03: sessions expire after 30 days without use; use slides the expiry.
func TestSessionExpiryIsSliding(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)

	idle, err := login(f, "192.0.2.1", "admin", adminPassword)
	require.NoError(t, err)
	used, err := login(f, "192.0.2.1", "admin", adminPassword)
	require.NoError(t, err)

	f.clock.Advance(29 * 24 * time.Hour)
	_, err = f.svc.ValidateSession(ctx, used.Secret)
	require.NoError(t, err)

	f.clock.Advance(24*time.Hour + time.Millisecond)
	_, err = f.svc.ValidateSession(ctx, idle.Secret)
	require.ErrorIs(t, err, ErrUnauthenticated)
	_, err = f.svc.ValidateSession(ctx, used.Secret)
	require.NoError(t, err)
}

func TestUnknownSessionFailsValidation(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.ValidateSession(context.Background(), "does-not-exist")
	require.ErrorIs(t, err, ErrUnauthenticated)
}
