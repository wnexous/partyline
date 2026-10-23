package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/testkit"
)

// offline waits until the server has seen the AI's last session end, so a refusal that follows can be told
// not to have attached anything.
func (w *placementWorld) offline(t *testing.T, id string) {
	t.Helper()
	require.Eventually(t, func() bool { return !w.env.App.Agents.Online(id) }, wsWait, 10*time.Millisecond)
}

func (w *placementWorld) positionOf(t *testing.T, org, iid string) string {
	t.Helper()
	m, ok, err := w.env.App.Authz.Member(context.Background(), org, iid)
	require.NoError(t, err)
	require.True(t, ok, "%s is a member of the organization", iid)
	return m.PositionID
}

// ORG-04: an AI keeps the home organization it was created with. A later start that names that organization
// (slug, any case, or id) is fine; one that names any other is 409 organization_mismatch - the host's own other
// organization, one the host is not in and one that does not exist alike - and the message and details name
// the home organization. A parent on a later start changes nothing, least of all the creator.
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
			t.Run("a parent on a later start does not change the creator", func(t *testing.T) {
				again := f
				again.Parent = "@bobparent"
				got, refused := path.start(t, tok, again)
				require.Nil(t, refused)
				require.Equal(t, id, got)
				w.requireStartedAs(t, id, w.acme.ID, w.bob.User.ID, member)
				managed, err := w.env.App.Authz.Manages(context.Background(), bobsParent, id, w.acme.ID)
				require.NoError(t, err)
				require.False(t, managed, "the parent AI does not become its manager")
			})
		})
	}
}

