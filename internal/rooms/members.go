package rooms

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
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
	// Name is the agent display name, chat title or user display name.
	Name string
	// AgentOwner is the username of an agent member's owner.
	AgentOwner string
	// ConnectorKind and ChatKind describe chat members.
	ConnectorKind string
	ChatKind      string
	// Intelligence is the profile of an agent or user member (ID empty for other kinds and for
	// members that have none).
	Intelligence db.Intelligence
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
		out[i] = Member{
			RoomMember: r.RoomMember, Name: r.Name, AgentOwner: r.AgentOwner, ConnectorKind: r.ConnectorKind, ChatKind: r.ChatKind,
			Intelligence: db.Intelligence{
				ID: r.IntelligenceID, Kind: r.IntelligenceKind, DisplayName: r.IntelligenceName,
				Handle: r.IntelligenceHandle, AvatarSha256: r.IntelligenceAvatar,
			},
		}
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

// authorizeMembers authorizes a change of the room's members and returns the room: a caller without
// view gets ErrNotFound; whoever can view a DM gets ErrDMMembersFixed, since a DM has exactly its two
// intelligences (DM-02); the others need manage.
func (s *Service) authorizeMembers(ctx context.Context, p authn.Principal, roomID string) (db.Room, error) {
	l, err := s.authorize(ctx, p, roomID, dto.LevelView)
	if err != nil {
		return db.Room{}, err
	}
	r, err := s.store.R().GetRoom(ctx, roomID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Room{}, access.ErrNotFound
	}
	if err != nil {
		return db.Room{}, err
	}
	if r.Kind == string(dto.ConversationKindDM) {
		return r, ErrDMMembersFixed
	}
	if !access.AtLeast(l, dto.LevelManage) {
		return r, access.ErrForbidden
	}
	return r, nil
}

// MemberInput names a member to add.
type MemberInput struct {
	Kind     dto.MemberKind
	MemberID string
}

// MembersRefusedError is a request to put members in a group that cannot be granted: nothing was created
// or added (422 members_refused, LAY-04, AGRP-02, ORG-03). Refused lists them in the order they were
// asked for, each with its reason.
type MembersRefusedError struct{ Refused []dto.RefusedMember }

func (e *MembersRefusedError) Error() string { return "some members cannot be added" }

// AddMember adds an agent or a user to a group; requires manage. The member must belong to the group's
// organization (ORG-03): a user or AI of another one is refused with a *MembersRefusedError
// (not_org_member). An AI the caller cannot see, and one that does not exist, are ErrForbidden, and an
// unknown or disabled user a validation error. A chat is never a member (ErrChatsNotMembers, CID-19):
// the check follows the caller's rights on the room, so a stranger still gets not found and a direct
// message still answers ErrDMMembersFixed.
func (s *Service) AddMember(ctx context.Context, p authn.Principal, roomID string, in MemberInput) (Member, error) {
	room, err := s.authorizeMembers(ctx, p, roomID)
	if err != nil {
		return Member{}, err
	}
	reason, err := s.CheckMember(ctx, p, room.OrgID.String, in)
	switch {
	case err != nil:
		return Member{}, err
	case reason == dto.RefusedReasonNotOrgMember:
		return Member{}, &MembersRefusedError{Refused: []dto.RefusedMember{{ID: in.MemberID, Reason: reason}}}
	case reason != "" && in.Kind == dto.MemberKindUser:
		return Member{}, &validation.Error{Fields: map[string]string{"member_id": "unknown user"}}
	case reason != "":
		return Member{}, access.ErrForbidden
	}
	id := store.NewID()
	err = s.store.WithTx(ctx, func(q *db.Queries) error {
		n, err := q.CountRoomMember(ctx, db.CountRoomMemberParams{RoomID: roomID, Kind: string(in.Kind), MemberID: in.MemberID})
		if err != nil {
			return err
		}
		if n > 0 {
			return ErrAlreadyMember
		}
		if err := q.CreateRoomMember(ctx, db.CreateRoomMemberParams{
			ID: id, RoomID: roomID, Kind: string(in.Kind), MemberID: in.MemberID,
			AddedBy: p.UserID, CreatedAt: s.now().UnixMilli(),
		}); err != nil {
			return err
		}
		if in.Kind != dto.MemberKindAgent {
			return nil
		}
		// An AI that was in the room before comes back caught up (ROOM-01): its cursor was left at the point
		// it left, so what the room wrote during the absence is skipped, and an AI that is added for the
		// first time keeps the cursor it has (none, so its backlog starts at the room's beginning, AGT-08).
		if _, err := q.GetAgentCursor(ctx, db.GetAgentCursorParams{AgentID: in.MemberID, RoomID: roomID}); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		high, err := q.GetRoomHighSeq(ctx, roomID)
		if err != nil {
			return err
		}
		return q.AdvanceAgentCursor(ctx, db.AdvanceAgentCursorParams{AgentID: in.MemberID, RoomID: roomID, Seq: high})
	})
	if err != nil {
		return Member{}, err
	}
	s.bus.Publish(events.MembersChanged{RoomID: roomID})
	if in.Kind == dto.MemberKindUser {
		s.accessChanged(roomID) // a user member gains access to the group (AD-011)
	}
	return s.member(ctx, roomID, id)
}

