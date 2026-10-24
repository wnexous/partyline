package whatsapp

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
)

// What the status of a connection says while evolution-go reports its number as not connected.
const notConnected = "the WhatsApp number is not connected; reconnect it in evolution-go"

// checkOnDemand makes the connector look at its instance when a test says so, not every minute:
// each send on the channel is one check, and one is accepted only once the check before it is over.
func checkOnDemand(c *Connector) chan<- struct{} {
	checks := make(chan struct{})
	c.wait = func(ctx context.Context, _ time.Duration) error {
		select {
		case <-checks:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return checks
}

// check makes the connector look at its instance once.
func check(t *testing.T, checks chan<- struct{}) {
	t.Helper()
	select {
	case checks <- struct{}{}:
	case <-time.After(wait):
		t.Fatal("the connector is not waiting to check its instance")
	}
}

// ended waits for Run to return on its own and returns what it returned.
func ended(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(wait):
		t.Fatal("Run did not end")
		return nil
	}
}

// WA-06: the status of a connection is the state of its number. While evolution-go says the
// instance is not connected (reconnecting, closed, logged out) the connection is in error with
// what to do about it, however well its API and WebSocket work; once the number is back it is
// connected again. A state that did not change is not reported again, and the other instances of
// the server do not count.
func TestRunFollowsTheNumberOfItsInstance(t *testing.T) {
	f := newFakeEvolution(t)
	c := newConnector(t, f)
	checks := checkOnDemand(c)
	rec := connectortest.NewRecorder()
	start(t, c, f, rec)
	require.Empty(t, rec.NextStatusDetail(t), "open")

	f.setConnected("acme", false) // reconnecting
	check(t, checks)
	require.Equal(t, connectors.StatusError, rec.NextStatus(t))
	require.Equal(t, notConnected, rec.NextStatusDetail(t))

	check(t, checks) // still reconnecting: nothing new to say
	check(t, checks)
	require.True(t, rec.NoStatus(), "an unchanged state is not reported again")

	f.setConnected("acme", true) // open again
	check(t, checks)
	require.Equal(t, connectors.StatusConnected, rec.NextStatus(t))
	require.Empty(t, rec.NextStatusDetail(t))

	f.setConnected("other", false) // another number of the same server
	check(t, checks)
	check(t, checks)
	require.True(t, rec.NoStatus(), "only the connection's own number counts")
}

// WA-06: with the production pause the connector looks at its instance every 60 seconds and not in
// a loop: it waits the minute the spec names, and no second check follows the one at start.
func TestRunChecksEveryMinuteByDefault(t *testing.T) {
	f := newFakeEvolution(t)
	c := newConnector(t, f)
	waits := make(chan time.Duration, 4)
	production := c.wait
	c.wait = func(ctx context.Context, d time.Duration) error {
		waits <- d
		return production(ctx, d)
	}
	rec := connectortest.NewRecorder()
	start(t, c, f, rec)

	select {
	case d := <-waits:
		require.Equal(t, 60*time.Second, d)
	case <-time.After(wait):
		t.Fatal("the connector never waited to check its instance")
	}
	select {
	case <-waits:
		t.Fatal("the connector checked its instance again without waiting")
	case <-time.After(100 * time.Millisecond):
	}
	require.Equal(t, 1, f.count("/instance/all"), "only the check at start")
}

// WA-06: a connection whose number is not connected when it starts says so at once and keeps its
// socket: what the number receives is delivered whatever the flag says, and the connection is
// connected again as soon as a check sees the number open, without a new dial.
func TestRunStartsInErrorWhileTheNumberIsNotConnected(t *testing.T) {
	f := newFakeEvolution(t)
	f.setConnected("acme", false)
	c := newConnector(t, f)
	checks := checkOnDemand(c)
	rec := connectortest.NewRecorder()
	sock := begin(t, c, f, rec)
	require.Equal(t, connectors.StatusError, rec.NextStatus(t))
	require.Equal(t, notConnected, rec.NextStatusDetail(t))

	push(t, sock, frame(t, testInstance, groupInfo("DURING"), `{"conversation":"hello"}`))
	require.Equal(t, "DURING", rec.NextInbound(t).MessageID, "the flag never keeps a message out")

	f.setConnected("acme", true)
	check(t, checks)
	require.Equal(t, connectors.StatusConnected, rec.NextStatus(t))
	require.Empty(t, rec.NextStatusDetail(t))
	require.Equal(t, 1, f.socketDials())
}

// WA-06: the number is checked again whenever the WebSocket reconnects: Run starts over, as the
// supervisor restarts it, and its first status is the state of the number then.
func TestRunChecksTheNumberAgainWhenTheSocketReconnects(t *testing.T) {
	f := newFakeEvolution(t)
	c := newConnector(t, f)
	rec := connectortest.NewRecorder()
	done, cancel := launch(c, rec)
	t.Cleanup(cancel)
	sock := f.socket()
	require.Equal(t, connectors.StatusConnected, rec.NextStatus(t))
	require.Empty(t, rec.NextStatusDetail(t))

	f.setConnected("acme", false)
	require.NoError(t, sock.CloseNow()) // evolution-go restarted
	err := ended(t, done)
	require.Error(t, err)
	require.NotErrorIs(t, err, context.Canceled)

	begin(t, c, f, rec)
	require.Equal(t, connectors.StatusError, rec.NextStatus(t))
	require.Equal(t, notConnected, rec.NextStatusDetail(t))
}

// WA-06, CON-06: when the instance list cannot be read, Run ends with that error, at start and
// while it runs. The supervisor shows the error and starts again after a backoff, so a connection
// is not left connected on the word of a check that no longer works (a key evolution-go rejects
// while its socket is still up, a server that fails).
func TestRunEndsWhenTheInstanceListFails(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		err    string
	}{
		{"server error", http.StatusInternalServerError, "GET /instance/all: status 500"},
		{"key rejected", http.StatusUnauthorized, "unauthorized"},
	} {
		t.Run(tc.name+" at start", func(t *testing.T) {
			f := newFakeEvolution(t)
			f.setListError(tc.status)
			rec := connectortest.NewRecorder()
			done, cancel := launch(newConnector(t, f), rec)
			t.Cleanup(cancel)
			require.EqualError(t, ended(t, done), tc.err)
			require.Zero(t, f.socketDials(), "no WebSocket dial")
			require.True(t, rec.NoStatus(), "the supervisor reports the failure, not Run")
		})

		t.Run(tc.name+" while running", func(t *testing.T) {
			f := newFakeEvolution(t)
			c := newConnector(t, f)
			checks := checkOnDemand(c)
			rec := connectortest.NewRecorder()
			done, cancel := launch(c, rec)
			t.Cleanup(cancel)
			f.socket()
			require.Equal(t, connectors.StatusConnected, rec.NextStatus(t))

			f.setListError(tc.status)
			check(t, checks)
			require.EqualError(t, ended(t, done), tc.err)
			require.True(t, rec.NoStatus(), "the supervisor reports the failure, not Run")
		})
	}
}

