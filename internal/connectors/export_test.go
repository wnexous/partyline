package connectors

// SentIDsLen returns how many ids the registry holds.
func SentIDsLen(s *SentIDs) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.ids)
}
