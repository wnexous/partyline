package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// startPath is one way a session starts an AI: the live WebSocket, or a tools-only call (Codex, the tools
// half of pi and OpenCode). Both resolve the folder and may be the first contact, so the placement rules
// are tested on each (L-034).
type startPath struct {
	name string
	// start returns the id of the AI the session is, or the refusal.
	start func(t *testing.T, tok *testkit.Client, f testkit.Folder) (id string, refused *testkit.Response)
}

var startPaths = []startPath{
	{"a live session", func(t *testing.T, tok *testkit.Client, f testkit.Folder) (string, *testkit.Response) {
		if f.Live == "" {
			f.Live = "placement-test-process" // one process: dialing a folder again is a reconnect
		}
		conn, resp := tok.DialFolder(f)
		if conn == nil {
			return "", resp
		}
		id := conn.Welcome(wsWait).Agent.ID
		require.NoError(t, conn.Conn.CloseNow())
		return id, nil
	}},
	{"a tools-only call", func(_ *testing.T, tok *testkit.Client, f testkit.Folder) (string, *testkit.Response) {
		resp := tok.AsFolder(f).Post("/api/v1/agent/register", nil)
		if resp.Status != http.StatusOK {
			return "", resp
		}
		return testkit.Decode[dto.AgentInfo](resp).ID, nil
	}},
}

// placementWorld is "Acme" from the spec's example chart:
//
//	Admin ── alice (admin; her default organization stays her own)
//	 ├ Director ── bob (member; Acme is his default)
//	 │   └ Lead developer ── the AI lead, hosted by alice
//	 │       └ Worktrees
//	 └ Sentinel ── the AI sentinel, hosted by alice
//
// Erin is a member with no position and Gus a guest alice invited (both with Acme as their default);
// Carol is not in Acme. Globex is a second organization alice administers and the AI lead is not in.
type placementWorld struct {
	env                    *testkit.Env
	alice, bob, carol      *testkit.Client
	erin, gus              *testkit.Client
	acme, globex           db.Organization
	director, leadDev      string // position ids
	worktrees, sentinelPos string
	rootPos                string
	lead, sentinel         string // AI ids
	alicePersonal          string
}

func newPlacementWorld(t *testing.T) *placementWorld {
	t.Helper()
	ctx := context.Background()
	env := testkit.New(t)
	w := &placementWorld{
		env: env, alice: env.NewUser("alice"), bob: env.NewUser("bob"), carol: env.NewUser("carol"),
		erin: env.NewUser("erin"), gus: env.NewUser("gus"),
	}
	w.alicePersonal = defaultOrgOf(t, env, w.alice)
	acme, err := env.App.Orgs.Create(ctx, w.alice.User.ID, "Acme")
	require.NoError(t, err)
	w.acme, w.rootPos = acme.Organization, acme.PositionID
	globex, err := env.App.Orgs.Create(ctx, w.alice.User.ID, "Globex")
	require.NoError(t, err)
	w.globex = globex.Organization

	w.director = env.AddPosition(w.acme.ID, "Director", w.rootPos)
	w.leadDev = env.AddPosition(w.acme.ID, "Lead developer", w.director)
	w.worktrees = env.AddPosition(w.acme.ID, "Worktrees", w.leadDev)
	w.sentinelPos = env.AddPosition(w.acme.ID, "Sentinel", w.rootPos)
	env.AddMember(w.acme.ID, w.bob.User.ID, orgs.PermissionMember, w.director)
	env.AddMember(w.acme.ID, w.erin.User.ID, orgs.PermissionMember, "")
	env.AddGuest(w.acme.ID, w.gus.User.ID, w.alice.User.ID, "")
	for _, u := range []*testkit.Client{w.bob, w.erin, w.gus} {
		require.NoError(t, env.App.Orgs.SetDefault(ctx, u.User.ID, w.acme.ID))
	}

	w.lead = env.NewAIIn(w.alice, w.acme.ID, "lead")
	w.sentinel = env.NewAIIn(w.alice, w.acme.ID, "sentinel")
	for ai, pos := range map[string]string{w.lead: w.leadDev, w.sentinel: w.sentinelPos} {
		_, err := env.App.Orgs.SetPosition(ctx, w.alice.User.ID, w.acme.ID, ai, pos)
		require.NoError(t, err)
	}
	require.Equal(t, w.alicePersonal, defaultOrgOf(t, env, w.alice), "fixture sanity: alice's default is her own")
	return w
}

