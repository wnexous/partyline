// Package agents is the server side of Claude Code sessions: the agent registry (agents
// register on their first WebSocket connection), their presence and the agent gateway.
package agents

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/handles"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// ErrNotRegistered means an agent token named an agent that never connected (403).
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

	// allocMu serializes the sessions whose agent name the server picks (Identity.Allocates),
	// from the name choice until the agent is online (WebSocket) or held (registration), so
	// two sessions never pick the same name (ID-01). It also serializes the creation and
	// adoption of folder identities (ResolveFolder).
	allocMu sync.Mutex

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
		posts: newPostLimiter(PostLimit, PostWindow, d.Now), machines: map[machineKey]machineSeen{},
	}
}

// Agent is an agent as seen by one user.
type Agent struct {
	db.Agent
	OwnerUsername    string
	OwnerDisplayName string
	// ProjectSlug and ProjectName describe the agent's project; empty without one.
	ProjectSlug string
	ProjectName string
	// Level is manage for the owner and use for grantees.
	Level access.Level
	// Online tells whether the agent has a live connection.
	Online bool
}

// Upsert returns the owner's agent named Slug(name), creating it on first use (AGT-01). It
// is idempotent per (owner, name).
func (r *Registry) Upsert(ctx context.Context, ownerID, name string) (db.Agent, error) {
	name = Slug(name)
	var out db.Agent
	err := r.store.WithTx(ctx, func(q *db.Queries) error {
		var err error
		out, err = q.GetAgentByName(ctx, db.GetAgentByNameParams{OwnerID: ownerID, Name: name})
		if !errors.Is(err, sql.ErrNoRows) {
			return err // an existing agent, or a failure
		}
		n, err := q.CountAgentsByOwner(ctx, ownerID)
		if err != nil {
			return err
		}
		if n >= MaxAgentsPerOwner {
			return ErrAgentLimit
		}
		if err := q.InsertAgentIfMissing(ctx, db.InsertAgentIfMissingParams{
			ID: store.NewID(), OwnerID: ownerID, Name: name, CreatedAt: r.now().UnixMilli(),
		}); err != nil {
			return err
		}
		out, err = q.GetAgentByName(ctx, db.GetAgentByNameParams{OwnerID: ownerID, Name: name})
		if err != nil {
			return err
		}
		return r.createIntelligence(ctx, q, out)
	})
	return out, err
}

// createIntelligence gives a newly inserted agent its intelligence (PROF-01): its name and
// handle derive from the agent name, which a later rename or readable session name updates.
func (r *Registry) createIntelligence(ctx context.Context, q *db.Queries, a db.Agent) error {
	_, err := intelligences.Create(ctx, q, intelligences.NewParams{
		ID: a.ID, Kind: handles.AI, DisplayName: delivery.AgentDisplayName(a), HandleBase: a.Name, Now: r.now(),
	})
	return err
}

// Register resolves the session's agent like a WebSocket connection would (Resolve: the
// agent cap applies) without bringing it online, and returns it with its owner. A tools-only
// channel uses it, since it never opens the WebSocket (PUSH-02).
func (r *Registry) Register(ctx context.Context, ownerID string, id Identity) (db.GetAgentWithOwnerRow, error) {
	if id.Allocates() {
		r.allocMu.Lock()
		defer r.allocMu.Unlock()
	}
	a, err := r.resolve(ctx, ownerID, id, false)
	if err != nil {
		return db.GetAgentWithOwnerRow{}, err
	}
	return r.store.R().GetAgentWithOwner(ctx, a.ID)
}

