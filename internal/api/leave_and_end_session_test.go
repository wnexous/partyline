package api_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/testkit"
)

// requireEnded checks that a live session was ended by a manager: the socket closes with 4002 (the code v0.5 CLIs
// already obey) and the reason names who did it.
func requireEnded(t *testing.T, conn *testkit.AgentConn, handle string) {
	t.Helper()
	closed := conn.Closed(wsWait)
	var ce websocket.CloseError
	require.True(t, errors.As(closed, &ce), "the session was closed: %v", closed)
	require.Equal(t, websocket.StatusCode(4002), ce.Code)
	require.Equal(t, "ended by @"+handle, ce.Reason)
}

// SR2-13 (matrix of DELETE /agents/{id}/session): whoever manages the AI ends its live session - its host
// (Manager) and its user instructor; the admin of its organization, a peer and a member see the AI and may not
// (403), and whoever cannot see it gets 404 - the instance admin included. Each refusal leaves the session up; the
// end closes the socket with 4002 and "ended by @handle".
func TestSR213TheHostAndAUserInstructorEndALiveSession(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	ai := r.TargetAI
	requireStanding(t, env, r.Org.ID, r.Manager.User.ID, ai, true, true)
	requireStanding(t, env, r.Org.ID, r.Instructor.User.ID, ai, true, false)
	requireStanding(t, env, r.Org.ID, r.Admin.User.ID, ai, false, true)
	requireStanding(t, env, r.Org.ID, r.Peer.User.ID, ai, false, false)
	host := r.Manager.Token(dto.TokenKindAgent, "laptop")
	dial := func() *testkit.AgentConn {
		conn, resp := host.DialAgent("acme-ai")
		require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
		conn.Welcome(wsWait)
		return conn
	}
	live := dial()

	d := &deletion{env: env, roles: r}
	for _, c := range []matrixCase{
		{"peer", http.StatusForbidden}, {"target", http.StatusForbidden}, {"member", http.StatusForbidden},
		{"admin", http.StatusForbidden},
		{"outsider", http.StatusNotFound}, {"instance_admin", http.StatusNotFound},
		{"anonymous", http.StatusUnauthorized}, {"agent token", http.StatusForbidden}, {"the AI itself", http.StatusForbidden},
	} {
		t.Run("refused/"+c.role, func(t *testing.T) {
			requireRefusal(t, bound(t, d.client(c.role)).Delete(sessionPath(ai)), c.status)
		})
	}
	require.NoError(t, live.Closed(300*time.Millisecond), "no refusal ended the session")
	require.True(t, env.App.Agents.Online(ai))

	r.Manager.Delete(sessionPath(ai)).RequireStatus(http.StatusNoContent)
	requireEnded(t, live, handleOf(t, r.Manager))
	require.False(t, env.App.Agents.Online(ai), "an ended session is offline")
	requireAIIntact(t, env, ai, r.Org.ID)

	live = dial() // the AI may connect again: ending a session does not delete or ban it
	r.Instructor.Delete(sessionPath(ai)).RequireStatus(http.StatusNoContent)
	requireEnded(t, live, handleOf(t, r.Instructor))

	r.Instructor.Delete(sessionPath(ai)).RequireStatus(http.StatusNoContent) // nothing is live: nothing to end
	requireRefusal(t, r.Manager.Delete(sessionPath(r.Member.User.ID)), http.StatusNotFound)
	requireRefusal(t, r.Manager.Delete(sessionPath(store.NewID())), http.StatusNotFound)
}

// SR2-13: the host of the workers the AI lead started ends their live sessions; sam, who neither hosts nor
// instructs them, cannot, and neither can lead, which instructs them but is no user.
func TestSR213TheHostEndsTheSessionsOfTheWorkers(t *testing.T) {
	w := newLeadWorld(t)

	requireRefusal(t, w.sam.Delete(sessionPath(w.workers[0])), http.StatusForbidden)
	requireRefusal(t, w.lee.Token(dto.TokenKindAgent, "laptop").AsAgent("lead").Delete(sessionPath(w.workers[0])), http.StatusForbidden)
	require.NoError(t, w.live.Closed(300*time.Millisecond), "neither ended it")

	w.lee.Delete(sessionPath(w.workers[0])).RequireStatus(http.StatusNoContent)
	requireEnded(t, w.live, handleOf(t, w.lee))
	requireAIIntact(t, w.env, w.workers[0], w.acme.ID)
}

