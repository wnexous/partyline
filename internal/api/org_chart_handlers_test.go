package api_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// chartWorld is "Acme" from the spec's example chart, with everyone the chart page has to place:
//
//	Admin ── alice (admin)
//	 ├ Director ── dora
//	 │   └ Manager ── mia
//	 │       └ Lead developer ── lee, and the AI lead (hosted by lee)
//	 │           └ Worktrees ── the three workers lead started (guests; the first is a live session)
//	 └ Sentinel ── sam, and the AI sentinel (hosted by sam)
//
// With no position: bob and erin (members nobody created), gus (bob's guest), the AI notes (lee created
// it), loose (a worker lead started without a position: its guest), ghost (an AI whose creator is not a
// member of Acme) and visitor, an AI of another organization that is a member of Acme.
type chartWorld struct {
	env                                          *testkit.Env
	alice, dora, mia, lee, sam, bob, erin, gus   *testkit.Client
	acme                                         db.Organization
	director, manager, leadDev, worktrees        string // position ids
	sentinelPos, rootPos                         string
	lead, sentinel, notes, loose, ghost, visitor string // AI ids
	workers                                      []string
	live                                         *testkit.AgentConn // the session of workers[0]
}

func newChartWorld(t *testing.T) *chartWorld {
	t.Helper()
	ctx := context.Background()
	env := testkit.New(t)
	w := &chartWorld{env: env}
	for name, c := range map[string]**testkit.Client{
		"alice": &w.alice, "dora": &w.dora, "mia": &w.mia, "lee": &w.lee, "sam": &w.sam, "bob": &w.bob, "erin": &w.erin, "gus": &w.gus,
	} {
		*c = env.NewUser(name)
	}
	zed := env.NewUser("zed") // belongs to another organization only
	acme, err := env.App.Orgs.Create(ctx, w.alice.User.ID, "Acme")
	require.NoError(t, err)
	w.acme, w.rootPos = acme.Organization, acme.PositionID
	w.director = env.AddPosition(w.acme.ID, "Director", w.rootPos)
	w.manager = env.AddPosition(w.acme.ID, "Manager", w.director)
	w.leadDev = env.AddPosition(w.acme.ID, "Lead developer", w.manager)
	w.worktrees = env.AddPosition(w.acme.ID, "Worktrees", w.leadDev)
	w.sentinelPos = env.AddPosition(w.acme.ID, "Sentinel", w.rootPos)
	env.AddMember(w.acme.ID, w.dora.User.ID, orgs.PermissionMember, w.director)
	env.AddMember(w.acme.ID, w.mia.User.ID, orgs.PermissionMember, w.manager)
	env.AddMember(w.acme.ID, w.lee.User.ID, orgs.PermissionMember, w.leadDev)
	env.AddMember(w.acme.ID, w.sam.User.ID, orgs.PermissionMember, w.sentinelPos)
	env.AddMember(w.acme.ID, w.bob.User.ID, orgs.PermissionMember, "")
	env.AddMember(w.acme.ID, w.erin.User.ID, orgs.PermissionMember, "")
	env.AddGuest(w.acme.ID, w.gus.User.ID, w.bob.User.ID, "")

	w.lead = env.NewAIIn(w.lee, w.acme.ID, "lead")
	w.sentinel = env.NewAIIn(w.sam, w.acme.ID, "sentinel")
	w.notes = env.NewAIIn(w.lee, w.acme.ID, "notes")
	w.ghost = env.NewAIIn(w.erin, w.acme.ID, "ghost")
	env.SetCreator(w.ghost, zed.User.ID) // its creator is not a member of Acme
	for ai, pos := range map[string]string{w.lead: w.leadDev, w.sentinel: w.sentinelPos} {
		_, err := env.App.Orgs.SetPosition(ctx, w.alice.User.ID, w.acme.ID, ai, pos)
		require.NoError(t, err)
	}
	w.visitor = env.NewAIIn(zed, zed.PersonalOrg().ID, "visitor")
	env.AddMember(w.acme.ID, w.visitor, orgs.PermissionMember, "")

	token := w.lee.Token(dto.TokenKindAgent, "laptop")
	worker := func(i int, position string) testkit.Folder {
		return testkit.Folder{Machine: "devbox", Path: fmt.Sprintf("/work/lead/wt%d", i), Harness: "claude", Parent: "@lead", Position: position}
	}
	conn, resp := token.DialFolder(worker(0, "Worktrees"))
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	w.live = conn
	w.workers = append(w.workers, conn.Welcome(wsWait).Agent.ID)
	for i := 1; i < 3; i++ {
		w.workers = append(w.workers, testkit.Decode[dto.AgentInfo](token.AsFolder(worker(i, "Worktrees")).Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK)).ID)
	}
	w.loose = testkit.Decode[dto.AgentInfo](token.AsFolder(worker(3, "")).Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK)).ID
	return w
}

