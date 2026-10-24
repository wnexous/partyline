package orgs

import (
	"context"
	"sort"
	"strings"
)

// viewerActions answers, for one viewer, what it may do to each member of one organization (CHART-03). It
// reads the chart only and follows the rules the routes enforce: Chart.CanDelete for deleting (POS-05) and
// the position rules of SetPosition for moving (POS-04).
type viewerActions struct {
	chart   *Chart
	viewer  Member
	managed map[string]bool
	// canPlace is true when the viewer holds a position with another below it: the only places a manager may
	// move a member to.
	canPlace bool
}

func newViewerActions(chart *Chart, viewerIID string) viewerActions {
	a := viewerActions{chart: chart, viewer: chart.Members[viewerIID], managed: chart.ManagedBy(viewerIID)}
	for _, p := range chart.Positions {
		if chart.Above(a.viewer.PositionID, p.ID) {
			a.canPlace = true
			break
		}
	}
	return a
}

// canMove: an admin moves anyone to any position or to none; anyone else moves a member it manages to a
// position strictly below its own, so it needs one.
func (a viewerActions) canMove(target string) bool {
	return a.viewer.Permission == PermissionAdmin || (a.managed[target] && a.canPlace)
}

// apply sets the viewer's rights over the row; homes holds the AIs whose home is the organization.
func (a viewerActions) apply(row *MemberRow, homes map[string]bool) {
	row.CanDelete = a.chart.CanDelete(a.viewer.IID, row.IID)
	row.CanMove = a.canMove(row.IID)
	row.DeletedForGood = homes[row.IID]
}

// ChartPosition is one position of the chart with the members that hold it.
type ChartPosition struct {
	Position
	Holders []MemberRow
}

// ChartView is an organization's org chart as one of its members sees it (CHART-01..03): every position in
// tree order with its holders, the members with no position that hang under another member (each row's Under
// names it) and the ones that hang under nobody. Every member is in exactly one of the three places.
type ChartView struct {
	ViewerAdmin bool
	Positions   []ChartPosition
	Members     []MemberRow
	Unassigned  []MemberRow
}

// ChartView reads the chart for a member of the organization, guests included; anyone else is ErrNotFound
// (CHART-02). Holders and members are ordered by name, positions in tree order.
func (s *Service) ChartView(ctx context.Context, viewerIID, orgID string) (ChartView, error) {
	snap, err := s.snapshot(ctx, viewerIID, orgID, "")
	if err != nil {
		return ChartView{}, err
	}
	chart := snap.chart
	view := ChartView{ViewerAdmin: chart.IsAdmin(viewerIID)}
	index := map[string]int{}
	for _, p := range treeOrder(chart) {
		index[p.ID] = len(view.Positions)
		view.Positions = append(view.Positions, ChartPosition{Position: p, Holders: []MemberRow{}})
	}
	view.Members, view.Unassigned = []MemberRow{}, []MemberRow{}
	for _, row := range snap.memberRows(viewerIID) {
		if i, held := index[row.PositionID]; held {
			view.Positions[i].Holders = append(view.Positions[i].Holders, row)
			continue
		}
		if under := hangsUnder(chart, row.IID); under != "" {
			row.Under = under
			view.Members = append(view.Members, row)
			continue
		}
		view.Unassigned = append(view.Unassigned, row)
	}
	return view, nil
}

// treeOrder lists the positions parents first, siblings by name (ignoring case, then id). A position whose
// parent is not a position of the chart is a root, and one a corrupt loop leaves out of every tree comes last.
func treeOrder(c *Chart) []Position {
	children := map[string][]Position{}
	for _, p := range c.Positions {
		parent := p.ParentID
		if _, ok := c.Positions[parent]; !ok {
			parent = ""
		}
		children[parent] = append(children[parent], p)
	}
	byName := func(list []Position) {
		sort.Slice(list, func(i, j int) bool {
			a, b := strings.ToLower(list[i].Name), strings.ToLower(list[j].Name)
			if a != b {
				return a < b
			}
			return list[i].ID < list[j].ID
		})
	}
	out := make([]Position, 0, len(c.Positions))
	seen := map[string]bool{}
	var walk func(parent string)
	walk = func(parent string) {
		byName(children[parent])
		for _, p := range children[parent] {
			if seen[p.ID] {
				continue
			}
			seen[p.ID] = true
			out = append(out, p)
			walk(p.ID)
		}
	}
	walk("")
	var lost []Position
	for _, p := range c.Positions {
		if !seen[p.ID] {
			lost = append(lost, p)
		}
	}
	byName(lost)
	return append(out, lost...)
}

// holdsPosition reports whether the member holds a position of the chart.
func holdsPosition(c *Chart, m Member) bool {
	_, ok := c.Positions[m.PositionID]
	return ok
}

// answersTo is the member a member with no position hangs under: a guest's inviter, else the creator, each
// only while it is a member of the organization; "" for a member that holds a position or has neither.
func answersTo(c *Chart, m Member) string {
	if holdsPosition(c, m) {
		return ""
	}
	if _, ok := c.Members[m.InviterIID]; ok && m.Permission == PermissionGuest && m.InviterIID != m.IID {
		return m.InviterIID
	}
	if _, ok := c.Members[m.CreatorIID]; ok && m.CreatorIID != m.IID {
		return m.CreatorIID
	}
	return ""
}

// hangsUnder is the member the member hangs under in the chart, or "" when it holds a position or has nobody
// to hang under. A member whose chain of creators and inviters leads back to itself is in a loop and hangs
// under nobody - otherwise none of the loop would be reachable from a root; a member that only hangs under a
// loop is placed under it as usual.
func hangsUnder(c *Chart, iid string) string {
	under := answersTo(c, c.Members[iid])
	seen := map[string]bool{}
	for cur := under; cur != ""; cur = answersTo(c, c.Members[cur]) {
		if cur == iid {
			return ""
		}
		if seen[cur] {
			break // a loop that does not include the member
		}
		seen[cur] = true
	}
	return under
}
