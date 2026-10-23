// Package agents is the server side of Claude Code sessions: the agent registry (agents
// register on their first WebSocket connection), their presence and the agent gateway.
package agents

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// ErrNotRegistered means the agent a request resolved is gone (403): it was deleted meanwhile.
var ErrNotRegistered = errors.New("agent not registered")

// MaxAgentsPerOwner is the most AIs one user can have. Every folder a session starts in is a
// permanent identity, so a token cannot create identities without bound.
const MaxAgentsPerOwner = 200

// ErrAgentLimit answers the registration of a new agent beyond MaxAgentsPerOwner (409).
var ErrAgentLimit = httpx.NewError(http.StatusConflict, dto.ErrorCodeConflict, "agent limit reached")

// Limits of agent fields.
const (
	// MaxNameLength is the longest agent name (dto.AgentNamePattern).
	MaxNameLength = 48
	// MaxDescriptionLength is the longest agent description in characters.
	MaxDescriptionLength = 1000
	// DefaultName replaces a name that is empty after slugifying (AGT-01).
	DefaultName = "agent"
)

// Slug turns a free-form name into an agent name matching dto.AgentNamePattern: lowercase,
// every run of other characters becomes one hyphen, leading and trailing separators are
// dropped and the result is cut to 48 characters. An empty result becomes "agent".
func Slug(name string) string {
	var b strings.Builder
	sep := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
			sep = false
			continue
		}
		if !sep {
			b.WriteByte('-')
			sep = true
		}
	}
	s := strings.Trim(b.String(), "-._")
	if len(s) > MaxNameLength {
		s = strings.TrimRight(s[:MaxNameLength], "-._")
	}
	if s == "" {
		return DefaultName
	}
	return s
}

// Deps are the collaborators of the Registry.
type Deps struct {
	Store    *store.Store
	Access   *access.Authorizer
	Bus      *events.Bus
	Presence *Presence
	// Audit records the adoption of a v0.3 identity (MIG-02); optional.
	Audit *audit.Log
	// Now returns the current time; defaults to time.Now.
	Now func() time.Time
}

// Registry stores agents. Agents are identified by (owner, name); management methods
// authorize the calling user through package access.
type Registry struct {
	store    *store.Store
	az       *access.Authorizer
	bus      *events.Bus
	presence *Presence
	audit    *audit.Log
	now      func() time.Time
	posts    *postLimiter
	creates  *postLimiter

	// allocMu serializes the creation and adoption of folder identities (ResolveFolder), so
	// concurrent sessions of one new folder end up with the same identity.
	allocMu sync.Mutex

	// appMu serializes the app of an AI: a live session sets it when it attaches, a tools-only
	// call only while nobody is connected, and the check and the write of the latter must not
	// interleave with an attach (AID-04).
	appMu sync.Mutex
	// afterAppCheck runs after a tools-only call found nobody connected and before it writes the
	// app; tests use it to attach a session in between. Nil in production.
	afterAppCheck func()

	// machines is the last sighting written per machine (touchMachine), guarded by machinesMu.
	machinesMu sync.Mutex
	machines   map[machineKey]machineSeen
}

// NewRegistry returns a Registry.
func NewRegistry(d Deps) *Registry {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Registry{
		store: d.Store, az: d.Access, bus: d.Bus, presence: d.Presence, audit: d.Audit, now: d.Now,
		posts: newPostLimiter(PostLimit, PostWindow, d.Now), creates: newPostLimiter(CreateLimit, CreateWindow, d.Now),
		machines: map[machineKey]machineSeen{},
	}
}

// Agent is an agent as seen by one user.
type Agent struct {
	db.Agent
	OwnerUsername    string
	OwnerDisplayName string
	// Handle, ProfileName and ProfileAvatar are the @handle, the display name and the avatar
	// blob of the agent's intelligence; empty for an agent without one.
	Handle        string
	ProfileName   string
	ProfileAvatar string
	// Level is manage for the owner and use for grantees.
	Level access.Level
	// Online tells whether the agent has a live connection.
	Online bool
}

// ByID returns the agent with the id the server resolved for a request (Principal.AgentID);
// ErrNotRegistered when it is gone.
func (r *Registry) ByID(ctx context.Context, id string) (db.Agent, error) {
	a, err := r.store.R().GetAgent(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Agent{}, ErrNotRegistered
	}
	return a, err
}

// Rooms lists the rooms the agent is a member of, with its cursor and the pause flag.
func (r *Registry) Rooms(ctx context.Context, agentID string) ([]db.ListAgentRoomStatesRow, error) {
	return r.store.R().ListAgentRoomStates(ctx, agentID)
}