func chartPath(org string) string { return "/api/v1/orgs/" + org + "/chart" }

func getChart(t *testing.T, c *testkit.Client, org string) dto.OrgChart {
	t.Helper()
	return testkit.Decode[dto.OrgChart](c.Get(chartPath(org)).RequireStatus(http.StatusOK))
}

// chartIndex finds every member of a chart by identity id and says where it stands: in a position, hanging under
// another member, or unassigned. It fails the test for a member that appears twice.
type chartIndex struct {
	byID     map[string]dto.ChartMember
	position map[string]string // position id, for holders
	region   map[string]string // "position", "under" or "unassigned"
}

func indexChart(t *testing.T, chart dto.OrgChart) chartIndex {
	t.Helper()
	ix := chartIndex{byID: map[string]dto.ChartMember{}, position: map[string]string{}, region: map[string]string{}}
	add := func(m dto.ChartMember, region, position string) {
		_, dup := ix.byID[m.Intelligence.ID]
		require.False(t, dup, "%s appears once in the chart", m.Intelligence.Handle)
		ix.byID[m.Intelligence.ID], ix.region[m.Intelligence.ID], ix.position[m.Intelligence.ID] = m, region, position
	}
	for _, p := range chart.Positions {
		for _, m := range p.Holders {
			add(m, "position", p.ID)
		}
	}
	for _, m := range chart.Members {
		add(m, "under", "")
	}
	for _, m := range chart.Unassigned {
		add(m, "unassigned", "")
	}
	return ix
}

// flagSet is what a viewer may do to a member.
type flagSet struct {
	del    bool
	effect dto.DeleteEffect
	move   bool
}

func flagsOf(m dto.ChartMember) flagSet {
	return flagSet{m.CanDelete, m.DeleteEffect, m.CanMove}
}

// CHART-01: the page shows the positions as a tree - the flat list in tree order, parents first, siblings by
// name, each with the parent that links it - and every position lists its holders with what the page draws:
// avatar, name, @handle, kind, online and permission.
func TestChartShowsThePositionsAndTheirHolders(t *testing.T) {
	w := newChartWorld(t)
	chart := getChart(t, w.erin, w.acme.ID)

	type node struct{ id, name, parent string }
	var shape []node
	for _, p := range chart.Positions {
		shape = append(shape, node{p.ID, p.Name, p.ParentID})
	}
	require.Equal(t, []node{
		{w.rootPos, "Admin", ""}, {w.director, "Director", w.rootPos}, {w.manager, "Manager", w.director},
		{w.leadDev, "Lead developer", w.manager}, {w.worktrees, "Worktrees", w.leadDev}, {w.sentinelPos, "Sentinel", w.rootPos},
	}, shape)

	holders := func(position string) []string {
		var ids []string
		for _, p := range chart.Positions {
			if p.ID == position {
				for _, h := range p.Holders {
					ids = append(ids, h.Intelligence.ID)
				}
			}
		}
		return ids
	}
	require.Equal(t, []string{w.alice.User.ID}, holders(w.rootPos))
	require.Equal(t, []string{w.dora.User.ID}, holders(w.director))
	require.Equal(t, []string{w.mia.User.ID}, holders(w.manager))
	require.ElementsMatch(t, []string{w.lee.User.ID, w.lead}, holders(w.leadDev))
	require.ElementsMatch(t, w.workers, holders(w.worktrees), "three workers positioned in Worktrees")
	require.ElementsMatch(t, []string{w.sam.User.ID, w.sentinel}, holders(w.sentinelPos))

	for _, p := range chart.Positions {
		names := make([]string, len(p.Holders))
		for i, h := range p.Holders {
			names[i] = strings.ToLower(h.Intelligence.DisplayName)
		}
		require.True(t, slices.IsSorted(names), "holders of %s are ordered by name: %v", p.Name, names)
	}

	ix := indexChart(t, chart)
	alice := ix.byID[w.alice.User.ID]
	require.Equal(t, dto.IntelligenceKindHuman, alice.Intelligence.Kind)
	require.Equal(t, dto.IntelligenceRoleUser, alice.Intelligence.Role)
	require.Equal(t, "alice", alice.Intelligence.Handle)
	require.Equal(t, dto.PermissionAdmin, alice.Permission, "the admin badge")
	require.Equal(t, dto.PermissionMember, ix.byID[w.lee.User.ID].Permission)
	lead := ix.byID[w.lead]
	require.Equal(t, dto.IntelligenceKindAI, lead.Intelligence.Kind)
	require.Equal(t, dto.IntelligenceRoleAI, lead.Intelligence.Role)
	for _, id := range w.workers {
		require.Equal(t, dto.PermissionGuest, ix.byID[id].Permission, "workers lead started are guests (the guest badge)")
	}
	require.Equal(t, dto.PermissionGuest, ix.byID[w.gus.User.ID].Permission)
}