func placementFolder(name string) testkit.Folder {
	return testkit.Folder{Machine: "devbox", Path: "/work/" + name, Harness: "claude"}
}

// census counts what a start creates, to prove a refused start created nothing.
type census struct{ agents, profiles, memberships, joins int }

func (w *placementWorld) census(t *testing.T) census {
	t.Helper()
	return census{
		agents:      countRows(t, w.env, `SELECT COUNT(*) FROM agents`),
		profiles:    countRows(t, w.env, `SELECT COUNT(*) FROM intelligences WHERE kind = 'ai'`),
		memberships: countRows(t, w.env, `SELECT COUNT(*) FROM org_members`),
		joins:       countRows(t, w.env, `SELECT COUNT(*) FROM audit_log WHERE action = 'org.member_join'`),
	}
}

// requireRefused checks a start was refused with status and code and created nothing.
func (w *placementWorld) requireRefused(t *testing.T, before census, refused *testkit.Response, status int, code dto.ErrorCode) dto.Error {
	t.Helper()
	require.NotNil(t, refused, "the start was accepted")
	e := refused.RequireError(status, code)
	require.Equal(t, before, w.census(t), "a refused start creates nothing")
	return e
}

// requireStartedAs checks where a new AI was put: home organization, creator and its membership.
func (w *placementWorld) requireStartedAs(t *testing.T, id, org, creator string, want orgs.Member) {
	t.Helper()
	requireHome(t, w.env, id, org, creator)
	m, ok, err := w.env.App.Authz.Member(context.Background(), org, id)
	require.NoError(t, err)
	require.True(t, ok, "the AI is a member of its home organization")
	want.IID, want.CreatorIID = id, creator
	require.Equal(t, want, m)
	require.Equal(t, 1, memberships(t, w.env, id), "and of no other organization")
}

// joinEntries returns the org.member_join audit entries about iid.
func (w *placementWorld) joinEntries(t *testing.T, iid string) (entries []db.ListAuditEntriesRow, details []map[string]any) {
	t.Helper()
	all, _, err := audit.New(w.env.App.Store, nil).List(context.Background(), "")
	require.NoError(t, err)
	for _, e := range all {
		if e.Action != audit.ActionOrgMemberJoin {
			continue
		}
		var detail map[string]any
		require.NoError(t, json.Unmarshal([]byte(e.DetailJson), &detail))
		if detail["iid"] == iid {
			entries, details = append(entries, e), append(details, detail)
		}
	}
	return entries, details
}

func (w *placementWorld) agentToken(c *testkit.Client) *testkit.Client {
	return c.Token(dto.TokenKindAgent, "laptop")
}

// ORG-04: --org puts a new AI in that organization, named by its slug (any case) or its id, whatever the
// creator's default is; the AI joins as a member with no position, created by the creator.
func TestStartWithAnOrganizationPlacesTheAIThere(t *testing.T) {
	for _, path := range startPaths {
		t.Run(path.name, func(t *testing.T) {
			w := newPlacementWorld(t)
			tok := w.agentToken(w.alice)
			for i, ref := range []string{"acme", "ACME", w.acme.ID} {
				t.Run(ref, func(t *testing.T) {
					f := placementFolder(fmt.Sprintf("org-%d", i))
					f.Org = ref
					id, refused := path.start(t, tok, f)
					require.Nil(t, refused)
					w.requireStartedAs(t, id, w.acme.ID, w.alice.User.ID, orgs.Member{Permission: orgs.PermissionMember})
					managed, err := w.env.App.Authz.Manages(context.Background(), w.alice.User.ID, id, w.acme.ID)
					require.NoError(t, err)
					require.True(t, managed, "the creator manages the AI it started (POS-03)")
				})
			}
			require.Equal(t, w.alicePersonal, defaultOrgOf(t, w.env, w.alice), "starting an AI elsewhere leaves the default alone")
			require.Zero(t, countRows(t, w.env, `SELECT COUNT(*) FROM org_members WHERE org_id = ? AND iid IN (SELECT id FROM agents WHERE folder LIKE '/work/org-%')`, w.alicePersonal))
		})
	}
}

