package orgs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/events"
)

// SR2-10: only the host adds and removes the instructors of an AI; the AI itself, an instructor, an admin and
// anyone else are forbidden, and an id that is no AI is not found.
func TestSR210OnlyTheHostChangesInstructors(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme() // w1: hosted by lee, instructed by notes and mia
	zed := f.user("zed")
	for _, actor := range []string{a.w1, a.mia, a.notes, a.alice, a.bob, ""} {
		_, err := f.svc.AddInstructor(ctx, actor, a.w1, "zed")
		require.ErrorIs(t, err, ErrForbidden, "add by %q", actor)
		require.ErrorIs(t, f.svc.RemoveInstructor(ctx, actor, a.w1, a.mia), ErrForbidden, "remove by %q", actor)
	}
	_, err := f.svc.AddInstructor(ctx, a.lee, "a-nothing", "zed")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = f.svc.AddInstructor(ctx, a.lee, a.bob, "zed")
	require.ErrorIs(t, err, ErrNotFound, "a user has no instructors")

	next := f.events()
	in, err := f.svc.AddInstructor(ctx, a.lee, a.w1, "@zed")
	require.NoError(t, err)
	require.Equal(t, zed, in.ID)
	require.Equal(t, a.lee, in.CreatedBy)
	ok, err := f.z.Trusts(ctx, zed, a.w1)
	require.NoError(t, err)
	require.True(t, ok, "SR2-07: the new instructor is trusted at once")
	got := next()
	require.Contains(t, got, events.IntelligenceChanged{ID: a.w1})
	require.Contains(t, got, events.OrgChanged{OrgID: a.org}, "the live sessions read their welcome again (SR2-11)")
	require.Contains(t, f.audited(), "agent.instructor_add "+a.w1)

	again, err := f.svc.AddInstructor(ctx, a.lee, a.w1, "zed")
	require.NoError(t, err, "adding one that is there changes nothing")
	require.Equal(t, in.ID, again.ID)
	require.Empty(t, next())

	require.NoError(t, f.svc.RemoveInstructor(ctx, a.lee, a.w1, zed))
	ok, err = f.z.Trusts(ctx, zed, a.w1)
	require.NoError(t, err)
	require.False(t, ok)
	require.Contains(t, f.audited(), "agent.instructor_remove "+a.w1)
	require.NoError(t, f.svc.RemoveInstructor(ctx, a.lee, a.w1, zed), "removing one that is not there is nothing")
}

// SR2-10: an instructor is an enabled user or an AI the host sees (one it hosts or shares an organization with);
// the AI itself and its host are no instructors; a person, a stranger AI and nobody are not found.
func TestSR210AddInstructorChecksTheCandidate(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.acme()
	gone := f.user("gone")
	f.exec(`UPDATE users SET disabled_at = 1 WHERE id = ?`, gone)
	far := f.user("far")
	stranger := f.ai("stranger", far) // hosted by someone lee shares nothing with
	globex := f.org("globex")
	f.join(globex, far, PermissionMember)
	shared := f.ai("shared", far)
	f.home(shared, a.org, far, PermissionMember) // lives in acme, which lee belongs to
	mine := f.ai("mine", a.lee)                  // another AI of lee, in no organization

	for _, handle := range []string{"nobody", "gone", "stranger", ""} {
		_, err := f.svc.AddInstructor(ctx, a.lee, a.w1, handle)
		require.ErrorIs(t, err, ErrContactNotFound, handle)
	}
	_, err := f.svc.AddInstructor(ctx, a.lee, a.w1, "w1")
	invalid(t, err, "handle")
	_, err = f.svc.AddInstructor(ctx, a.lee, a.w1, "lee")
	invalid(t, err, "handle")
	for _, ai := range []string{"shared", "mine", "far"} {
		_, err := f.svc.AddInstructor(ctx, a.lee, a.w1, ai)
		require.NoError(t, err, ai)
	}
	host, list, err := f.svc.Instructors(ctx, a.lee, a.w1)
	require.NoError(t, err)
	require.Equal(t, a.lee, host.ID)
	ids := make([]string, len(list))
	for i, in := range list {
		ids[i] = in.ID
	}
	require.ElementsMatch(t, []string{a.notes, a.mia, shared, mine, far}, ids)
	_ = stranger

	// The host and the AI itself read them; anyone else is forbidden.
	_, _, err = f.svc.Instructors(ctx, a.w1, a.w1)
	require.NoError(t, err)
	for _, who := range []string{a.mia, a.alice, ""} {
		_, _, err := f.svc.Instructors(ctx, who, a.w1)
		require.ErrorIs(t, err, ErrForbidden, who)
	}
}