// CHART-01, CHART-02: members with no position hang under who answers for them - a guest under its inviter, an AI
// under its creator - and the ones with nobody to hang under are unassigned. Everyone is in the chart once.
func TestChartPlacesMembersWithoutAPosition(t *testing.T) {
	w := newChartWorld(t)
	chart := getChart(t, w.erin, w.acme.ID)
	ix := indexChart(t, chart)

	require.Len(t, ix.byID, 17, "every member, once")
	members := listMembers(t, w.alice, w.acme.ID)
	require.Len(t, members, len(ix.byID))
	for id := range members {
		require.Contains(t, ix.byID, id, "the chart lists every member")
	}

	for who, want := range map[string]string{ // the member -> who it hangs under
		w.loose:       w.lead,        // a guest lead started without a position: under lead
		w.notes:       w.lee.User.ID, // an AI lee created: under its creator
		w.gus.User.ID: w.bob.User.ID, // a guest bob invited: under bob (CHART-02)
	} {
		require.Equal(t, "under", ix.region[who], who)
		require.Equal(t, want, ix.byID[who].Under, who)
	}
	for _, id := range []string{w.bob.User.ID, w.erin.User.ID, w.ghost, w.visitor} {
		require.Equal(t, "unassigned", ix.region[id], "nobody to hang under: %s", ix.byID[id].Intelligence.Handle)
		require.Empty(t, ix.byID[id].Under)
	}
	// What has a position never hangs under anybody.
	for _, id := range append([]string{w.alice.User.ID, w.lee.User.ID, w.lead, w.sam.User.ID, w.sentinel}, w.workers...) {
		require.Equal(t, "position", ix.region[id])
		require.Empty(t, ix.byID[id].Under)
	}
}

// CHART-01: an AI that has a live session is online; people have no presence and everything else is offline.
func TestChartShowsWhichAIsAreOnline(t *testing.T) {
	w := newChartWorld(t)
	ix := indexChart(t, getChart(t, w.erin, w.acme.ID))
	for id, m := range ix.byID {
		require.Equal(t, id == w.workers[0], m.Online, "%s: only the worker with a live session is online", m.Intelligence.Handle)
	}
}

