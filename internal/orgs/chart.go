// Package orgs is the organization model (AD-016, AD-017): organizations, their org chart of
// positions, memberships and invitations, the rule that says who manages whom (Chart) and the
// Authz facade every other service asks. The rules in this file are pure: they read a snapshot of
// one organization and do no I/O.
package orgs

import "sort"

// Permission is what a membership allows in the organization itself, independent of positions
// (OMEM-04): an admin looks after the organization, a member takes part, a guest is disposable.
type Permission string

// Permissions.
const (
	PermissionAdmin  Permission = "admin"
	PermissionMember Permission = "member"
	PermissionGuest  Permission = "guest"
)

// Valid reports whether p is one of the three permissions.
func (p Permission) Valid() bool {
	return p == PermissionAdmin || p == PermissionMember || p == PermissionGuest
}

// Member is one membership of an organization: an identity (a user or an AI) with its permission
// and its position.
type Member struct {
	IID        string
	Permission Permission
	PositionID string // "" = no position
	CreatorIID string // agents.creator_iid, who created the AI; "" for users
	InviterIID string // org_members.inviter_iid, a guest's inviter; "" otherwise
}

// Position is a node of the org chart; ParentID is "" for a root.
type Position struct {
	ID, Name, ParentID string
}

// Chart is the snapshot of one organization the rules read: its positions and its members. It is
// never changed after it is built, so it is safe to share within one operation.
type Chart struct {
	OrgID     string
	Positions map[string]Position
	Members   map[string]Member
}

// Member returns the membership of iid.
func (c *Chart) Member(iid string) (Member, bool) {
	m, ok := c.Members[iid]
	return m, ok
}

// WithPosition returns a copy of the chart in which iid holds positionID ("" for none); the chart
// itself is untouched. An identity that is not a member leaves the copy unchanged. The positions are
// shared: a move changes a membership, never the chart's positions.
func (c *Chart) WithPosition(iid, positionID string) *Chart {
	out := *c
	out.Members = make(map[string]Member, len(c.Members))
	for id, m := range c.Members {
		out.Members[id] = m
	}
	if m, ok := out.Members[iid]; ok {
		m.PositionID = positionID
		out.Members[iid] = m
	}
	return &out
}

// IsAdmin reports whether iid is a member with the admin permission.
func (c *Chart) IsAdmin(iid string) bool {
	m, ok := c.Members[iid]
	return ok && m.Permission == PermissionAdmin
}

// Above reports whether position pa is strictly above pb: a proper ancestor of it through the
// parents. A position is never above itself, and an empty or unknown position is above nothing.
func (c *Chart) Above(pa, pb string) bool {
	if pa == "" || pb == "" || pa == pb {
		return false
	}
	p := c.Positions[pb].ParentID
	// The walk is bounded: positions never form a cycle (POS-02), but a corrupt chart must not hang.
	for steps := 0; p != "" && steps <= len(c.Positions); steps++ {
		if p == pa {
			return true
		}
		p = c.Positions[p].ParentID
	}
	return false
}

// HasCycle reports whether making newParentID the parent of positionID would create a cycle (POS-02):
// the new parent is the position itself or one of its descendants. An empty newParentID makes a root,
// which never does.
func (c *Chart) HasCycle(positionID, newParentID string) bool {
	if newParentID == "" {
		return false
	}
	return positionID == newParentID || c.Above(positionID, newParentID)
}

// answersTo lists the members that m is the responsibility of, other than through positions: the
// creator of a member without a position and the inviter of a guest. An identity that is not a member
// is not listed, which breaks the chain there.
func (c *Chart) answersTo(m Member) []string {
	var out []string
	if m.PositionID == "" && m.CreatorIID != "" {
		if _, ok := c.Members[m.CreatorIID]; ok {
			out = append(out, m.CreatorIID)
		}
	}
	if m.Permission == PermissionGuest && m.InviterIID != "" {
		if _, ok := c.Members[m.InviterIID]; ok {
			out = append(out, m.InviterIID)
		}
	}
	return out
}

