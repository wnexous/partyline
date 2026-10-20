package identities_test

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/identities"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

var t0 = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type fixture struct {
	st    *store.Store
	bus   *events.Bus
	clock *clock
	svc   *identities.Service
	alice string
	bob   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "partyline.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(context.Background()))
	f := &fixture{st: st, bus: events.NewBus(), clock: &clock{now: t0}, alice: store.NewID(), bob: store.NewID()}
	require.NoError(t, st.WithTx(context.Background(), func(q *db.Queries) error {
		for name, id := range map[string]string{"alice": f.alice, "bob": f.bob} {
			if err := q.CreateUser(context.Background(), db.CreateUserParams{ID: id, Username: name, PasswordHash: "x", Role: "member"}); err != nil {
				return err
			}
		}
		return nil
	}))
	f.svc = identities.New(identities.Deps{Store: st, Bus: f.bus, Now: f.clock.Now})
	return f
}

var codePattern = regexp.MustCompile(`^[A-HJ-NP-Z2-9]{6}$`)

// IDN-01: a 6-character code from A-Z (no I, O) and 2-9, valid for 10 minutes.
func TestCreatePairingReturnsShortLivedCode(t *testing.T) {
	f := newFixture(t)
	seen := map[string]bool{}
	for range 50 {
		code, expires, err := f.svc.CreatePairing(context.Background(), f.alice)
		require.NoError(t, err)
		require.Regexp(t, codePattern, code)
		require.Equal(t, t0.Add(10*time.Minute), expires)
		seen[code] = true
	}
	require.Greater(t, len(seen), 40, "codes are random")
}

// IDN-02: a valid code links (kind, sender id, alt id) to the code's user and is consumed.
func TestLinkConsumesCodeAndLinksSender(t *testing.T) {
	f := newFixture(t)
	ch, cancel := f.bus.Subscribe(16)
	defer cancel()
	code, _, err := f.svc.CreatePairing(context.Background(), f.alice)
	require.NoError(t, err)

	user, ok, err := f.svc.Link(context.Background(), code, "whatsapp", "5511999990000@s.whatsapp.net", "100000000000001@lid", "Alice")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, f.alice, user)

	list, err := f.svc.List(context.Background(), f.alice)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "whatsapp", list[0].ConnectorKind)
	require.Equal(t, "5511999990000@s.whatsapp.net", list[0].ExternalID)
	require.Equal(t, "100000000000001@lid", list[0].AltExternalID)
	require.Equal(t, "Alice", list[0].Display)
	require.Equal(t, events.IdentityLinked{UserID: f.alice, IdentityID: list[0].ID}, <-eventsOf(ch))

	_, ok, err = f.svc.Link(context.Background(), code, "whatsapp", "5511999990001@s.whatsapp.net", "", "")
	require.NoError(t, err)
	require.False(t, ok, "single use")
}

func eventsOf(ch <-chan events.Event) <-chan events.IdentityLinked {
	out := make(chan events.IdentityLinked, 1)
	go func() {
		for e := range ch {
			if l, ok := e.(events.IdentityLinked); ok {
				out <- l
				return
			}
		}
	}()
	return out
}

// IDN-02: codes are matched case-insensitively.
func TestLinkAcceptsLowercaseCode(t *testing.T) {
	f := newFixture(t)
	code, _, err := f.svc.CreatePairing(context.Background(), f.alice)
	require.NoError(t, err)
	_, ok, err := f.svc.Link(context.Background(), strings.ToLower(code), "telegram", "42", "", "")
	require.NoError(t, err)
	require.True(t, ok)
}

// IDN-03: unknown and expired codes link nothing.
func TestLinkIgnoresUnknownAndExpiredCodes(t *testing.T) {
	f := newFixture(t)
	_, ok, err := f.svc.Link(context.Background(), "ABCDEF", "telegram", "42", "", "")
	require.NoError(t, err)
	require.False(t, ok)

	code, _, err := f.svc.CreatePairing(context.Background(), f.alice)
	require.NoError(t, err)
	f.clock.Advance(10*time.Minute + time.Second)
	_, ok, err = f.svc.Link(context.Background(), code, "telegram", "42", "", "")
	require.NoError(t, err)
	require.False(t, ok, "expired after 10 minutes")

	resolved, err := f.svc.ResolveSender(context.Background(), "telegram", "42", "")
	require.NoError(t, err)
	require.Empty(t, resolved)
}

// A code used within its 10 minutes still links.
func TestLinkAcceptsCodeBeforeExpiry(t *testing.T) {
	f := newFixture(t)
	code, _, err := f.svc.CreatePairing(context.Background(), f.alice)
	require.NoError(t, err)
	f.clock.Advance(9*time.Minute + 59*time.Second)
	_, ok, err := f.svc.Link(context.Background(), code, "telegram", "42", "", "")
	require.NoError(t, err)
	require.True(t, ok)
}

// ResolveSender matches the sender id or the alternate id, within the connector kind;
// IDN-04: after Delete the sender resolves to no user.
func TestResolveSenderAndDelete(t *testing.T) {
	f := newFixture(t)
	code, _, err := f.svc.CreatePairing(context.Background(), f.alice)
	require.NoError(t, err)
	_, ok, err := f.svc.Link(context.Background(), code, "whatsapp", "5511999990000@s.whatsapp.net", "100000000000001@lid", "")
	require.NoError(t, err)
	require.True(t, ok)

	for _, tc := range []struct{ kind, sender, alt, want string }{
		{"whatsapp", "5511999990000@s.whatsapp.net", "", f.alice},
		{"whatsapp", "100000000000001@lid", "", f.alice},
		{"whatsapp", "other@lid", "5511999990000@s.whatsapp.net", f.alice},
		{"whatsapp", "other@lid", "100000000000001@lid", f.alice},
		{"telegram", "5511999990000@s.whatsapp.net", "", ""},
		{"whatsapp", "5511999990002@s.whatsapp.net", "", ""},
		{"whatsapp", "", "", ""},
	} {
		got, err := f.svc.ResolveSender(context.Background(), tc.kind, tc.sender, tc.alt)
		require.NoError(t, err)
		require.Equal(t, tc.want, got, "%+v", tc)
	}

	list, err := f.svc.List(context.Background(), f.alice)
	require.NoError(t, err)
	require.ErrorIs(t, f.svc.Delete(context.Background(), f.bob, list[0].ID), access.ErrNotFound, "only the owner deletes")
	require.NoError(t, f.svc.Delete(context.Background(), f.alice, list[0].ID))
	got, err := f.svc.ResolveSender(context.Background(), "whatsapp", "5511999990000@s.whatsapp.net", "")
	require.NoError(t, err)
	require.Empty(t, got)
	require.ErrorIs(t, f.svc.Delete(context.Background(), f.alice, list[0].ID), access.ErrNotFound)
}

// Linking an account again with another user's code moves it to that user.
func TestLinkMovesAccountToNewUser(t *testing.T) {
	f := newFixture(t)
	for _, user := range []string{f.alice, f.bob} {
		code, _, err := f.svc.CreatePairing(context.Background(), user)
		require.NoError(t, err)
		_, ok, err := f.svc.Link(context.Background(), code, "telegram", "42", "", "")
		require.NoError(t, err)
		require.True(t, ok)
	}
	got, err := f.svc.ResolveSender(context.Background(), "telegram", "42", "")
	require.NoError(t, err)
	require.Equal(t, f.bob, got)
	aliceList, err := f.svc.List(context.Background(), f.alice)
	require.NoError(t, err)
	require.Empty(t, aliceList)
}
