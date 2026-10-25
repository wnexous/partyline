package connectors

// SetSecretValues makes m read a kind's secret values with f, so a test can make that call panic.
func SetSecretValues(m *Manager, f func(Kind, Config) []string) {
	m.secretValues = f
}

// SentIDsLen returns how many ids the registry holds.
func SentIDsLen(s *SentIDs) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.ids)
}
