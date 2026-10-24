package conversations

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// OpenDM returns the direct message between the caller and the intelligence withID, creating it
// when the pair has none (DM-01); created tells which. The caller is the user, or the AI of an
// agent principal, whose visibility is its owner's (DM-04). A DM is a room of kind dm whose two
// participants, keyed by their sorted ids, are its only members; its owner column names the user
// who started it (the AI's owner for an AI) and gives no access (AD-011).
//
//   - withID is the caller itself: ErrDMSelf. An empty withID is a validation error.
//   - withID is unknown or not visible to the caller: ErrContactNotFound, the same for both.
//   - withID is an external person: always ErrNoRouteToPerson. Partyline no longer writes to
//     people (DM-05 is retired, CID-22): a person is answered on their platform, by the AI that
//     operates the connection they wrote to. The old platform DMs with them are history.
//   - An AI that has to create the DM is asked to AllowCreate first: ErrRateLimited. Opening an
//     existing DM is never counted.
//
// Concurrent calls for one pair create one room: the creation is a single write transaction on
// the unique pair key, and exactly one caller sees created.
func (s *Service) OpenDM(ctx context.Context, p authn.Principal, withID string) (rooms.Room, bool, error) {
	me, err := selfOf(p)
	if err != nil {
		return rooms.Room{}, false, err
	}
	if withID == "" {
		return rooms.Room{}, false, &validation.Error{Fields: map[string]string{"with": "required"}}
	}
	if withID == me.id {
		return rooms.Room{}, false, ErrDMSelf
	}
	target, err := s.contact(ctx, p, withID)
	if err != nil {
		return rooms.Room{}, false, err
	}
	var targetKind dto.MemberKind
	switch intelligences.Role(target.Role) {
	case intelligences.RoleUser:
		targetKind = dto.MemberKindUser
	case intelligences.RoleAI:
		targetKind = dto.MemberKindAgent
	case intelligences.RolePerson:
		return rooms.Room{}, false, ErrNoRouteToPerson
	default: // an intelligence without account: nobody to talk to
		return rooms.Room{}, false, ErrContactNotFound
	}
	pair := sortedPair(me.id, withID)
	existing, err := s.store.R().GetDMRoom(ctx, pair)
	switch {
	case err == nil:
		room, err := s.rooms.Get(ctx, p, existing.ID)
		return room, false, err
	case !errors.Is(err, sql.ErrNoRows):
		return rooms.Room{}, false, err
	}
	if me.kind == dto.MemberKindAgent && s.allowCreate != nil && !s.allowCreate(p.AgentID) {
		return rooms.Room{}, false, ErrRateLimited
	}
	roomID, created, err := s.createDM(ctx, p, pair, [2]participant{{me.id, me.kind}, {withID, targetKind}})
	if err != nil {
		return rooms.Room{}, false, err
	}
	if created {
		s.bus.Publish(events.RoomChanged{RoomID: roomID})
		s.bus.Publish(events.MembersChanged{RoomID: roomID})
	}
	room, err := s.rooms.Get(ctx, p, roomID)
	return room, created, err
}

// contact resolves the target of a DM: the intelligence must exist and be visible to the caller
// (AD-016): every user of the instance, and the AIs the caller shares an organization with; an AI
// sees what it sees itself, not what its owner does.
func (s *Service) contact(ctx context.Context, p authn.Principal, id string) (db.GetIntelligenceWithRoleRow, error) {
	see, err := s.ints.CanSee(ctx, p, id)
	if err != nil {
		return db.GetIntelligenceWithRoleRow{}, err
	}
	if !see {
		return db.GetIntelligenceWithRoleRow{}, ErrContactNotFound
	}
	d, err := s.store.R().GetIntelligenceWithRole(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return db.GetIntelligenceWithRoleRow{}, ErrContactNotFound
	}
	return d, err
}

// participant is one side of a DM and the kind of room member it becomes.
type participant struct {
	id   string
	kind dto.MemberKind
}