// Lookup returns the owner's agent named Slug(name) without creating it; ErrNotRegistered
// when it does not exist.
func (r *Registry) Lookup(ctx context.Context, ownerID, name string) (db.Agent, error) {
	a, err := r.store.R().GetAgentByName(ctx, db.GetAgentByNameParams{OwnerID: ownerID, Name: Slug(name)})
	if errors.Is(err, sql.ErrNoRows) {
		return db.Agent{}, ErrNotRegistered
	}
	return a, err
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
// flag: as a member, or through its project (ProjectID set; the cursor and settings are the
// project member's).
type RoomState struct {
	db.ListAgentRoomStatesRow
	ProjectID string
}

// SessionRooms lists the agent's rooms by name: those it is a member of and, while it is its
// project's active session, the rooms its project is a member of (PROJ-07). A room it is in
// both ways counts as a member room.
func (r *Registry) SessionRooms(ctx context.Context, agentID string) ([]RoomState, error) {
	q := r.store.R()
	direct, err := q.ListAgentRoomStates(ctx, agentID)
	if err != nil {
		return nil, err
	}
	out := make([]RoomState, 0, len(direct))
	for _, st := range direct {
		out = append(out, RoomState{ListAgentRoomStatesRow: st})
	}
	a, err := q.GetAgent(ctx, agentID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !a.ProjectID.Valid) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	active, ok, err := delivery.ActiveSession(ctx, q, r.presence.Online, a.ProjectID.String)
	if err != nil || !ok || active.ID != agentID {
		return out, err
	}
	viaProject, err := q.ListProjectRoomStates(ctx, db.ListProjectRoomStatesParams{ProjectID: a.ProjectID.String, AgentID: agentID})
	if err != nil {
		return nil, err
	}
	for _, st := range viaProject {
		out = append(out, RoomState{ListAgentRoomStatesRow: db.ListAgentRoomStatesRow(st), ProjectID: a.ProjectID.String})
	}
	slices.SortStableFunc(out, func(x, y RoomState) int {
		return cmp.Or(strings.Compare(x.Name, y.Name), strings.Compare(x.ID, y.ID))
	})
	return out, nil
}

// ActiveSession returns the project's active session (PROJ-07); ok is false when none of its
// agents is online.
func (r *Registry) ActiveSession(ctx context.Context, projectID string) (db.Agent, bool, error) {
	return delivery.ActiveSession(ctx, r.store.R(), r.presence.Online, projectID)
}

// Online reports whether the agent has a live connection.
func (r *Registry) Online(agentID string) bool { return r.presence.Online(agentID) }

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
	return r.view(row.Agent, row.OwnerUsername, row.OwnerDisplayName, row.ProjectSlug, row.ProjectName, l), nil
}

func (r *Registry) view(a db.Agent, ownerUsername, ownerDisplay, projectSlug, projectName string, l access.Level) Agent {
	return Agent{
		Agent: a, OwnerUsername: ownerUsername, OwnerDisplayName: ownerDisplay,
		ProjectSlug: projectSlug, ProjectName: projectName, Level: l, Online: r.presence.Online(a.ID),
	}
}

// DisplayName is the name people read for the agent (delivery.AgentDisplayName).
func DisplayName(a db.Agent) string { return delivery.AgentDisplayName(a) }

// ListVisible returns the agents the user owns plus those shared with the user (use grants,
// own or everyone), by name. Archived agents are left out unless includeArchived (PROJ-03).
func (r *Registry) ListVisible(ctx context.Context, p authn.Principal, includeArchived bool) ([]Agent, error) {
	if p.Kind != authn.KindUser {
		return nil, access.ErrForbidden
	}
	rows, err := r.store.R().ListAgentsForUser(ctx, db.ListAgentsForUserParams{UserID: p.UserID, IncludeArchived: includeArchived})
	if err != nil {
		return nil, err
	}
	out := make([]Agent, 0, len(rows))
	for _, row := range rows {
		l := dto.LevelUse
		if row.Agent.OwnerID == p.UserID {
			l = dto.LevelManage
		}
		out = append(out, r.view(row.Agent, row.OwnerUsername, row.OwnerDisplayName, row.ProjectSlug, row.ProjectName, l))
	}
	return out, nil
}

// AgentUpdate changes an agent; nil fields are unchanged.
type AgentUpdate struct {
	Description *string
	// DisplayName renames the agent (ID-04): 1 to 64 characters after trimming.
	DisplayName *string
	// Fixed promotes a temporary agent to fixed (ID-05); only true is accepted.
	Fixed *bool
}

// Update changes the agent's description, display name or kind; requires ownership
// (manage). An invalid field changes nothing (422).
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
		check.Add("fixed", "can only be true: a fixed agent stays fixed")
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
			if err := q.SetAgentDisplayName(ctx, db.SetAgentDisplayNameParams{DisplayName: display, ID: agentID}); err != nil {
				return err
			}
			var err error
			if renamed, err = intelligences.SetDisplayName(ctx, q, agentID, display, r.now()); err != nil {
				return err
			}
			if roomIDs, err = q.ListAgentRoomIDs(ctx, agentID); err != nil {
				return err
			}
		}
		if u.Fixed != nil {
			return q.PromoteAgent(ctx, agentID)
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
// live connection; requires ownership (manage).
func (r *Registry) Delete(ctx context.Context, p authn.Principal, agentID string) error {
	if _, err := r.authorize(ctx, p, agentID, dto.LevelManage); err != nil {
		return err
	}
	var roomIDs []string
	err := r.store.WithTx(ctx, func(q *db.Queries) error {
		var err error
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
	for _, id := range roomIDs {
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
