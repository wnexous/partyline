package rooms

import (
	"context"
	"database/sql"
	"errors"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// Member is a room member with its display details.
type Member struct {
	db.RoomMember
	// Name is the agent name, chat title or user display name.
	Name string
	// AgentOwner is the username of an agent member's owner.
	AgentOwner string
	// ConnectorKind and ChatKind describe chat members.
	ConnectorKind string
	ChatKind      string
}

// Members lists a room's members in the order they were added; requires view.
func (s *Service) Members(ctx context.Context, p authn.Principal, roomID string) ([]Member, error) {
	if _, err := s.authorize(ctx, p, roomID, dto.LevelView); err != nil {
		return nil, err
	}
	return s.members(ctx, roomID)
}

func (s *Service) members(ctx context.Context, roomID string) ([]Member, error) {
	rows, err := s.store.R().ListRoomMembers(ctx, roomID)
	if err != nil {
		return nil, err
	}
	out := make([]Member, len(rows))
	for i, r := range rows {
		out[i] = Member{RoomMember: r.RoomMember, Name: r.Name, AgentOwner: r.AgentOwner, ConnectorKind: r.ConnectorKind, ChatKind: r.ChatKind}
	}
	return out, nil
}

func (s *Service) member(ctx context.Context, roomID, memberRowID string) (Member, error) {
	list, err := s.members(ctx, roomID)
	if err != nil {
		return Member{}, err
	}
	for _, m := range list {
		if m.ID == memberRowID {
			return m, nil
		}
	}
	return Member{}, access.ErrNotFound
}

// MemberInput names a member to add. Outbound defaults to true.
type MemberInput struct {
	Kind     dto.MemberKind
	MemberID string
	Outbound *bool
}

// AddMember adds an agent, chat or user to a room; requires manage. Agents need the caller
// to own or use the agent (ACL-05), chats to own or use the chat's connector (ACL-06);
// otherwise ErrForbidden.
func (s *Service) AddMember(ctx context.Context, p authn.Principal, roomID string, in MemberInput) (Member, error) {
	if _, err := s.authorize(ctx, p, roomID, dto.LevelManage); err != nil {
		return Member{}, err
	}
	if err := s.checkUse(ctx, p.UserID, in); err != nil {
		return Member{}, err
	}
	outbound := true
	if in.Outbound != nil {
		outbound = *in.Outbound
	}
	id := store.NewID()
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		n, err := q.CountRoomMember(ctx, db.CountRoomMemberParams{RoomID: roomID, Kind: string(in.Kind), MemberID: in.MemberID})
		if err != nil {
			return err
		}
		if n > 0 {
			return ErrAlreadyMember
		}
		return q.CreateRoomMember(ctx, db.CreateRoomMemberParams{
			ID: id, RoomID: roomID, Kind: string(in.Kind), MemberID: in.MemberID,
			Outbound: outbound, AddedBy: p.UserID, CreatedAt: s.now().UnixMilli(),
		})
	})
	if err != nil {
		return Member{}, err
	}
	s.bus.Publish(events.MembersChanged{RoomID: roomID})
	return s.member(ctx, roomID, id)
}

// checkUse verifies the caller may put the agent or chat in a room.
func (s *Service) checkUse(ctx context.Context, userID string, in MemberInput) error {
	switch in.Kind {
	case dto.MemberKindAgent:
		ok, err := s.az.CanUseAgent(ctx, userID, in.MemberID)
		if err != nil {
			return err
		}
		if !ok {
			return access.ErrForbidden
		}
	case dto.MemberKindChat:
		chat, err := s.store.R().GetChat(ctx, in.MemberID)
		if errors.Is(err, sql.ErrNoRows) {
			return access.ErrForbidden
		}
		if err != nil {
			return err
		}
		ok, err := s.az.CanUseConnector(ctx, userID, chat.ConnectorID)
		if err != nil {
			return err
		}
		if !ok {
			return access.ErrForbidden
		}
	case dto.MemberKindUser:
		u, err := s.store.R().GetUser(ctx, in.MemberID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && u.DisabledAt.Valid) {
			return &validation.Error{Fields: map[string]string{"member_id": "unknown user"}}
		}
		return err
	default:
		return &validation.Error{Fields: map[string]string{"kind": "must be agent, chat or user"}}
	}
	return nil
}

// UpdateMember changes a member's outbound flag; requires manage.
func (s *Service) UpdateMember(ctx context.Context, p authn.Principal, roomID, memberRowID string, outbound *bool) (Member, error) {
	if _, err := s.authorize(ctx, p, roomID, dto.LevelManage); err != nil {
		return Member{}, err
	}
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		m, err := q.GetRoomMember(ctx, db.GetRoomMemberParams{ID: memberRowID, RoomID: roomID})
		if errors.Is(err, sql.ErrNoRows) {
			return access.ErrNotFound
		}
		if err != nil || outbound == nil {
			return err
		}
		return q.UpdateRoomMemberOutbound(ctx, db.UpdateRoomMemberOutboundParams{Outbound: *outbound, ID: m.ID})
	})
	if err != nil {
		return Member{}, err
	}
	s.bus.Publish(events.MembersChanged{RoomID: roomID})
	return s.member(ctx, roomID, memberRowID)
}

// RemoveMember removes a member; later messages are no longer delivered to it, and a chat's
// pending (not yet sent) outbox entries in this room are deleted in the same tx (ROOM-01).
// Requires manage.
func (s *Service) RemoveMember(ctx context.Context, p authn.Principal, roomID, memberRowID string) error {
	if _, err := s.authorize(ctx, p, roomID, dto.LevelManage); err != nil {
		return err
	}
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		m, err := q.GetRoomMember(ctx, db.GetRoomMemberParams{ID: memberRowID, RoomID: roomID})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return access.ErrNotFound
			}
			return err
		}
		if m.Kind == string(dto.MemberKindChat) {
			// Cancel what was queued for this chat in this room but not sent yet (ROOM-01).
			if _, err := q.DeletePendingRoomChatOutbox(ctx, db.DeletePendingRoomChatOutboxParams{ChatID: m.MemberID, RoomID: roomID}); err != nil {
				return err
			}
		}
		return q.DeleteRoomMember(ctx, memberRowID)
	})
	if err != nil {
		return err
	}
	s.bus.Publish(events.MembersChanged{RoomID: roomID})
	return nil
}