// CHART-02: the chart is for the organization's members, guests included; whoever is not a member - an
// instance admin without membership too - does not find it. The same page with nothing to do for a guest.
func TestChartVisibility(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	for _, role := range r.All() {
		t.Run(role.Name, func(t *testing.T) {
			resp := bound(t, role.Client).Get(chartPath(r.Org.ID))
			if role.Name == "outsider" || role.Name == "instance_admin" {
				resp.RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
				return
			}
			chart := testkit.Decode[dto.OrgChart](resp.RequireStatus(http.StatusOK))
			require.Equal(t, role.Name == "admin", chart.Viewer.Admin, "viewer.admin")
			require.Len(t, indexChart(t, chart).byID, 8, "seven people and the organization's AI")
		})
	}
	r.Admin.Get(chartPath("no-such-org")).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	// {org} is an id or an address, in any case.
	require.Len(t, getChart(t, r.Guest, "ACME").Positions, 3)
	// An agent token never reaches the page, and nobody anonymous does.
	r.Admin.Token(dto.TokenKindAgent, "t").Get(chartPath(r.Org.ID)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	env.Anon().Get(chartPath(r.Org.ID)).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
}

// CHART-03, POS-05, POS-06: the admin may delete everyone but itself - members and AIs alike, with the effect that
// follows ownership: an AI of Acme is deleted for good, a person and an AI of another organization are removed from
// it - and may move everyone.
func TestChartFlagsForTheAdmin(t *testing.T) {
	w := newChartWorld(t)
	chart := getChart(t, w.alice, w.acme.ID)
	require.True(t, chart.Viewer.Admin)
	ix := indexChart(t, chart)

	for id, m := range ix.byID {
		want := flagSet{del: id != w.alice.User.ID, effect: dto.DeleteEffectRemove, move: true}
		switch id {
		case w.lead, w.sentinel, w.notes, w.loose, w.ghost, w.workers[0], w.workers[1], w.workers[2]:
			want.effect = dto.DeleteEffectDelete // an AI whose home is Acme
		}
		require.Equal(t, want, flagsOf(m), m.Intelligence.Handle)
	}
	require.Equal(t, dto.DeleteEffectRemove, ix.byID[w.visitor].DeleteEffect, "an AI of another organization is only removed")
	require.Equal(t, dto.DeleteEffectRemove, ix.byID[w.dora.User.ID].DeleteEffect, "a person is only removed")
}

// CHART-03, POS-05 (the spec's Independent Test): the person who leads the Lead developer position may delete the
// guests started below it - the workers - but not a member such as Sentinel, and may move whom it manages.
func TestChartFlagsForTheLeadDeveloper(t *testing.T) {
	w := newChartWorld(t)
	chart := getChart(t, w.lee, w.acme.ID)
	require.False(t, chart.Viewer.Admin)
	ix := indexChart(t, chart)

	deleted := flagSet{true, dto.DeleteEffectDelete, true}
	for _, id := range w.workers {
		require.Equal(t, deleted, flagsOf(ix.byID[id]), "a worker lead started is a guest below lee")
	}
	require.Equal(t, flagSet{false, dto.DeleteEffectDelete, true}, flagsOf(ix.byID[w.notes]), "lee created notes, a member: it can be moved, not deleted")
	require.Equal(t, flagSet{false, dto.DeleteEffectDelete, false}, flagsOf(ix.byID[w.sentinel]), "Sentinel is a member, and beside lee")
	require.Equal(t, flagSet{false, dto.DeleteEffectRemove, false}, flagsOf(ix.byID[w.sam.User.ID]))
	// lead holds lee's own position, so lee manages neither lead nor what lead started without a position.
	require.Equal(t, flagSet{false, dto.DeleteEffectDelete, false}, flagsOf(ix.byID[w.lead]))
	require.Equal(t, flagSet{false, dto.DeleteEffectDelete, false}, flagsOf(ix.byID[w.loose]))
	// Nothing above lee, nobody else's guest.
	for _, id := range []string{w.alice.User.ID, w.dora.User.ID, w.mia.User.ID, w.lee.User.ID, w.bob.User.ID, w.erin.User.ID, w.gus.User.ID, w.visitor} {
		require.False(t, ix.byID[id].CanDelete, ix.byID[id].Intelligence.Handle)
		require.False(t, ix.byID[id].CanMove, ix.byID[id].Intelligence.Handle)
	}
}

// CHART-03: a manager above Lead developer manages what lead started without a position as well, and moves the
// people below it; a member with no position and no relation can do nothing; a guest who invited a guest can
// delete it.
func TestChartFlagsForAManagerAMemberAndAGuest(t *testing.T) {
	w := newChartWorld(t)

	mia := indexChart(t, getChart(t, w.mia, w.acme.ID))
	for _, id := range append([]string{w.loose}, w.workers...) {
		require.Equal(t, flagSet{true, dto.DeleteEffectDelete, true}, flagsOf(mia.byID[id]), "mia manages lead's guests")
	}
	require.Equal(t, flagSet{false, dto.DeleteEffectRemove, true}, flagsOf(mia.byID[w.lee.User.ID]), "lee is a member below mia: moved, not deleted")
	require.Equal(t, flagSet{false, dto.DeleteEffectDelete, true}, flagsOf(mia.byID[w.lead]))
	require.Equal(t, flagSet{false, dto.DeleteEffectDelete, true}, flagsOf(mia.byID[w.notes]), "through lee, who created it")
	for _, id := range []string{w.alice.User.ID, w.dora.User.ID, w.sam.User.ID, w.sentinel, w.bob.User.ID, w.erin.User.ID, w.gus.User.ID} {
		require.Equal(t, flagSet{false, mia.byID[id].DeleteEffect, false}, flagsOf(mia.byID[id]), mia.byID[id].Intelligence.Handle)
	}

	// erin is a member with no position and no relation: nothing is offered.
	for id, m := range indexChart(t, getChart(t, w.erin, w.acme.ID)).byID {
		require.False(t, m.CanDelete, id)
		require.False(t, m.CanMove, id)
	}

	// bob invited gus: bob may delete him (POS-05), though bob holds no position to move him with.
	bob := indexChart(t, getChart(t, w.bob, w.acme.ID))
	require.Equal(t, flagSet{true, dto.DeleteEffectRemove, false}, flagsOf(bob.byID[w.gus.User.ID]))
	require.False(t, bob.byID[w.erin.User.ID].CanDelete)
	// gus, the guest, sees the chart (200) and may do nothing to anyone.
	gus := indexChart(t, getChart(t, w.gus, w.acme.ID))
	require.Equal(t, "under", gus.region[w.gus.User.ID])
	require.Equal(t, w.bob.User.ID, gus.byID[w.gus.User.ID].Under, "a guest invited by Bob appears under Bob")
	for id, m := range gus.byID {
		require.False(t, m.CanDelete || m.CanMove, id)
	}
}

// CHART-03: the chart's flags are the rules the server enforces - every "delete" flag is POS-05, and a move the flag
// allows is accepted while one it does not is refused - so the page never offers a refused action. A member is
// moved to Worktrees (below mia and lee) and put back each time.
func TestChartFlagsMatchWhatTheRoutesAllow(t *testing.T) {
	w := newChartWorld(t)
	ctx := context.Background()
	for _, viewer := range []*testkit.Client{w.alice, w.mia, w.lee, w.sam, w.erin, w.bob, w.gus} {
		viewer := bound(t, viewer)
		ix := indexChart(t, getChart(t, viewer, w.acme.ID))
		for id, m := range ix.byID {
			can, err := w.env.App.Authz.CanDelete(ctx, viewer.User.ID, id, w.acme.ID)
			require.NoError(t, err)
			require.Equal(t, can, m.CanDelete, "%s deleting %s", viewer.User.Username, m.Intelligence.Handle)

			resp := viewer.Patch(memberPath(w.acme.ID, id), toPosition(w.worktrees))
			if !m.CanMove {
				resp.RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
				continue
			}
			resp.RequireStatus(http.StatusOK)
			w.alice.Patch(memberPath(w.acme.ID, id), toPosition(ix.position[id])).RequireStatus(http.StatusOK) // put it back
		}
	}
}

// The members table reads the same flags: GET /members carries them for the viewer, row by row, as the chart does.
func TestMemberRowsCarryTheChartFlags(t *testing.T) {
	w := newChartWorld(t)
	for _, viewer := range []*testkit.Client{w.alice, w.mia, w.lee, w.erin, w.gus} {
		ix := indexChart(t, getChart(t, viewer, w.acme.ID))
		rows := listMembers(t, viewer, w.acme.ID)
		require.Len(t, rows, len(ix.byID))
		for id, row := range rows {
			require.Equal(t, flagsOf(ix.byID[id]), flagSet{row.CanDelete, row.DeleteEffect, row.CanMove}, "%s sees %s", viewer.User.Username, row.Intelligence.Handle)
		}
	}
	// The row an update answers with carries them too.
	moved := testkit.Decode[dto.OrgMember](w.mia.Patch(memberPath(w.acme.ID, w.workers[1]), toPosition(w.worktrees)).RequireStatus(http.StatusOK))
	require.Equal(t, flagSet{true, dto.DeleteEffectDelete, true}, flagSet{moved.CanDelete, moved.DeleteEffect, moved.CanMove})
	again := testkit.Decode[dto.OrgMember](w.erin.Patch(memberPath(w.acme.ID, w.workers[1]), dto.UpdateOrgMemberRequest{}).RequireStatus(http.StatusOK))
	require.Equal(t, flagSet{false, dto.DeleteEffectDelete, false}, flagSet{again.CanDelete, again.DeleteEffect, again.CanMove})
}
