package rooms

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

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
	// Name is the agent display name, chat title, user display name or project name.
	Name string
	// AgentOwner is the username of an agent or project member's owner.
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

// AddMember adds an agent, chat, user or project to a room; requires manage. Agents need the
// caller to own or use the agent (ACL-05), chats to own or use the chat's connector (ACL-06),
// projects to own or use the project (PROJ-06); otherwise ErrForbidden.
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
	case dto.MemberKindProject:
		// PROJ-06: the caller owns the project or holds use on it; unknown projects alike.
		ok, err := s.az.CanUseProject(ctx, userID, in.MemberID)
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
		return &validation.Error{Fields: map[string]string{"kind": "must be agent, chat, user or project"}}
	}
	return nil
}

// MemberUpdate changes a member; nil fields are unchanged. DisplayName, SignOutbound,
// Delivery and Keywords apply to agent and project members only (MEM-01..05, PROJ-06).
type MemberUpdate struct {
	Outbound     *bool
	DisplayName  *string
	SignOutbound *bool
	Delivery     *dto.DeliveryMode
	Keywords     *[]string
}

func (u MemberUpdate) agentSettings() map[string]bool {
	return map[string]bool{
		"display_name": u.DisplayName != nil, "sign_outbound": u.SignOutbound != nil,
		"delivery": u.Delivery != nil, "keywords": u.Keywords != nil,
	}
}

// Keywords decodes a member's mention keywords (never nil).
func Keywords(m db.RoomMember) []string {
	out := []string{}
	_ = json.Unmarshal([]byte(m.KeywordsJson), &out)
	if out == nil {
		out = []string{}
	}
	return out
}

// apply validates u against the member and returns the updated row (MEM-05).
func (u MemberUpdate) apply(m db.RoomMember) (db.RoomMember, error) {
	check := validation.Check{}
	if m.Kind != string(dto.MemberKindAgent) && m.Kind != string(dto.MemberKindProject) {
		for field, set := range u.agentSettings() {
			if set {
				check.Add(field, "only for agent members")
			}
		}
		if err := check.Err(); err != nil {
			return m, err
		}
	}
	if u.Outbound != nil {
		m.Outbound = *u.Outbound
	}
	if u.DisplayName != nil {
		name := strings.TrimSpace(*u.DisplayName)
		if utf8.RuneCountInString(name) > dto.MaxMemberDisplayName {
			check.Add("display_name", "must have at most 64 characters")
		}
		m.DisplayName = name
	}
	if u.SignOutbound != nil {
		m.SignOutbound = *u.SignOutbound
	}
	if u.Delivery != nil {
		switch *u.Delivery {
		case dto.DeliveryModeAll, dto.DeliveryModeMentions, dto.DeliveryModeTrusted:
			m.Delivery = string(*u.Delivery)
		default:
			check.Add("delivery", "must be all, mentions or trusted")
		}
	}
	if u.Keywords != nil {
		words := make([]string, 0, len(*u.Keywords))
		for _, w := range *u.Keywords {
			w = strings.TrimSpace(w)
			if n := utf8.RuneCountInString(w); n == 0 || n > dto.MaxMemberKeyword {
				check.Add("keywords", "each keyword must have 1 to 64 characters")
			}
			words = append(words, w)
		}
		if len(words) > dto.MaxMemberKeywords {
			check.Add("keywords", "at most 20 keywords")
		}
		data, err := json.Marshal(words)
		if err != nil {
			return m, err
		}
		m.KeywordsJson = string(data)
	}
	return m, check.Err()
}

// UpdateMember changes a member's outbound flag and, for agents, its settings (MEM-01..05);
// requires manage. Invalid settings change nothing.
func (s *Service) UpdateMember(ctx context.Context, p authn.Principal, roomID, memberRowID string, u MemberUpdate) (Member, error) {
	if _, err := s.authorize(ctx, p, roomID, dto.LevelManage); err != nil {
		return Member{}, err
	}
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		m, err := q.GetRoomMember(ctx, db.GetRoomMemberParams{ID: memberRowID, RoomID: roomID})
		if errors.Is(err, sql.ErrNoRows) {
			return access.ErrNotFound
		}
		if err != nil {
			return err
		}
		if m, err = u.apply(m); err != nil {
			return err
		}
		return q.UpdateRoomMemberSettings(ctx, db.UpdateRoomMemberSettingsParams{
			Outbound: m.Outbound, DisplayName: m.DisplayName, SignOutbound: m.SignOutbound,
			Delivery: m.Delivery, KeywordsJson: m.KeywordsJson, ID: m.ID,
		})
	})
	if err != nil {
		return Member{}, err
	}
	s.bus.Publish(events.MembersChanged{RoomID: roomID})
	return s.member(ctx, roomID, memberRowID)
}

// AutoRouteSkip is how long a chat removed from its connector's catch-all room stays out of
// it (ROUTE-02 edge case); a manager may add it back meanwhile.
const AutoRouteSkip = 24 * time.Hour

// skipAutoRoute keeps a chat removed from its connector's catch-all room out of it for
// AutoRouteSkip.
func (s *Service) skipAutoRoute(ctx context.Context, q *db.Queries, roomID, chatID string) error {
	r, err := q.GetChatAutoRoute(ctx, chatID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil || r.AutoRouteRoomID.String != roomID {
		return err
	}
	return q.UpsertAutoRouteSkip(ctx, db.UpsertAutoRouteSkipParams{
		ConnectorID: r.ConnectorID, ChatID: chatID, Until: s.now().Add(AutoRouteSkip).UnixMilli(),
	})
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
			if err := s.skipAutoRoute(ctx, q, roomID, m.MemberID); err != nil {
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
