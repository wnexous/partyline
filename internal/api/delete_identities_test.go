package api_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

func memberPath(org, iid string) string { return "/api/v1/orgs/" + org + "/members/" + iid }

func agentDeletePath(id string) string { return "/api/v1/agents/" + id }

func sessionPath(agentID string) string { return "/api/v1/agents/" + agentID + "/session" }

// bound returns a copy of c whose assertions belong to t, to use it inside a subtest.
func bound(t *testing.T, c *testkit.Client) *testkit.Client {
	cc := *c
	cc.T = t
	return &cc
}

// requireRefusal checks a refusal: its status and the error code that goes with it.
func requireRefusal(t *testing.T, r *testkit.Response, status int) {
	t.Helper()
	code := map[int]dto.ErrorCode{
		http.StatusUnauthorized: dto.ErrorCodeUnauthorized,
		http.StatusForbidden:    dto.ErrorCodeForbidden,
		http.StatusNotFound:     dto.ErrorCodeNotFound,
	}[status]
	require.NotEmpty(t, code, "%d is not a refusal", status)
	r.RequireError(status, code)
}

func isMember(t *testing.T, env *testkit.Env, org, iid string) bool {
	t.Helper()
	_, ok, err := env.App.Authz.Member(context.Background(), org, iid)
	require.NoError(t, err)
	return ok
}

// requireStanding checks what one identity may do to another in an organization: whether it manages it
// (SR2-13) and whether it may delete it there (SR2-12: an AI whose home is the organization by its host or an
// admin, anyone else by an admin). It is the precondition that makes a refusal below a test of one rule only
// (L-018).
func requireStanding(t *testing.T, env *testkit.Env, org, actor, target string, manages, canDelete bool) {
	t.Helper()
	ctx := context.Background()
	got, err := env.App.Authz.Manages(ctx, actor, target)
	require.NoError(t, err)
	require.Equal(t, manages, got, "manages")
	home, err := env.App.Store.R().GetAgentOrg(ctx, target)
	if err == nil && home == org {
		got, err = env.App.Authz.CanDeleteAI(ctx, actor, target)
		require.NoError(t, err)
	} else {
		roster, err := env.App.Authz.Roster(ctx, org)
		require.NoError(t, err)
		got = roster.CanRemove(actor, target)
	}
	require.Equal(t, canDelete, got, "can delete")
}

// requireRemovedOnly checks that a person was taken out of the organization and nothing more (POS-06): the
// account lives, logs in again and still belongs to their own organization.
func requireRemovedOnly(t *testing.T, env *testkit.Env, org string, person *testkit.Client) {
	t.Helper()
	require.False(t, isMember(t, env, org, person.User.ID), "out of the organization")
	person.Get("/api/v1/me").RequireStatus(http.StatusOK)
	env.LoginAs(person.User.Username, testkit.Password)
	require.True(t, isMember(t, env, person.PersonalOrg().ID, person.User.ID), "still a member of their own organization")
}

// requireDeletedForGood checks that no row of an AI is left (POS-06): not its identity, its memberships in any
// organization nor in any room.
func requireDeletedForGood(t *testing.T, env *testkit.Env, ai string) {
	t.Helper()
	for _, q := range []string{
		`SELECT COUNT(*) FROM agents WHERE id = ?`,
		`SELECT COUNT(*) FROM intelligences WHERE id = ?`,
		`SELECT COUNT(*) FROM org_members WHERE iid = ?`,
		`SELECT COUNT(*) FROM room_members WHERE member_id = ?`,
	} {
		require.Zero(t, countRows(t, env, q, ai), q)
	}
}

func requireAIIntact(t *testing.T, env *testkit.Env, ai, org string) {
	t.Helper()
	require.Equal(t, 1, countRows(t, env, `SELECT COUNT(*) FROM agents WHERE id = ?`, ai), "the AI exists")
	require.True(t, isMember(t, env, org, ai), "and is a member of the organization")
}

// deletion is one target in a fresh OrgRoles organization with the way to delete it: do deletes it as a client,
// intact checks that nothing changed and done that the deletion had the effect its kind of target has.
type deletion struct {
	env    *testkit.Env
	roles  *testkit.OrgRoles
	target string
	do     func(c *testkit.Client) *testkit.Response
	intact func(t *testing.T)
	done   func(t *testing.T)
}

