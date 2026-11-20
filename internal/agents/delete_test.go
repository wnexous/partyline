package agents

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store/db"
)

// addTo makes the identity a member of the organization.
func (f *fixture) addTo(t *testing.T, org, iid string, perm orgs.Permission) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, f.st.WithTx(ctx, func(q *db.Queries) error {
		return orgs.AddMember(ctx, q, orgs.AddMemberInput{OrgID: org, IID: iid, Permission: perm, Now: t0})
	}))
}

// instruct makes the identity an instructor of the AI (SR2-06).
func (f *fixture) instruct(t *testing.T, ai, iid string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, f.st.WithTx(ctx, func(q *db.Queries) error {
		_, err := q.AddAgentInstructor(ctx, db.AddAgentInstructorParams{AgentIid: ai, InstructorIid: iid, CreatedAt: t0.UnixMilli()})
		return err
	}))
}

// aiIn starts the host's AI in the organization, as `partyline <harness> --org` would.
func (f *fixture) aiIn(t *testing.T, host authn.Principal, org db.Organization, name string) string {
	t.Helper()
	a, err := f.reg.ResolveFolder(context.Background(), host.UserID, FolderID{Machine: "devbox", Folder: "/work/" + name, Org: org.Slug}, false)
	require.NoError(t, err)
	require.Equal(t, org.ID, a.OrgID.String)
	return a.ID
}

// SR2-12, AD-016: an AI is deleted by its host or by an admin of its home organization. Whoever else sees it - a
// member, the admin of another organization it is only a member of, a user that instructs it - gets
// access.ErrForbidden, and whoever does not see it access.ErrNotFound. Each case is the only thing that differs
// from the last (L-018).
func TestSR212DeleteIsTheHostsOrAHomeAdmins(t *testing.T) {
	ctx := context.Background()
	newWorld := func(t *testing.T) (f *fixture, ai string, who map[string]authn.Principal) {
		f = newFixture(t)
		alice, bob, gina, hank, ivy, stranger := f.user(t, "alice"), f.user(t, "bobby"), f.user(t, "gina"), f.user(t, "hank"), f.user(t, "ivy"), f.user(t, "stranger")
		team, globex := f.teamOf(t, alice, "Team"), f.teamOf(t, gina, "Globex")
		f.addTo(t, team.ID, bob.UserID, orgs.PermissionMember)
		f.addTo(t, team.ID, hank.UserID, orgs.PermissionMember)
		ai = f.aiIn(t, hank, team, "ward") // hosted by hank, its home is Team
		f.addTo(t, globex.ID, ai, orgs.PermissionMember)
		f.instruct(t, ai, ivy.UserID)
		who = map[string]authn.Principal{
			"admin of the home": alice, "member of the home": bob, "admin of the other organization": gina, "host": hank,
			"instructor": ivy, "stranger": stranger,
		}
		return f, ai, who
	}

	f, ai, who := newWorld(t)
	require.ErrorIs(t, f.reg.Delete(ctx, who["member of the home"], ai), access.ErrForbidden)
	require.ErrorIs(t, f.reg.Delete(ctx, who["admin of the other organization"], ai), access.ErrForbidden, "it sees the AI, which is a member of its organization")
	require.ErrorIs(t, f.reg.Delete(ctx, who["instructor"], ai), access.ErrForbidden, "an instructor sees it and does not delete it")
	require.ErrorIs(t, f.reg.Delete(ctx, who["stranger"], ai), access.ErrNotFound)
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM agents WHERE id = ?", ai), "no refusal deleted it")
	require.NoError(t, f.reg.Delete(ctx, who["admin of the home"], ai))
	require.Zero(t, f.count(t, "SELECT COUNT(*) FROM agents WHERE id = ?", ai))

	f, ai, who = newWorld(t)
	// the host deletes it, even when it left every organization
	f.exec(t, "DELETE FROM org_members WHERE iid = ?", who["host"].UserID)
	require.NoError(t, f.reg.Delete(ctx, who["host"], ai))
	require.Zero(t, f.count(t, "SELECT COUNT(*) FROM agents WHERE id = ?", ai))
	require.Zero(t, f.count(t, "SELECT COUNT(*) FROM agent_instructors WHERE agent_iid = ?", ai), "its links go with it")
}

