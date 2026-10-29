package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// SESS-50: the calls of a tools-only process (a tool call, a session read, a hook) stamp its AI's
// last_seen_at, at most once a minute, while the AI stays offline: `partyline agents` can then say "Nm ago"
// for an AI used only through a tools call. A call never makes the AI online.
func TestToolsOnlyCallsStampLastSeenWithoutGoingOnline(t *testing.T) {
	l := newLiveEnv(t)
	tools := l.tok.AsFolder(acme)
	seen := func() (online bool, at *time.Time) {
		t.Helper()
		for _, a := range ownedAIs(t, l.alice) {
			return a.Online, a.LastSeenAt
		}
		t.Fatal("no AI")
		return
	}

	tools.Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK)
	online, at := seen()
	require.False(t, online)
	require.NotNil(t, at, "the first call stamps the time")
	require.True(t, l.start.Equal(*at))

	l.clock.Advance(30 * time.Second)
	tools.Get("/api/v1/agent/rooms").RequireStatus(http.StatusOK)
	_, at = seen()
	require.True(t, l.start.Equal(*at), "a second call within the minute writes nothing")

	l.clock.Advance(90 * time.Second)
	tools.Get("/api/v1/agent/rooms").RequireStatus(http.StatusOK)
	online, at = seen()
	require.False(t, online, "a tools-only call is no presence")
	require.True(t, l.start.Add(2*time.Minute).Equal(*at))
	require.False(t, l.env.App.Presence.Online(ownedAIs(t, l.alice)[0].ID))
}

// SESS-50: an AI with a live session is not stamped by the calls of its tools: its socket stamps it when
// it connects and when it ends.
func TestCallsOfALiveAIDoNotStampLastSeen(t *testing.T) {
	l := newLiveEnv(t)
	_, id := l.dial(t, acme)
	before := ownedAIs(t, l.alice)[0].LastSeenAt
	l.clock.Advance(5 * time.Minute)
	l.tok.AsFolder(acme).Get("/api/v1/agent/session").RequireStatus(http.StatusOK)
	l.tok.AsFolder(acme).Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK)
	after := ownedAIs(t, l.alice)[0]
	require.Equal(t, id, after.ID)
	require.True(t, after.Online)
	require.Equal(t, before, after.LastSeenAt)
}