// OMEM-05, POS-05 (leave, "Self is leave"): a member leaves Acme with 204 and keeps their account. The only
// admin cannot - 409 last_admin, the call that differs from the next only in Acme having a second admin (L-018) -
// and with a second admin the first leaves and the second stays.
func TestLeavingAnOrganization(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	r := env.OrgRoles("acme")

	r.Member.Delete(memberPath(r.Org.ID, r.Member.User.ID)).RequireStatus(http.StatusNoContent)
	requireRemovedOnly(t, env, r.Org.ID, r.Member)

	r.Admin.Delete(memberPath(r.Org.ID, r.Admin.User.ID)).RequireError(http.StatusConflict, dto.ErrorCodeLastAdmin)
	require.True(t, isMember(t, env, r.Org.ID, r.Admin.User.ID), "the only admin is still in")

	_, err := env.App.Orgs.SetPermission(ctx, r.Admin.User.ID, r.Org.ID, r.Manager.User.ID, orgs.PermissionAdmin)
	require.NoError(t, err)
	r.Admin.Delete(memberPath(r.Org.ID, r.Admin.User.ID)).RequireStatus(http.StatusNoContent)
	requireRemovedOnly(t, env, r.Org.ID, r.Admin)
	require.True(t, isMember(t, env, r.Org.ID, r.Manager.User.ID))
	require.NoError(t, env.App.Authz.RequireAdmin(ctx, r.Org.ID, r.Manager.User.ID), "and Manager administers it now")
}

// What the caller cannot see is 404 (design: outsiders get 404 on anything they cannot see), not a refusal that
// tells it exists: an organization they are not in, an identity that is not a member of it, an id that is
// nothing. The organization is found by id or by address in any case.
func TestDeleteInAnOrganizationOfWhatTheCallerCannotSee(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	other := env.OrgRoles("globex")

	requireRefusal(t, r.Admin.Delete(memberPath("no-such-organization", r.Target.User.ID)), http.StatusNotFound)
	requireRefusal(t, r.Admin.Delete(memberPath(r.Org.ID, store.NewID())), http.StatusNotFound)
	requireRefusal(t, r.Admin.Delete(memberPath(r.Org.ID, r.Outsider.User.ID)), http.StatusNotFound)   // not a member of Acme
	requireRefusal(t, r.Admin.Delete(memberPath(r.Org.ID, other.Target.User.ID)), http.StatusNotFound) // a member of Globex only
	requireRefusal(t, r.Admin.Delete(memberPath(r.Org.ID, other.TargetAI)), http.StatusNotFound)       // an AI of Globex only
	requireRefusal(t, r.Admin.Delete(memberPath(other.Org.ID, r.Target.User.ID)), http.StatusNotFound) // Globex is not Acme's admin's
	requireRefusal(t, r.Member.Delete(memberPath(r.Org.ID, r.Outsider.User.ID)), http.StatusNotFound)  // 404 for a member too, not 403
	requireRefusal(t, r.Outsider.Delete(memberPath(r.Org.ID, r.Member.User.ID)), http.StatusNotFound)
	require.True(t, isMember(t, env, r.Org.ID, r.Target.User.ID) && isMember(t, env, other.Org.ID, other.Target.User.ID))
	requireAIIntact(t, env, other.TargetAI, other.Org.ID)

	r.Admin.Delete(memberPath("ACME", r.Peer.User.ID)).RequireStatus(http.StatusNoContent) // the address, in another case
	require.False(t, isMember(t, env, r.Org.ID, r.Peer.User.ID))
	r.Admin.Delete(memberPath(r.Org.ID, r.Member.User.ID)).RequireStatus(http.StatusNoContent) // the id
	require.False(t, isMember(t, env, r.Org.ID, r.Member.User.ID))
}

// AD-016, SR2-12, SR2-13: an AI is seen by its host and by the members of its organizations, an organization
// only by its members. A host who left the organization still hosts the AI there: it ends its session and deletes
// it by the AI's own route, while the organization's route finds nothing (404).
func TestSR212AHostWhoLeftTheOrganizationStillDeletesItsAI(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	hank := env.NewUser("hank")
	env.AddMember(r.Org.ID, hank.User.ID, orgs.PermissionMember)
	ai := env.NewAIIn(hank, r.Org.ID, "ward")
	require.NoError(t, env.App.Orgs.Leave(ctx, hank.User.ID, r.Org.ID))

	requireRefusal(t, hank.Delete(memberPath(r.Org.ID, ai)), http.StatusNotFound)
	hank.Delete(sessionPath(ai)).RequireStatus(http.StatusNoContent) // nothing live, and hank manages it
	requireAIIntact(t, env, ai, r.Org.ID)

	hank.Delete(agentDeletePath(ai)).RequireStatus(http.StatusNoContent)
	requireDeletedForGood(t, env, ai)
}
