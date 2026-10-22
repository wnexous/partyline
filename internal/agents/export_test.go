package agents

// SetAfterBacklogCount makes g run f between the backlog count and list reads.
func SetAfterBacklogCount(g *Gateway, f func()) { g.afterBacklogCount = f }

// SetBeforeAttach makes g run f after a connection upgrades and before its session attaches.
func SetBeforeAttach(g *Gateway, f func()) { g.beforeAttach = f }