// ORG-04, POS-08: --parent naming an AI the token holder hosts makes that AI the creator, the AI joins the
// parent's home organization (not the host's default) as a guest the parent invited, and it may hold a
// position below the parent's.
func TestStartWithAParentAIMakesTheParentTheCreator(t *testing.T) {
	for _, path := range startPaths {
		t.Run(path.name, func(t *testing.T) {
			w := newPlacementWorld(t)
			tok := w.agentToken(w.alice)
			for name, parent := range map[string]string{"by handle": "@lead", "by upper-case handle": "@LEAD", "by id": w.lead, "by handle without @": "lead"} {
				t.Run(name, func(t *testing.T) {
					f := placementFolder("worker-" + strings.ReplaceAll(name, " ", "-"))
					f.Parent, f.Position = parent, "Worktrees"
					id, refused := path.start(t, tok, f)
					require.Nil(t, refused)

					w.requireStartedAs(t, id, w.acme.ID, w.lead, orgs.Member{
						Permission: orgs.PermissionGuest, PositionID: w.worktrees, InviterIID: w.lead,
					})
					managed, err := w.env.App.Authz.Manages(context.Background(), w.lead, id, w.acme.ID)
					require.NoError(t, err)
					require.True(t, managed, "the parent manages the worker it started")
					sentinelManages, err := w.env.App.Authz.Manages(context.Background(), w.sentinel, id, w.acme.ID)
					require.NoError(t, err)
					require.False(t, sentinelManages, "a peer of the parent does not")

					entries, details := w.joinEntries(t, id)
					require.Len(t, entries, 1, "the join is audited once")
					require.Equal(t, w.alice.User.ID, entries[0].ActorID, "the token holder started it")
					require.Equal(t, w.acme.ID, entries[0].TargetID)
					require.Equal(t, "start", details[0]["by"])
					require.Equal(t, "guest", details[0]["permission"], "the entry says what the AI joined as")
				})
			}
		})
	}
}

// POS-08: a parent the token holder may not act for is ignored without a word: the token holder is the creator
// and the AI joins as a member of their default organization. Each case has the parent be the only thing wrong.
func TestAParentTheHostCannotActForFallsBackToTheHost(t *testing.T) {
	for _, path := range startPaths {
		t.Run(path.name, func(t *testing.T) {
			w := newPlacementWorld(t)
			cases := []struct {
				name   string
				host   *testkit.Client
				parent string
				org    string // the host's default
			}{
				{"an AI hosted by another user", w.bob, "@lead", w.acme.ID},
				{"another user", w.alice, "@bob", w.alicePersonal},
				{"an unknown handle", w.alice, "@nobody-at-all", w.alicePersonal},
				{"an unknown id", w.alice, "0198f0f0-0000-7000-8000-000000000000", w.alicePersonal},
				{"the host", w.alice, "@alice", w.alicePersonal},
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					f := placementFolder("fallback-" + strings.ReplaceAll(c.name, " ", "-"))
					f.Parent = c.parent
					id, refused := path.start(t, w.agentToken(c.host), f)
					require.Nil(t, refused, "no refusal for a parent that does not count")
					w.requireStartedAs(t, id, c.org, c.host.User.ID, orgs.Member{Permission: orgs.PermissionMember})
				})
			}
		})
	}
}