// sortedPair is the key of a DM: the two ids in order (dm_a < dm_b).
func sortedPair(x, y string) db.GetDMRoomParams {
	if x > y {
		x, y = y, x
	}
	return db.GetDMRoomParams{DmA: sql.NullString{String: x, Valid: true}, DmB: sql.NullString{String: y, Valid: true}}
}

// createDM inserts the room and one member row per participant in one write transaction. When the
// pair already has its DM (a concurrent caller was first) it returns that room and created false.
func (s *Service) createDM(ctx context.Context, p authn.Principal, pair db.GetDMRoomParams, who [2]participant) (string, bool, error) {
	id := store.NewID()
	now := s.now().UnixMilli()
	created := false
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		n, err := q.InsertDMRoom(ctx, db.InsertDMRoomParams{
			ID: id, OwnerID: p.UserID, DmA: pair.DmA, DmB: pair.DmB, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			existing, err := q.GetDMRoom(ctx, pair)
			id = existing.ID
			return err
		}
		created = true
		for _, m := range who {
			if err := q.CreateRoomMember(ctx, db.CreateRoomMemberParams{
				ID: store.NewID(), RoomID: id, Kind: string(m.kind), MemberID: m.id, AddedBy: p.UserID, CreatedAt: now,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return id, created, err
}

// Contact resolves how an AI names an intelligence, "@handle", a bare handle or an id (any case
// for a handle), to the intelligence's id. A reference that names no handle is returned as it is,
// so that the checks that follow answer for it as for any unknown id: whether a handle or an id
// exists is not told to anyone who cannot see it.
func (s *Service) Contact(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if handle := strings.TrimPrefix(ref, "@"); handle != "" {
		i, err := s.store.R().GetIntelligenceByHandle(ctx, strings.ToLower(handle))
		if err == nil {
			return i.ID, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	return ref, nil
}

// SendDM posts the AI's message in its DM with the intelligence named by to (an "@handle", a
// handle or an id), opening the DM first when the pair has none (DM-04). The request is validated
// before anything is created; the DM is opened by OpenDM, with its rules and its allowance (an
// existing DM is not counted); and a DM this call created that then refuses the post is deleted
// again, so no empty DM is left behind.
func (s *Service) SendDM(ctx context.Context, p authn.Principal, to string, in rooms.PostInput) (rooms.Message, error) {
	if err := ValidateDM(to, in); err != nil {
		return rooms.Message{}, err
	}
	id, err := s.Contact(ctx, to)
	if err != nil {
		return rooms.Message{}, err
	}
	room, created, err := s.OpenDM(ctx, p, id)
	if err != nil {
		return rooms.Message{}, err
	}
	msg, err := s.rooms.Post(ctx, p, room.ID, in)
	if err != nil && created {
		s.discard(ctx, room.ID)
	}
	return msg, err
}

// discard deletes a DM nobody has written in and tells the sockets that heard of it. Best effort:
// a DM that cannot be deleted stays, empty.
func (s *Service) discard(ctx context.Context, roomID string) {
	var n int64
	err := s.store.WithTx(ctx, func(q *db.Queries) (err error) {
		n, err = q.DeleteEmptyDMRoom(ctx, roomID)
		return err
	})
	if err == nil && n > 0 {
		s.bus.Publish(events.RoomChanged{RoomID: roomID, Deleted: true})
	}
}

// ValidateDM checks the request of SendDM and writes nothing: the target must be named and the
// message valid. Both problems are reported together, as fields to and text.
func ValidateDM(to string, in rooms.PostInput) error {
	check := validation.Check{}
	if h := strings.TrimSpace(to); h == "" || h == "@" {
		check.Add("to", "required")
	}
	if err := rooms.ValidatePost(in); err != nil {
		var invalid *validation.Error
		if !errors.As(err, &invalid) {
			return err
		}
		for field, reason := range invalid.Fields {
			check.Add(field, reason)
		}
	}
	return check.Err()
}
