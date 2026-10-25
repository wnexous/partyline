package webpush

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// PUSH-13: at most limit events per key in a rolling window; the window slides, and another key
// has its own.
func TestLimiterSlidingWindow(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	l := newLimiter(10, time.Minute, func() time.Time { return now })

	for i := range 10 {
		require.True(t, l.allow("user-1"), "event %d of 10 is allowed", i+1)
	}
	require.False(t, l.allow("user-1"), "the eleventh event in the minute is dropped")
	require.True(t, l.allow("user-2"), "another key has its own window")

	now = now.Add(time.Minute)
	require.True(t, l.allow("user-1"), "a minute later the window is clear")
}
