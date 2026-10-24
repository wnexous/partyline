package orgs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
)

// POS-03, POS-10: the standing of an identity is its organization, the name of its position and the
// identities that manage it, nearest first - the facts the welcome frame and GET /agent/whoami tell.
func TestStandingOf(t *testing.T) {
	f := newFixture(t)
	a := f.acme()
	ctx := context.Background()
	stand := func(org, iid string, limit int) (Standing, bool) {
		t.Helper()
		s, ok, err := f.z.StandingOf(ctx, org, iid, limit)
		require.NoError(t, err)
		return s, ok
	}

	t.Run("a worker lists every manager above it, nearest first", func(t *testing.T) {
		s, ok := stand(a.org, a.walt, -1)
		require.True(t, ok)
		require.Equal(t, a.org, s.Org.ID)
		require.Equal(t, "acme", s.Org.Slug)
		require.Equal(t, "Worktrees", s.Position)
		require.Equal(t, []string{a.lee, a.mia, a.dora, a.alice}, s.Managers, "Lead, Manager, Director, Admin")
	})
	t.Run("the limit keeps the nearest managers", func(t *testing.T) {
		for limit, want := range map[int][]string{
			0: {}, 1: {a.lee}, 2: {a.lee, a.mia}, 3: {a.lee, a.mia, a.dora}, 4: {a.lee, a.mia, a.dora, a.alice}, 5: {a.lee, a.mia, a.dora, a.alice},
		} {
			s, ok := stand(a.org, a.walt, limit)
			require.True(t, ok)
			require.Equal(t, want, append([]string{}, s.Managers...), "limit %d", limit)
		}
	})
	t.Run("the holder of the root position has no manager", func(t *testing.T) {
		s, ok := stand(a.org, a.alice, tenManagers)
		require.True(t, ok)
		require.Equal(t, "Admin", s.Position)
		require.Empty(t, s.Managers)
	})
	t.Run("an AI with no position is managed by its creator and then by whoever manages the creator", func(t *testing.T) {
		s, ok := stand(a.org, a.notes, tenManagers)
		require.True(t, ok)
		require.Equal(t, "", s.Position)
		require.Equal(t, []string{a.lee, a.mia, a.dora, a.alice}, s.Managers)
	})
	t.Run("a member that holds no position and has no creator has no manager", func(t *testing.T) {
		s, ok := stand(a.org, a.bob, tenManagers)
		require.True(t, ok)
		require.Equal(t, "", s.Position)
		require.Empty(t, s.Managers)
	})
	t.Run("a guest is managed by its inviter", func(t *testing.T) {
		s, ok := stand(a.org, a.gus, tenManagers)
		require.True(t, ok)
		require.Equal(t, []string{a.bob}, s.Managers)
	})
	t.Run("someone who is not a member, and an organization that does not exist, have no standing", func(t *testing.T) {
		stranger := f.user("stranger")
		_, ok := stand(a.org, stranger, tenManagers)
		require.False(t, ok)
		_, ok = stand("o-nothing", a.alice, tenManagers)
		require.False(t, ok)
		_, ok = stand("", "", tenManagers)
		require.False(t, ok)
	})
	t.Run("reading a standing reads one chart", func(t *testing.T) {
		before := f.z.Loads()
		_, _ = stand(a.org, a.walt, tenManagers)
		require.Equal(t, before+1, f.z.Loads())
	})
}

// tenManagers is the limit the welcome frame asks for (dto.MaxManagers); this package does not import dto.
const tenManagers = 10

// DefaultStandingOf reads the standing in the organization a new AI of the identity joins.
func TestDefaultStandingOf(t *testing.T) {
	f := newFixture(t)
	a := f.acme()
	ctx := context.Background()

	t.Run("the chosen default organization", func(t *testing.T) {
		f.tx(func(ctx context.Context, q *db.Queries) error {
			return q.SetUserDefaultOrg(ctx, db.SetUserDefaultOrgParams{DefaultOrgID: nullable(a.org), ID: a.walt})
		})
		s, ok, err := f.z.DefaultStandingOf(ctx, a.walt, tenManagers)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, a.org, s.Org.ID)
		require.Equal(t, "Worktrees", s.Position)
		require.Equal(t, []string{a.lee, a.mia, a.dora, a.alice}, s.Managers)
	})
	t.Run("with no choice, the organization the user has belonged to the longest", func(t *testing.T) {
		s, ok, err := f.z.DefaultStandingOf(ctx, a.dee, tenManagers)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, a.org, s.Org.ID)
		require.Equal(t, []string{a.dora, a.alice}, s.Managers)
	})
	t.Run("belonging to none: no standing", func(t *testing.T) {
		_, ok, err := f.z.DefaultStandingOf(ctx, f.user("loner"), tenManagers)
		require.NoError(t, err)
		require.False(t, ok)
	})
}