// The warning for an instance evolution-go does not have follows the list too: it shows as soon as
// a check misses the instance, and clears when it is back.
func TestRunWarnsWhenTheInstanceGoesAwayAndComesBack(t *testing.T) {
	f := newFakeEvolution(t)
	c := newConnector(t, f)
	checks := checkOnDemand(c)
	rec := connectortest.NewRecorder()
	start(t, c, f, rec)
	require.Empty(t, rec.NextStatusDetail(t))

	f.mu.Lock()
	all := f.instances
	f.instances = all[1:] // acme is the first
	f.mu.Unlock()
	check(t, checks)
	require.Equal(t, connectors.StatusConnected, rec.NextStatus(t), "an instance that is gone is no number to report")
	require.Contains(t, rec.NextStatusDetail(t), "no instance named acme")

	f.mu.Lock()
	f.instances = all
	f.mu.Unlock()
	check(t, checks)
	require.Equal(t, connectors.StatusConnected, rec.NextStatus(t))
	require.Empty(t, rec.NextStatusDetail(t))
}

// An evolution-go that does not say whether the instance is connected leaves the connection as it
// is: nothing is known, so nothing is claimed.
func TestRunTrustsAnInstanceThatDoesNotSayIfItIsConnected(t *testing.T) {
	for name, inst := range map[string]map[string]any{
		"absent": {"name": "acme", "token": acmeToken, "websocketEnable": true},
		"null":   {"name": "acme", "token": acmeToken, "websocketEnable": true, "connected": nil},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeEvolution(t)
			f.mu.Lock()
			f.instances = []map[string]any{inst}
			f.mu.Unlock()
			c := newConnector(t, f)
			checks := checkOnDemand(c)
			rec := connectortest.NewRecorder()
			start(t, c, f, rec)
			require.Empty(t, rec.NextStatusDetail(t))

			check(t, checks)
			check(t, checks)
			require.True(t, rec.NoStatus())
		})
	}
}

// WA-06: a change of the number's state is logged with the instance and the status, and nothing
// else: no message text and no secret.
func TestRunLogsTheStateOfTheNumberWithoutContent(t *testing.T) {
	f := newFakeEvolution(t)
	var logs strings.Builder
	c, err := New(connectors.Config{"base_url": f.srv.URL, "api_key": globalKey, "instances": []string{testInstance}},
		connectors.Deps{AllowPrivateNetworks: true, UserAgent: userAgent, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	require.NoError(t, err)
	checks := checkOnDemand(c)
	rec := connectortest.NewRecorder()
	sock := start(t, c, f, rec)
	require.Empty(t, rec.NextStatusDetail(t))
	push(t, sock, frame(t, testInstance, groupInfo("LOG1"), `{"conversation":"PRIVATE-TEXT"}`))
	require.Equal(t, "LOG1", rec.NextInbound(t).MessageID)

	f.setConnected("acme", false)
	check(t, checks)
	require.Equal(t, connectors.StatusError, rec.NextStatus(t))
	require.Contains(t, logs.String(), "instance=acme")
	require.Contains(t, logs.String(), "status=error")
	for _, secret := range []string{"PRIVATE-TEXT", globalKey, acmeToken} {
		require.NotContains(t, logs.String(), secret)
	}
}