// ORG-05: a creator that is not a member of the organization cannot start an AI there - 403, and no AI,
// profile or membership is created. The creator is the token holder, or the parent AI when there is one.
func TestStartInAnOrganizationTheCreatorIsNotInIsRefused(t *testing.T) {
	for _, path := range startPaths {
		t.Run(path.name, func(t *testing.T) {
			w := newPlacementWorld(t)
			t.Run("the host is not a member", func(t *testing.T) {
				before := w.census(t)
				f := placementFolder("foreign")
				f.Org = "acme"
				_, refused := path.start(t, w.agentToken(w.carol), f)
				w.requireRefused(t, before, refused, http.StatusForbidden, dto.ErrorCodeForbidden)
			})
			t.Run("the parent AI is not a member but the host is", func(t *testing.T) {
				before := w.census(t)
				f := placementFolder("foreign-parent")
				f.Org, f.Parent = w.globex.Slug, "@lead"
				_, refused := path.start(t, w.agentToken(w.alice), f)
				w.requireRefused(t, before, refused, http.StatusForbidden, dto.ErrorCodeForbidden)
				// the same start without the parent is fine: alice is an admin of Globex
				f.Parent = ""
				id, refused := path.start(t, w.agentToken(w.alice), f)
				require.Nil(t, refused)
				requireHome(t, w.env, id, w.globex.ID, w.alice.User.ID)
			})
			t.Run("an organization by its id", func(t *testing.T) {
				before := w.census(t)
				f := placementFolder("foreign-id")
				f.Org = w.acme.ID
				_, refused := path.start(t, w.agentToken(w.carol), f)
				w.requireRefused(t, before, refused, http.StatusForbidden, dto.ErrorCodeForbidden)
			})
		})
	}
}

// POS-07: --position names a position of the organization (any case). The creator may hold it only when its
// own position is strictly above it, or when the creator is an admin there; otherwise 403 and nothing is
// created. With a parent AI, the parent is the creator and its position is the one that counts.
func TestStartWithAPositionFollowsTheCreatorsStanding(t *testing.T) {
	for _, path := range startPaths {
		t.Run(path.name, func(t *testing.T) {
			w := newPlacementWorld(t)
			cases := []struct {
				name     string
				host     *testkit.Client
				position string
				parent   string
				held     string // position id; "" means refused with 403
				creator  string
			}{
				{"below the creator's position", w.bob, "Lead developer", "", w.leadDev, w.bob.User.ID},
				{"two levels below", w.bob, "Worktrees", "", w.worktrees, w.bob.User.ID},
				{"another letter case", w.bob, "lead DEVELOPER", "", w.leadDev, w.bob.User.ID},
				{"the creator's own level", w.bob, "Director", "", "", ""},
				{"above the creator", w.bob, "Admin", "", "", ""},
				{"another branch of the chart", w.bob, "Sentinel", "", "", ""},
				{"a creator without a position", w.erin, "Worktrees", "", "", ""},
				{"a guest creator", w.gus, "Worktrees", "", "", ""},
				{"an admin creator, at its own level", w.alice, "Admin", "", w.rootPos, w.alice.User.ID},
				{"an admin creator, anywhere", w.alice, "Sentinel", "", w.sentinelPos, w.alice.User.ID},
				{"the parent's position counts: below it", w.alice, "Worktrees", "@lead", w.worktrees, w.lead},
				{"the parent's position counts: above it, though the host is an admin", w.alice, "Director", "@lead", "", ""},
				{"the parent's position counts: its own level", w.alice, "Lead developer", "@lead", "", ""},
				{"a parent without enough standing: a peer's branch", w.alice, "Worktrees", "@sentinel", "", ""},
			}
			for i, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					before := w.census(t)
					f := placementFolder(fmt.Sprintf("pos-%d", i))
					f.Position, f.Parent = c.position, c.parent
					if c.host == w.alice && c.parent == "" {
						f.Org = "acme" // alice's default is her own organization
					}
					id, refused := path.start(t, w.agentToken(c.host), f)
					if c.held == "" {
						w.requireRefused(t, before, refused, http.StatusForbidden, dto.ErrorCodeForbidden)
						return
					}
					require.Nil(t, refused)
					m, ok, err := w.env.App.Authz.Member(context.Background(), w.acme.ID, id)
					require.NoError(t, err)
					require.True(t, ok)
					require.Equal(t, c.held, m.PositionID, "the position the AI holds")
					require.Equal(t, c.creator, m.CreatorIID)
				})
			}
		})
	}
}

