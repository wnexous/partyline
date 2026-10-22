package intelligences

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/store/db"
)

// MaxConversations bounds the conversations a person's profile lists.
const MaxConversations = 100

// PersonInfo is what a profile shows about a person: the account behind it.
type PersonInfo struct {
	db.Person
	Connector db.Connector
	// CanUse tells whether the caller owns or may use the connector; only they see the account
	// and who it is linked to (PEOP-04).
	CanUse bool
	// LinkedUser is the user whose verified account it is (AD-008); set only when CanUse.
	LinkedUser *db.Intelligence
}

// personInfo loads the account behind a person for the caller.
func (s *Service) personInfo(ctx context.Context, userID, personID string) (*PersonInfo, error) {
	q := s.store.R()
	pr, err := q.GetPerson(ctx, personID)
	if err != nil {
		return nil, err
	}
	c, err := q.GetConnector(ctx, pr.ConnectorID)
	if err != nil {
		return nil, err
	}
	canUse, err := s.az.CanUseConnector(ctx, userID, c.ID)
	if err != nil {
		return nil, err
	}
	info := &PersonInfo{Person: pr, Connector: c, CanUse: canUse}
	if !canUse {
		return info, nil
	}
	alt := pr.AltExternalID
	if alt == "" {
		alt = pr.ExternalID
	}
	uid, err := q.ResolveIdentityUser(ctx, db.ResolveIdentityUserParams{ConnectorID: c.ID, SenderID: pr.ExternalID, AltID: alt})
	if errors.Is(err, sql.ErrNoRows) {
		return info, nil
	}
	if err != nil {
		return nil, err
	}
	user, err := q.GetIntelligence(ctx, uid)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		info.LinkedUser = &user
	}
	return info, nil
}

// sharesPersonConversation reports whether the user can view a room that involves the person:
// one with a chat member where the person was seen, or where it wrote a message (PEOP-03).
func (s *Service) sharesPersonConversation(ctx context.Context, userID, personID string) (bool, error) {
	roomIDs, err := s.store.R().ListPersonRoomIDs(ctx, personID)
	if err != nil {
		return false, err
	}
	for _, roomID := range roomIDs {
		l, err := s.az.UserRoomLevel(ctx, userID, roomID)
		if err != nil {
			return false, err
		}
		if access.AtLeast(l, dto.LevelView) {
			return true, nil
		}
	}
	return false, nil
}

// ConversationRef is a conversation listed on a profile.
type ConversationRef struct {
	ID   string
	Name string
	Kind dto.ConversationKind
}

// Conversations lists the conversations of a person that the caller can view, the most
// recently active first (the newest message, else the room's creation), at most MaxConversations
// (PEOP-03). Profiles of other kinds list none.
func (s *Service) Conversations(ctx context.Context, p authn.Principal, prof Profile) ([]ConversationRef, error) {
	out := []ConversationRef{}
	if prof.Role != RolePerson {
		return out, nil
	}
	q := s.store.R()
	ids, err := q.ListPersonRoomIDs(ctx, prof.ID)
	if err != nil || len(ids) == 0 {
		return out, err
	}
	rooms, err := q.ListRoomActivity(ctx, ids)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(rooms, func(a, b db.ListRoomActivityRow) int {
		if c := cmp.Compare(b.ActivityAt, a.ActivityAt); c != 0 {
			return c
		}
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	for _, r := range rooms {
		if len(out) == MaxConversations {
			break
		}
		l, err := s.az.UserRoomLevel(ctx, p.UserID, r.ID)
		if err != nil {
			return nil, err
		}
		if access.AtLeast(l, dto.LevelView) {
			out = append(out, ConversationRef{ID: r.ID, Name: r.Name, Kind: dto.ConversationKind(r.Kind)})
		}
	}
	return out, nil
}

// ChatPerson is a person seen in a chat, with the platform it writes through.
type ChatPerson struct {
	db.Intelligence
	Platform string
}

// ChatPeople returns the people seen in each chat, the most recently seen first and at most
// dto.MaxChatPeople per chat (GRP-02). It does not authorize: the caller already may view the
// room the chats belong to.
func (s *Service) ChatPeople(ctx context.Context, chatIDs []string) (map[string][]ChatPerson, error) {
	out := make(map[string][]ChatPerson, len(chatIDs))
	for _, id := range chatIDs {
		rows, err := s.store.R().ListChatPeopleRecent(ctx, db.ListChatPeopleRecentParams{ChatID: id, Limit: dto.MaxChatPeople})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out[id] = append(out[id], ChatPerson{Intelligence: r.Intelligence, Platform: r.Platform})
		}
	}
	return out, nil
}

// LinkedAccount is a chat account a user verified (AD-008) and the connector it was linked
// through.
type LinkedAccount struct {
	Identity  db.Identity
	Connector db.Connector
}

// linkedAccounts lists the accounts of a user that the viewer may see: the user's own, or those
// linked through a connector the viewer owns or may use (who can already see which user an
// account is, PersonDetail.LinkedUser). They are the user's apps (PEOP-02).
func (s *Service) linkedAccounts(ctx context.Context, viewerID, userID string) ([]LinkedAccount, error) {
	q := s.store.R()
	idents, err := q.ListIdentities(ctx, userID)
	if err != nil {
		return nil, err
	}
	var out []LinkedAccount
	for _, ident := range idents {
		if viewerID != userID {
			ok, err := s.az.CanUseConnector(ctx, viewerID, ident.ConnectorID)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		c, err := q.GetConnector(ctx, ident.ConnectorID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, LinkedAccount{Identity: ident, Connector: c})
	}
	return out, nil
}
