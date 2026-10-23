package conversations

import (
	"context"
	"database/sql"
	"errors"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// Party is one side of a platform DM. A person has no member row, only its intelligence: it
// takes part through the chat.
type Party struct {
	ID string
	// Kind is the room member kind of a user or an AI; empty for a person.
	Kind dto.MemberKind
}

// PlatformDM describes a DM chat of a connector whose messages reach the connector's operator
// (ACC-02).
type PlatformDM struct {
	// OwnerID is the connector's owner; the DM room row names them.
	OwnerID string
	// ChatID is the id of the DM chat.
	ChatID string
	// Author is who writes in the chat: the user its sender is verified as, else the person.
	Author Party
	// Operator is who the connector's account belongs to: the owner, or an AI of the owner.
	Operator Party
}

// PlatformRoute is the outcome of EnsurePlatformDM.
type PlatformRoute struct {
	// RoomID is the DM between the author and the operator.
	RoomID string
	// Created tells that the DM was made now, Added that the chat joined it now (also when the DM
	// existed, as the web DM of a user does when their phone writes to the account).
	Created, Added bool
	// Left are the DMs, with earlier operators, that the chat left now; they keep their history.
	Left []string
}

// Changed tells whether the chat gained a route now: the cap on new routes counts it.
func (r PlatformRoute) Changed() bool { return r.Created || r.Added }

// EnsurePlatformDM makes the chat a member (outbound on) of the DM between its author and the
// connector's operator, creating the DM when the pair has none (ACC-02), and takes the chat out
// of its DMs with other intelligences: the operator changed, and the chat's next message follows
// the new one. A DM is one per pair, so a user who writes from their phone joins the DM they
// already have with the operator on the web. A sender that is the operator has no DM with
// themself (ErrDMSelf). Concurrent calls for one pair create one DM.
func (s *Service) EnsurePlatformDM(ctx context.Context, in PlatformDM) (PlatformRoute, error) {
	if in.Author.ID == "" || in.Author.ID == in.Operator.ID {
		return PlatformRoute{}, ErrDMSelf
	}
	pair := sortedPair(in.Author.ID, in.Operator.ID)
	now := s.now().UnixMilli()
	var route PlatformRoute
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		route = PlatformRoute{}
		id := store.NewID()
		n, err := q.InsertDMRoom(ctx, db.InsertDMRoomParams{
			ID: id, OwnerID: in.OwnerID, DmA: pair.DmA, DmB: pair.DmB, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			existing, err := q.GetDMRoom(ctx, pair)
			if err != nil {
				return err
			}
			id = existing.ID
		} else {
			route.Created = true
			for _, m := range []Party{in.Operator, in.Author} {
				if m.Kind == "" {
					continue
				}
				if err := q.CreateRoomMember(ctx, db.CreateRoomMemberParams{
					ID: store.NewID(), RoomID: id, Kind: string(m.Kind), MemberID: m.ID, Outbound: true, AddedBy: in.OwnerID, CreatedAt: now,
				}); err != nil {
					return err
				}
			}
		}
		route.RoomID = id
		member, err := q.CountRoomMember(ctx, db.CountRoomMemberParams{RoomID: id, Kind: string(dto.MemberKindChat), MemberID: in.ChatID})
		if err != nil {
			return err
		}
		if member == 0 {
			if err := q.CreateRoomMember(ctx, db.CreateRoomMemberParams{
				ID: store.NewID(), RoomID: id, Kind: string(dto.MemberKindChat), MemberID: in.ChatID, Outbound: true, AddedBy: in.OwnerID, CreatedAt: now,
			}); err != nil {
				return err
			}
			route.Added = true
		}
		dms, err := q.ListChatDMRoomIDs(ctx, in.ChatID)
		if err != nil {
			return err
		}
		for _, other := range dms {
			if other == id {
				continue
			}
			m, err := q.GetRoomMemberByMember(ctx, db.GetRoomMemberByMemberParams{RoomID: other, Kind: string(dto.MemberKindChat), MemberID: in.ChatID})
			if err != nil {
				return err
			}
			if err := q.DeleteRoomMember(ctx, m.ID); err != nil {
				return err
			}
			route.Left = append(route.Left, other)
		}
		return nil
	})
	if err != nil {
		return PlatformRoute{}, err
	}
	if route.Created {
		s.bus.Publish(events.RoomChanged{RoomID: route.RoomID})
	}
	if route.Changed() {
		s.bus.Publish(events.MembersChanged{RoomID: route.RoomID})
	}
	for _, left := range route.Left {
		s.bus.Publish(events.MembersChanged{RoomID: left})
	}
	return route, nil
}

// PlatformDMRoom returns the DM between two intelligences, if the pair has one: the room a chat
// of a connector sits in when it already routes to the right operator.
func (s *Service) PlatformDMRoom(ctx context.Context, authorID, operatorID string) (string, bool, error) {
	r, err := s.store.R().GetDMRoom(ctx, sortedPair(authorID, operatorID))
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return r.ID, err == nil, err
}