// client returns the caller of a role of the organization, or an anonymous caller ("anonymous"), the token of
// the AI's host that names no AI ("agent token") or the AI's own token ("the AI itself").
func (d *deletion) client(role string) *testkit.Client {
	switch role {
	case "anonymous":
		return d.env.Anon()
	case "agent token":
		return d.roles.Manager.Token(dto.TokenKindAgent, "laptop")
	case "the AI itself":
		return d.roles.Manager.Token(dto.TokenKindAgent, "laptop").AsAgent("acme-ai")
	}
	for _, rc := range d.roles.All() {
		if rc.Name == role {
			return rc.Client
		}
	}
	require.FailNow(d.env.T, "no such role", role)
	return nil
}

// matrixCase is what deleting the target must answer to one caller.
type matrixCase struct {
	role   string
	status int
}

// runDeletionMatrix calls the deletion as every role. The refused calls go to one organization, which must be as
// it was after each; every call that is allowed gets an organization of its own, because a deletion is for good.
func runDeletionMatrix(t *testing.T, build func(t *testing.T) *deletion, cases []matrixCase) {
	t.Helper()
	refusing := build(t)
	for _, c := range cases {
		if c.status == http.StatusNoContent {
			continue
		}
		t.Run("refused/"+c.role, func(t *testing.T) {
			requireRefusal(t, refusing.do(bound(t, refusing.client(c.role))), c.status)
			refusing.intact(t)
		})
	}
	for _, c := range cases {
		if c.status != http.StatusNoContent {
			continue
		}
		t.Run("allowed/"+c.role, func(t *testing.T) {
			d := build(t)
			d.do(bound(t, d.client(c.role))).RequireStatus(http.StatusNoContent)
			d.done(t)
		})
	}
}

// deletePerson deletes Target, a member, in the organization.
func deletePerson(t *testing.T) *deletion {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	return &deletion{env: env, roles: r, target: r.Target.User.ID,
		do: func(c *testkit.Client) *testkit.Response { return c.Delete(memberPath(r.Org.ID, r.Target.User.ID)) },
		intact: func(t *testing.T) {
			require.True(t, isMember(t, env, r.Org.ID, r.Target.User.ID), "still a member")
		},
		done: func(t *testing.T) { requireRemovedOnly(t, env, r.Org.ID, r.Target) },
	}
}

// aiRoute is one way to delete an AI of an organization: by the organization's members, or as the AI itself.
type aiRoute struct {
	name string
	path func(org, ai string) string
}

var aiRoutes = []aiRoute{
	{"DELETE /orgs/{org}/members/{iid}", memberPath},
	{"DELETE /agents/{id}", func(_, ai string) string { return agentDeletePath(ai) }},
}

// deleteAI deletes TargetAI, a member AI Manager created and hosts and Instructor instructs, by the route.
func deleteAI(route aiRoute) func(t *testing.T) *deletion {
	return func(t *testing.T) *deletion {
		env := testkit.New(t)
		r := env.OrgRoles("acme")
		return &deletion{env: env, roles: r, target: r.TargetAI,
			do:     func(c *testkit.Client) *testkit.Response { return c.Delete(route.path(r.Org.ID, r.TargetAI)) },
			intact: func(t *testing.T) { requireAIIntact(t, env, r.TargetAI, r.Org.ID) },
			done:   func(t *testing.T) { requireDeletedForGood(t, env, r.TargetAI) },
		}
	}
}

// SR2-12, POS-06 (matrix of DELETE /orgs/{org}/members/{iid}, a person): only an admin removes a person, so the
// host of an AI and its instructor are refused while the admin is not - each refusal is the only thing wrong with
// the call (L-018). A person is removed from the organization and keeps their account. Outsiders, the instance
// admin included, cannot see the organization (404); a member may leave (204).
func TestSR212DeletingAMemberOfAnOrganization(t *testing.T) {
	probe := deletePerson(t)
	org, r := probe.roles.Org.ID, probe.roles
	requireStanding(t, probe.env, org, r.Manager.User.ID, probe.target, false, false)
	requireStanding(t, probe.env, org, r.Admin.User.ID, probe.target, false, true) // removes, never manages a user
	requireStanding(t, probe.env, org, r.Peer.User.ID, probe.target, false, false)

	runDeletionMatrix(t, deletePerson, []matrixCase{
		{"admin", http.StatusNoContent},
		{"target", http.StatusNoContent}, // leaving
		{"manager", http.StatusForbidden},
		{"instructor", http.StatusForbidden},
		{"peer", http.StatusForbidden},
		{"member", http.StatusForbidden},
		{"outsider", http.StatusNotFound},
		{"instance_admin", http.StatusNotFound},
		{"anonymous", http.StatusUnauthorized},
		{"agent token", http.StatusForbidden},
	})
}