// POS-07, ORG-04: an organization or a position that does not exist is 422 with its own code and the
// offending field (organization_required/org, position_unknown/position); a position that exists only in
// another organization is unknown too; and nothing is created.
func TestAnUnknownOrganizationOrPositionIsUnprocessable(t *testing.T) {
	for _, path := range startPaths {
		t.Run(path.name, func(t *testing.T) {
			w := newPlacementWorld(t)
			globexOnly := w.env.AddPosition(w.globex.ID, "Globex chief", "")
			require.NotEmpty(t, globexOnly)
			tok := w.agentToken(w.alice)

			t.Run("an unknown organization", func(t *testing.T) {
				before := w.census(t)
				f := placementFolder("no-org")
				f.Org = "no-such-organization"
				_, refused := path.start(t, tok, f)
				e := w.requireRefused(t, before, refused, http.StatusUnprocessableEntity, dto.ErrorCodeOrganizationRequired)
				require.Contains(t, e.Fields, "org")
			})
			t.Run("an unknown position", func(t *testing.T) {
				before := w.census(t)
				f := placementFolder("no-position")
				f.Org, f.Position = "acme", "Overlord"
				_, refused := path.start(t, tok, f)
				e := w.requireRefused(t, before, refused, http.StatusUnprocessableEntity, dto.ErrorCodePositionUnknown)
				require.Contains(t, e.Fields, "position")
			})
			t.Run("a position of another organization", func(t *testing.T) {
				before := w.census(t)
				f := placementFolder("other-org-position")
				f.Org, f.Position = "acme", "Globex chief"
				_, refused := path.start(t, tok, f)
				w.requireRefused(t, before, refused, http.StatusUnprocessableEntity, dto.ErrorCodePositionUnknown)
			})
			t.Run("a position that is not valid percent-encoding", func(t *testing.T) {
				before := w.census(t)
				h := placementFolder("bad-encoding").Headers()
				h.Set(dto.HeaderOrg, "acme")
				h.Set(dto.HeaderPosition, "100%zz")
				var refused *testkit.Response
				if path.name == "a live session" {
					conn, resp := tok.DialAgentHeaders(h)
					require.Nil(t, conn)
					refused = resp
				} else {
					refused = tok.WithHeader(dto.HeaderOrg, "acme").WithHeader(dto.HeaderPosition, "100%zz").
						AsFolder(placementFolder("bad-encoding")).Post("/api/v1/agent/register", nil)
				}
				w.requireRefused(t, before, refused, http.StatusUnprocessableEntity, dto.ErrorCodePositionUnknown)
			})
			t.Run("a position whose name has spaces and accents is sent percent-encoded", func(t *testing.T) {
				pos := w.env.AddPosition(w.acme.ID, "Líder de equipe", w.rootPos)
				f := placementFolder("accented")
				f.Org, f.Position = "acme", "Líder de equipe"
				id, refused := path.start(t, tok, f)
				require.Nil(t, refused)
				m, _, err := w.env.App.Authz.Member(context.Background(), w.acme.ID, id)
				require.NoError(t, err)
				require.Equal(t, pos, m.PositionID)
			})
		})
	}
}

// v0.5 compatibility (AD-020): a session that sends none of the headers - or sends them empty - starts as
// before: the host's default organization, the host as creator, a member with no position.
func TestSessionsWithoutPlacementHeadersKeepTheDefaults(t *testing.T) {
	for _, path := range startPaths {
		t.Run(path.name, func(t *testing.T) {
			w := newPlacementWorld(t)
			tok := w.agentToken(w.bob)
			id, refused := path.start(t, tok, placementFolder("plain"))
			require.Nil(t, refused)
			w.requireStartedAs(t, id, w.acme.ID, w.bob.User.ID, orgs.Member{Permission: orgs.PermissionMember})

			blank := placementFolder("blank")
			blank.Org, blank.Parent = " ", " "
			id, refused = path.start(t, tok, blank)
			require.Nil(t, refused, "blank headers are no headers")
			w.requireStartedAs(t, id, w.acme.ID, w.bob.User.ID, orgs.Member{Permission: orgs.PermissionMember})
		})
	}
}