// POS-07, POS-04: a later start with --position moves the AI when the token holder manages it or is an admin of
// its organization, and the position is strictly below the holder's own (admins: any). Outside the rule it is
// 403 and the position stays; a position that does not exist is 422; the position it already holds is nothing,
// and a start without --position never clears it.
func TestALaterPositionMovesTheAIUnderTheSameRule(t *testing.T) {
	for _, path := range startPaths {
		t.Run(path.name, func(t *testing.T) {
			ctx := context.Background()
			w := newPlacementWorld(t)
			bob := w.agentToken(w.bob) // Director
			f := placementFolder("mover")
			f.Position = "Worktrees"
			id, refused := path.start(t, bob, f)
			require.Nil(t, refused)
			require.Equal(t, w.worktrees, w.positionOf(t, w.acme.ID, id))

			asAdmin := func(iid, position string) {
				_, err := w.env.App.Orgs.SetPosition(ctx, w.alice.User.ID, w.acme.ID, iid, position)
				require.NoError(t, err)
			}
			restart := func(host *testkit.Client, folder testkit.Folder, position string) *testkit.Response {
				folder.Position = position
				_, refused := path.start(t, w.agentToken(host), folder)
				return refused
			}
			reset := func() { asAdmin(id, w.worktrees) }
			unchanged := func(t *testing.T, start func() *testkit.Response, status int, code dto.ErrorCode) {
				t.Helper()
				before := w.census(t)
				refused := start()
				require.NotNil(t, refused, "the start was accepted")
				refused.RequireError(status, code)
				require.Equal(t, before, w.census(t))
				require.Equal(t, w.worktrees, w.positionOf(t, w.acme.ID, id), "the position is unchanged")
			}
			bobMoves := func(position string) func() *testkit.Response {
				return func() *testkit.Response { return restart(w.bob, f, position) }
			}

			t.Run("to a position below the holder's", func(t *testing.T) {
				reset()
				bus, unsubscribe := w.env.App.Bus.Subscribe(64)
				defer unsubscribe()
				require.Nil(t, restart(w.bob, f, "Lead developer"))
				require.Equal(t, w.leadDev, w.positionOf(t, w.acme.ID, id))
				require.Eventually(t, func() bool {
					for {
						select {
						case e := <-bus:
							if e == (events.OrgChanged{OrgID: w.acme.ID}) {
								return true
							}
						default:
							return false
						}
					}
				}, wsWait, 10*time.Millisecond, "the organization is told its chart changed")
				all, _, err := audit.New(w.env.App.Store, nil).List(ctx, "")
				require.NoError(t, err)
				var moves int
				for _, e := range all {
					if e.Action == audit.ActionOrgMemberPosition && e.TargetID == w.acme.ID && e.ActorID == w.bob.User.ID {
						moves++
						require.Contains(t, e.DetailJson, id)
					}
				}
				require.Equal(t, 1, moves, "the move is audited once, by the token holder")
			})
			t.Run("to the holder's own level", func(t *testing.T) {
				reset()
				unchanged(t, bobMoves("Director"), http.StatusForbidden, dto.ErrorCodeForbidden)
			})
			t.Run("to a position above the holder", func(t *testing.T) {
				reset()
				unchanged(t, bobMoves("Admin"), http.StatusForbidden, dto.ErrorCodeForbidden)
			})
			t.Run("to a position on another branch", func(t *testing.T) {
				reset()
				unchanged(t, bobMoves("Sentinel"), http.StatusForbidden, dto.ErrorCodeForbidden)
			})
			t.Run("to a position that does not exist", func(t *testing.T) {
				reset()
				unchanged(t, bobMoves("Overlord"), http.StatusUnprocessableEntity, dto.ErrorCodePositionUnknown)
			})
			t.Run("to the position it holds is nothing", func(t *testing.T) {
				reset()
				asAdmin(w.bob.User.ID, "") // bob has lost his position: he could move nothing now
				defer asAdmin(w.bob.User.ID, w.director)
				before := countRows(t, w.env, `SELECT COUNT(*) FROM audit_log WHERE action = 'org.member_position'`)
				require.Nil(t, restart(w.bob, f, "worktrees"))
				require.Equal(t, w.worktrees, w.positionOf(t, w.acme.ID, id))
				require.Equal(t, before, countRows(t, w.env, `SELECT COUNT(*) FROM audit_log WHERE action = 'org.member_position'`), "nothing to audit")
				// the same holder cannot move it elsewhere
				unchanged(t, bobMoves("Lead developer"), http.StatusForbidden, dto.ErrorCodeForbidden)
			})
			t.Run("without --position the position stays", func(t *testing.T) {
				reset()
				require.Nil(t, restart(w.bob, f, ""))
				require.Equal(t, w.worktrees, w.positionOf(t, w.acme.ID, id))
			})

			t.Run("by a holder who does not manage the AI", func(t *testing.T) {
				// orphan is bob's AI, created by erin (a member with no position): bob has the standing for
				// "Lead developer" - he is a Director - but he does not manage the AI.
				of := placementFolder("orphan")
				orphan, refused := path.start(t, bob, of)
				require.Nil(t, refused)
				w.env.SetCreator(orphan, w.erin.User.ID)
				before := w.census(t)
				got := restart(w.bob, of, "Lead developer")
				require.NotNil(t, got)
				got.RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
				require.Equal(t, before, w.census(t))
				require.Empty(t, w.positionOf(t, w.acme.ID, orphan), "the position is unchanged")

				// the same start by the AI's manager moves it: only the relation to the AI differed
				w.env.SetCreator(orphan, w.bob.User.ID)
				require.Nil(t, restart(w.bob, of, "Lead developer"))
				require.Equal(t, w.leadDev, w.positionOf(t, w.acme.ID, orphan))
			})

			t.Run("by an admin, to any position, and a parent does not limit it", func(t *testing.T) {
				af := placementFolder("admins-worker")
				af.Org = "acme" // alice's default is her own organization
				adm, refused := path.start(t, w.agentToken(w.alice), af)
				require.Nil(t, refused)
				require.Nil(t, restart(w.alice, af, "Sentinel"))
				require.Equal(t, w.sentinelPos, w.positionOf(t, w.acme.ID, adm))
				require.Nil(t, restart(w.alice, af, "Admin"))
				require.Equal(t, w.rootPos, w.positionOf(t, w.acme.ID, adm))
				// --parent counts at creation only: alice still acts as herself, an admin, although the
				// AI she names could not place anything above itself
				af.Parent = "@lead"
				require.Nil(t, restart(w.alice, af, "Director"))
				require.Equal(t, w.director, w.positionOf(t, w.acme.ID, adm))
				requireHome(t, w.env, adm, w.acme.ID, w.alice.User.ID)
			})
		})
	}
}
