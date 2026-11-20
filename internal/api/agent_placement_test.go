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

// startPath is one way a session starts an AI: the live WebSocket, or a tools-only call (the tools
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

// placementWorld is "Acme":
//
//	alice (admin; her default organization stays her own), bob and erin (members; Acme is their default)
//	lead and sentinel: AIs alice hosts, home Acme
//
// Carol is not in Acme. Globex is a second organization alice administers and the AI lead is not in.
type placementWorld struct {
	env               *testkit.Env
	alice, bob, carol *testkit.Client
	erin              *testkit.Client
	acme, globex      db.Organization
	lead, sentinel    string // AI ids
	alicePersonal     string
}

func newPlacementWorld(t *testing.T) *placementWorld {
	t.Helper()
	ctx := context.Background()
	env := testkit.New(t)
	w := &placementWorld{
		env: env, alice: env.NewUser("alice"), bob: env.NewUser("bob"), carol: env.NewUser("carol"),
		erin: env.NewUser("erin"),
	}
	w.alicePersonal = defaultOrgOf(t, env, w.alice)
	acme, err := env.App.Orgs.Create(ctx, w.alice.User.ID, "Acme")
	require.NoError(t, err)
	w.acme = acme.Organization
	globex, err := env.App.Orgs.Create(ctx, w.alice.User.ID, "Globex")
	require.NoError(t, err)
	w.globex = globex.Organization

	env.AddMember(w.acme.ID, w.bob.User.ID, orgs.PermissionMember)
	env.AddMember(w.acme.ID, w.erin.User.ID, orgs.PermissionMember)
	for _, u := range []*testkit.Client{w.bob, w.erin} {
		require.NoError(t, env.App.Orgs.SetDefault(ctx, u.User.ID, w.acme.ID))
	}

	w.lead = env.NewAIIn(w.alice, w.acme.ID, "lead")
	w.sentinel = env.NewAIIn(w.alice, w.acme.ID, "sentinel")
	require.Equal(t, w.alicePersonal, defaultOrgOf(t, env, w.alice), "fixture sanity: alice's default is her own")
	return w
}

// instructorsOf lists the instructors of an AI besides its host, by id (SR2-06).
func instructorsOf(t *testing.T, env *testkit.Env, agentID string) []string {
	t.Helper()
	rows, err := env.App.Store.R().ListAgentInstructors(context.Background(), agentID)
	require.NoError(t, err)
	out := []string{}
	for _, r := range rows {
		out = append(out, r.Intelligence.ID)
	}
	return out
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
	want.IID = id
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
// creator's default is; the AI joins as a member, created by the creator.
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
					managed, err := w.env.App.Authz.Manages(context.Background(), w.alice.User.ID, id)
					require.NoError(t, err)
					require.True(t, managed, "the host manages the AI it started (SR2-13)")
				})
			}
			require.Equal(t, w.alicePersonal, defaultOrgOf(t, w.env, w.alice), "starting an AI elsewhere leaves the default alone")
			require.Zero(t, countRows(t, w.env, `SELECT COUNT(*) FROM org_members WHERE org_id = ? AND iid IN (SELECT id FROM agents WHERE folder LIKE '/work/org-%')`, w.alicePersonal))
		})
	}
}

