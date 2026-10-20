package agents

// SetAfterBacklogCount makes g run f between the backlog count and list reads.
func SetAfterBacklogCount(g *Gateway, f func()) { g.afterBacklogCount = f }
