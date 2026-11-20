package orgs

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// Errors of the organization checks. They are the sentinels of the whole authorization layer: package
// access re-exports these same values as access.ErrNotFound and access.ErrForbidden (it imports this
// package, never the reverse), so the API maps an organization check and a room check alike.
var (
	// ErrNotFound means the organization or the resource does not exist or the caller cannot see it:
	// an outsider learns nothing (404).
	ErrNotFound = errors.New("not found")
	// ErrForbidden means the caller can see the organization but not do the action (403).
	ErrForbidden = errors.New("forbidden")
)

// Load reads the members of one organization with their permission (AD-017). q may be the queries of a
// transaction, so a rule is checked on the state that transaction sees (OMEM-05). An organization that
// does not exist loads as an empty roster.
func Load(ctx context.Context, q *db.Queries, orgID string) (*Roster, error) {
	members, err := q.ListOrgRosterMembers(ctx, orgID)
	if err != nil {
		return nil, err
	}
	r := &Roster{OrgID: orgID, Members: make(map[string]Member, len(members))}
	for _, m := range members {
		r.Members[m.Iid] = Member{IID: m.Iid, Permission: Permission(m.Permission)}
	}
	return r, nil
}

// Links are the explicit links of one AI (SR2-06, SR2-07): the user who hosts it and the identities that
// instruct it besides the host.
type Links struct {
	// Host is the user who hosts the AI (agents.owner_id).
	Host string
	// instructors maps each instructor to whether it is a user (true) or an AI (false).
	instructors map[string]bool
}

// LoadLinks reads the links of an AI; ok is false when agentID is no AI. q may belong to a transaction.
func LoadLinks(ctx context.Context, q *db.Queries, agentID string) (Links, bool, error) {
	host, err := q.GetAgentHost(ctx, agentID)
	if errors.Is(err, sql.ErrNoRows) {
		return Links{}, false, nil
	}
	if err != nil {
		return Links{}, false, err
	}
	rows, err := q.ListAgentInstructorKinds(ctx, agentID)
	if err != nil {
		return Links{}, false, err
	}
	l := Links{Host: host, instructors: make(map[string]bool, len(rows))}
	for _, r := range rows {
		l.instructors[r.InstructorIid] = r.Kind != "ai"
	}
	return l, true, nil
}

// Instructs reports whether iid instructs the AI: its host or one of its instructors (SR2-07). Their
// messages are the AI's trusted ones.
func (l Links) Instructs(iid string) bool {
	if iid == "" {
		return false
	}
	if iid == l.Host {
		return true
	}
	_, ok := l.instructors[iid]
	return ok
}

// Manages reports whether iid may edit the AI's profile and settings, end or arrange its sessions and
// answer its invitations (SR2-13): its host and its instructors that are users.
func (l Links) Manages(iid string) bool {
	return iid != "" && (iid == l.Host || l.instructors[iid])
}

