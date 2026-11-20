package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

func instructorsPath(agentID string) string { return "/api/v1/agents/" + agentID + "/instructors" }

func addInstructor(c *testkit.Client, agentID, handle string) *testkit.Response {
	return c.Post(instructorsPath(agentID), dto.AddInstructorRequest{Handle: handle})
}

func instructorHandles(list dto.AgentInstructors) []string {
	out := []string{}
	for _, in := range list.Instructors {
		out = append(out, in.Intelligence.Handle)
	}
	return out
}

// SR2-10 (matrix of GET /agents/{id}/instructors): the host reads who instructs the AI - the host and the
// instructors besides it; whoever else sees the AI (its user instructor and the admin of its organization included)
// is refused (403), and whoever cannot see it finds nothing (404). The AI's own token reaches agent routes only
// (TOK-03): it is told its instructors in its welcome (SR2-11).
func TestSR210ListingTheInstructorsOfAnAI(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	ai := roles.TargetAI

	got := testkit.Decode[dto.AgentInstructors](roles.Manager.Get(instructorsPath(ai)).RequireStatus(http.StatusOK))
	require.Equal(t, roles.Manager.User.ID, got.Host.ID, "the host")
	require.Equal(t, dto.IntelligenceRoleUser, got.Host.Role)
	require.Equal(t, []string{"acme-instructor"}, instructorHandles(got))
	require.Equal(t, dto.IntelligenceKindHuman, got.Instructors[0].Intelligence.Kind)
	require.False(t, got.Instructors[0].AddedAt.IsZero())

	itself := roles.Manager.Token(dto.TokenKindAgent, "laptop").AsAgent("acme-ai")
	requireRefusal(t, itself.Get(instructorsPath(ai)), http.StatusForbidden)

	for _, rc := range roles.All() {
		switch rc.Name {
		case "manager":
			continue
		case "outsider", "instance_admin":
			requireRefusal(t, rc.Client.Get(instructorsPath(ai)), http.StatusNotFound)
		default:
			requireRefusal(t, rc.Client.Get(instructorsPath(ai)), http.StatusForbidden)
		}
	}
	env.Anon().Get(instructorsPath(ai)).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	requireRefusal(t, roles.Manager.Get(instructorsPath("no-such-ai")), http.StatusNotFound)
	requireRefusal(t, roles.Manager.Get(instructorsPath(roles.Target.User.ID)), http.StatusNotFound)
}

// SR2-10: the host adds an instructor by @handle - a user, its own AI or an AI it shares an organization with -
// and removes one; the change is audited. Adding one that is already there, or removing one that is not, changes
// nothing.
func TestSR210TheHostAddsAndRemovesInstructors(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	host, ai := roles.Manager, roles.TargetAI
	own := env.NewAIIn(host, roles.Org.ID, "helper") // another AI of the host
	env.NewAIIn(roles.Peer, roles.Org.ID, "scout")   // an AI of a colleague, in the same organization

	added := testkit.Decode[dto.AgentInstructor](addInstructor(host, ai, "@"+roles.Outsider.User.Username).RequireStatus(http.StatusCreated))
	require.Equal(t, roles.Outsider.User.ID, added.Intelligence.ID, "any user of the instance")
	added = testkit.Decode[dto.AgentInstructor](addInstructor(host, ai, "helper").RequireStatus(http.StatusCreated))
	require.Equal(t, own, added.Intelligence.ID, "an AI of the host, without @")
	require.Equal(t, dto.IntelligenceKindAI, added.Intelligence.Kind)
	addInstructor(host, ai, "@SCOUT").RequireStatus(http.StatusCreated) // an AI the host shares an organization with, any case
	require.Equal(t, []string{"acme-instructor", "acme-outsider", "helper", "scout"},
		instructorHandles(testkit.Decode[dto.AgentInstructors](host.Get(instructorsPath(ai)).RequireStatus(http.StatusOK))), "oldest first")

	again := testkit.Decode[dto.AgentInstructor](addInstructor(host, ai, "@helper").RequireStatus(http.StatusCreated))
	require.Equal(t, own, again.Intelligence.ID)
	require.Equal(t, 4, countRows(t, env, `SELECT COUNT(*) FROM agent_instructors WHERE agent_iid = ?`, ai), "adding again changes nothing")
	require.Equal(t, 3, countRows(t, env, `SELECT COUNT(*) FROM audit_log WHERE action = 'agent.instructor_add' AND target_id = ?`, ai),
		"each addition is audited once")

	host.Delete(instructorsPath(ai) + "/" + own).RequireStatus(http.StatusNoContent)
	host.Delete(instructorsPath(ai) + "/" + own).RequireStatus(http.StatusNoContent) // not one any more: nothing to do
	require.Equal(t, []string{"acme-instructor", "acme-outsider", "scout"},
		instructorHandles(testkit.Decode[dto.AgentInstructors](host.Get(instructorsPath(ai)).RequireStatus(http.StatusOK))))
	entry := findAudit(t, auditPage(roles.InstanceAdmin, "").Items, "agent.instructor_remove")
	require.Equal(t, host.User.ID, entry.Actor.ID)
	require.Equal(t, ai, entry.TargetID)
	require.Equal(t, own, entry.Detail["instructor"])
}

