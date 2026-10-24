// Package access resolves what a caller may do with rooms and who may see an AI or a connection. A room's
// level comes from room membership and from organizations (AD-016): a group is posted to by its members and
// managed by its creator and by the admins of its organization, and a direct message is posted to by its two
// participants and viewed by the managers of an AI participant in that AI's home organization. An AI and a
// connection are seen through membership of the organizations they belong to (CanSeeAI, CanSeeConnection).
// Instance admins get no implicit access (ACL-08).
package access

import (
	"context"
	"database/sql"
	"errors"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// Errors returned by services that authorize through this package.
var (
	// ErrNotFound means the resource does not exist or the caller cannot view it (404). It is the
	// sentinel of package orgs, so a room check and an organization check report the same error.
	ErrNotFound = orgs.ErrNotFound
	// ErrForbidden means the caller can view the resource but not perform the action (403). It is
	// the sentinel of package orgs.
	ErrForbidden = orgs.ErrForbidden
	// ErrHistoryOnly means the caller may act on the resource but it is read-only history: a
	// post in a platform DM with a person, or an edit of a person's profile (422 history_only,
	// CID-21).
	ErrHistoryOnly = errors.New("history is read-only")
)

// Kind is a kind of resource whose access an access.changed event reports.
type Kind string

// Resource kinds.
const (
	KindRoom      Kind = "room"
	KindAgent     Kind = "agent"
	KindConnector Kind = "connector"
)

// Level is an access level; None ("") means no access. Rooms order view < post < manage; AIs and
// connections order use < manage, where use is seeing the resource and manage is managing it in its
// organization.
type Level = dto.Level

// None is the absence of access.
const None Level = ""

// rank orders levels. use only ever compares with use and manage, so it shares view's rank.
var rank = map[Level]int{None: 0, dto.LevelView: 1, dto.LevelUse: 1, dto.LevelPost: 2, dto.LevelManage: 3}

// AtLeast reports whether have is at least want.
func AtLeast(have, want Level) bool { return rank[have] >= rank[want] }

func maxLevel(a, b Level) Level {
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// Options tune the Authorizer; zero values use the defaults.
type Options struct {
	// Authz answers the organization questions the levels depend on (who is an admin, who manages
	// whom). The app passes the facade every service shares; the default is one over the store.
	Authz *orgs.Authz
}

// Authorizer resolves access levels from room membership and organizations.
type Authorizer struct {
	store *store.Store
	authz *orgs.Authz
}

// New returns an Authorizer.
func New(st *store.Store, opts Options) *Authorizer {
	if opts.Authz == nil {
		opts.Authz = orgs.NewAuthz(st)
	}
	return &Authorizer{store: st, authz: opts.Authz}
}

// Authz is the organization facade the room levels are computed with. A service that holds the
// Authorizer asks it the same organization questions (membership, who manages whom) instead of
// building a second one.
func (a *Authorizer) Authz() *orgs.Authz { return a.authz }

// CanSeeAI reports whether the caller can see the AI (AD-016): a user sees the AIs hosted on their
// computers and the AIs of the organizations they belong to; an AI sees itself and the AIs it shares an
// organization with, and never reaches further through its host, because it acts as itself. A disabled
// user and an unknown AI see and are seen by nobody.
func (a *Authorizer) CanSeeAI(ctx context.Context, p authn.Principal, aiID string) (bool, error) {
	if aiID == "" {
		return false, nil
	}
	agent, err := a.store.R().GetAgent(ctx, aiID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ok, err := a.activeUser(ctx, p.UserID)
	if err != nil || !ok {
		return false, err
	}
	if p.Kind == authn.KindAgent {
		if p.AgentID == aiID {
			return true, nil
		}
		return a.authz.SharesOrg(ctx, p.AgentID, aiID)
	}
	if agent.OwnerID == p.UserID {
		return true, nil
	}
	return a.authz.SharesOrg(ctx, p.UserID, aiID)
}

// CanSeeConnection reports whether the caller can see the connection (AD-016): it is a member of the organization
// the connection belongs to, with any permission. A user acts as themself and an AI as itself, never through its
// host; a disabled user, a connection without an organization and an unknown connection are seen by nobody. It is
// what the people of a connection, the linked accounts and the apps of an operator are shown through.
func (a *Authorizer) CanSeeConnection(ctx context.Context, p authn.Principal, connectorID string) (bool, error) {
	c, err := a.store.R().GetConnector(ctx, connectorID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	iid := p.IID()
	if iid == "" || !c.OrgID.Valid {
		return false, nil
	}
	ok, err := a.activeUser(ctx, p.UserID)
	if err != nil || !ok {
		return false, err
	}
	_, member, err := a.authz.Member(ctx, c.OrgID.String, iid)
	return member, err
}

// RoomLevel resolves the caller's level on a room (ACL-02). A user's level comes from room membership
// and organizations (see UserRoomLevel); an agent gets post when it is a member of the room. Missing
// rooms resolve to None.
func (a *Authorizer) RoomLevel(ctx context.Context, p authn.Principal, roomID string) (Level, error) {
	if p.Kind == authn.KindAgent {
		return a.AgentRoomLevel(ctx, p.AgentID, roomID)
	}
	return a.UserRoomLevel(ctx, p.UserID, roomID)
}

// UserRoomLevel resolves a user's level on a room; disabled or unknown users resolve to None.
//
// Group (AD-016): the highest of post, as a user member of the room, and manage, as the group's
// creator or as an admin of the group's organization. The creator keeps managing only while still a
// member of the organization, and not as a guest, who may take part in groups but not run them
// (OMEM-04, OMEM-06). Neither a position, the owner column of the room row nor the instance admin
// role gives anything.
//
// DM (AD-011, DM-08): post for a participant; view for whoever manages an AI participant in that AI's
// home organization (POS-03); none otherwise, admins included. Nobody manages a DM.
func (a *Authorizer) UserRoomLevel(ctx context.Context, userID, roomID string) (Level, error) {
	room, err := a.store.R().GetRoom(ctx, roomID)
	if errors.Is(err, sql.ErrNoRows) {
		return None, nil
	}
	if err != nil {
		return None, err
	}
	ok, err := a.activeUser(ctx, userID)
	if err != nil || !ok {
		return None, err
	}
	if room.Kind == string(dto.ConversationKindDM) {
		return a.dmLevel(ctx, room, userID)
	}
	return a.groupLevel(ctx, room, userID)
}

// groupLevel is an active user's level on a group.
func (a *Authorizer) groupLevel(ctx context.Context, room db.Room, userID string) (Level, error) {
	level := None
	n, err := a.store.R().CountRoomMember(ctx, db.CountRoomMemberParams{RoomID: room.ID, Kind: string(dto.MemberKindUser), MemberID: userID})
	if err != nil {
		return None, err
	}
	if n > 0 {
		level = dto.LevelPost
	}
	if !room.OrgID.Valid {
		return level, nil // a group written before organizations has nobody to manage it
	}
	m, member, err := a.authz.Member(ctx, room.OrgID.String, userID)
	if err != nil || !member {
		return level, err
	}
	creator := room.CreatorIid.Valid && room.CreatorIid.String == userID && m.Permission != orgs.PermissionGuest
	if m.Permission == orgs.PermissionAdmin || creator {
		return dto.LevelManage, nil
	}
	return level, nil
}

// dmLevel is an active user's level on a DM, from its participant columns alone.
func (a *Authorizer) dmLevel(ctx context.Context, room db.Room, userID string) (Level, error) {
	participants := []string{room.DmA.String, room.DmB.String}
	if userID == participants[0] || userID == participants[1] {
		return dto.LevelPost, nil
	}
	z := a.authz.Session()
	for _, id := range participants {
		ok, err := a.managesAI(ctx, z, userID, id)
		if err != nil {
			return None, err
		}
		if ok {
			return dto.LevelView, nil
		}
	}
	return None, nil
}

// managesAI reports whether the user manages the participant in its home organization. Only an AI
// has one: a user, a person and an AI without a home organization are managed by nobody.
func (a *Authorizer) managesAI(ctx context.Context, z *orgs.Session, userID, participantID string) (bool, error) {
	if participantID == "" {
		return false, nil
	}
	home, err := a.store.R().GetAgentOrg(ctx, participantID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // a user or a person
	}
	if err != nil || home == "" {
		return false, err
	}
	return z.Manages(ctx, userID, participantID, home)
}

// AgentRoomLevel is post when the agent is a member of the room (it then uses the room's tools
// as a member would: post, reply, react, history, attachments), else None.
func (a *Authorizer) AgentRoomLevel(ctx context.Context, agentID, roomID string) (Level, error) {
	if agentID == "" {
		return None, nil
	}
	n, err := a.store.R().CountRoomMember(ctx, db.CountRoomMemberParams{RoomID: roomID, Kind: string(dto.MemberKindAgent), MemberID: agentID})
	if err != nil || n == 0 {
		return None, err
	}
	return dto.LevelPost, nil
}

// RoomLevels returns, per room id, the highest level the user holds as a member or a manager
// (AD-016): post in the groups they are a member of and the DMs they take part in, manage in the groups
// they created and in every group of the organizations they administer. The DMs they only view as the
// manager of an AI are not included. Disabled users get an empty map.
func (a *Authorizer) RoomLevels(ctx context.Context, userID string) (map[string]Level, error) {
	ok, err := a.activeUser(ctx, userID)
	if err != nil || !ok {
		return map[string]Level{}, err
	}
	rows, err := a.store.R().ListUserRoomLevels(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Level, len(rows))
	for _, r := range rows {
		out[r.RoomID] = maxLevel(out[r.RoomID], Level(r.Level))
	}
	return out, nil
}

func (a *Authorizer) activeUser(ctx context.Context, userID string) (bool, error) {
	if userID == "" {
		return false, nil
	}
	u, err := a.store.R().GetUser(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return !u.DisabledAt.Valid, nil
}
