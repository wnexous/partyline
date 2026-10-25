package webpush

import (
	"sync"
	"time"
)

// limiter allows at most limit events per key within a sliding window. It is copied from the
// agents' post limiter (internal/agents/limit.go): in memory, mutex-guarded, and reset by a
// restart, which a rate limit tolerates.
type limiter struct {
	limit  int
	window time.Duration
	now    func() time.Time

	mu     sync.Mutex
	events map[string][]time.Time
}

// newLimiter returns a limiter that keeps at most limit events per key in the last window.
func newLimiter(limit int, window time.Duration, now func() time.Time) *limiter {
	return &limiter{limit: limit, window: window, now: now, events: map[string][]time.Time{}}
}

// allow records an event for key unless limit events already happened within the window.
func (l *limiter) allow(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.events[key][:0]
	for _, t := range l.events[key] {
		if now.Sub(t) < l.window {
			recent = append(recent, t)
		}
	}
	if len(recent) >= l.limit {
		l.events[key] = recent
		return false
	}
	l.events[key] = append(recent, now)
	return true
}
