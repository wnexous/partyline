package schedules

// DropAnswers makes the service drop the bus events of the answers drop names, as a lagged bus would. Only tests
// set it.
func DropAnswers(s *Service, drop func(messageID string) bool) { s.dropAnswer.Store(&drop) }
