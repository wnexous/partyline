package connectors

// SetRouteHook makes the pipeline call hook once a catch-all route passed the caps and before
// the chat is added, so tests can hold routes at that point.
func (p *Pipeline) SetRouteHook(hook func(connectorID string)) { p.routeHook = hook }