// accessChanged tells live clients that who may access the room changed through membership: new
// members learn the room, removed ones lose it.
func (s *Service) accessChanged(roomID string) {
	s.bus.Publish(events.AccessChanged{ResourceKind: string(access.KindRoom), ResourceID: roomID})
}

// CheckMember says why the principal may not put the member in a group of the organization, "" when it
// may (ORG-03, AGRP-02). A user must exist and be enabled, and an AI must exist and be one the
// principal can see; both are dto.RefusedReasonNotFound otherwise, so that an AI the principal cannot
// see is not told apart from an id that does not exist. Then the member must belong to the organization,
// with any permission - a guest in a group posts in it - or it is dto.RefusedReasonNotOrgMember. A chat
// is refused with ErrChatsNotMembers (CID-19) and a kind that is not one with a validation error.
func (s *Service) CheckMember(ctx context.Context, p authn.Principal, orgID string, in MemberInput) (dto.RefusedReason, error) {
	switch in.Kind {
	case dto.MemberKindAgent:
		see, err := s.az.CanSeeAI(ctx, p, in.MemberID)
		if err != nil || !see {
			return dto.RefusedReasonNotFound, err
		}
	case dto.MemberKindChat:
		return "", ErrChatsNotMembers
	case dto.MemberKindUser:
		u, err := s.store.R().GetUser(ctx, in.MemberID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && u.DisabledAt.Valid) {
			return dto.RefusedReasonNotFound, nil
		}
		if err != nil {
			return "", err
		}
	default:
		return "", &validation.Error{Fields: map[string]string{"kind": "must be agent or user"}}
	}
	if _, ok, err := s.az.Authz().Member(ctx, orgID, in.MemberID); err != nil || !ok {
		return dto.RefusedReasonNotOrgMember, err
	}
	return "", nil
}

// MemberUpdate changes a member; nil fields are unchanged. All of them apply to agent members
// only (MEM-02..05).
type MemberUpdate struct {
	DisplayName *string
	Delivery    *dto.DeliveryMode
	Keywords    *[]string
}

func (u MemberUpdate) agentSettings() map[string]bool {
	return map[string]bool{
		"display_name": u.DisplayName != nil, "delivery": u.Delivery != nil, "keywords": u.Keywords != nil,
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
	if m.Kind != string(dto.MemberKindAgent) {
		for field, set := range u.agentSettings() {
			if set {
				check.Add(field, "only for agent members")
			}
		}
		if err := check.Err(); err != nil {
			return m, err
		}
	}
	if u.DisplayName != nil {
		name := strings.TrimSpace(*u.DisplayName)
		if utf8.RuneCountInString(name) > dto.MaxMemberDisplayName {
			check.Add("display_name", "must have at most 64 characters")
		}
		m.DisplayName = name
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

// UpdateMember changes an agent member's settings (MEM-02..05); requires manage. Invalid settings
// change nothing.
func (s *Service) UpdateMember(ctx context.Context, p authn.Principal, roomID, memberRowID string, u MemberUpdate) (Member, error) {
	if _, err := s.authorizeMembers(ctx, p, roomID); err != nil {
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
			DisplayName: m.DisplayName, Delivery: m.Delivery, KeywordsJson: m.KeywordsJson, ID: m.ID,
		})
	})
	if err != nil {
		return Member{}, err
	}
	s.bus.Publish(events.MembersChanged{RoomID: roomID})
	return s.member(ctx, roomID, memberRowID)
}

