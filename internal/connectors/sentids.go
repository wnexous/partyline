package connectors

import (
	"sync"
	"time"
)

// SentIDs remembers, for a few minutes, the ids the platforms gave to the messages Partyline
// sent, every part and file of a send: a platform that reports an account's own messages
// (WhatsApp, Discord) echoes each of them back, and the echo must not be taken for something the
// operator typed (ACC-05). The record of a send keeps only the id of the first part, and these are
// lost on a restart, which an echo seconds later does not outlive.
type SentIDs struct {
	mu  sync.Mutex
	ids map[sentKey]time.Time
	now func() time.Time
}

type sentKey struct{ chat, id string }

// Remembered is how long an id is kept.
const Remembered = 5 * time.Minute

// pruneAt is the size from which Add drops the ids that expired.
const pruneAt = 512

// NewSentIDs returns an empty registry; now defaults to time.Now.
func NewSentIDs(now func() time.Time) *SentIDs {
	if now == nil {
		now = time.Now
	}
	return &SentIDs{ids: map[sentKey]time.Time{}, now: now}
}

// Add records the platform id of a message sent to the chat (Partyline's chat id).
func (s *SentIDs) Add(chatID, id string) {
	if s == nil || id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if len(s.ids) >= pruneAt {
		for k, at := range s.ids {
			if now.Sub(at) >= Remembered {
				delete(s.ids, k)
			}
		}
	}
	s.ids[sentKey{chatID, id}] = now
}

// Has tells whether Partyline sent the message with this id to the chat in the last few minutes.
func (s *SentIDs) Has(chatID, id string) bool {
	if s == nil || id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok := s.ids[sentKey{chatID, id}]
	return ok && s.now().Sub(at) < Remembered
}
