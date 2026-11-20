package orgs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
)

// The standing of an identity is its organization and its permission there - what the welcome frame and GET
// /agent/whoami tell. Positions and managers are gone (SR2-04, SR2-11).
func TestStandingOf(t *testing.T) {
	f := newFixture(t)
	a := f.acme()
	ctx := context.Background()
	s, ok, err := f.z.StandingOf(ctx, a.org, a.walt)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, a.org, s.Org.ID)
	require.Equal(t, "acme", s.Org.Slug)
	require.Equal(t, PermissionMember, s.Permission)
	s, ok, err = f.z.StandingOf(ctx, a.org, a.alice)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, PermissionAdmin, s.Permission)

	for _, tc := range [][2]string{{a.org, f.user("stranger")}, {"o-nothing", a.alice}, {"", ""}} {
		_, ok, err := f.z.StandingOf(ctx, tc[0], tc[1])
		require.NoError(t, err)
		require.False(t, ok, "%v has no standing", tc)
	}
}

// DefaultStandingOf reads the standing in the organization a new AI of the identity joins.
func TestDefaultStandingOf(t *testing.T) {
	f := newFixture(t)
	a := f.acme()
	ctx := context.Background()
	f.tx(func(ctx context.Context, q *db.Queries) error {
		return q.SetUserDefaultOrg(ctx, db.SetUserDefaultOrgParams{DefaultOrgID: nullable(a.org), ID: a.walt})
	})
	s, ok, err := f.z.DefaultStandingOf(ctx, a.walt)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, a.org, s.Org.ID)

	s, ok, err = f.z.DefaultStandingOf(ctx, a.dee)
	require.NoError(t, err)
	require.True(t, ok, "with no choice, the organization the user has belonged to the longest")
	require.Equal(t, a.org, s.Org.ID)

	_, ok, err = f.z.DefaultStandingOf(ctx, f.user("loner"))
	require.NoError(t, err)
	require.False(t, ok, "belonging to none: no standing")
}