// SR2-12, POS-06 (matrix of both routes that delete an AI of the organization): the AI is deleted for good by its
// host (Manager) or by an admin of its home organization. Its user instructor manages it and still may not delete
// it. Whoever cannot see it gets 404 (including the instance admin), an AI token 403 whatever AI it acts as, and
// the answers are the same on the two routes (L-034).
func TestSR212DeletingAnAIOfAnOrganization(t *testing.T) {
	for _, route := range aiRoutes {
		t.Run(route.name, func(t *testing.T) {
			probe := deleteAI(route)(t)
			org, r := probe.roles.Org.ID, probe.roles
			requireStanding(t, probe.env, org, r.Manager.User.ID, probe.target, true, true)
			requireStanding(t, probe.env, org, r.Instructor.User.ID, probe.target, true, false)
			requireStanding(t, probe.env, org, r.Admin.User.ID, probe.target, false, true)

			runDeletionMatrix(t, deleteAI(route), []matrixCase{
				{"admin", http.StatusNoContent},
				{"manager", http.StatusNoContent},
				{"instructor", http.StatusForbidden},
				{"peer", http.StatusForbidden},
				{"target", http.StatusForbidden},
				{"member", http.StatusForbidden},
				{"outsider", http.StatusNotFound},
				{"instance_admin", http.StatusNotFound},
				{"anonymous", http.StatusUnauthorized},
				{"agent token", http.StatusForbidden},
				{"the AI itself", http.StatusForbidden},
			})
		})
	}
}

// leadWorld: in Acme, the AI lead (hosted by lee) starts three workers with --parent @lead. Lead instructs the
// workers, lee hosts them; sam hosts the AI sentinel and alice is Acme's admin.
type leadWorld struct {
	env             *testkit.Env
	alice, lee, sam *testkit.Client
	acme            db.Organization
	lead, sentinel  string // AI ids
	workers         []string
	live            *testkit.AgentConn // the session of workers[0]
}

func newLeadWorld(t *testing.T) *leadWorld {
	t.Helper()
	ctx := context.Background()
	env := testkit.New(t)
	w := &leadWorld{env: env, alice: env.NewUser("alice"), lee: env.NewUser("lee"), sam: env.NewUser("sam")}
	acme, err := env.App.Orgs.Create(ctx, w.alice.User.ID, "Acme")
	require.NoError(t, err)
	w.acme = acme.Organization
	env.AddMember(w.acme.ID, w.lee.User.ID, orgs.PermissionMember)
	env.AddMember(w.acme.ID, w.sam.User.ID, orgs.PermissionMember)
	w.lead = env.NewAIIn(w.lee, w.acme.ID, "lead")
	w.sentinel = env.NewAIIn(w.sam, w.acme.ID, "sentinel")

	token := w.lee.Token(dto.TokenKindAgent, "laptop")
	start := func(i int) testkit.Folder {
		return testkit.Folder{Machine: "devbox", Path: fmt.Sprintf("/work/lead/wt%d", i), Harness: "claude", Parent: "@lead"}
	}
	conn, resp := token.DialFolder(start(0)) // the first worker is a live session
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	w.live = conn
	w.workers = append(w.workers, conn.Welcome(wsWait).Agent.ID)
	for i := 1; i < 3; i++ { // the others are tools-only calls
		w.workers = append(w.workers, testkit.Decode[dto.AgentInfo](token.AsFolder(start(i)).Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK)).ID)
	}
	for _, id := range w.workers {
		m, ok, err := env.App.Authz.Member(ctx, w.acme.ID, id)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, orgs.Member{IID: id, Permission: orgs.PermissionMember}, m, "SR2-02: a worker is a member of Acme")
		require.Equal(t, []string{w.lead}, instructorsOf(t, env, id), "SR2-09: lead instructs it")
	}
	return w
}

