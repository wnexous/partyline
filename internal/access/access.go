// Package access authorizes every resource through one grants table (AD-005): it resolves a
// caller's level on rooms, agents and connectors and replaces grants from visibility presets.
// Instance admins get no implicit access (ACL-08).
package access

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// Errors returned by services that authorize through this package.
var (
	// ErrNotFound means the resource does not exist or the caller cannot view it (404).
	ErrNotFound = errors.New("not found")
	// ErrForbidden means the caller can view the resource but not perform the action (403).
	ErrForbidden = errors.New("forbidden")
)

// Kind is a resource kind that carries grants.
type Kind string

// Resource kinds.
const (
	KindRoom      Kind = "room"
	KindAgent     Kind = "agent"
	KindConnector Kind = "connector"
)

// Level is an access level; None ("") means no access. Rooms order view < post < manage;
// agents and connectors order use < manage, where manage means ownership.
type Level = dto.Level

// None is the absence of access.
const None Level = ""

// Everyone is the grant subject that applies to every user (public visibility).
const Everyone = "*"

// rank orders levels. use only ever compares with use and manage, so it shares view's rank.
var rank = map[Level]int{None: 0, dto.LevelView: 1, dto.LevelUse: 1, dto.LevelPost: 2, dto.LevelManage: 3}

// AtLeast reports whether have grants at least want.
func AtLeast(have, want Level) bool { return rank[have] >= rank[want] }

