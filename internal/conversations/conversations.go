// Package conversations opens direct messages and creates groups with members, the two kinds of
// conversation of the messenger (AD-011). Both are rooms (AD-002): this package decides who may
// start one with whom and writes its rows; package rooms owns what happens inside a room.
package conversations

import (
	"errors"
	"time"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store"
)

// Errors of opening a DM; the API maps each to its status and code.
var (
	// ErrDMSelf is a DM with oneself (422 dm_self).
	ErrDMSelf = errors.New("a direct message needs two intelligences")
	// ErrContactNotFound is a DM with an intelligence that does not exist or that the caller
	// cannot see; the two cases are indistinguishable (404 contact_not_found).
	ErrContactNotFound = errors.New("contact not found")
	// ErrNoRouteToPerson is a DM with an external person that no platform conversation of the
	// sender reaches (422 no_route_to_person, DM-05).
	ErrNoRouteToPerson = errors.New("no route to the person")
	// ErrRateLimited is an AI that opened too many new conversations (429 rate_limited).
	ErrRateLimited = errors.New("too many new conversations")
)

// Deps are the collaborators of the Service.
type Deps struct {
	Store         *store.Store
	Rooms         *rooms.Service
	Intelligences *intelligences.Service
	Bus           *events.Bus
	// AllowCreate says whether an AI may create one more conversation now (and counts it); the
	// API wires the registry's limit of 10 per hour. Nil allows everything. It is asked only
	// when a conversation is about to be created, never for an existing DM.
	AllowCreate func(agentID string) bool
	// Now returns the current time; defaults to time.Now.
	Now func() time.Time
}

// Service opens DMs and creates groups.
type Service struct {
	store       *store.Store
	rooms       *rooms.Service
	ints        *intelligences.Service
	bus         *events.Bus
	allowCreate func(agentID string) bool
	now         func() time.Time
}

// New returns a Service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{store: d.Store, rooms: d.Rooms, ints: d.Intelligences, bus: d.Bus, allowCreate: d.AllowCreate, now: d.Now}
}

// self is the intelligence a principal acts as: the user, or the AI of an agent principal.
type self struct {
	id   string
	kind dto.MemberKind
}

func selfOf(p authn.Principal) (self, error) {
	switch p.Kind {
	case authn.KindUser:
		return self{id: p.UserID, kind: dto.MemberKindUser}, nil
	case authn.KindAgent:
		return self{id: p.AgentID, kind: dto.MemberKindAgent}, nil
	}
	return self{}, access.ErrForbidden
}