// SR2-12, POS-06: the workers lead started are deleted for good by their host lee or by the admin, whichever
// route does it; lead instructs them and still does not delete them, and sam, who neither hosts nor administers,
// gets 403 on both routes while the workers stay. The deletion ends a live session as that of a deleted AI (4001).
func TestSR212TheHostDeletesTheWorkersAndAnInstructorCannot(t *testing.T) {
	ctx := context.Background()
	w := newLeadWorld(t)
	for _, id := range w.workers {
		for who, actor := range map[string]string{"lee (the host)": w.lee.User.ID, "alice (admin)": w.alice.User.ID} {
			can, err := w.env.App.Authz.CanDeleteAI(ctx, actor, id)
			require.NoError(t, err)
			require.True(t, can, "%s may delete a worker", who)
		}
		for who, actor := range map[string]string{"lead (an AI instructor)": w.lead, "sentinel (the AI)": w.sentinel, "sam": w.sam.User.ID} {
			can, err := w.env.App.Authz.CanDeleteAI(ctx, actor, id)
			require.NoError(t, err)
			require.False(t, can, "%s may not delete a worker", who)
		}
	}

	leadToken := w.lee.Token(dto.TokenKindAgent, "laptop").AsAgent("lead")
	for _, id := range w.workers {
		requireRefusal(t, w.sam.Delete(memberPath(w.acme.ID, id)), http.StatusForbidden)
		requireRefusal(t, w.sam.Delete(agentDeletePath(id)), http.StatusForbidden)
		requireRefusal(t, leadToken.Delete(agentDeletePath(id)), http.StatusForbidden)
		requireAIIntact(t, w.env, id, w.acme.ID)
	}
	require.NoError(t, w.live.Closed(300*time.Millisecond), "the refusals did not end the worker's session")

	w.lee.Delete(memberPath(w.acme.ID, w.workers[0])).RequireStatus(http.StatusNoContent)
	requireDeletedForGood(t, w.env, w.workers[0])
	closed := w.live.Closed(wsWait)
	var ce websocket.CloseError
	require.True(t, errors.As(closed, &ce), "the worker's session was closed: %v", closed)
	require.Equal(t, websocket.StatusCode(4001), ce.Code, "as the session of a deleted AI")

	w.lee.Delete(agentDeletePath(w.workers[1])).RequireStatus(http.StatusNoContent)
	requireDeletedForGood(t, w.env, w.workers[1])
	w.alice.Delete(memberPath(w.acme.ID, w.workers[2])).RequireStatus(http.StatusNoContent)
	requireDeletedForGood(t, w.env, w.workers[2])
	require.Zero(t, countRows(t, w.env, `SELECT COUNT(*) FROM agent_instructors WHERE agent_iid IN (?, ?, ?)`, w.workers[0], w.workers[1], w.workers[2]),
		"the links go with the AIs")

	requireAIIntact(t, w.env, w.lead, w.acme.ID)
	requireAIIntact(t, w.env, w.sentinel, w.acme.ID)
	// deleted for good: the folder starts a new AI instead of finding the old one
	again := testkit.Decode[dto.AgentInfo](w.lee.Token(dto.TokenKindAgent, "laptop").AsFolder(testkit.Folder{
		Machine: "devbox", Path: "/work/lead/wt1", Harness: "claude", Parent: "@lead",
	}).Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK)).ID
	require.NotContains(t, w.workers, again)
}