// RoomState is a room an agent receives messages from, with its cursor, settings and pause
// flag.
type RoomState struct {
	db.ListAgentRoomStatesRow
}

// SessionRooms lists the agent's rooms by name: those it is a member of.
func (r *Registry) SessionRooms(ctx context.Context, agentID string) ([]RoomState, error) {
	direct, err := r.store.R().ListAgentRoomStates(ctx, agentID)
	if err != nil {
		return nil, err
	}
	out := make([]RoomState, 0, len(direct))
	for _, st := range direct {
		out = append(out, RoomState{ListAgentRoomStatesRow: st})
	}
	return out, nil
}

// Online reports whether the agent has a live connection.
func (r *Registry) Online(agentID string) bool { return r.presence.Online(agentID) }

// AllowCreate records the creation of a conversation (a DM or a group) by the agent; false means
// it already created CreateLimit within CreateWindow (DM-04, AGRP-01).
func (r *Registry) AllowCreate(agentID string) bool { return r.creates.allow(agentID) }

// AllowPost records a post by the agent; false means the agent already posted PostLimit
// messages within PostWindow (TOOL-08).
func (r *Registry) AllowPost(agentID string) bool { return r.posts.allow(agentID) }

// authorize requires the user to hold want on the agent: no access is access.ErrNotFound,
// use without manage is access.ErrForbidden.
func (r *Registry) authorize(ctx context.Context, p authn.Principal, agentID string, want access.Level) (access.Level, error) {
	if p.Kind != authn.KindUser {
		return access.None, access.ErrForbidden
	}
	l, err := r.az.AgentLevel(ctx, p.UserID, agentID)
	if err != nil {
		return access.None, err
	}
	if !access.AtLeast(l, dto.LevelUse) {
		return access.None, access.ErrNotFound
	}
	if !access.AtLeast(l, want) {
		return l, access.ErrForbidden
	}
	return l, nil
}

// Get returns an agent the user owns or may use.
func (r *Registry) Get(ctx context.Context, p authn.Principal, agentID string) (Agent, error) {
	l, err := r.authorize(ctx, p, agentID, dto.LevelUse)
	if err != nil {
		return Agent{}, err
	}
	row, err := r.store.R().GetAgentWithOwner(ctx, agentID)
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, access.ErrNotFound
	}
	if err != nil {
		return Agent{}, err
	}
	return r.view(row.Agent, row.OwnerUsername, row.OwnerDisplayName, row.Handle, row.ProfileName, row.ProfileAvatar, l), nil
}

func (r *Registry) view(a db.Agent, ownerUsername, ownerDisplay, handle, profileName, profileAvatar string, l access.Level) Agent {
	return Agent{
		Agent: a, OwnerUsername: ownerUsername, OwnerDisplayName: ownerDisplay,
		Handle: handle, ProfileName: profileName, ProfileAvatar: profileAvatar,
		Level: l, Online: r.presence.Online(a.ID),
	}
}

