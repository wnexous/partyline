package agents

import "time"

// SetAfterBacklogCount makes g run f between the backlog count and list reads.
func SetAfterBacklogCount(g *Gateway, f func()) { g.afterBacklogCount = f }

// SetBeforeAttach makes g run f after a connection upgrades and before its session attaches.
func SetBeforeAttach(g *Gateway, f func()) { g.beforeAttach = f }

// SetPingTimings makes g ping every interval and give up on a pong after timeout.
func SetPingTimings(g *Gateway, interval, timeout time.Duration) {
	g.d.PingInterval, g.d.PingTimeout = interval, timeout
}

// SetPingInterval makes g ping every interval and keeps its pong timeout (the production default
// unless SetPingTimings changed it).
func SetPingInterval(g *Gateway, interval time.Duration) { g.d.PingInterval = interval }
