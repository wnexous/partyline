package orgs

import (
	"context"
	"database/sql"
	"errors"
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

// Load reads the chart of one organization with two queries: its positions and its members with
// their permission, position, inviter and, for an AI, creator (AD-017). q may be the queries of a
// transaction, so a rule is checked on the state that transaction sees (POS-02, OMEM-05). An
// organization that does not exist loads as an empty chart.
func Load(ctx context.Context, q *db.Queries, orgID string) (*Chart, error) {
	positions, err := q.ListOrgPositions(ctx, orgID)
	if err != nil {
		return nil, err
	}
	members, err := q.ListOrgChartMembers(ctx, orgID)
	if err != nil {
		return nil, err
	}
	c := &Chart{OrgID: orgID, Positions: make(map[string]Position, len(positions)), Members: make(map[string]Member, len(members))}
	for _, p := range positions {
		c.Positions[p.ID] = Position{ID: p.ID, Name: p.Name, ParentID: p.ParentID}
	}
	for _, m := range members {
		c.Members[m.Iid] = chartMember(m.Iid, m.Permission, m.PositionID, m.InviterIid, m.CreatorIid)
	}
	return c, nil
}

func chartMember(iid, permission, position, inviter, creator string) Member {
	return Member{IID: iid, Permission: Permission(permission), PositionID: position, InviterIID: inviter, CreatorIID: creator}
}

// Authz answers every authorization question about organizations: membership, the admin permission,
// who manages whom and who is trusted. Services ask it instead of reading the tables themselves.
//
// Nothing is cached across requests, so an answer is never stale (AD-017); an operation that asks
// several questions uses Session to read each chart once.
type Authz struct {
	store *store.Store
	loads atomic.Int64
}

// NewAuthz returns the authorization facade over st.
func NewAuthz(st *store.Store) *Authz { return &Authz{store: st} }

// Loads is the number of charts the facade has read since it was created, sessions included. Tests
// compare it before and after an operation to prove how many times the operation read a chart.
func (z *Authz) Loads() int64 { return z.loads.Load() }

// Chart reads the current chart of an organization, in one read snapshot.
func (z *Authz) Chart(ctx context.Context, orgID string) (*Chart, error) {
	z.loads.Add(1)
	var c *Chart
	err := z.store.ReadTx(ctx, func(q *db.Queries) (err error) {
		c, err = Load(ctx, q, orgID)
		return err
	})
	return c, err
}

// Member returns the membership of iid in the organization, and whether there is one.
func (z *Authz) Member(ctx context.Context, orgID, iid string) (Member, bool, error) {
	return memberOf(ctx, z.store.R(), orgID, iid)
}

// memberOf reads one membership with the given queries, which may be a transaction's.
func memberOf(ctx context.Context, q *db.Queries, orgID, iid string) (Member, bool, error) {
	m, err := q.GetOrgChartMember(ctx, db.GetOrgChartMemberParams{OrgID: orgID, Iid: iid})
	if errors.Is(err, sql.ErrNoRows) {
		return Member{}, false, nil
	}
	if err != nil {
		return Member{}, false, err
	}
	return chartMember(m.Iid, m.Permission, m.PositionID, m.InviterIid, m.CreatorIid), true, nil
}

// RequireMember is ErrNotFound unless iid is a member of the organization, with any permission:
// guests see the members and the chart (CHART-02).
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

// RequireAdmin is ErrNotFound for an outsider and ErrForbidden for a member or guest who is not an
// admin (OMEM-04).
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

// Manages reports whether actor manages target in the organization (POS-03).
func (z *Authz) Manages(ctx context.Context, actor, target, orgID string) (bool, error) {
	return manages(ctx, z, actor, target, orgID)
}

// ManagesAnywhere reports whether actor manages target in any organization they are both members of.
func (z *Authz) ManagesAnywhere(ctx context.Context, actor, target string) (bool, error) {
	return managesAnywhere(ctx, z, actor, target)
}

// CanDelete reports whether actor may delete target in the organization (POS-05).
func (z *Authz) CanDelete(ctx context.Context, actor, target, orgID string) (bool, error) {
	return canDelete(ctx, z, actor, target, orgID)
}

// Trusts reports whether the message of authorIID is trusted by the AI agentID: the author manages
// the AI in the AI's home organization, and in no other (POS-10). An AI without a home organization
// trusts nobody.
func (z *Authz) Trusts(ctx context.Context, authorIID, agentID string) (bool, error) {
	return trusts(ctx, z, authorIID, agentID)
}

// SharesOrg reports whether a and b are members of a common organization.
func (z *Authz) SharesOrg(ctx context.Context, a, b string) (bool, error) {
	n, err := z.store.R().CountSharedOrgs(ctx, db.CountSharedOrgsParams{FirstIid: a, SecondIid: b})
	return n > 0, err
}

// Session returns a view of the facade for one operation: a dispatch, a request, a page. It reads
// the chart of each organization it is asked about once and the home organization of each AI once,
// and keeps them for as long as the operation lasts. Never keep a session across requests.
func (z *Authz) Session() *Session {
	return &Session{z: z, charts: map[string]*Chart{}, homes: map[string]string{}}
}

// Session is an Authz memoized for one operation (AD-017). It has the same questions as Authz and is
// safe for concurrent use.
type Session struct {
	z      *Authz
	mu     sync.Mutex
	charts map[string]*Chart
	homes  map[string]string
}

// Chart returns the chart of the organization, read at most once per session.
func (s *Session) Chart(ctx context.Context, orgID string) (*Chart, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.charts[orgID]; ok {
		return c, nil
	}
	c, err := s.z.Chart(ctx, orgID)
	if err != nil {
		return nil, err
	}
	s.charts[orgID] = c
	return c, nil
}

// homeOf returns the home organization of an AI, read at most once per session.
func (s *Session) homeOf(ctx context.Context, agentID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h, ok := s.homes[agentID]; ok {
		return h, nil
	}
	h, err := s.z.homeOf(ctx, agentID)
	if err != nil {
		return "", err
	}
	s.homes[agentID] = h
	return h, nil
}

// Member, RequireMember, RequireAdmin and SharesOrg read one row each: a session adds nothing.

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

// Manages is Authz.Manages over the session's charts.
func (s *Session) Manages(ctx context.Context, actor, target, orgID string) (bool, error) {
	return manages(ctx, s, actor, target, orgID)
}

// ManagesAnywhere is Authz.ManagesAnywhere over the session's charts.
func (s *Session) ManagesAnywhere(ctx context.Context, actor, target string) (bool, error) {
	return managesAnywhere(ctx, s, actor, target)
}

// CanDelete is Authz.CanDelete over the session's charts.
func (s *Session) CanDelete(ctx context.Context, actor, target, orgID string) (bool, error) {
	return canDelete(ctx, s, actor, target, orgID)
}

// Trusts is Authz.Trusts over the session's charts and homes.
func (s *Session) Trusts(ctx context.Context, authorIID, agentID string) (bool, error) {
	return trusts(ctx, s, authorIID, agentID)
}

// source is what the questions read: the facade reads every time, a session remembers.
type source interface {
	Chart(ctx context.Context, orgID string) (*Chart, error)
	homeOf(ctx context.Context, agentID string) (string, error)
	sharedOrgs(ctx context.Context, a, b string) ([]string, error)
}

func (z *Authz) homeOf(ctx context.Context, agentID string) (string, error) {
	org, err := z.store.R().GetAgentOrg(ctx, agentID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return org, err
}

func (z *Authz) sharedOrgs(ctx context.Context, a, b string) ([]string, error) {
	return z.store.R().ListSharedOrgIDs(ctx, db.ListSharedOrgIDsParams{FirstIid: a, SecondIid: b})
}

func (s *Session) sharedOrgs(ctx context.Context, a, b string) ([]string, error) {
	return s.z.sharedOrgs(ctx, a, b)
}

func manages(ctx context.Context, src source, actor, target, orgID string) (bool, error) {
	if actor == "" || target == "" || actor == target {
		return false, nil
	}
	c, err := src.Chart(ctx, orgID)
	if err != nil {
		return false, err
	}
	return c.Manages(actor, target), nil
}

func canDelete(ctx context.Context, src source, actor, target, orgID string) (bool, error) {
	if actor == "" || target == "" || actor == target {
		return false, nil
	}
	c, err := src.Chart(ctx, orgID)
	if err != nil {
		return false, err
	}
	return c.CanDelete(actor, target), nil
}

func managesAnywhere(ctx context.Context, src source, actor, target string) (bool, error) {
	if actor == "" || target == "" || actor == target {
		return false, nil
	}
	shared, err := src.sharedOrgs(ctx, actor, target)
	if err != nil {
		return false, err
	}
	for _, orgID := range shared {
		ok, err := manages(ctx, src, actor, target, orgID)
		if err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}

func trusts(ctx context.Context, src source, authorIID, agentID string) (bool, error) {
	if authorIID == "" || agentID == "" {
		return false, nil
	}
	home, err := src.homeOf(ctx, agentID)
	if err != nil || home == "" {
		return false, err
	}
	return manages(ctx, src, authorIID, agentID, home)
}
