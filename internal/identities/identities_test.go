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
	// wa and wa2 are two WhatsApp connectors (alice's and bob's), tg a Telegram one.
	wa  string
	wa2 string
	tg  string
}

// acct is an account seen by connector.
func acct(connector, kind, sender, alt, display string) identities.Account {
	return identities.Account{ConnectorID: connector, ConnectorKind: kind, SenderID: sender, AltID: alt, Display: display}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "partyline.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(context.Background()))
	f := &fixture{st: st, bus: events.NewBus(), clock: &clock{now: t0}, alice: store.NewID(), bob: store.NewID(),
		wa: store.NewID(), wa2: store.NewID(), tg: store.NewID()}
	require.NoError(t, st.WithTx(context.Background(), func(q *db.Queries) error {
		for name, id := range map[string]string{"alice": f.alice, "bob": f.bob} {
			if err := q.CreateUser(context.Background(), db.CreateUserParams{ID: id, Username: name, PasswordHash: "x", Role: "member"}); err != nil {
				return err
			}
		}
		for _, c := range []struct{ id, owner, kind string }{{f.wa, f.alice, "whatsapp"}, {f.wa2, f.bob, "whatsapp"}, {f.tg, f.alice, "telegram"}} {
			if err := q.CreateConnector(context.Background(), db.CreateConnectorParams{ID: c.id, OwnerID: c.owner, Kind: c.kind, Name: c.kind, ConfigJson: "{}"}); err != nil {
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
		code, expires, err := f.svc.CreatePairing(context.Background(), f.alice, f.wa)
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
	code, _, err := f.svc.CreatePairing(context.Background(), f.alice, f.wa)
	require.NoError(t, err)

	user, ok, err := f.svc.Link(context.Background(), code, acct(f.wa, "whatsapp", "5511999990000@s.whatsapp.net", "100000000000001@lid", "Alice"), "")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, f.alice, user)

	list, err := f.svc.List(context.Background(), f.alice)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, f.wa, list[0].ConnectorID)
	require.Equal(t, "whatsapp", list[0].ConnectorKind)
	require.Equal(t, "5511999990000@s.whatsapp.net", list[0].ExternalID)
	require.Equal(t, "100000000000001@lid", list[0].AltExternalID)
	require.Equal(t, "Alice", list[0].Display)
	require.Equal(t, events.IdentityLinked{UserID: f.alice, IdentityID: list[0].ID}, <-eventsOf(ch))

	_, ok, err = f.svc.Link(context.Background(), code, acct(f.wa, "whatsapp", "5511999990001@s.whatsapp.net", "", ""), "")
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
	code, _, err := f.svc.CreatePairing(context.Background(), f.alice, f.tg)
	require.NoError(t, err)
	_, ok, err := f.svc.Link(context.Background(), strings.ToLower(code), acct(f.tg, "telegram", "42", "", ""), "")
	require.NoError(t, err)
	require.True(t, ok)
}

// IDN-03: unknown and expired codes link nothing.
func TestLinkIgnoresUnknownAndExpiredCodes(t *testing.T) {
	f := newFixture(t)
	_, ok, err := f.svc.Link(context.Background(), "ABCDEF", acct(f.tg, "telegram", "42", "", ""), "")
	require.NoError(t, err)
	require.False(t, ok)

	code, _, err := f.svc.CreatePairing(context.Background(), f.alice, f.tg)
	require.NoError(t, err)
	f.clock.Advance(10*time.Minute + time.Second)
	_, ok, err = f.svc.Link(context.Background(), code, acct(f.tg, "telegram", "42", "", ""), "")
	require.NoError(t, err)
	require.False(t, ok, "expired after 10 minutes")

	resolved, err := f.svc.ResolveSender(context.Background(), f.tg, "42", "")
	require.NoError(t, err)
	require.Empty(t, resolved)
}

// A code used within its 10 minutes still links.
func TestLinkAcceptsCodeBeforeExpiry(t *testing.T) {
	f := newFixture(t)
	code, _, err := f.svc.CreatePairing(context.Background(), f.alice, f.tg)
	require.NoError(t, err)
	f.clock.Advance(9*time.Minute + 59*time.Second)
	_, ok, err := f.svc.Link(context.Background(), code, acct(f.tg, "telegram", "42", "", ""), "")
	require.NoError(t, err)
	require.True(t, ok)
}

// ResolveSender matches the sender id or the alternate id, only on the connector the
// identity was linked through (IDN-02: another connector of the same kind never matches);
// IDN-04: after Delete the sender resolves to no user.
func TestResolveSenderAndDelete(t *testing.T) {
	f := newFixture(t)
	code, _, err := f.svc.CreatePairing(context.Background(), f.alice, f.wa)
	require.NoError(t, err)
	_, ok, err := f.svc.Link(context.Background(), code, acct(f.wa, "whatsapp", "5511999990000@s.whatsapp.net", "100000000000001@lid", ""), "")
	require.NoError(t, err)
	require.True(t, ok)

	for _, tc := range []struct{ connector, sender, alt, want string }{
		{f.wa, "5511999990000@s.whatsapp.net", "", f.alice},
		{f.wa, "100000000000001@lid", "", f.alice},
		{f.wa, "other@lid", "5511999990000@s.whatsapp.net", f.alice},
		{f.wa, "other@lid", "100000000000001@lid", f.alice},
		{f.wa2, "5511999990000@s.whatsapp.net", "100000000000001@lid", ""},
		{f.tg, "5511999990000@s.whatsapp.net", "", ""},
		{f.wa, "5511999990002@s.whatsapp.net", "", ""},
		{f.wa, "", "", ""},
		{"", "5511999990000@s.whatsapp.net", "", ""},
	} {
		got, err := f.svc.ResolveSender(context.Background(), tc.connector, tc.sender, tc.alt)
		require.NoError(t, err)
		require.Equal(t, tc.want, got, "%+v", tc)
	}

	list, err := f.svc.List(context.Background(), f.alice)
	require.NoError(t, err)
	require.ErrorIs(t, f.svc.Delete(context.Background(), f.bob, list[0].ID), access.ErrNotFound, "only the owner deletes")
	require.NoError(t, f.svc.Delete(context.Background(), f.alice, list[0].ID))
	got, err := f.svc.ResolveSender(context.Background(), f.wa, "5511999990000@s.whatsapp.net", "")
	require.NoError(t, err)
	require.Empty(t, got)
	require.ErrorIs(t, f.svc.Delete(context.Background(), f.alice, list[0].ID), access.ErrNotFound)
}

// Linking an account again with another user's code moves it to that user.
func TestLinkMovesAccountToNewUser(t *testing.T) {
	f := newFixture(t)
	for _, user := range []string{f.alice, f.bob} {
		code, _, err := f.svc.CreatePairing(context.Background(), user, f.tg)
		require.NoError(t, err)
		_, ok, err := f.svc.Link(context.Background(), code, acct(f.tg, "telegram", "42", "", ""), "")
		require.NoError(t, err)
		require.True(t, ok)
	}
	got, err := f.svc.ResolveSender(context.Background(), f.tg, "42", "")
	require.NoError(t, err)
	require.Equal(t, f.bob, got)
	aliceList, err := f.svc.List(context.Background(), f.alice)
	require.NoError(t, err)
	require.Empty(t, aliceList)
}

// IDN-02: the same account linked through two connectors is two identities, each trusted
// only on its own connector.
func TestLinkIsPerConnector(t *testing.T) {
	f := newFixture(t)
	for _, c := range []struct{ connector, user string }{{f.wa, f.alice}, {f.wa2, f.bob}} {
		code, _, err := f.svc.CreatePairing(context.Background(), c.user, c.connector)
		require.NoError(t, err)
		_, ok, err := f.svc.Link(context.Background(), code, acct(c.connector, "whatsapp", "5511999990000@s.whatsapp.net", "", ""), "")
		require.NoError(t, err)
		require.True(t, ok)
	}
	for connector, want := range map[string]string{f.wa: f.alice, f.wa2: f.bob} {
		got, err := f.svc.ResolveSender(context.Background(), connector, "5511999990000@s.whatsapp.net", "")
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	list, err := f.svc.List(context.Background(), f.alice)
	require.NoError(t, err)
	require.Len(t, list, 1, "the link on bob's connector did not move alice's")
}

// IDN-05: with a required user, a code of anyone else links nothing and stays usable. The
// code is issued for that same connector, so only the owner check refuses it.
func TestLinkOnlyForRequiredUser(t *testing.T) {
	f := newFixture(t)
	code, _, err := f.svc.CreatePairing(context.Background(), f.alice, f.wa2)
	require.NoError(t, err)
	_, ok, err := f.svc.Link(context.Background(), code, acct(f.wa2, "whatsapp", "alice-id", "", ""), f.bob)
	require.NoError(t, err)
	require.False(t, ok, "alice's code on bob's owner-only connector")
	got, err := f.svc.ResolveSender(context.Background(), f.wa2, "alice-id", "")
	require.NoError(t, err)
	require.Empty(t, got)

	user, ok, err := f.svc.Link(context.Background(), code, acct(f.wa2, "whatsapp", "alice-id", "", ""), "")
	require.NoError(t, err)
	require.True(t, ok, "the refused attempt did not consume the code")
	require.Equal(t, f.alice, user)
}

// IDN-02 (S5): a code links only through the connector it was issued for; through any
// other connector, even of the same kind, it links nothing and is not consumed.
func TestPairingCodeIsBoundToItsConnector(t *testing.T) {
	f := newFixture(t)
	code, _, err := f.svc.CreatePairing(context.Background(), f.alice, f.wa)
	require.NoError(t, err)
	for _, other := range []string{f.wa2, f.tg} {
		_, ok, err := f.svc.Link(context.Background(), code, acct(other, "whatsapp", "alice-id", "", ""), "")
		require.NoError(t, err)
		require.False(t, ok, "code of another connector")
		got, err := f.svc.ResolveSender(context.Background(), other, "alice-id", "")
		require.NoError(t, err)
		require.Empty(t, got)
	}
	list, err := f.svc.List(context.Background(), f.alice)
	require.NoError(t, err)
	require.Empty(t, list)

	user, ok, err := f.svc.Link(context.Background(), code, acct(f.wa, "whatsapp", "alice-id", "", ""), "")
	require.NoError(t, err)
	require.True(t, ok, "still usable on its own connector")
	require.Equal(t, f.alice, user)
}

// Deleting a connector removes the identities linked through it.
func TestIdentitiesFollowTheirConnector(t *testing.T) {
	f := newFixture(t)
	code, _, err := f.svc.CreatePairing(context.Background(), f.alice, f.tg)
	require.NoError(t, err)
	_, ok, err := f.svc.Link(context.Background(), code, acct(f.tg, "telegram", "42", "", ""), "")
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, f.st.WithTx(context.Background(), func(q *db.Queries) error {
		return q.DeleteConnector(context.Background(), f.tg)
	}))
	list, err := f.svc.List(context.Background(), f.alice)
	require.NoError(t, err)
	require.Empty(t, list)
}