// Instructors lists the instructors besides the host, by id.
func (l Links) Instructors() []string {
	out := make([]string, 0, len(l.instructors))
	for id := range l.instructors {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Authz answers every authorization question about organizations and AIs: membership, the admin
// permission, who instructs, manages and may delete an AI. Services ask it instead of reading the tables
// themselves.
//
// Nothing is cached across requests, so an answer is never stale (AD-017); an operation that asks
// several questions uses Session to read each roster and each AI's links once.
type Authz struct {
	store *store.Store
	loads atomic.Int64
}

// NewAuthz returns the authorization facade over st.
func NewAuthz(st *store.Store) *Authz { return &Authz{store: st} }

// Loads is the number of rosters and links the facade has read since it was created, sessions included.
// Tests compare it before and after an operation to prove how many times the operation read them.
func (z *Authz) Loads() int64 { return z.loads.Load() }

// Roster reads the current members of an organization.
func (z *Authz) Roster(ctx context.Context, orgID string) (*Roster, error) {
	z.loads.Add(1)
	return Load(ctx, z.store.R(), orgID)
}

// Links reads the links of an AI; ok is false when agentID is no AI.
func (z *Authz) Links(ctx context.Context, agentID string) (Links, bool, error) {
	z.loads.Add(1)
	var (
		l  Links
		ok bool
	)
	err := z.store.ReadTx(ctx, func(q *db.Queries) (err error) {
		l, ok, err = LoadLinks(ctx, q, agentID)
		return err
	})
	return l, ok, err
}

// Member returns the membership of iid in the organization, and whether there is one.
func (z *Authz) Member(ctx context.Context, orgID, iid string) (Member, bool, error) {
	return memberOf(ctx, z.store.R(), orgID, iid)
}

// memberOf reads one membership with the given queries, which may be a transaction's.
func memberOf(ctx context.Context, q *db.Queries, orgID, iid string) (Member, bool, error) {
	m, err := q.GetOrgRosterMember(ctx, db.GetOrgRosterMemberParams{OrgID: orgID, Iid: iid})
	if errors.Is(err, sql.ErrNoRows) {
		return Member{}, false, nil
	}
	if err != nil {
		return Member{}, false, err
	}
	return Member{IID: m.Iid, Permission: Permission(m.Permission)}, true, nil
}

// RequireMember is ErrNotFound unless iid is a member of the organization, with any permission.
func (z *Authz) RequireMember(ctx context.Context, orgID, iid string) error {
	_, ok, err := z.Member(ctx, orgID, iid)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

// RequireAdmin is ErrNotFound for an outsider and ErrForbidden for a member who is not an admin
// (OMEM-04).
func (z *Authz) RequireAdmin(ctx context.Context, orgID, iid string) error {
	return requireAdmin(ctx, z.store.R(), orgID, iid)
}

// requireAdmin is RequireAdmin with the given queries; the services call it inside the write
// transaction that changes the organization, so the check and the change cannot interleave with a
// demotion.
func requireAdmin(ctx context.Context, q *db.Queries, orgID, iid string) error {
	m, ok, err := memberOf(ctx, q, orgID, iid)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if m.Permission != PermissionAdmin {
		return ErrForbidden
	}
	return nil
}

// Trusts reports whether the message of authorIID is trusted by the AI agentID: the author is the AI's host
// or one of its instructors (SR2-07). Whatever organizations either belongs to changes nothing.
func (z *Authz) Trusts(ctx context.Context, authorIID, agentID string) (bool, error) {
	return trusts(ctx, z, authorIID, agentID)
}

// Manages reports whether actor manages the AI target (SR2-13): its host or a user that instructs it.
// Nobody manages a user or themselves.
func (z *Authz) Manages(ctx context.Context, actor, target string) (bool, error) {
	return manages(ctx, z, actor, target)
}

// CanDeleteAI reports whether actor may delete the AI (SR2-12): its host, or an admin of its home
// organization.
func (z *Authz) CanDeleteAI(ctx context.Context, actor, agentID string) (bool, error) {
	return CanDeleteAI(ctx, z.store.R(), actor, agentID)
}

// CanDeleteAI is Authz.CanDeleteAI with the given queries, which may be a transaction's.
func CanDeleteAI(ctx context.Context, q *db.Queries, actor, agentID string) (bool, error) {
	if actor == "" || agentID == "" || actor == agentID {
		return false, nil
	}
	host, err := q.GetAgentHost(ctx, agentID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if host == actor {
		return true, nil
	}
	home, err := q.GetAgentOrg(ctx, agentID)
	if err != nil || home == "" {
		return false, err
	}
	m, ok, err := memberOf(ctx, q, home, actor)
	return ok && m.Permission == PermissionAdmin, err
}

// SharesOrg reports whether a and b are members of a common organization.
func (z *Authz) SharesOrg(ctx context.Context, a, b string) (bool, error) {
	n, err := z.store.R().CountSharedOrgs(ctx, db.CountSharedOrgsParams{FirstIid: a, SecondIid: b})
	return n > 0, err
}

// Session returns a view of the facade for one operation: a dispatch, a request, a page. It reads the
// roster of each organization and the links of each AI it is asked about once, and keeps them for as long
// as the operation lasts. Never keep a session across requests.
func (z *Authz) Session() *Session {
	return &Session{z: z, rosters: map[string]*Roster{}, links: map[string]linksEntry{}}
}

type linksEntry struct {
	l  Links
	ok bool
}

// Session is an Authz memoized for one operation (AD-017). It has the same questions as Authz and is
// safe for concurrent use.
type Session struct {
	z       *Authz
	mu      sync.Mutex
	rosters map[string]*Roster
	links   map[string]linksEntry
}

// Roster returns the members of the organization, read at most once per session.
func (s *Session) Roster(ctx context.Context, orgID string) (*Roster, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.rosters[orgID]; ok {
		return r, nil
	}
	r, err := s.z.Roster(ctx, orgID)
	if err != nil {
		return nil, err
	}
	s.rosters[orgID] = r
	return r, nil
}

// Links returns the links of an AI, read at most once per session.
func (s *Session) Links(ctx context.Context, agentID string) (Links, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.links[agentID]; ok {
		return e.l, e.ok, nil
	}
	l, ok, err := s.z.Links(ctx, agentID)
	if err != nil {
		return Links{}, false, err
	}
	s.links[agentID] = linksEntry{l: l, ok: ok}
	return l, ok, nil
}

// Member, RequireMember, RequireAdmin, CanDeleteAI and SharesOrg read a row or two: a session adds nothing.

// Member is Authz.Member.
func (s *Session) Member(ctx context.Context, orgID, iid string) (Member, bool, error) {
	return s.z.Member(ctx, orgID, iid)
}

// RequireMember is Authz.RequireMember.
func (s *Session) RequireMember(ctx context.Context, orgID, iid string) error {
	return s.z.RequireMember(ctx, orgID, iid)
}

// RequireAdmin is Authz.RequireAdmin.
func (s *Session) RequireAdmin(ctx context.Context, orgID, iid string) error {
	return s.z.RequireAdmin(ctx, orgID, iid)
}

// SharesOrg is Authz.SharesOrg.
func (s *Session) SharesOrg(ctx context.Context, a, b string) (bool, error) {
	return s.z.SharesOrg(ctx, a, b)
}

// CanDeleteAI is Authz.CanDeleteAI.
func (s *Session) CanDeleteAI(ctx context.Context, actor, agentID string) (bool, error) {
	return s.z.CanDeleteAI(ctx, actor, agentID)
}

// Trusts is Authz.Trusts over the session's links.
func (s *Session) Trusts(ctx context.Context, authorIID, agentID string) (bool, error) {
	return trusts(ctx, s, authorIID, agentID)
}

// Manages is Authz.Manages over the session's links.
func (s *Session) Manages(ctx context.Context, actor, target string) (bool, error) {
	return manages(ctx, s, actor, target)
}

// linkSource is what the questions read: the facade reads every time, a session remembers.
type linkSource interface {
	Links(ctx context.Context, agentID string) (Links, bool, error)
}

func trusts(ctx context.Context, src linkSource, authorIID, agentID string) (bool, error) {
	if authorIID == "" || agentID == "" || authorIID == agentID {
		return false, nil
	}
	l, ok, err := src.Links(ctx, agentID)
	if err != nil || !ok {
		return false, err
	}
	return l.Instructs(authorIID), nil
}

func manages(ctx context.Context, src linkSource, actor, target string) (bool, error) {
	if actor == "" || target == "" || actor == target {
		return false, nil
	}
	l, ok, err := src.Links(ctx, target)
	if err != nil || !ok {
		return false, err
	}
	return l.Manages(actor), nil
}

// homeOf returns the home organization of an AI; empty for anything else.
func homeOf(ctx context.Context, q *db.Queries, agentID string) (string, error) {
	org, err := q.GetAgentOrg(ctx, agentID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return org, err
}