func maxLevel(a, b Level) Level {
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// Options tune the Authorizer; zero values use the defaults.
type Options struct {
	// Now returns the current time; defaults to time.Now.
	Now func() time.Time
}

// Authorizer resolves access levels and manages grants.
type Authorizer struct {
	store *store.Store
	bus   *events.Bus
	now   func() time.Time
	audit *audit.Log
}

// New returns an Authorizer. GrantsChanged events are published on bus.
func New(st *store.Store, bus *events.Bus, opts Options) *Authorizer {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Authorizer{store: st, bus: bus, now: opts.Now, audit: audit.New(st, opts.Now)}
}

// RoomLevel resolves the caller's level on a room (ACL-02). A user gets the highest of
// ownership (manage), their own grant and the everyone grant; an agent gets post when it
// is a member of the room. Missing rooms resolve to None.
func (a *Authorizer) RoomLevel(ctx context.Context, p authn.Principal, roomID string) (Level, error) {
	if p.Kind == authn.KindAgent {
		return a.AgentRoomLevel(ctx, p.AgentID, roomID)
	}
	return a.UserRoomLevel(ctx, p.UserID, roomID)
}

// UserRoomLevel resolves a user's level on a room; disabled or unknown users resolve to None.
func (a *Authorizer) UserRoomLevel(ctx context.Context, userID, roomID string) (Level, error) {
	room, err := a.store.R().GetRoom(ctx, roomID)
	if errors.Is(err, sql.ErrNoRows) {
		return None, nil
	}
	if err != nil {
		return None, err
	}
	return a.level(ctx, KindRoom, roomID, room.OwnerID, userID)
}

// AgentRoomLevel is post when the agent is a member of the room, else None.
func (a *Authorizer) AgentRoomLevel(ctx context.Context, agentID, roomID string) (Level, error) {
	if agentID == "" {
		return None, nil
	}
	n, err := a.store.R().CountRoomMember(ctx, db.CountRoomMemberParams{RoomID: roomID, Kind: string(dto.MemberKindAgent), MemberID: agentID})
	if err != nil {
		return None, err
	}
	if n == 0 {
		return None, nil
	}
	return dto.LevelPost, nil
}

// grantedLevels returns, per resource id of kind, the highest level granted to the user or everyone.
func (a *Authorizer) grantedLevels(ctx context.Context, kind Kind, userID string) (map[string]Level, error) {
	rows, err := a.store.R().ListGrantLevelsByKind(ctx, db.ListGrantLevelsByKindParams{ResourceKind: string(kind), UserID: userID})
	if err != nil {
		return nil, err
	}
	out := make(map[string]Level, len(rows))
	for _, r := range rows {
		out[r.ResourceID] = maxLevel(out[r.ResourceID], Level(r.Level))
	}
	return out, nil
}

// GrantedRoomLevels returns, per room id, the highest level the user holds through grants
// (own or everyone). Ownership is not included. Disabled users get an empty map.
func (a *Authorizer) GrantedRoomLevels(ctx context.Context, userID string) (map[string]Level, error) {
	ok, err := a.activeUser(ctx, userID)
	if err != nil || !ok {
		return map[string]Level{}, err
	}
	return a.grantedLevels(ctx, KindRoom, userID)
}

// AgentLevel is manage for the agent's owner, use for grantees, else None.
func (a *Authorizer) AgentLevel(ctx context.Context, userID, agentID string) (Level, error) {
	agent, err := a.store.R().GetAgent(ctx, agentID)
	if errors.Is(err, sql.ErrNoRows) {
		return None, nil
	}
	if err != nil {
		return None, err
	}
	return a.level(ctx, KindAgent, agentID, agent.OwnerID, userID)
}

// ConnectorLevel is manage for the connector's owner, use for grantees, else None.
func (a *Authorizer) ConnectorLevel(ctx context.Context, userID, connectorID string) (Level, error) {
	c, err := a.store.R().GetConnector(ctx, connectorID)
	if errors.Is(err, sql.ErrNoRows) {
		return None, nil
	}
	if err != nil {
		return None, err
	}
	return a.level(ctx, KindConnector, connectorID, c.OwnerID, userID)
}

// CanUseAgent reports whether the user owns the agent or holds a use grant on it (ACL-05).
func (a *Authorizer) CanUseAgent(ctx context.Context, userID, agentID string) (bool, error) {
	l, err := a.AgentLevel(ctx, userID, agentID)
	return AtLeast(l, dto.LevelUse), err
}

// CanUseConnector reports whether the user owns the connector or holds a use grant on it (ACL-06).
func (a *Authorizer) CanUseConnector(ctx context.Context, userID, connectorID string) (bool, error) {
	l, err := a.ConnectorLevel(ctx, userID, connectorID)
	return AtLeast(l, dto.LevelUse), err
}

// level is the highest of ownership and the user's and everyone's grants on a resource.
// Disabled and unknown users have no access, whatever their grants (AUTH-10).
func (a *Authorizer) level(ctx context.Context, kind Kind, id, ownerID, userID string) (Level, error) {
	ok, err := a.activeUser(ctx, userID)
	if err != nil || !ok {
		return None, err
	}
	if userID == ownerID {
		return dto.LevelManage, nil
	}
	levels, err := a.store.R().ListGrantLevels(ctx, db.ListGrantLevelsParams{ResourceKind: string(kind), ResourceID: id, UserID: userID})
	if err != nil {
		return None, err
	}
	best := None
	for _, l := range levels {
		best = maxLevel(best, Level(l))
	}
	return best, nil
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

// UserLevel is one user's grant in a Visibility. Username and DisplayName are filled by Sharing.
type UserLevel struct {
	UserID      string
	Level       Level
	Username    string
	DisplayName string
}

// Visibility is a sharing preset (ACL-07): private (no grants), shared (Users at their
// levels) or public (everyone at PublicLevel).
type Visibility struct {
	Mode        dto.Visibility
	Users       []UserLevel
	PublicLevel Level
}

// Sharing reads a resource's grants back as a visibility preset.
func (a *Authorizer) Sharing(ctx context.Context, kind Kind, id string) (Visibility, error) {
	rows, err := a.store.R().ListGrantsWithUsers(ctx, db.ListGrantsWithUsersParams{ResourceKind: string(kind), ResourceID: id})
	if err != nil {
		return Visibility{}, err
	}
	v := Visibility{Mode: dto.VisibilityPrivate, Users: []UserLevel{}}
	for _, r := range rows {
		if r.Subject == Everyone {
			v.Mode, v.PublicLevel = dto.VisibilityPublic, Level(r.Level)
			continue
		}
		v.Users = append(v.Users, UserLevel{UserID: r.Subject, Level: Level(r.Level), Username: r.Username, DisplayName: r.DisplayName})
	}
	if v.Mode == dto.VisibilityPrivate && len(v.Users) > 0 {
		v.Mode = dto.VisibilityShared
	}
	return v, nil
}

// grantLevels lists the levels a preset may grant per resource kind.
func grantLevels(kind Kind, mode dto.Visibility) []Level {
	switch {
	case kind != KindRoom:
		return []Level{dto.LevelUse}
	case mode == dto.VisibilityPublic:
		return []Level{dto.LevelView, dto.LevelPost}
	default:
		return []Level{dto.LevelView, dto.LevelPost, dto.LevelManage}
	}
}

func allowed(l Level, set []Level) bool {
	for _, s := range set {
		if l == s {
			return true
		}
	}
	return false
}

// SetVisibility replaces a resource's grants in one transaction (ACL-07): private removes
// them all, shared grants each listed user its level, public grants everyone PublicLevel.
// The owner is skipped when listed. Callers check that actorID may manage the resource.
func (a *Authorizer) SetVisibility(ctx context.Context, actorID string, kind Kind, id, ownerID string, v Visibility) error {
	grants, err := a.presetGrants(ctx, kind, ownerID, v)
	if err != nil {
		return err
	}
	now := a.now().UnixMilli()
	err = a.store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.DeleteGrants(ctx, db.DeleteGrantsParams{ResourceKind: string(kind), ResourceID: id}); err != nil {
			return err
		}
		users := []map[string]any{}
		for _, g := range grants {
			if err := q.CreateGrant(ctx, db.CreateGrantParams{
				ResourceKind: string(kind), ResourceID: id, Subject: g.UserID, Level: string(g.Level),
				CreatedBy: actorID, CreatedAt: now,
			}); err != nil {
				return err
			}
			users = append(users, map[string]any{"subject": g.UserID, "level": string(g.Level)})
		}
		return a.audit.Record(ctx, q, audit.Entry{
			ActorID: actorID, Action: audit.ActionGrantsSet, TargetKind: string(kind), TargetID: id,
			Detail: map[string]any{"mode": string(v.Mode), "grants": users},
		})
	})
	if err != nil {
		return err
	}
	a.bus.Publish(events.GrantsChanged{ResourceKind: string(kind), ResourceID: id})
	return nil
}

