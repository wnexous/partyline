package connectors

// SetRouteHook makes the pipeline call hook once a catch-all route passed the caps and before
// the chat is added, so tests can hold routes at that point.
func (p *Pipeline) SetRouteHook(hook func(connectorID string)) { p.routeHook = hook }

// SentIDsLen returns how many ids the registry holds.
func SentIDsLen(s *SentIDs) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.ids)
}
