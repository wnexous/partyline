package mcp

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The production readiness waits, read before TestMain shortens them for the other tests.
var defaultReadyDelay, defaultReadyFallback = ReadyDelay, ReadyFallback

func TestMain(m *testing.M) {
	// The protocol tests expect notifications right after notifications/initialized.
	ReadyDelay, ReadyFallback = 0, 0
	os.Exit(m.Run())
}

// withReadiness makes the next servers use delay and fallback.
func withReadiness(t *testing.T, delay, fallback time.Duration) {
	t.Helper()
	ReadyDelay, ReadyFallback = delay, fallback
	t.Cleanup(func() { ReadyDelay, ReadyFallback = 0, 0 })
}

// UAT finding (PROJ-08 with Claude Code 2.1.304): notifications wait until the client is set
// up: ReadyDelay after the answer to its first tools/list, with the production defaults
// 2 s after tools/list and 5 s without one.
func TestNotificationWaitsUntilAfterToolsList(t *testing.T) {
	require.Equal(t, 2*time.Second, defaultReadyDelay)
	require.Equal(t, 5*time.Second, defaultReadyFallback)
	withReadiness(t, 400*time.Millisecond, time.Minute)
	h := newHarness(t)
	notified := make(chan error, 1)
	go func() { notified <- h.srv.Notify("notifications/claude/channel", map[string]any{"content": "backlog"}) }()

	h.initialize("2025-11-25")
	h.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	h.expectNone(300 * time.Millisecond) // initialized alone is not enough

	h.send(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	listed := time.Now()
	require.Contains(t, h.next()["result"], "tools")
	m := h.next()
	require.Equal(t, "notifications/claude/channel", m["method"])
	require.GreaterOrEqual(t, time.Since(listed), 400*time.Millisecond, "sent before ReadyDelay passed")
	require.NoError(t, <-notified)
}

// A client that never lists the tools gets notifications ReadyFallback after
// notifications/initialized.
func TestNotificationFallbackWithoutToolsList(t *testing.T) {
	withReadiness(t, time.Minute, 400*time.Millisecond)
	h := newHarness(t)
	notified := make(chan error, 1)
	go func() { notified <- h.srv.Notify("notifications/claude/channel", map[string]any{"content": "hi"}) }()
	h.initialize("2025-11-25")
	h.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	initialized := time.Now()
	require.Equal(t, "notifications/claude/channel", h.next()["method"])
	require.GreaterOrEqual(t, time.Since(initialized), 400*time.Millisecond)
	require.NoError(t, <-notified)
}
