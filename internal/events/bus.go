// Package events is the typed in-process event bus. Publishing never blocks: a subscriber
// whose buffer is full misses events and receives a Lagged marker instead, so it can refetch.
package events

import "sync"

// Event is any value published on the bus. The set is closed to this package.
type Event interface{ isEvent() }

// MessageCreated reports that a message was stored in a room.
type MessageCreated struct {
	RoomID    string
	MessageID string
	Seq       int64
}

// RoomChanged reports that a room was created, updated or deleted.
type RoomChanged struct {
	RoomID  string
	Deleted bool
}

// MembersChanged reports that a room's members were added, updated or removed.
type MembersChanged struct{ RoomID string }

// GrantsChanged reports that the grants of a room, agent or connector changed.
type GrantsChanged struct{ ResourceKind, ResourceID string }

// AgentPresence reports that an agent connected or disconnected.
type AgentPresence struct {
	AgentID string
	OwnerID string
	Online  bool
}

// ConnectorStatus reports that a connector changed status (connecting, connected, error, stopped).
type ConnectorStatus struct {
	ConnectorID string
	Status      string
	Detail      string
}

// ChatDiscovered reports that a connector saw a chat for the first time or refreshed it.
type ChatDiscovered struct{ ConnectorID, ChatID string }

// OutboxChanged reports that an outbound delivery changed status.
type OutboxChanged struct {
	OutboxID  string
	MessageID string
	RoomID    string
	ChatID    string
	Status    string
}

// IdentityLinked reports that a chat identity was linked to a user.
type IdentityLinked struct{ UserID, IdentityID string }

// Lagged tells a subscriber it missed events because its buffer was full.
type Lagged struct{}

func (MessageCreated) isEvent()  {}
func (RoomChanged) isEvent()     {}
func (MembersChanged) isEvent()  {}
func (GrantsChanged) isEvent()   {}
func (AgentPresence) isEvent()   {}
func (ConnectorStatus) isEvent() {}
func (ChatDiscovered) isEvent()  {}
func (OutboxChanged) isEvent()   {}
func (IdentityLinked) isEvent()  {}
func (Lagged) isEvent()          {}

// Bus fans out events to subscribers in publish order.
type Bus struct {
	mu   sync.Mutex
	subs map[*subscriber]struct{}
}

type subscriber struct {
	ch     chan Event // capacity size+1; the extra slot is reserved for Lagged
	size   int
	lagged bool
}

// NewBus returns an empty bus.
func NewBus() *Bus {
	return &Bus{subs: make(map[*subscriber]struct{})}
}

// Subscribe registers a subscriber with the given buffer and returns its channel and an
// idempotent cancel function that closes the channel.
func (b *Bus) Subscribe(buffer int) (<-chan Event, func()) {
	buffer = max(buffer, 1)
	s := &subscriber{ch: make(chan Event, buffer+1), size: buffer}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	return s.ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, s)
			close(s.ch)
			b.mu.Unlock()
		})
	}
}

// Publish delivers e to every subscriber without blocking.
func (b *Bus) Publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for s := range b.subs {
		// Only publishers (holding mu) grow the channel, so len is a safe upper bound here.
		switch {
		case len(s.ch) < s.size:
			s.ch <- e
			s.lagged = false
		case !s.lagged:
			s.ch <- Lagged{}
			s.lagged = true
		}
	}
}