// ListVisible returns the agents the user owns plus those shared with the user (use grants,
// own or everyone), by name.
func (r *Registry) ListVisible(ctx context.Context, p authn.Principal) ([]Agent, error) {
	if p.Kind != authn.KindUser {
		return nil, access.ErrForbidden
	}
	rows, err := r.store.R().ListAgentsForUser(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	out := make([]Agent, 0, len(rows))
	for _, row := range rows {
		l := dto.LevelUse
		if row.Agent.OwnerID == p.UserID {
			l = dto.LevelManage
		}
		out = append(out, r.view(row.Agent, row.OwnerUsername, row.OwnerDisplayName, row.Handle, row.ProfileName, row.ProfileAvatar, l))
	}
	return out, nil
}

// AgentUpdate changes an agent; nil fields are unchanged.
type AgentUpdate struct {
	Description *string
	// DisplayName renames the agent (ID-04): 1 to 64 characters after trimming.
	DisplayName *string
	// Fixed is accepted for clients from before folders: every AI is permanent, so true changes
	// nothing and false is refused.
	Fixed *bool
}

// Update changes the agent's description or display name; requires ownership (manage). An
// invalid field changes nothing (422).
func (r *Registry) Update(ctx context.Context, p authn.Principal, agentID string, u AgentUpdate) (Agent, error) {
	if _, err := r.authorize(ctx, p, agentID, dto.LevelManage); err != nil {
		return Agent{}, err
	}
	check := validation.Check{}
	if u.Description != nil && utf8.RuneCountInString(*u.Description) > MaxDescriptionLength {
		check.Add("description", "must have at most 1000 characters")
	}
	var display string
	if u.DisplayName != nil {
		display = strings.TrimSpace(*u.DisplayName)
		if n := utf8.RuneCountInString(display); n == 0 || n > dto.MaxAgentDisplayName {
			check.Add("display_name", "must have 1 to 64 characters")
		}
	}
	if u.Fixed != nil && !*u.Fixed {
		check.Add("fixed", "can only be true: an agent is permanent")
	}
	if err := check.Err(); err != nil {
		return Agent{}, err
	}
	var roomIDs []string
	var renamed bool
	err := r.store.WithTx(ctx, func(q *db.Queries) error {
		if u.Description != nil {
			if err := q.UpdateAgentDescription(ctx, db.UpdateAgentDescriptionParams{Description: *u.Description, ID: agentID}); err != nil {
				return err
			}
		}
		if u.DisplayName != nil {
			var err error
			if renamed, err = intelligences.SetDisplayName(ctx, q, agentID, display, r.now()); err != nil {
				return err
			}
			if roomIDs, err = q.ListAgentRoomIDs(ctx, agentID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Agent{}, err
	}
	if renamed {
		intelligences.Announce(ctx, r.bus, r.store.R(), agentID, false)
	}
	r.membersChanged(roomIDs) // member names changed
	return r.Get(ctx, p, agentID)
}

// membersChanged publishes MembersChanged once per room.
func (r *Registry) membersChanged(roomIDs []string) {
	seen := map[string]bool{}
	for _, id := range roomIDs {
		if !seen[id] {
			seen[id] = true
			r.bus.Publish(events.MembersChanged{RoomID: id})
		}
	}
}

// Delete removes the agent with its room memberships, grants and cursors and closes its
// live connection; requires ownership (manage). Its direct messages go with it (DM-08): the
// intelligence's deletion cascades to every room that names it as a participant. The platform
// queue goes too (its rows cascade, CID-24), and the connections the agent operated have no
// operator afterwards: the bus tells the connector manager, which stops them and shows what to
// do (CID-02).
func (r *Registry) Delete(ctx context.Context, p authn.Principal, agentID string) error {
	if _, err := r.authorize(ctx, p, agentID, dto.LevelManage); err != nil {
		return err
	}
	var roomIDs, orphaned []string
	err := r.store.WithTx(ctx, func(q *db.Queries) error {
		roomIDs, orphaned = nil, nil
		// Read before the deletion: the foreign key empties operator_iid with it.
		operated, err := q.ListConnectorsOperatedBy(ctx, sql.NullString{String: agentID, Valid: true})
		if err != nil {
			return err
		}
		for _, c := range operated {
			orphaned = append(orphaned, c.ID)
		}
		if roomIDs, err = q.DeleteAgentMemberships(ctx, agentID); err != nil {
			return err
		}
		if err := q.DeleteGrants(ctx, db.DeleteGrantsParams{ResourceKind: string(access.KindAgent), ResourceID: agentID}); err != nil {
			return err
		}
		return q.DeleteAgent(ctx, agentID) // cursors cascade
	})
	if err != nil {
		return err
	}
	r.presence.Kick(ctx, agentID, CloseDeleted, ReasonDeleted)
	if len(orphaned) > 0 {
		r.bus.Publish(events.ConnectorsOrphaned{IDs: orphaned})
	}
	for _, id := range roomIDs {
		if _, err := r.store.R().GetRoom(ctx, id); errors.Is(err, sql.ErrNoRows) {
			r.bus.Publish(events.RoomChanged{RoomID: id, Deleted: true}) // a DM of the agent
			continue
		}
		r.bus.Publish(events.MembersChanged{RoomID: id})
	}
	return nil
}

// Sharing returns the agent's visibility preset; requires use.
func (r *Registry) Sharing(ctx context.Context, p authn.Principal, agentID string) (access.Visibility, error) {
	if _, err := r.authorize(ctx, p, agentID, dto.LevelUse); err != nil {
		return access.Visibility{}, err
	}
	return r.az.Sharing(ctx, access.KindAgent, agentID)
}

// SetSharing replaces the agent's use grants with a visibility preset (ACL-07); requires
// ownership (manage).
func (r *Registry) SetSharing(ctx context.Context, p authn.Principal, agentID string, v access.Visibility) (access.Visibility, error) {
	if _, err := r.authorize(ctx, p, agentID, dto.LevelManage); err != nil {
		return access.Visibility{}, err
	}
	a, err := r.store.R().GetAgent(ctx, agentID)
	if err != nil {
		return access.Visibility{}, err
	}
	if err := r.az.SetVisibility(ctx, p.UserID, access.KindAgent, agentID, a.OwnerID, v); err != nil {
		return access.Visibility{}, err
	}
	return r.az.Sharing(ctx, access.KindAgent, agentID)
}

func nullMillis(t time.Time) sql.NullInt64 { return sql.NullInt64{Int64: t.UnixMilli(), Valid: true} }
