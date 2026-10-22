package agents

import (
	"sync"
	"time"
)

// Agent post rate limit (TOOL-08).
const (
	PostLimit  = 30
	PostWindow = time.Minute
)

// Agent conversation creation rate limit: an AI creates at most CreateLimit new DMs and groups
// per CreateWindow (DM-04, AGRP-01).
const (
	CreateLimit  = 10
	CreateWindow = time.Hour
)

// postLimiter allows at most limit events per agent within a sliding window.
type postLimiter struct {
	limit  int
	window time.Duration
	now    func() time.Time

	mu     sync.Mutex
	events map[string][]time.Time
}

func newPostLimiter(limit int, window time.Duration, now func() time.Time) *postLimiter {
	return &postLimiter{limit: limit, window: window, now: now, events: map[string][]time.Time{}}
}

// allow records an event for key unless limit events already happened within the window.
func (l *postLimiter) allow(key string) bool {
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
