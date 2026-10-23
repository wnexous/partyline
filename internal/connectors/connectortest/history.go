package connectortest

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/handles"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// SeenInterval is how long the times a person and a chat link were last seen stay as they
// are: a busy group is not a write per message.
const SeenInterval = 10 * time.Minute

// HistoryDeps are the collaborators of History.
type HistoryDeps struct {
	Store *store.Store
	Bus   *events.Bus
	// Now returns the current time; defaults to time.Now.
	Now func() time.Time
}

// History seeds, for tests, the people the inbound pipeline used to record before it stopped
// (CID-07): one person, with an intelligence, per (connector, external id), the people seen in
// each chat and the counterpart of each DM chat. Nothing in the product creates people any more;
// the people of older instances stay as history, and the tests of what reads that history build
// it with this.
type History struct {
	d HistoryDeps
}

// NewHistory returns History.
func NewHistory(d HistoryDeps) *History {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &History{d: d}
}

// Sighting is a sender seen writing in a chat of a connector.
type Sighting struct {
	ConnectorID   string
	ConnectorKind string
	// Chat is the chat as just recorded.
	Chat db.Chat
	// SenderID, AltID and Name are what the platform reports about the sender; the alternative
	// id is the account's other id on platforms with two (a WhatsApp phone and its LID).
	SenderID string
	AltID    string
	Name     string
	// At is when the sender wrote; zero or in the future counts as now.
	At time.Time
}

// ids returns the sender's ids, the primary one first, without empty or repeated ones.
func (s Sighting) ids() []string {
	return slices.DeleteFunc([]string{s.SenderID, s.AltID}, func(id string) bool { return id == "" })
}

// Seen records the sender of a message that reaches rooms: the person is created on first
// sight (PEOP-01), follows the platform's name unless a user chose one (PEOP-05), is linked
// to the chat (GRP-02) and, in a DM chat, is its counterpart. It returns the person; the zero
// value when the platform gave no sender id. A message that changes nothing is answered
// without a write.
func (p *History) Seen(ctx context.Context, s Sighting) (db.Person, error) {
	now := p.d.Now()
	s.Name = intelligences.CleanDisplayName(s.Name)
	if s.SenderID == "" {
		s.SenderID, s.AltID = s.AltID, ""
	}
	if s.AltID == s.SenderID {
		s.AltID = ""
	}
	if s.SenderID == "" {
		return db.Person{}, nil
	}
	if s.At.IsZero() || s.At.After(now) {
		s.At = now
	}
	st, err := p.state(ctx, p.d.Store.R(), s)
	if err != nil {
		return db.Person{}, err
	}
	if st.found && st.plan(s).empty() {
		return st.person, nil
	}
	var person db.Person
	var renamed bool
	err = p.d.Store.WithTx(ctx, func(q *db.Queries) error {
		// The state may have changed since the read: decide again under the write lock, which
		// also makes concurrent first messages of one sender create one person.
		st, err := p.state(ctx, q, s)
		if err != nil {
			return err
		}
		person, renamed, err = p.apply(ctx, q, st, s, now)
		return err
	})
	if err != nil {
		return db.Person{}, err
	}
	if renamed {
		p.d.Bus.Publish(events.IntelligenceChanged{ID: person.ID})
	}
	return person, nil
}

// Find returns the id of the person of the sender on the connector, by either of its ids, without
// recording anything; empty when the sender has no person yet.
func (p *History) Find(ctx context.Context, connectorID, senderID, altID string) (string, error) {
	s := Sighting{ConnectorID: connectorID, SenderID: senderID, AltID: altID}
	if s.SenderID == "" {
		s.SenderID, s.AltID = s.AltID, ""
	}
	if s.SenderID == "" {
		return "", nil
	}
	st, err := p.state(ctx, p.d.Store.R(), s)
	if err != nil || !st.found {
		return "", err
	}
	return st.person.ID, nil
}

// seenState is what is stored about a sender before a sighting.
type seenState struct {
	person db.Person
	found  bool
	// link is last_seen_at of the person's row in the chat; not Valid when there is none.
	link sql.NullInt64
}

// state finds the sender's person by either of its ids and its link to the chat.
func (p *History) state(ctx context.Context, q *db.Queries, s Sighting) (seenState, error) {
	var st seenState
	for _, id := range s.ids() {
		row, err := q.GetPersonByExternalID(ctx, db.GetPersonByExternalIDParams{ConnectorID: s.ConnectorID, SenderID: id})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return st, err
		}
		st.person, st.found = row.Person, true
		break
	}
	if !st.found {
		return st, nil
	}
	cp, err := q.GetChatPerson(ctx, db.GetChatPersonParams{ChatID: s.Chat.ID, PersonID: st.person.ID})
	switch {
	case err == nil:
		st.link = sql.NullInt64{Int64: cp.LastSeenAt, Valid: true}
	case !errors.Is(err, sql.ErrNoRows):
		return st, err
	}
	return st, nil
}

// seenPlan is what a sighting changes about a known person.
type seenPlan struct {
	name       string // the platform name when it differs from the stored one
	alt        string // the alternative id to store, when altChanged
	altChanged bool
	touch      bool // people.last_seen_at is due
	link       bool // the chat_people row is missing or its time is due
	dm         bool // chats.person_id must name the person
}

