package schedules

// DropAnswers makes the service drop the bus events of the answers drop names, as a lagged bus would. Only tests
// set it.
func DropAnswers(s *Service, drop func(messageID string) bool) { s.dropAnswer.Store(&drop) }

// AfterRead runs fn after each update or delete read its routine, before its write transaction. Only tests set
// it.
func AfterRead(s *Service, fn func()) { s.afterRead.Store(&fn) }
