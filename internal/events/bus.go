// Package events is the typed in-process event bus. Publishing never blocks: a subscriber
// whose buffer is full misses events and receives a Lagged marker instead, so it can refetch.
package events

import (
	"slices"
	"sync"
)

// Event is any value published on the bus. The set is closed to this package.
type Event interface{ isEvent() }

// MessageCreated reports that a message was stored in a room.
type MessageCreated struct {
	RoomID    string
	MessageID string
	Seq       int64
}

// ReactionsChanged reports that someone reacted to a message or took a reaction back (RCT-06).
// Nothing is published for a call that changed nothing.
type ReactionsChanged struct {
	RoomID    string
	MessageID string
}

// MessageUpdated reports that the text of a message was edited or that the message was deleted (EDT-01,
// DLT-01): Deleted tells which. The subscribers read the message as it is now. PrevText is the text the message
// had before the change; the agent dispatcher needs it to tell the AIs that were sent the old text, and it
// never leaves the process.
type MessageUpdated struct {
	RoomID    string
	MessageID string
	Seq       int64
	Deleted   bool
	PrevText  string
}

// RoomChanged reports that a room was created, updated or deleted.
type RoomChanged struct {
	RoomID  string
	Deleted bool
}

// MembersChanged reports that a room's members were added, updated or removed.
type MembersChanged struct{ RoomID string }

// IntelligenceChanged reports that an intelligence's profile (name, handle, bio or avatar)
// changed. ID is the intelligence id, shared with its user, agent or person row.
type IntelligenceChanged struct{ ID string }

// AccessChanged reports that who may access a room or an AI changed through something other than an
// organization: a group's user members, or an AI that was merged into another. ResourceKind is
// "room", "agent" or "connector" (as an access.changed frame names it). Live clients look the
// resource up again, so those who gained it learn it and those who lost it forget it. A change of an
// organization's own members is OrgChanged.
type AccessChanged struct{ ResourceKind, ResourceID string }

// OrgChanged reports that something about an organization changed: its name or avatar, its org chart,
// its members or their permissions and positions. Live clients refresh what they show of it and
// re-evaluate what they may see.
type OrgChanged struct{ OrgID string }

// InvitationReceived reports that an identity was invited to an organization, or invited again: the
// invitee, and for an AI the identities that manage it, should see the invitation (OMEM-01). It carries
// no more than the ids; the receiver reads the invitation.
type InvitationReceived struct{ OrgID, InvitationID, InviteeIID string }

// ReadChanged reports that a user marked a room read up to Seq (the position that holds, never
// behind an earlier one). Only that user's own sockets hear about it.
type ReadChanged struct {
	UserID string
	RoomID string
	Seq    int64
}

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

// ConnectorsOrphaned reports that connections lost their operator because its AI was deleted
// (CID-24): IDs are the connections it operated. The connector manager applies them again, which
// stops them and shows what to do (CID-02).
type ConnectorsOrphaned struct{ IDs []string }

// ChatDiscovered reports that a connector saw a chat for the first time or refreshed it.
type ChatDiscovered struct{ ConnectorID, ChatID string }

// PlatformQueued reports that an item joined the platform queue of an AI: the AI's live session
// should read it. Seq is the item's position in the queue, so a session can skip an item it
// already sent (CID-08). ItemID is empty when several items were handed to the AI at once.
type PlatformQueued struct {
	AgentID string
	ItemID  string
	Seq     int64
}

// IdentityLinked reports that a chat identity was linked to a user.
type IdentityLinked struct{ UserID, IdentityID string }

// SessionsRevoked reports that credentials of a user ended: sessions (logout, session delete,
// password change or reset), tokens (revocation) or all of them (user disabled). Live sockets
// authenticated by those credentials must close.
type SessionsRevoked struct {
	UserID     string
	SessionIDs []string
	TokenIDs   []string
	// UserDisabled ends every session and token of UserID.
	UserDisabled bool
}

// Revokes reports whether e ends the credential (session or token) of userID.
func (e SessionsRevoked) Revokes(userID, sessionID, tokenID string) bool {
	switch {
	case e.UserDisabled && e.UserID == userID:
		return true
	case sessionID != "":
		return slices.Contains(e.SessionIDs, sessionID)
	case tokenID != "":
		return slices.Contains(e.TokenIDs, tokenID)
	}
	return false
}

// Lagged tells a subscriber it missed events because its buffer was full.
type Lagged struct{}

func (MessageCreated) isEvent()      {}
func (ReactionsChanged) isEvent()    {}
func (MessageUpdated) isEvent()      {}
func (RoomChanged) isEvent()         {}
func (MembersChanged) isEvent()      {}
func (IntelligenceChanged) isEvent() {}
func (AccessChanged) isEvent()       {}
func (OrgChanged) isEvent()          {}
func (InvitationReceived) isEvent()  {}
func (ReadChanged) isEvent()         {}
func (AgentPresence) isEvent()       {}
func (ConnectorStatus) isEvent()     {}
func (ConnectorsOrphaned) isEvent()  {}
func (ChatDiscovered) isEvent()      {}
func (PlatformQueued) isEvent()      {}
func (IdentityLinked) isEvent()      {}
func (SessionsRevoked) isEvent()     {}
func (Lagged) isEvent()              {}

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
