// Package agents is the server side of Claude Code sessions: the agent registry (agents
// register on their first WebSocket connection), their presence and the agent gateway.
package agents

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
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
	"github.com/wnexous/partyline/internal/orgs"
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

// ErrNoOrganization answers the first session of a folder whose host belongs to no organization: there is
// nowhere to put its AI (422 organization_required, ORG-04). Nothing is created.
var ErrNoOrganization = &httpx.Error{
	Status: http.StatusUnprocessableEntity, Code: dto.ErrorCodeOrganizationRequired,
	Message: "you belong to no organization: create or join one first",
	Fields:  map[string]string{"org": "you belong to no organization"},
}

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
	// Logger receives the failures of best-effort work; defaults to slog.Default().
	Logger *slog.Logger
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
	log      *slog.Logger
	now      func() time.Time
	posts    *postLimiter
	creates  *postLimiter
	newChats *postLimiter

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
	// afterFolderMiss runs when a session found no AI for its folder and before it creates or adopts
	// one; tests use it to let another session create the AI in between. Nil in production.
	afterFolderMiss func()

	// machines is the last sighting written per machine (touchMachine), guarded by machinesMu.
	machinesMu sync.Mutex
	machines   map[machineKey]machineSeen
}

// NewRegistry returns a Registry.
func NewRegistry(d Deps) *Registry {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &Registry{
		store: d.Store, az: d.Access, bus: d.Bus, presence: d.Presence, audit: d.Audit, log: d.Logger, now: d.Now,
		posts: newPostLimiter(PostLimit, PostWindow, d.Now), creates: newPostLimiter(CreateLimit, CreateWindow, d.Now),
		newChats: newPostLimiter(NewChatLimit, NewChatWindow, d.Now),
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
	// OrgName and OrgSlug name the AI's home organization; empty when it has none.
	OrgName, OrgSlug string
	// Home is the home folder of the host's machine the AI lives on, as the machine reported it; empty
	// when unknown. It lets a folder read "~/x".
	Home string
	// Level is manage for whoever manages the agent in an organization they share with it, use for
	// whoever only sees it.
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

// AllowNewChat records a chat the agent starts on one of its connections; false means it already started
// NewChatLimit within NewChatWindow (CID-31).
func (r *Registry) AllowNewChat(agentID string) bool { return r.newChats.allow(agentID) }

// AllowPost records a post by the agent; false means the agent already posted PostLimit
// messages within PostWindow (TOOL-08).
func (r *Registry) AllowPost(agentID string) bool { return r.posts.allow(agentID) }

// authorize requires the user to hold want on the agent (AD-016): use is seeing it - hosting it, or sharing
// an organization with it - and manage is managing it in an organization they share with it (POS-03).
// No access is access.ErrNotFound; use without manage is access.ErrForbidden.
func (r *Registry) authorize(ctx context.Context, p authn.Principal, agentID string, want access.Level) (access.Level, error) {
	if p.Kind != authn.KindUser {
		return access.None, access.ErrForbidden
	}
	see, err := r.az.CanSeeAI(ctx, p, agentID)
	if err != nil {
		return access.None, err
	}
	if !see {
		return access.None, access.ErrNotFound
	}
	l := dto.LevelUse
	managed, err := r.az.Authz().ManagesAnywhere(ctx, p.UserID, agentID)
	if err != nil {
		return access.None, err
	}
	if managed {
		l = dto.LevelManage
	}
	if !access.AtLeast(l, want) {
		return l, access.ErrForbidden
	}
	return l, nil
}

// Get returns an agent the user hosts or shares an organization with.
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
	a := r.view(row.Agent, row.OwnerUsername, row.OwnerDisplayName, row.Handle, row.ProfileName, row.ProfileAvatar, l)
	a.OrgName, a.OrgSlug, a.Home = row.OrgName, row.OrgSlug, row.MachineHome
	return a, nil
}

func (r *Registry) view(a db.Agent, ownerUsername, ownerDisplay, handle, profileName, profileAvatar string, l access.Level) Agent {
	return Agent{
		Agent: a, OwnerUsername: ownerUsername, OwnerDisplayName: ownerDisplay,
		Handle: handle, ProfileName: profileName, ProfileAvatar: profileAvatar,
		Level: l, Online: r.presence.Online(a.ID),
	}
}

// ListVisible returns the agents the user hosts plus those of the organizations they belong to, by name,
// each at manage when the user manages it in an organization they share with it and at use otherwise.
func (r *Registry) ListVisible(ctx context.Context, p authn.Principal) ([]Agent, error) {
	if p.Kind != authn.KindUser {
		return nil, access.ErrForbidden
	}
	rows, err := r.store.R().ListVisibleAgents(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	managed, err := r.managedBy(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	out := make([]Agent, 0, len(rows))
	for _, row := range rows {
		l := dto.LevelUse
		if managed[row.Agent.ID] {
			l = dto.LevelManage
		}
		a := r.view(row.Agent, row.OwnerUsername, row.OwnerDisplayName, row.Handle, row.ProfileName, row.ProfileAvatar, l)
		a.OrgName, a.OrgSlug, a.Home = row.OrgName, row.OrgSlug, row.MachineHome
		out = append(out, a)
	}
	return out, nil
}

// managedBy is the set of identities the user manages in the organizations they belong to (POS-03),
// reading the chart of each organization once.
func (r *Registry) managedBy(ctx context.Context, iid string) (map[string]bool, error) {
	memberships, err := r.store.R().ListOrgsOfMember(ctx, iid)
	if err != nil {
		return nil, err
	}
	z := r.az.Authz().Session()
	out := map[string]bool{}
	for _, m := range memberships {
		chart, err := z.Chart(ctx, m.Organization.ID)
		if err != nil {
			return nil, err
		}
		for id := range chart.ManagedBy(iid) {
			out[id] = true
		}
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

// Update changes the agent's description or display name; requires managing it (POS-04). An
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

// Delete deletes the AI for good in its home organization (POS-05, POS-06): the right is the organization's
// (authorizeDelete), decided in the transaction that deletes. The AI goes with its memberships in every
// organization and room, and its cursors. Its direct messages go with it (DM-08): the intelligence's
// deletion cascades to every room that names it as a participant. The platform queue goes too (its rows cascade,
// CID-24), and the connections the agent operated have no operator afterwards: the bus tells the connector
// manager, which stops them and shows what to do (CID-02). The AI that is the only admin of an organization is
// not deleted (orgs.ErrLastAdmin, OMEM-05). Afterwards its live session ends as that of a deleted AI and every
// organization it belonged to is told its chart changed: the AIs and guests it created or invited lose that link.
//
// The commit is the end of everything that can fail: once the row is gone the delete has happened, and what
// follows (afterDelete) is best effort that is logged and never returned, so a delete that took effect answers
// success (204) whatever the live session, the subscribers or the request do after it.
func (r *Registry) Delete(ctx context.Context, p authn.Principal, agentID string) error {
	var gone deleted
	err := r.store.WithTx(ctx, func(q *db.Queries) error {
		gone = deleted{}
		a, err := authorizeDelete(ctx, q, p, agentID)
		if err != nil {
			return err
		}
		alone, err := q.ListLastAdminOrgIDs(ctx, agentID)
		if err != nil {
			return err
		}
		if len(alone) > 0 {
			return orgs.ErrLastAdmin
		}
		memberships, err := q.ListOrgsOfMember(ctx, agentID)
		if err != nil {
			return err
		}
		for _, m := range memberships {
			gone.orgIDs = append(gone.orgIDs, m.Organization.ID)
		}
		// Read before the deletion: the foreign key empties operator_iid with it.
		operated, err := q.ListConnectorsOperatedBy(ctx, sql.NullString{String: agentID, Valid: true})
		if err != nil {
			return err
		}
		for _, c := range operated {
			gone.connectionIDs = append(gone.connectionIDs, c.ID)
		}
		if r.audit != nil {
			profile, err := q.GetIntelligence(ctx, agentID) // an AI of a fixture may have none
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err := r.audit.Record(ctx, q, audit.Entry{
				ActorID: p.IID(), Action: audit.ActionAgentDelete, TargetKind: audit.TargetAgent, TargetID: agentID,
				Detail: map[string]any{"handle": profile.Handle, "org": a.OrgID.String},
			}); err != nil {
				return err
			}
		}
		memberOf, err := q.DeleteAgentMemberships(ctx, agentID)
		if err != nil {
			return err
		}
		if err := q.DeleteAgent(ctx, agentID); err != nil { // cursors cascade
			return err
		}
		// Which of its rooms went with it (its DMs) is read here, once it is deleted, so that the cleanup after the
		// commit reads nothing: a room cannot be mistaken for a surviving one because a read failed.
		for _, id := range memberOf {
			_, err := q.GetRoom(ctx, id)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				gone.dmIDs = append(gone.dmIDs, id)
			case err != nil:
				return err
			default:
				gone.groupIDs = append(gone.groupIDs, id)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	r.afterDelete(ctx, agentID, gone)
	return nil
}

// deleted is what the transaction of Delete found out about the AI it deleted, for the cleanup that follows.
type deleted struct {
	orgIDs        []string // the organizations it belonged to
	groupIDs      []string // the rooms it was a member of that stay: their member list changed
	dmIDs         []string // the rooms it was a member of that went with it
	connectionIDs []string // the connections it operated, which have no operator now
}

// afterDelete is the cleanup of an AI whose row is gone: its live session ends as that of a deleted AI and the
// clients and the connector manager are told what changed. It has no result and touches no database: the delete
// has happened, so a step that fails is logged, the steps after it still run, and nothing here can be turned
// into an error for the caller (a panic of a session included).
func (r *Registry) afterDelete(ctx context.Context, agentID string, d deleted) {
	r.bestEffort(ctx, agentID, "end the live session", func() {
		r.presence.Drop(agentID, CloseDeleted, ReasonDeleted)
	})
	r.bestEffort(ctx, agentID, "announce the deletion", func() {
		if len(d.connectionIDs) > 0 {
			r.bus.Publish(events.ConnectorsOrphaned{IDs: d.connectionIDs})
		}
		for _, id := range d.groupIDs {
			r.bus.Publish(events.MembersChanged{RoomID: id})
		}
		for _, id := range d.dmIDs {
			r.bus.Publish(events.RoomChanged{RoomID: id, Deleted: true})
		}
		for _, id := range d.orgIDs {
			r.bus.Publish(events.OrgChanged{OrgID: id})
		}
	})
}

// bestEffort runs a step of work that follows a change that is already committed. A step that panics is logged and
// recovered, so that it neither fails the caller nor skips the steps after it. Like the API's recoverer, it logs
// the recovered value.
func (r *Registry) bestEffort(ctx context.Context, agentID, step string, fn func()) {
	defer func() {
		if v := recover(); v != nil {
			r.log.ErrorContext(ctx, "agents: cleanup after a delete failed", "agent_id", agentID, "step", step, "panic", v)
		}
	}()
	fn()
}

func nullMillis(t time.Time) sql.NullInt64 { return sql.NullInt64{Int64: t.UnixMilli(), Valid: true} }