// OMEM-06, POS-06: an admin deletes a person from Acme. They are taken out of Acme's groups, and out of nothing
// else: their account, their other organization and its groups stay, and so do Acme's AIs - the one Manager made
// and the one the person hosts, which the person still deletes as its host and the admins as admins (SR2-12).
func TestDeletingAPersonOnlyRemovesThemFromThatOrganization(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	bob := r.Target
	globex, err := env.App.Orgs.Create(ctx, bob.User.ID, "Globex")
	require.NoError(t, err)

	acmeChat := createRoom(r.Admin, "Acme chat")
	execSQL(t, env, `UPDATE rooms SET org_id = ?, creator_iid = ? WHERE id = ?`, r.Org.ID, r.Admin.User.ID, acmeChat.ID)
	globexChat := createRoom(bob, "Globex chat")
	execSQL(t, env, `UPDATE rooms SET org_id = ?, creator_iid = ? WHERE id = ?`, globex.ID, bob.User.ID, globexChat.ID)
	addRoomMember(t, env, acmeChat.ID, "user", bob.User.ID, r.Admin.User.ID)
	addRoomMember(t, env, acmeChat.ID, "user", r.Manager.User.ID, r.Admin.User.ID)
	addRoomMember(t, env, globexChat.ID, "user", bob.User.ID, bob.User.ID)
	bobsAI := env.NewAIIn(bob, r.Org.ID, "bobs-ai")
	sub, cancel := env.App.Bus.Subscribe(64)
	defer cancel()

	r.Admin.Delete(memberPath(r.Org.ID, bob.User.ID)).RequireStatus(http.StatusNoContent)

	requireRemovedOnly(t, env, r.Org.ID, bob)
	require.Zero(t, countRows(t, env, `SELECT COUNT(*) FROM room_members WHERE room_id = ? AND member_id = ?`, acmeChat.ID, bob.User.ID), "out of Acme's group")
	require.Equal(t, 1, countRows(t, env, `SELECT COUNT(*) FROM room_members WHERE room_id = ? AND member_id = ?`, acmeChat.ID, r.Manager.User.ID), "the others stay in it")
	require.Equal(t, 1, countRows(t, env, `SELECT COUNT(*) FROM room_members WHERE room_id = ? AND member_id = ?`, globexChat.ID, bob.User.ID), "and in the group of another organization")
	require.True(t, isMember(t, env, globex.ID, bob.User.ID), "a member of the other organization")

	requireAIIntact(t, env, r.TargetAI, r.Org.ID)
	requireAIIntact(t, env, bobsAI, r.Org.ID)
	// Bob is out of Acme and still hosts it: hosting does not depend on the organization (SR2-13, SR2-12)
	requireStanding(t, env, r.Org.ID, bob.User.ID, bobsAI, true, true)
	requireStanding(t, env, r.Org.ID, r.Manager.User.ID, bobsAI, false, false)
	requireStanding(t, env, r.Org.ID, r.Admin.User.ID, bobsAI, false, true)

	told := drainEvents(sub)
	require.Contains(t, told, events.MembersChanged{RoomID: acmeChat.ID}, "the group that lost a member is announced")
	require.Contains(t, told, events.OrgChanged{OrgID: r.Org.ID}, "and so is the organization")
	removal := findAudit(t, auditPage(r.InstanceAdmin, "").Items, "org.member_remove")
	require.Equal(t, r.Admin.User.ID, removal.Actor.ID)
	require.Equal(t, r.Org.ID, removal.TargetID)
	require.Equal(t, bob.User.ID, removal.Detail["iid"])
}

// addRoomMember makes the user or AI a member of the room, as an organization's group has them after the upgrade
// or once an owner added them.
func addRoomMember(t *testing.T, env *testkit.Env, room, kind, member, addedBy string) {
	t.Helper()
	execSQL(t, env, `INSERT INTO room_members (id, room_id, kind, member_id, added_by, created_at) VALUES (?, ?, ?, ?, ?, 1)`,
		store.NewID(), room, kind, member, addedBy)
}

// drainEvents returns the events published so far on a subscription.
func drainEvents(sub <-chan events.Event) []events.Event {
	var out []events.Event
	for {
		select {
		case e := <-sub:
			out = append(out, e)
		default:
			return out
		}
	}
}

// POS-06: what "delete" removes follows ownership. An AI whose home is Globex is only removed from Acme when
// Acme's admin deletes it there - it still exists, in Globex - and Acme's admin cannot delete it for good
// (403 on the route of its home organization), while Globex's admin can, which takes it out of Acme too.
func TestDeletingAnAIOfAnotherOrganizationRemovesItOnlyFromThisOne(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	gina := env.NewUser("gina")
	globex, err := env.App.Orgs.Create(ctx, gina.User.ID, "Globex")
	require.NoError(t, err)
	visitor := env.NewAIIn(gina, globex.ID, "visitor") // its home is Globex
	env.AddMember(r.Org.ID, visitor, orgs.PermissionMember)
	acmeChat := createRoom(r.Admin, "Acme chat")
	execSQL(t, env, `UPDATE rooms SET org_id = ?, creator_iid = ? WHERE id = ?`, r.Org.ID, r.Admin.User.ID, acmeChat.ID)
	addRoomMember(t, env, acmeChat.ID, "agent", visitor, r.Admin.User.ID)

	// Acme's admin sees the AI but is no admin of its home, so it cannot delete it for good
	requireRefusal(t, r.Admin.Delete(agentDeletePath(visitor)), http.StatusForbidden)
	requireRefusal(t, r.Manager.Delete(memberPath(r.Org.ID, visitor)), http.StatusForbidden) // a member of Acme: not for a manager
	requireAIIntact(t, env, visitor, globex.ID)
	require.True(t, isMember(t, env, r.Org.ID, visitor))

	r.Admin.Delete(memberPath(r.Org.ID, visitor)).RequireStatus(http.StatusNoContent)

	requireAIIntact(t, env, visitor, globex.ID) // it still exists, in its home organization
	require.False(t, isMember(t, env, r.Org.ID, visitor), "and is out of Acme")
	require.Zero(t, countRows(t, env, `SELECT COUNT(*) FROM room_members WHERE room_id = ? AND member_id = ?`, acmeChat.ID, visitor), "and of its groups")
	require.Equal(t, 1, countRows(t, env, `SELECT COUNT(*) FROM agents WHERE id = ? AND org_id = ?`, visitor, globex.ID), "its home is unchanged")

	gina.Delete(agentDeletePath(visitor)).RequireStatus(http.StatusNoContent)
	requireDeletedForGood(t, env, visitor)
}

