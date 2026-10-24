package conversations

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/validation"
)

// MembersRefusedError is a new group with members its creator may not add: nothing was created
// (422 members_refused, LAY-04, AGRP-02, ORG-03). Refused lists them in the order they were asked for.
type MembersRefusedError = rooms.MembersRefusedError

// GroupInput is a new group: its name, description, the ids of its members and its organization.
type GroupInput struct {
	Name        string
	Description string
	Members     []string
	// Org is the organization the group belongs to, by id or slug; empty means the creator's default one
	// (an AI's is its home). The creator must be a member or an admin of it (ORG-03, OMEM-04).
	Org string
}

// CreateGroup creates a group created by the caller - the user, or the AI acting as itself - in an
// organization with the members in one step, and returns it as the caller sees it (LAY-04, AGRP-01,
// ORG-03). The caller must be a member or an admin of the organization (in.Org, else their default): a
// guest, someone who is not a member and an unknown organization are access.ErrForbidden, reported
// before anything else is looked at. An AI is a member of the group it creates. Every member is an
// intelligence id, checked with the add-member rules (rooms.CheckMember): a user must exist and be
// enabled, an AI must be one the caller can see, and either must belong to the organization; an
// external person cannot be added (people join groups through bridged chats). If any member is refused
// nothing is created and the error is a *MembersRefusedError with all of them (AGRP-02); a bad name or
// description, a blank id, or more than dto.MaxGroupMembers members is a validation error, reported
// before the members are looked at. The creator is a member only when listed. Repeated ids count once.
// An AI that would create the group is asked to AllowCreate last, after the checks above, so a refused
// request costs nothing (ErrRateLimited).
func (s *Service) CreateGroup(ctx context.Context, p authn.Principal, in GroupInput) (rooms.Room, error) {
	me, err := selfOf(p)
	if err != nil {
		return rooms.Room{}, err
	}
	orgID, err := s.rooms.CreatorOrg(ctx, p, in.Org)
	if err != nil {
		return rooms.Room{}, err
	}
	create := rooms.CreateInput{Name: in.Name, Description: in.Description, Org: orgID}
	if err := rooms.ValidateCreate(create); err != nil {
		return rooms.Room{}, err
	}
	ids, err := memberIDs(me, in.Members)
	if err != nil {
		return rooms.Room{}, err
	}
	members := make([]rooms.MemberInput, 0, len(ids))
	var refused []dto.RefusedMember
	for _, id := range ids {
		if me.kind == dto.MemberKindAgent && id == me.id {
			members = append(members, rooms.MemberInput{Kind: dto.MemberKindAgent, MemberID: id}) // it belongs to the organization: it just passed the check above
			continue
		}
		m, reason, err := s.member(ctx, p, orgID, id)
		if err != nil {
			return rooms.Room{}, err
		}
		if reason != "" {
			refused = append(refused, dto.RefusedMember{ID: id, Reason: reason})
			continue
		}
		members = append(members, m)
	}
	if len(refused) > 0 {
		return rooms.Room{}, &MembersRefusedError{Refused: refused}
	}
	if me.kind == dto.MemberKindAgent && s.allowCreate != nil && !s.allowCreate(p.AgentID) {
		return rooms.Room{}, ErrRateLimited // only a group that would be created uses a slot
	}
	roomID, err := s.rooms.CreateWithMembers(ctx, p, create, members)
	if err != nil {
		return rooms.Room{}, err
	}
	return s.rooms.Get(ctx, p, roomID)
}

// memberIDs is the distinct ids of the group's members, the creating AI first; blank ids and a
// list longer than dto.MaxGroupMembers are validation errors.
func memberIDs(me self, asked []string) ([]string, error) {
	ids := make([]string, 0, len(asked)+1)
	if me.kind == dto.MemberKindAgent {
		ids = append(ids, me.id)
	}
	for _, id := range asked {
		if strings.TrimSpace(id) == "" {
			return nil, &validation.Error{Fields: map[string]string{"members": "ids must not be empty"}}
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) > dto.MaxGroupMembers {
		return nil, &validation.Error{Fields: map[string]string{"members": "at most 50 members"}}
	}
	return ids, nil
}

// member resolves an id to the member it would become and checks that the caller may add it to a group
// of the organization; a non-empty reason says why not. An external person is reported as such only to a
// caller who can see them: to anyone else they are as unknown as an id that does not exist.
func (s *Service) member(ctx context.Context, p authn.Principal, orgID, id string) (rooms.MemberInput, dto.RefusedReason, error) {
	d, err := s.store.R().GetIntelligenceWithRole(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return rooms.MemberInput{}, dto.RefusedReasonNotFound, nil
	}
	if err != nil {
		return rooms.MemberInput{}, "", err
	}
	m := rooms.MemberInput{MemberID: id}
	switch intelligences.Role(d.Role) {
	case intelligences.RoleUser:
		m.Kind = dto.MemberKindUser
	case intelligences.RoleAI:
		m.Kind = dto.MemberKindAgent
	case intelligences.RolePerson:
		see, err := s.ints.CanSee(ctx, p, id)
		if err != nil {
			return m, "", err
		}
		if !see {
			return m, dto.RefusedReasonNotFound, nil
		}
		return m, dto.RefusedReasonPerson, nil
	default:
		return m, dto.RefusedReasonNotFound, nil
	}
	reason, err := s.rooms.CheckMember(ctx, p, orgID, m)
	return m, reason, err
}

// CreateGroupNamed is CreateGroup for members named the way an AI names them: "@handle", a bare
// handle or an id (Contact). The members an owner cannot add come back as they were named, not
// as the ids they resolved to, so the AI recognises them (AGRP-02).
func (s *Service) CreateGroupNamed(ctx context.Context, p authn.Principal, in GroupInput) (rooms.Room, error) {
	named := map[string]string{} // resolved id -> how the first reference to it read
	ids := make([]string, 0, len(in.Members))
	for _, ref := range in.Members {
		id, err := s.Contact(ctx, ref)
		if err != nil {
			return rooms.Room{}, err
		}
		if _, ok := named[id]; !ok {
			named[id] = strings.TrimSpace(ref)
		}
		ids = append(ids, id)
	}
	in.Members = ids
	room, err := s.CreateGroup(ctx, p, in)
	var refused *MembersRefusedError
	if errors.As(err, &refused) {
		for i, r := range refused.Refused {
			if ref, ok := named[r.ID]; ok {
				refused.Refused[i].ID = ref
			}
		}
	}
	return room, err
}