// RemoveMember removes a member; later messages are no longer delivered to it (ROOM-01).
// A manager removes any member; a user takes their own user row out of a group to leave it, without
// manage, and the group keeps its creator and organization admins. A DM is unchanged: nobody takes a
// member out of one (DM-02), its own row included.
func (s *Service) RemoveMember(ctx context.Context, p authn.Principal, roomID, memberRowID string) error {
	m, err := s.store.R().GetRoomMember(ctx, db.GetRoomMemberParams{ID: memberRowID, RoomID: roomID})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// The caller's own user row in a group is theirs to take out: it is what a member leaving is.
	// Everything else needs manage, exactly as before.
	leaving := err == nil && p.Kind == authn.KindUser &&
		m.Kind == string(dto.MemberKindUser) && m.MemberID == p.UserID
	if leaving {
		if err := s.authorizeLeaving(ctx, p, roomID); err != nil {
			return err
		}
	} else if _, err := s.authorizeMembers(ctx, p, roomID); err != nil {
		return err
	}
	var removedKind string
	err = s.store.WithTx(ctx, func(q *db.Queries) error {
		m, err := q.GetRoomMember(ctx, db.GetRoomMemberParams{ID: memberRowID, RoomID: roomID})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return access.ErrNotFound
			}
			return err
		}
		removedKind = m.Kind
		if err := q.DeleteRoomMember(ctx, memberRowID); err != nil {
			return err
		}
		if m.Kind != string(dto.MemberKindAgent) {
			return nil
		}
		// An AI taken out of a room forgets what it had read and what it had not (ROOM-01): the next time it
		// is added, its sessions join at the new membership, so nothing from the absence comes back as
		// context, as unread or as the principal's backlog. Its cursor is reset to the point it left rather
		// than dropped: the row is also what tells a later membership that the AI was in the room before
		// (AddMember), so the absence is skipped exactly, in the room's sequence and not by its clock.
		if err := q.DeleteSessionRoomStatesInRoom(ctx, db.DeleteSessionRoomStatesInRoomParams{
			AgentID: m.MemberID, RoomID: roomID,
		}); err != nil {
			return err
		}
		high, err := q.GetRoomHighSeq(ctx, roomID)
		if err != nil {
			return err
		}
		return q.AdvanceAgentCursor(ctx, db.AdvanceAgentCursorParams{AgentID: m.MemberID, RoomID: roomID, Seq: high})
	})
	if err != nil {
		return err
	}
	s.bus.Publish(events.MembersChanged{RoomID: roomID})
	if removedKind == string(dto.MemberKindUser) {
		s.accessChanged(roomID) // the user loses the access membership gave
	}
	return nil
}

// authorizeLeaving authorizes a user taking their own member row out of a group: it is enough that
// they can see the room, and a DM keeps its two members (DM-02).
func (s *Service) authorizeLeaving(ctx context.Context, p authn.Principal, roomID string) error {
	if _, err := s.authorize(ctx, p, roomID, dto.LevelView); err != nil {
		return err
	}
	r, err := s.store.R().GetRoom(ctx, roomID)
	if errors.Is(err, sql.ErrNoRows) {
		return access.ErrNotFound
	}
	if err != nil {
		return err
	}
	if r.Kind == string(dto.ConversationKindDM) {
		return ErrDMMembersFixed
	}
	return nil
}