// presetGrants validates v and returns the grants it stands for.
func (a *Authorizer) presetGrants(ctx context.Context, kind Kind, ownerID string, v Visibility) ([]UserLevel, error) {
	check := validation.Check{}
	levels := grantLevels(kind, v.Mode)
	var out []UserLevel
	switch v.Mode {
	case dto.VisibilityPrivate:
	case dto.VisibilityPublic:
		l := v.PublicLevel
		if l == None && kind != KindRoom {
			l = dto.LevelUse
		}
		if !allowed(l, levels) {
			check.Add("public_level", "must be one of "+join(levels))
		}
		out = append(out, UserLevel{UserID: Everyone, Level: l})
	case dto.VisibilityShared:
		if len(v.Users) == 0 {
			check.Add("users", "must list at least one user")
		}
		seen := map[string]bool{}
		for _, u := range v.Users {
			if u.Level == None && kind != KindRoom {
				u.Level = dto.LevelUse
			}
			switch {
			case !allowed(u.Level, levels):
				check.Add("users", "level must be one of "+join(levels))
			case seen[u.UserID]:
				check.Add("users", "lists a user twice")
			case u.UserID == ownerID:
				// The owner always has manage; no grant needed.
			default:
				exists, err := a.userExists(ctx, u.UserID)
				if err != nil {
					return nil, err
				}
				if !exists {
					check.Add("users", "unknown user")
				}
				out = append(out, u)
			}
			seen[u.UserID] = true
		}
	default:
		check.Add("mode", "must be private, shared or public")
	}
	if err := check.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (a *Authorizer) userExists(ctx context.Context, id string) (bool, error) {
	_, err := a.store.R().GetUser(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func join(levels []Level) string {
	names := make([]string, len(levels))
	for i, l := range levels {
		names[i] = string(l)
	}
	return strings.Join(names, ", ")
}