// An AI no organization owns - only a fixture, since the upgrade gives every AI a home - is deleted by its host.
func TestDeleteOfAnAIWithoutAHomeIsTheHosts(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice, bob := f.user(t, "alice"), f.user(t, "bobby")
	legacy := f.legacy(t, alice, "old", "old")

	require.ErrorIs(t, f.reg.Delete(ctx, bob, legacy), access.ErrNotFound)
	require.NoError(t, f.reg.Delete(ctx, alice, legacy))
	require.Zero(t, f.count(t, "SELECT COUNT(*) FROM agents WHERE id = ?", legacy))
	require.ErrorIs(t, f.reg.Delete(ctx, alice, legacy), access.ErrNotFound, "and once it is gone it is not found")
}

// OMEM-05: an AI that is the only admin of an organization cannot be deleted, wherever its home is; the check is in
// the deletion's transaction, so nothing is deleted when it refuses.
func TestDeleteRefusesAnAIThatIsTheOnlyAdminOfAnOrganization(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice, gina := f.user(t, "alice"), f.user(t, "gina")
	team, globex := f.teamOf(t, alice, "Team"), f.teamOf(t, gina, "Globex")
	ai := f.aiIn(t, alice, team, "steward")
	f.addTo(t, globex.ID, ai, orgs.PermissionAdmin)
	f.exec(t, "UPDATE org_members SET permission = 'member' WHERE org_id = ? AND iid = ?", globex.ID, gina.UserID)

	require.ErrorIs(t, f.reg.Delete(ctx, alice, ai), orgs.ErrLastAdmin)
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM agents WHERE id = ?", ai))
	require.Equal(t, 2, f.count(t, "SELECT COUNT(*) FROM org_members WHERE iid = ?", ai), "its memberships are all there")

	f.exec(t, "UPDATE org_members SET permission = 'admin' WHERE org_id = ? AND iid = ?", globex.ID, gina.UserID)
	require.NoError(t, f.reg.Delete(ctx, alice, ai))
}

// SR2-13: ending a session closes it with 4002 and the reason "ended by @handle"; the host and the users that
// instruct the AI may; whoever sees the AI and does not manage it is refused and whoever does not see it is not
// found; an AI that is not live has nothing to end.
func TestEndSessionClosesTheLiveSessionOfWhoeverIsManagedByTheCaller(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice, bob, carol, stranger := f.user(t, "alice"), f.user(t, "bobby"), f.user(t, "carol"), f.user(t, "stranger")
	team := f.teamOf(t, alice, "Team")
	f.addTo(t, team.ID, bob.UserID, orgs.PermissionMember)
	f.addTo(t, team.ID, carol.UserID, orgs.PermissionMember)
	ai := f.aiIn(t, bob, team, "worker") // hosted by bob: bob manages it, carol does not
	session := &fakeSession{}
	f.presence.AttachLabeled(ctx, mustAgent(t, f, ai).Agent, session, "claude", "process-one", "", false)

	require.ErrorIs(t, f.reg.EndSession(ctx, carol, ai), access.ErrForbidden)
	require.ErrorIs(t, f.reg.EndSession(ctx, alice, ai), access.ErrForbidden, "an admin is no manager")
	require.ErrorIs(t, f.reg.EndSession(ctx, stranger, ai), access.ErrNotFound)
	require.ErrorIs(t, f.reg.EndSession(ctx, bob, "no-such-ai"), access.ErrNotFound)
	_, _, closed := session.state()
	require.Zero(t, closed, "no refusal ended it")

	require.NoError(t, f.reg.EndSession(ctx, bob, ai))
	code, reason, closed := session.state()
	require.Equal(t, 1, closed)
	require.Equal(t, CloseTakenOver, code)
	require.Equal(t, "ended by @user-bobby", reason, "the handle of the one who ended it")
	require.False(t, f.presence.Online(ai))
	require.NoError(t, f.reg.EndSession(ctx, bob, ai), "nothing is live: nothing to end")
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM agents WHERE id = ?", ai), "the AI is still there")

	// SR2-13: a user that instructs the AI ends its session too.
	f.instruct(t, ai, carol.UserID)
	again := &fakeSession{}
	f.presence.AttachLabeled(ctx, mustAgent(t, f, ai).Agent, again, "claude", "process-two", "", false)
	require.NoError(t, f.reg.EndSession(ctx, carol, ai))
	_, reason, closed = again.state()
	require.Equal(t, 1, closed)
	require.Equal(t, "ended by @user-carol", reason)
}