// walk visits b and then every member that b answers to, transitively (creators and inviters), nearest
// first with the number of steps from b. visit returns true to stop. Each member is visited once, so a
// cycle ends the walk.
func (c *Chart) walk(b string, visit func(m Member, steps int) bool) {
	start, ok := c.Members[b]
	if !ok {
		return
	}
	seen := map[string]bool{b: true}
	level := []Member{start}
	for steps := 0; len(level) > 0; steps++ {
		var next []Member
		for _, m := range level {
			if visit(m, steps) {
				return
			}
			for _, id := range c.answersTo(m) {
				if !seen[id] {
					seen[id] = true
					next = append(next, c.Members[id])
				}
			}
		}
		level = next
	}
}

// Manages reports whether a manages b in this organization (POS-03, AD-017). Both must be members and
// differ. Then a manages b when any of these holds:
//   - a's position is strictly above b's;
//   - b holds no position and a is b's creator or manages b's creator, following the creators
//     upward (a positionless creator is itself managed by its creator);
//   - b is a guest and a is b's inviter or manages b's inviter, following the inviters upward.
//
// The chain stops at an identity that is not a member, and a cycle ends it. The admin permission
// gives no management: it is independent of positions.
func (c *Chart) Manages(a, b string) bool {
	if a == b {
		return false
	}
	ma, ok := c.Members[a]
	if !ok {
		return false
	}
	found := false
	c.walk(b, func(m Member, _ int) bool {
		// m is b itself (a != b) or someone b answers to: a is that creator or inviter, or sits above it.
		found = m.IID == a || c.Above(ma.PositionID, m.PositionID)
		return found
	})
	return found
}

// ManagersOf lists the identities that manage b, nearest first: by the number of steps from b - the
// creator or inviter is one step, whoever holds the position above b's is one step, and each further
// creator, inviter or position level is one more - and by id within the same distance. Used by the
// welcome notice of an AI.
func (c *Chart) ManagersOf(b string) []string {
	dist := map[string]int{}  // manager → distance
	above := map[string]int{} // position → distance of its holders
	c.walk(b, func(m Member, steps int) bool {
		if steps > 0 {
			setMin(dist, m.IID, steps)
		}
		// Each position above m's is one step further than the one below it.
		p := c.Positions[m.PositionID].ParentID
		for h := 1; p != "" && h <= len(c.Positions); h++ {
			setMin(above, p, steps+h)
			p = c.Positions[p].ParentID
		}
		return false
	})
	for iid, m := range c.Members {
		if d, ok := above[m.PositionID]; ok && iid != b {
			setMin(dist, iid, d)
		}
	}
	out := make([]string, 0, len(dist))
	for iid := range dist {
		out = append(out, iid)
	}
	sort.Slice(out, func(i, j int) bool {
		if dist[out[i]] != dist[out[j]] {
			return dist[out[i]] < dist[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

func setMin(m map[string]int, key string, d int) {
	if cur, ok := m[key]; !ok || d < cur {
		m[key] = d
	}
}

// ManagedBy returns the set of identities a manages.
func (c *Chart) ManagedBy(a string) map[string]bool {
	out := map[string]bool{}
	if _, ok := c.Members[a]; !ok {
		return out
	}
	for b := range c.Members {
		if c.Manages(a, b) {
			out[b] = true
		}
	}
	return out
}

// CanDelete reports whether a may delete b in this organization (POS-05): a is an admin, or b is a
// guest and a manages it - b's inviter, an identity up b's inviter chain, or whoever manages one of
// them. Nobody deletes themselves, and both must be members. What the deletion does follows ownership
// (POS-06): the caller decides.
func (c *Chart) CanDelete(a, b string) bool {
	if a == b {
		return false
	}
	ma, ok := c.Members[a]
	if !ok {
		return false
	}
	mb, ok := c.Members[b]
	if !ok {
		return false
	}
	if ma.Permission == PermissionAdmin {
		return true
	}
	return mb.Permission == PermissionGuest && c.Manages(a, b)
}
