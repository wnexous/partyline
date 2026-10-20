// Package agents is the server side of Claude Code sessions: the agent registry (agents
// register on their first WebSocket connection), their presence and the agent gateway.
package agents

import (
	"context"
	"database/sql"
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

// ErrNotRegistered means an agent token named an agent that never connected (403).
var ErrNotRegistered = errors.New("agent not registered")

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
	now      func() time.Time
	posts    *postLimiter
}

// NewRegistry returns a Registry.
func NewRegistry(d Deps) *Registry {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Registry{
		store: d.Store, az: d.Access, bus: d.Bus, presence: d.Presence, now: d.Now,
		posts: newPostLimiter(PostLimit, PostWindow, d.Now),
	}
}

// Agent is an agent as seen by one user.
type Agent struct {
	db.Agent
	OwnerUsername    string
	OwnerDisplayName string
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
		if err := q.InsertAgentIfMissing(ctx, db.InsertAgentIfMissingParams{
			ID: store.NewID(), OwnerID: ownerID, Name: name, CreatedAt: r.now().UnixMilli(),
		}); err != nil {
			return err
		}
		var err error
		out, err = q.GetAgentByName(ctx, db.GetAgentByNameParams{OwnerID: ownerID, Name: name})
		return err
	})
	return out, err
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

// Rooms lists the rooms the agent is a member of, with its cursor and the pause flag.
func (r *Registry) Rooms(ctx context.Context, agentID string) ([]db.ListAgentRoomStatesRow, error) {
	return r.store.R().ListAgentRoomStates(ctx, agentID)
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
	return r.view(row.Agent, row.OwnerUsername, row.OwnerDisplayName, l), nil
}

func (r *Registry) view(a db.Agent, ownerUsername, ownerDisplay string, l access.Level) Agent {
	return Agent{Agent: a, OwnerUsername: ownerUsername, OwnerDisplayName: ownerDisplay, Level: l, Online: r.presence.Online(a.ID)}
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
		out = append(out, r.view(row.Agent, row.OwnerUsername, row.OwnerDisplayName, l))
	}
	return out, nil
}

// UpdateDescription changes the agent's description; requires ownership (manage).
func (r *Registry) UpdateDescription(ctx context.Context, p authn.Principal, agentID, description string) (Agent, error) {
	if _, err := r.authorize(ctx, p, agentID, dto.LevelManage); err != nil {
		return Agent{}, err
	}
	if utf8.RuneCountInString(description) > MaxDescriptionLength {
		return Agent{}, &validation.Error{Fields: map[string]string{"description": "must have at most 1000 characters"}}
	}
	err := r.store.WithTx(ctx, func(q *db.Queries) error {
		return q.UpdateAgentDescription(ctx, db.UpdateAgentDescriptionParams{Description: description, ID: agentID})
	})
	if err != nil {
		return Agent{}, err
	}
	return r.Get(ctx, p, agentID)
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
