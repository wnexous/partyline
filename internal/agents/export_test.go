package agents

import "time"

// SetAfterBacklogCount makes g run f between the backlog count and list reads.
func SetAfterBacklogCount(g *Gateway, f func()) { g.afterBacklogCount = f }

// SetBeforeBounds makes g run f after a session attached and before its backlog bounds are read.
func SetBeforeBounds(g *Gateway, f func()) { g.beforeBounds = f }

// SetAfterWelcome makes g run f right after it wrote a connection's welcome frame.
func SetAfterWelcome(g *Gateway, f func()) { g.afterWelcome = f }

// SetAfterAttach makes g run f after a session attached and before its per-room state rows are written.
func SetAfterAttach(g *Gateway, f func()) { g.afterAttach = f }

// SetBeforeAttach makes g run f after a connection upgrades and before its session attaches.
func SetBeforeAttach(g *Gateway, f func()) { g.beforeAttach = f }

// SetPingTimings makes g ping every interval and give up on a pong after timeout.
func SetPingTimings(g *Gateway, interval, timeout time.Duration) {
	g.d.PingInterval, g.d.PingTimeout = interval, timeout
}

// SetPingInterval makes g ping every interval and keeps its pong timeout (the production default
// unless SetPingTimings changed it).
func SetPingInterval(g *Gateway, interval time.Duration) { g.d.PingInterval = interval }

// SetAfterFolderMiss makes r run f when a session found no AI for its folder, before it creates one.
func SetAfterFolderMiss(r *Registry, f func()) { r.afterFolderMiss = f }

// SetAfterOwnerMiss makes g run f when a pending row found no owner connection, before the release
// re-checks under the life lock.
func SetAfterOwnerMiss(g *Gateway, f func()) { g.afterOwnerMiss = f }

// SetQueueSize changes the capacity of the live push queue every connection created from now on has. Tests
// use it to fill a queue; QueueSize is the production value.
func SetQueueSize(n int) { queueSize = n }

// SetAfterNoteRoute makes g run f after Push routed a message with missing labels to the principal and before
// it pushes the other sessions.
func SetAfterNoteRoute(g *Gateway, f func()) { g.afterNoteRoute = f }