func (pl seenPlan) empty() bool {
	return pl.name == "" && !pl.altChanged && !pl.touch && !pl.link && !pl.dm
}

func (st seenState) plan(s Sighting) seenPlan {
	pr := st.person
	var pl seenPlan
	if s.Name != "" && s.Name != pr.PlatformName {
		pl.name = s.Name
	}
	if alt := nextAlt(pr, s); alt != pr.AltExternalID {
		pl.alt, pl.altChanged = alt, true
	}
	pl.touch = due(pr.LastSeenAt, s.At)
	pl.link = due(st.link, s.At)
	pl.dm = s.Chat.Kind == string(dto.ChatKindDM) && (!s.Chat.PersonID.Valid || s.Chat.PersonID.String != pr.ID)
	return pl
}

// due tells whether a stored last-seen time is missing or at least SeenInterval before at.
func due(stored sql.NullInt64, at time.Time) bool {
	return !stored.Valid || at.UnixMilli()-stored.Int64 >= SeenInterval.Milliseconds()
}

// nextAlt is the alternative id a known person keeps after a sighting: the stored one while
// the message still reports it, otherwise the message's id that is not the person's external
// id (the most recent other id), and the stored one when the message reports none.
func nextAlt(pr db.Person, s Sighting) string {
	others := slices.DeleteFunc([]string{s.AltID, s.SenderID}, func(id string) bool { return id == "" || id == pr.ExternalID })
	if len(others) == 0 || slices.Contains(others, pr.AltExternalID) {
		return pr.AltExternalID
	}
	return others[0]
}

// apply writes a sighting in the caller's transaction and returns the person and whether its
// intelligence was renamed. An unknown sender gets a new person.
func (p *History) apply(ctx context.Context, q *db.Queries, st seenState, s Sighting, now time.Time) (db.Person, bool, error) {
	var pl seenPlan
	person, renamed := st.person, false
	if !st.found {
		var err error
		if person, err = p.create(ctx, q, s, now); err != nil {
			return db.Person{}, false, err
		}
		pl = seenPlan{link: true, dm: s.Chat.Kind == string(dto.ChatKindDM)}
	} else {
		pl = st.plan(s)
		if pl.name != "" || pl.altChanged || pl.touch {
			next := person
			if pl.name != "" {
				next.PlatformName = pl.name
			}
			if pl.altChanged {
				next.AltExternalID = pl.alt
			}
			if pl.touch {
				next.LastSeenAt = sql.NullInt64{Int64: s.At.UnixMilli(), Valid: true}
			}
			if err := q.UpdatePersonSeen(ctx, db.UpdatePersonSeenParams{
				AltExternalID: next.AltExternalID, PlatformName: next.PlatformName, LastSeenAt: next.LastSeenAt, ID: person.ID,
			}); err != nil {
				return db.Person{}, false, err
			}
			person = next
		}
		if pl.name != "" && !person.NameLocked {
			// PEOP-05: the person follows the platform's name until a user chose one.
			var err error
			if renamed, err = intelligences.SetDisplayName(ctx, q, person.ID, pl.name, now); err != nil {
				return db.Person{}, false, err
			}
		}
	}
	if pl.link {
		if err := q.UpsertChatPerson(ctx, db.UpsertChatPersonParams{ChatID: s.Chat.ID, PersonID: person.ID, LastSeenAt: s.At.UnixMilli()}); err != nil {
			return db.Person{}, false, err
		}
	}
	if pl.dm {
		if err := q.SetChatPerson(ctx, db.SetChatPersonParams{PersonID: sql.NullString{String: person.ID, Valid: true}, ID: s.Chat.ID}); err != nil {
			return db.Person{}, false, err
		}
	}
	return person, renamed, nil
}

// create inserts a person and its intelligence: the platform's name, or "<Platform> contact"
// when it gave none (PEOP-01), and a handle from it that is free across the instance.
func (p *History) create(ctx context.Context, q *db.Queries, s Sighting, now time.Time) (db.Person, error) {
	display := s.Name
	if display == "" {
		display = connectors.PlatformLabel(s.ConnectorKind) + " contact"
	}
	id := store.NewID()
	if _, err := intelligences.Create(ctx, q, intelligences.NewParams{
		ID: id, Kind: handles.Person, DisplayName: display, HandleBase: display, Now: now,
	}); err != nil {
		return db.Person{}, err
	}
	person := db.Person{
		ID: id, ConnectorID: s.ConnectorID, ExternalID: s.SenderID, AltExternalID: s.AltID, PlatformName: s.Name,
		LastSeenAt: sql.NullInt64{Int64: s.At.UnixMilli(), Valid: true}, CreatedAt: now.UnixMilli(),
	}
	err := q.CreatePerson(ctx, db.CreatePersonParams{
		ID: person.ID, ConnectorID: person.ConnectorID, ExternalID: person.ExternalID, AltExternalID: person.AltExternalID,
		PlatformName: person.PlatformName, NameLocked: false, LastSeenAt: person.LastSeenAt, CreatedAt: person.CreatedAt,
	})
	return person, err
}