// OMEM-08: deleting the AI that operates a connection, from the organization it belongs to, leaves the
// connection with no operator and says what to do.
func TestDeletingTheOperatorAIFromTheOrganizationLeavesItsConnectionWithoutOperator(t *testing.T) {
	f := newOperatorFixture(t)
	inst := f.operate(t)
	inst.Connected()
	org := f.alice.PersonalOrg()

	f.alice.Delete(memberPath(org.ID, f.atlas)).RequireStatus(http.StatusNoContent)

	inst.WaitStop(t)
	require.Eventually(t, func() bool {
		got := f.connector(f.alice)
		return got.Operator == nil && got.Status == dto.ConnectorStatusError && got.LastError == noOperatorDetail
	}, time.Minute, 20*time.Millisecond, "the connection asks for an operator")
	requireDeletedForGood(t, f.env, f.atlas)
	requireAIIntact(t, f.env, f.notes, org.ID)
}

// A deleted AI is announced to every organization it belonged to - the chart of each changes - and audited with
// who deleted it and where it lived, never with anything it said (POS-05, spec observability).
func TestDeletingAnAIIsAnnouncedToEveryOrganizationItBelongedToAndAudited(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	gina := env.NewUser("gina")
	globex, err := env.App.Orgs.Create(ctx, gina.User.ID, "Globex")
	require.NoError(t, err)
	env.AddMember(globex.ID, r.TargetAI, orgs.PermissionMember)
	sub, cancel := env.App.Bus.Subscribe(64)
	defer cancel()

	r.Admin.Delete(agentDeletePath(r.TargetAI)).RequireStatus(http.StatusNoContent)

	told := drainEvents(sub)
	require.Contains(t, told, events.OrgChanged{OrgID: r.Org.ID}, "its home organization")
	require.Contains(t, told, events.OrgChanged{OrgID: globex.ID}, "and the one it was only a member of")
	entry := findAudit(t, auditPage(r.InstanceAdmin, "").Items, "agent.delete")
	require.Equal(t, r.Admin.User.ID, entry.Actor.ID)
	require.Equal(t, "agent", entry.TargetKind)
	require.Equal(t, r.TargetAI, entry.TargetID)
	require.Equal(t, r.Org.ID, entry.Detail["org"])
	require.Equal(t, "acme-ai", entry.Detail["handle"])
}

// OMEM-05: deleting an AI must not leave an organization without an admin either. The AI is the only admin of
// Globex, so Acme's admin cannot delete it (409 last_admin, nothing changes); once Globex has another admin it
// can.
func TestAnAIThatIsTheOnlyAdminOfAnOrganizationCannotBeDeleted(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	gina := env.NewUser("gina")
	globex, err := env.App.Orgs.Create(ctx, gina.User.ID, "Globex")
	require.NoError(t, err)
	env.AddMember(globex.ID, r.TargetAI, orgs.PermissionAdmin)
	_, err = env.App.Orgs.SetPermission(ctx, r.TargetAI, globex.ID, gina.User.ID, orgs.PermissionMember)
	require.NoError(t, err, "gina steps down: the AI is Globex's only admin")

	for _, route := range aiRoutes {
		r.Admin.Delete(route.path(r.Org.ID, r.TargetAI)).RequireError(http.StatusConflict, dto.ErrorCodeLastAdmin)
	}
	requireAIIntact(t, env, r.TargetAI, r.Org.ID)
	require.True(t, isMember(t, env, globex.ID, r.TargetAI), "and still Globex's admin")

	_, err = env.App.Orgs.SetPermission(ctx, r.TargetAI, globex.ID, gina.User.ID, orgs.PermissionAdmin)
	require.NoError(t, err, "Globex has another admin")
	r.Admin.Delete(agentDeletePath(r.TargetAI)).RequireStatus(http.StatusNoContent)
	requireDeletedForGood(t, env, r.TargetAI)
}