// SR2-02, SR2-09, POS-08: --parent naming an AI the token holder hosts makes that AI the creator, the AI joins
// the parent's home organization (not the host's default) as a member, and the parent becomes its instructor:
// its messages are trusted, while it does not manage the new AI (it is no user) and a peer of the parent gets
// nothing.
func TestSR209StartWithAParentAIMakesTheParentAnInstructor(t *testing.T) {
	for _, path := range startPaths {
		t.Run(path.name, func(t *testing.T) {
			w := newPlacementWorld(t)
			tok := w.agentToken(w.alice)
			for name, parent := range map[string]string{"by handle": "@lead", "by upper-case handle": "@LEAD", "by id": w.lead, "by handle without @": "lead"} {
				t.Run(name, func(t *testing.T) {
					ctx := context.Background()
					f := placementFolder("worker-" + strings.ReplaceAll(name, " ", "-"))
					f.Parent = parent
					id, refused := path.start(t, tok, f)
					require.Nil(t, refused)

					w.requireStartedAs(t, id, w.acme.ID, w.lead, orgs.Member{Permission: orgs.PermissionMember})
					require.Equal(t, []string{w.lead}, instructorsOf(t, w.env, id), "the parent instructs the new AI")
					require.Equal(t, 1, countRows(t, w.env, `SELECT COUNT(*) FROM agent_instructors WHERE agent_iid = ? AND created_by = ?`, id, w.alice.User.ID),
						"the token holder made the link")
					trusted, err := w.env.App.Authz.Trusts(ctx, w.lead, id)
					require.NoError(t, err)
					require.True(t, trusted, "the parent's messages are trusted")
					managed, err := w.env.App.Authz.Manages(ctx, w.lead, id)
					require.NoError(t, err)
					require.False(t, managed, "an AI instructor does not manage it (SR2-13)")
					trusted, err = w.env.App.Authz.Trusts(ctx, w.sentinel, id)
					require.NoError(t, err)
					require.False(t, trusted, "a peer of the parent does not instruct it")

					entries, details := w.joinEntries(t, id)
					require.Len(t, entries, 1, "the join is audited once")
					require.Equal(t, w.alice.User.ID, entries[0].ActorID, "the token holder started it")
					require.Equal(t, w.acme.ID, entries[0].TargetID)
					require.Equal(t, "start", details[0]["by"])
					require.Equal(t, "member", details[0]["permission"], "SR2-02: an AI started by an AI joins as a member")
					require.Equal(t, w.lead, details[0]["instructor"])
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
					require.Empty(t, instructorsOf(t, w.env, id), "a parent that does not count instructs nothing")
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

// ORG-04: an organization that does not exist is 422 organization_required on the field org, and nothing is
// created.
func TestAnUnknownOrganizationIsUnprocessable(t *testing.T) {
	for _, path := range startPaths {
		t.Run(path.name, func(t *testing.T) {
			w := newPlacementWorld(t)
			before := w.census(t)
			f := placementFolder("no-org")
			f.Org = "no-such-organization"
			_, refused := path.start(t, w.agentToken(w.alice), f)
			e := w.requireRefused(t, before, refused, http.StatusUnprocessableEntity, dto.ErrorCodeOrganizationRequired)
			require.Contains(t, e.Fields, "org")
		})
	}
}

// SR2-05: positions were removed. The position header an older launcher still sends - a name, an unknown one
// or one that is not even valid percent-encoding - is ignored: the AI starts as it would without it.
func TestSR205APositionHeaderIsIgnored(t *testing.T) {
	for _, path := range startPaths {
		t.Run(path.name, func(t *testing.T) {
			w := newPlacementWorld(t)
			tok := w.agentToken(w.bob)
			for i, value := range []string{"Worktrees", "Overlord", "100%zz"} {
				t.Run(value, func(t *testing.T) {
					folder := placementFolder(fmt.Sprintf("old-launcher-%d", i))
					h := folder.Headers()
					h.Set("X-Partyline-Position", value)
					var id string
					if path.name == "a live session" {
						conn, resp := tok.DialAgentHeaders(h)
						require.NotNil(t, conn, "refused: %d %s", resp.Status, resp.Body)
						id = conn.Welcome(wsWait).Agent.ID
						require.NoError(t, conn.Conn.CloseNow())
					} else {
						resp := tok.WithHeader("X-Partyline-Position", value).AsFolder(folder).Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK)
						id = testkit.Decode[dto.AgentInfo](resp).ID
					}
					w.requireStartedAs(t, id, w.acme.ID, w.bob.User.ID, orgs.Member{Permission: orgs.PermissionMember})
				})
			}
		})
	}
}

// v0.5 compatibility (AD-020): a session that sends none of the headers - or sends them empty - starts as
// before: the host's default organization, the host as creator, a member.
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
