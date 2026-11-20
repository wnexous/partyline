package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/orgs"
)

// offline waits until the server has seen the AI's last session end, so a refusal that follows can be told
// not to have attached anything.
func (w *placementWorld) offline(t *testing.T, id string) {
	t.Helper()
	require.Eventually(t, func() bool { return !w.env.App.Agents.Online(id) }, wsWait, 10*time.Millisecond)
}

// ORG-04: an AI keeps the home organization it was created with. A later start that names that organization
// (slug, any case, or id) is fine; one that names any other is 409 organization_mismatch - the host's own other
// organization, one the host is not in and one that does not exist alike - and the message and details name
// the home organization. A parent on a later start changes nothing (SR2-09): neither the creator nor the
// instructors.
func TestAnExistingAIKeepsItsHomeOrganization(t *testing.T) {
	for _, path := range startPaths {
		t.Run(path.name, func(t *testing.T) {
			w := newPlacementWorld(t)
			tok := w.agentToken(w.bob)
			f := placementFolder("homebody")
			id, refused := path.start(t, tok, f)
			require.Nil(t, refused)
			member := orgs.Member{Permission: orgs.PermissionMember}
			w.requireStartedAs(t, id, w.acme.ID, w.bob.User.ID, member)
			bobsOwn := w.bob.PersonalOrg()
			bobsParent := w.env.NewAIIn(w.bob, w.acme.ID, "bobparent")

			for _, ref := range []string{"acme", "ACME", w.acme.ID} {
				t.Run("the home organization as "+ref, func(t *testing.T) {
					again := f
					again.Org = ref
					got, refused := path.start(t, tok, again)
					require.Nil(t, refused)
					require.Equal(t, id, got, "the same AI")
				})
			}
			for name, ref := range map[string]string{
				"another organization of the host":       bobsOwn.Slug,
				"an organization the host is not in":     w.globex.Slug,
				"an organization that does not exist":    "no-such-organization",
				"another organization of the host by id": bobsOwn.ID,
			} {
				t.Run(name, func(t *testing.T) {
					w.offline(t, id)
					before := w.census(t)
					again := f
					again.Org = ref
					_, refused := path.start(t, tok, again)
					e := w.requireRefused(t, before, refused, http.StatusConflict, dto.ErrorCodeOrganizationMismatch)
					require.Equal(t, "acme", e.Details[dto.MismatchDetailOrganization])
					require.Equal(t, "Acme", e.Details[dto.MismatchDetailName])
					require.Contains(t, e.Message, "Acme", "the message names the home organization")
					w.requireStartedAs(t, id, w.acme.ID, w.bob.User.ID, member)
					require.False(t, w.env.App.Agents.Online(id), "a refused session never attaches")
				})
			}
			t.Run("SR2-09: a parent on a later start does not change the creator nor the instructors", func(t *testing.T) {
				again := f
				again.Parent = "@bobparent"
				got, refused := path.start(t, tok, again)
				require.Nil(t, refused)
				require.Equal(t, id, got)
				w.requireStartedAs(t, id, w.acme.ID, w.bob.User.ID, member)
				require.Empty(t, instructorsOf(t, w.env, id), "the parent AI does not become an instructor")
				trusted, err := w.env.App.Authz.Trusts(context.Background(), bobsParent, id)
				require.NoError(t, err)
				require.False(t, trusted)
			})
		})
	}
}