// SR2-10: only the host changes the instructors. The AI itself - an AI does not change its own instructors - its
// user instructor, the admin of its organization and its members are refused (403), whoever cannot see the AI
// finds nothing (404), and every refusal changes nothing.
func TestSR210OnlyTheHostChangesTheInstructors(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	ai := roles.TargetAI
	itself := roles.Manager.Token(dto.TokenKindAgent, "laptop").AsAgent("acme-ai")
	refusals := map[string]*testkit.Client{"the AI itself": itself}
	for _, rc := range roles.All() {
		if rc.Name != "manager" {
			refusals[rc.Name] = rc.Client
		}
	}
	for name, c := range refusals {
		t.Run(name, func(t *testing.T) {
			c := bound(t, c)
			want := http.StatusForbidden
			if name == "outsider" || name == "instance_admin" {
				want = http.StatusNotFound
			}
			requireRefusal(t, addInstructor(c, ai, "@"+roles.Peer.User.Username), want)
			requireRefusal(t, c.Delete(instructorsPath(ai)+"/"+roles.Instructor.User.ID), want)
		})
	}
	require.Equal(t, 1, countRows(t, env, `SELECT COUNT(*) FROM agent_instructors WHERE agent_iid = ?`, ai), "nothing changed")
	env.Anon().Post(instructorsPath(ai), dto.AddInstructorRequest{Handle: "@x"}).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
}

// SR2-10: what cannot instruct the AI is refused and adds nothing: an unknown handle, and an AI the host cannot
// see, are 404 contact_not_found (nothing is told about AIs the host does not see); the AI itself and its host are
// 422 on handle (the host instructs it anyway).
func TestSR210InstructorRefusals(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	host, ai := roles.Manager, roles.TargetAI
	env.NewAI(roles.Outsider, "hidden") // an AI of the outsider's own organization

	for _, handle := range []string{"nobody-by-that-name", "", "@", "hidden", "@hidden"} {
		addInstructor(host, ai, handle).RequireError(http.StatusNotFound, dto.ErrorCodeContactNotFound)
	}
	for _, handle := range []string{"@acme-ai", "@" + host.User.Username} {
		e := addInstructor(host, ai, handle).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, e.Fields, "handle", handle)
	}
	host.DoRaw(http.MethodPost, instructorsPath(ai), "application/json", nil).RequireStatus(http.StatusBadRequest)
	require.Equal(t, 1, countRows(t, env, `SELECT COUNT(*) FROM agent_instructors WHERE agent_iid = ?`, ai), "nothing was added")
}

// SR2-10, SR2-11: the AI's profile shows who instructs it, and whether the caller may change that (the host
// only); adding an instructor reaches the AI's live session at once as a fresh welcome that names it.
func TestSR210TheProfileAndTheLiveSessionFollowTheInstructors(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	host, ai := roles.Manager, roles.TargetAI

	mine := profileOf(t, host, ai).AI
	require.NotNil(t, mine.InstructedBy)
	require.Equal(t, []string{"acme-instructor"}, handlesOf(*mine.InstructedBy))
	require.True(t, mine.CanEditInstructors, "the host changes them")
	require.Equal(t, host.User.ID, mine.Owner.ID, "the host is the profile's owner")
	for _, c := range []*testkit.Client{roles.Instructor, roles.Admin, roles.Member} {
		theirs := profileOf(t, c, ai).AI
		require.NotNil(t, theirs.InstructedBy, "%s shares its home organization", c.User.Username)
		require.False(t, theirs.CanEditInstructors, c.User.Username)
	}

	conn, resp := host.Token(dto.TokenKindAgent, "laptop").DialAgent("acme-ai")
	require.NotNil(t, conn, resp)
	defer conn.Close()
	first := conn.Welcome(wsWait)
	require.Equal(t, []string{"acme-instructor"}, handlesOf(first.Instructors))
	conn.Drain(300 * time.Millisecond)

	addInstructor(host, ai, "@"+roles.Peer.User.Username).RequireStatus(http.StatusCreated)
	again := conn.Welcome(wsWait)
	require.Equal(t, []string{"acme-instructor", "acme-peer"}, handlesOf(again.Instructors), "SR2-11: the live session is told")
	require.Equal(t, first.Session, again.Session, "the same session")
	require.Equal(t, []string{"acme-instructor", "acme-peer"}, handlesOf(*profileOf(t, host, ai).AI.InstructedBy))
}

// SR2-13: a user instructor edits the AI's profile and settings and ends its session, like its host; an AI
// instructor does not (an AI token reaches no profile route), and neither does an admin of its organization who
// neither hosts nor instructs it.
func TestSR213AUserInstructorEditsTheAIAndAnAIInstructorDoesNot(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	ai := roles.TargetAI
	lead := env.NewAIIn(roles.Manager, roles.Org.ID, "lead")
	env.AddInstructor(ai, lead)

	patchProfile(roles.Instructor, ai, dto.UpdateProfileRequest{Bio: ptr("Edited by an instructor")}).RequireStatus(http.StatusOK)
	desc := "set by an instructor"
	roles.Instructor.Patch("/api/v1/agents/"+ai, dto.UpdateAgentRequest{Description: &desc}).RequireStatus(http.StatusOK)
	roles.Instructor.Delete(sessionPath(ai)).RequireStatus(http.StatusNoContent)
	require.True(t, profileOf(t, roles.Instructor, ai).CanEdit)

	asLead := roles.Manager.Token(dto.TokenKindAgent, "laptop").AsAgent("lead")
	requireRefusal(t, patchProfile(asLead, ai, dto.UpdateProfileRequest{Bio: ptr("Edited by an AI")}), http.StatusForbidden)
	requireRefusal(t, asLead.Delete(sessionPath(ai)), http.StatusForbidden)

	requireRefusal(t, patchProfile(roles.Admin, ai, dto.UpdateProfileRequest{Bio: ptr("Edited by the admin")}), http.StatusForbidden)
	require.False(t, profileOf(t, roles.Admin, ai).CanEdit)
	require.Equal(t, "Edited by an instructor", profileOf(t, roles.Admin, ai).Bio)
}
