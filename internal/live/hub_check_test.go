package live_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/live"
	"github.com/wnexous/partyline/internal/testkit"
)

// failingChecker is a session store that cannot answer, like a database error.
type failingChecker struct{ calls atomic.Int32 }

func (c *failingChecker) PrincipalActive(context.Context, authn.Principal) (bool, error) {
	c.calls.Add(1)
	return false, errors.New("database is unavailable")
}

// The live session re-check fails closed (AUTH-09/AUTH-10): when the check itself fails, the
// socket is closed with 1013 (try again later), so the client reconnects and authenticates
// again instead of staying live on an unverified session.
func TestLiveClosesSocketWhenSessionCheckFails(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")

	srv := httptest.NewUnstartedServer(nil)
	public := &url.URL{Scheme: "http", Host: srv.Listener.Addr().String()}
	checker := &failingChecker{}
	hub := live.New(live.Deps{
		Bus: env.App.Bus, Access: env.App.Access, Rooms: env.App.Rooms, Store: env.App.Store,
		PublicURL: public, Sessions: checker, SessionCheckInterval: 50 * time.Millisecond,
	})
	p := authn.Principal{Kind: authn.KindUser, UserID: alice.User.ID, Role: alice.User.Role, SessionID: "session-1"}
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.ServeHTTP(w, r.WithContext(authn.WithPrincipal(r.Context(), p)))
	})
	srv.Start()
	t.Cleanup(func() {
		hub.Close()
		srv.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"),
		&websocket.DialOptions{HTTPHeader: http.Header{"Origin": {public.Scheme + "://" + public.Host}}})
	require.NoError(t, err)
	defer func() { _ = conn.CloseNow() }()

	// The hello frame arrives, then the first failing re-check ends the connection.
	_, data, err := conn.Read(ctx)
	require.NoError(t, err)
	require.Contains(t, string(data), `"hello"`)
	_, _, err = conn.Read(ctx)
	require.Error(t, err)
	require.Equal(t, websocket.StatusTryAgainLater, websocket.CloseStatus(err), "close: %v", err)
	require.GreaterOrEqual(t, checker.calls.Load(), int32(1))
}
