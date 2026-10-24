package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// sessionCheck asks GET /agent/session as the launcher does before it starts the harness, with the placement
// the start would ask for.
func sessionCheck(tok *testkit.Client, f testkit.Folder) *testkit.Response {
	return tok.AsFolder(f).Get("/api/v1/agent/session")
}

// launchFolder is the folder of a start with the placement a launcher passes on.
func launchFolder(name, org, position, parent string) testkit.Folder {
	f := placementFolder(name)
	f.Org, f.Position, f.Parent = org, position, parent
	return f
}

// quiet watches the bus for events published while it is open; changes reports the first one.
func (w *placementWorld) quiet(t *testing.T) func() {
	t.Helper()
	sub, unsubscribe := w.env.App.Bus.Subscribe(64)
	t.Cleanup(unsubscribe)
	return func() {
		t.Helper()
		select {
		case e := <-sub:
			require.Failf(t, "the check published an event", "%T %v", e, e)
		default:
		}
	}
}

// SES-01, ORG-04, ORG-05, POS-07, POS-08 (T36): before the launcher starts the harness it asks GET
// /agent/session. When the start names an organization, a position or a parent, the answer is the refusal the
// session itself would get - the same status and code, the same words - so that the person reads it in the
// terminal where they typed the command. The check changes nothing: no AI, profile, membership or audit entry.
func TestTheSessionCheckRefusesWhatTheStartWouldRefuse(t *testing.T) {
	w := newPlacementWorld(t)
	globexOnly := w.env.AddPosition(w.globex.ID, "Treasurer", "")
	require.NotEmpty(t, globexOnly)

	refusals := []struct {
		name   string
		host   *testkit.Client
		org    string
		pos    string
		parent string
		status int
		code   dto.ErrorCode
	}{
		{"an organization that does not exist", w.alice, "no-such-organization", "", "", http.StatusUnprocessableEntity, dto.ErrorCodeOrganizationRequired},
		{"an organization the host is not in", w.carol, "acme", "", "", http.StatusForbidden, dto.ErrorCodeForbidden},
		{"the same organization by its id", w.carol, w.acme.ID, "", "", http.StatusForbidden, dto.ErrorCodeForbidden},
		{"a position that does not exist", w.alice, "acme", "Nope", "", http.StatusUnprocessableEntity, dto.ErrorCodePositionUnknown},
		{"a position only another organization has", w.alice, "acme", "Treasurer", "", http.StatusUnprocessableEntity, dto.ErrorCodePositionUnknown},
		{"the creator's own level", w.bob, "", "Director", "", http.StatusForbidden, dto.ErrorCodeForbidden},
		{"above the creator", w.bob, "", "Admin", "", http.StatusForbidden, dto.ErrorCodeForbidden},
		{"another branch of the chart", w.bob, "", "Sentinel", "", http.StatusForbidden, dto.ErrorCodeForbidden},
		{"a creator without a position", w.erin, "", "Worktrees", "", http.StatusForbidden, dto.ErrorCodeForbidden},
		{"a guest creator", w.gus, "", "Worktrees", "", http.StatusForbidden, dto.ErrorCodeForbidden},
		{"a parent AI that is not in the organization", w.alice, w.globex.Slug, "", "@lead", http.StatusForbidden, dto.ErrorCodeForbidden},
		{"a position above the parent AI's own, though the host is an admin", w.alice, "", "Director", "@lead", http.StatusForbidden, dto.ErrorCodeForbidden},
		{"a parent AI of another branch", w.alice, "", "Worktrees", "@sentinel", http.StatusForbidden, dto.ErrorCodeForbidden},
	}
	for i, c := range refusals {
		t.Run(c.name, func(t *testing.T) {
			f := launchFolder(fmt.Sprintf("refused-%d", i), c.org, c.pos, c.parent)
			tok := w.agentToken(c.host)
			before := w.census(t)
			noEvents := w.quiet(t)

			asked := sessionCheck(tok, f)
			checked := asked.RequireError(c.status, c.code)
			require.NotEmpty(t, checked.Message, "the server says why")
			require.Equal(t, before, w.census(t), "the check creates nothing")
			noEvents()

			// the session itself is refused the same way, in the same words
			_, refused := startPaths[0].start(t, tok, f)
			started := w.requireRefused(t, before, refused, c.status, c.code)
			require.Equal(t, started.Message, checked.Message, "the launcher prints what the session would be told")
		})
	}
}

// ORG-04, POS-07, POS-08: a start the server would accept is answered like any lookup of a folder that has no AI
// yet - 200, and the AI does not exist - and the check still creates nothing; the start that follows works.
func TestTheSessionCheckAcceptsWhatTheStartWouldAccept(t *testing.T) {
	w := newPlacementWorld(t)
	accepted := []struct {
		name   string
		host   *testkit.Client
		org    string
		pos    string
		parent string
	}{
		{"no placement at all", w.alice, "", "", ""},
		{"an organization, in capitals", w.alice, "ACME", "", ""},
		{"an organization by its id", w.alice, "", "", ""},
		{"a position below the creator's, in other letters", w.bob, "", "lead DEVELOPER", ""},
		{"an admin creator, at any position", w.alice, "acme", "Sentinel", ""},
		{"a parent AI and a position below its own", w.alice, "", "Worktrees", "@lead"},
		{"a parent the host cannot act for is ignored", w.alice, "", "", "@bob"},
	}
	for i, c := range accepted {
		t.Run(c.name, func(t *testing.T) {
			org := c.org
			if c.name == "an organization by its id" {
				org = w.acme.ID
			}
			f := launchFolder(fmt.Sprintf("accepted-%d", i), org, c.pos, c.parent)
			tok := w.agentToken(c.host)
			before := w.census(t)
			noEvents := w.quiet(t)

			state := testkit.Decode[dto.SessionState](sessionCheck(tok, f).RequireStatus(http.StatusOK))
			require.Equal(t, dto.SessionState{}, state, "there is no AI of this folder yet")
			require.Equal(t, before, w.census(t), "the check creates nothing")
			noEvents()

			id, refused := startPaths[0].start(t, tok, f)
			require.Nil(t, refused, "the start the check accepted")
			require.NotEmpty(t, id)
		})
	}
}

// ORG-04, POS-07: for an AI that already exists the check applies the later-start rules. --org may only name
// its home organization (409 organization_mismatch, with the home organization in the details, for any other),
// --position that it holds is nothing and another one is allowed or refused under the rule of a later start,
// and --parent is ignored. An allowed move is only checked: the AI keeps its position.
func TestTheSessionCheckAppliesTheRulesOfALaterStart(t *testing.T) {
	w := newPlacementWorld(t)
	f := testkit.FolderOf("lead") // the AI lead of acme, hosted by alice, holding Lead developer
	alice := w.agentToken(w.alice)

	for _, ref := range []string{"acme", "ACME", w.acme.ID} {
		t.Run("its home organization as "+ref, func(t *testing.T) {
			f := f
			f.Org = ref
			state := testkit.Decode[dto.SessionState](sessionCheck(alice, f).RequireStatus(http.StatusOK))
			require.True(t, state.Exists, "the AI of the folder is reported as before")
			require.Equal(t, "lead", state.Intelligence.Handle)
		})
	}
	for name, ref := range map[string]string{
		"another organization of the host":     w.alicePersonalSlug(t),
		"an organization the host administers": w.globex.Slug,
		"an organization that does not exist":  "no-such-organization",
	} {
		t.Run(name, func(t *testing.T) {
			f := f
			f.Org = ref
			before := w.census(t)
			e := sessionCheck(alice, f).RequireError(http.StatusConflict, dto.ErrorCodeOrganizationMismatch)
			require.Equal(t, "acme", e.Details[dto.MismatchDetailOrganization])
			require.Equal(t, "Acme", e.Details[dto.MismatchDetailName])
			require.Contains(t, e.Message, "Acme", "the message names the home organization")
			require.Equal(t, before, w.census(t))
		})
	}
	t.Run("the position it holds", func(*testing.T) {
		f := f
		f.Position = "Lead developer"
		sessionCheck(alice, f).RequireStatus(http.StatusOK)
	})
	t.Run("a position that does not exist", func(*testing.T) {
		f := f
		f.Position = "Nope"
		sessionCheck(alice, f).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodePositionUnknown)
	})
	t.Run("a move an admin may make is checked, not made", func(t *testing.T) {
		f := f
		f.Position = "Director"
		noEvents := w.quiet(t)
		sessionCheck(alice, f).RequireStatus(http.StatusOK)
		require.Equal(t, w.leadDev, w.positionOf(t, w.acme.ID, w.lead), "the AI keeps its position")
		noEvents()
	})
	t.Run("a move the host may not make", func(t *testing.T) {
		// erin, a member with no position, hosts an AI that gus created: she does not manage it
		ctx := context.Background()
		erins := w.env.NewAIIn(w.erin, w.acme.ID, "erins")
		w.env.SetCreator(erins, w.gus.User.ID)
		managed, err := w.env.App.Authz.Manages(ctx, w.erin.User.ID, erins, w.acme.ID)
		require.NoError(t, err)
		require.False(t, managed, "fixture: erin does not manage her own AI")

		f := testkit.FolderOf("erins")
		f.Position = "Worktrees"
		before := w.census(t)
		checked := sessionCheck(w.agentToken(w.erin), f).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		require.Equal(t, before, w.census(t))
		require.Equal(t, "", w.positionOf(t, w.acme.ID, erins), "nothing moved")

		_, refused := startPaths[0].start(t, w.agentToken(w.erin), f)
		started := refused.RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		require.Equal(t, started.Message, checked.Message, "the session is told the same")
	})
	t.Run("a parent is ignored", func(*testing.T) {
		f := f
		f.Parent = "@sentinel"
		sessionCheck(alice, f).RequireStatus(http.StatusOK)
	})
}

// A machine name is cut to its limit before the AI of the folder is looked for, as the start does it, so the
// check finds the AI a session from that machine would find and judges the start by the rules of a later start,
// not of a first one.
func TestTheSessionCheckFindsTheAIByTheMachineAsTheStartCuts(t *testing.T) {
	w := newPlacementWorld(t)
	alice := w.agentToken(w.alice)
	long := strings.Repeat("m", agents.MaxMachineLength)
	f := testkit.Folder{Machine: long, Path: "/work/longhaul", Harness: "claude", Org: "acme"}
	_, refused := startPaths[0].start(t, alice, f)
	require.Nil(t, refused, "the first session creates the AI in Acme")

	g := f
	g.Machine, g.Org = long+"-and-more", w.globex.Slug
	e := sessionCheck(alice, g).RequireError(http.StatusConflict, dto.ErrorCodeOrganizationMismatch)
	require.Equal(t, "acme", e.Details[dto.MismatchDetailOrganization], "the AI of the folder was found")
}

func (w *placementWorld) alicePersonalSlug(t *testing.T) string {
	t.Helper()
	return w.alice.PersonalOrg().Slug
}

// MIG-02 with ORG-04: a session that would adopt a v0.3 identity is checked against that identity's home
// organization - the one the upgrade gave it - and the identity stays unlinked until the session really starts.
func TestTheSessionCheckAppliesTheRulesToTheAIASessionWouldAdopt(t *testing.T) {
	w := newPlacementWorld(t)
	veteran := w.env.NewLegacyAI(w.alice, "veteran")
	w.env.PlaceInDefaultOrg(w.alice, veteran) // the upgrade put it in alice's own organization
	alice := w.agentToken(w.alice)
	unlinked := func() int {
		return countRows(t, w.env, `SELECT COUNT(*) FROM agents WHERE id = ? AND folder = ''`, veteran)
	}
	require.Equal(t, 1, unlinked(), "fixture: the identity has no folder")

	for name, f := range map[string]testkit.Folder{
		"by --as":                     {Machine: "devbox", Path: "/work/somewhere", Harness: "claude", Adopt: "veteran"},
		"by --as, in other letters":   {Machine: "devbox", Path: "/work/somewhere", Harness: "claude", Adopt: "VeTeRan"},
		"by the handle of the folder": {Machine: "devbox", Path: "/work/veteran", Harness: "claude"},
	} {
		t.Run(name, func(t *testing.T) {
			f.Org = "acme"
			e := sessionCheck(alice, f).RequireError(http.StatusConflict, dto.ErrorCodeOrganizationMismatch)
			require.Equal(t, w.alicePersonalSlug(t), e.Details[dto.MismatchDetailOrganization], "it belongs to alice's own organization")
			f.Org = w.alicePersonalSlug(t)
			sessionCheck(alice, f).RequireStatus(http.StatusOK)
			require.Equal(t, 1, unlinked(), "the check adopts nothing")
		})
	}
}

// The authorization of the check is the token's: it is an agent route, and the rules apply to the user whose
// token asks, never to whoever hosts the AI the folder names.
func TestTheSessionCheckIsForTheHostOfTheToken(t *testing.T) {
	w := newPlacementWorld(t)
	f := testkit.FolderOf("lead")
	f.Org = "acme"

	t.Run("a user session", func(*testing.T) {
		w.alice.AsFolder(f).Get("/api/v1/agent/session").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	})
	t.Run("a user token", func(*testing.T) {
		w.alice.Token(dto.TokenKindUser, "cli").AsFolder(f).Get("/api/v1/agent/session").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	})
	t.Run("no credentials", func(*testing.T) {
		w.env.Anon().AsFolder(f).Get("/api/v1/agent/session").RequireStatus(http.StatusUnauthorized)
	})
	t.Run("another user's agent token does not see alice's AI", func(t *testing.T) {
		state := testkit.Decode[dto.SessionState](sessionCheck(w.agentToken(w.bob), f).RequireStatus(http.StatusOK))
		require.Equal(t, dto.SessionState{}, state, "bob has no AI in this folder, and sees none of alice's")
	})
	t.Run("another user's token is checked as that user", func(*testing.T) {
		g := f
		g.Org = w.globex.Slug // alice administers Globex, bob is not in it
		sessionCheck(w.agentToken(w.bob), g).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		sessionCheck(w.agentToken(w.alice), g).RequireError(http.StatusConflict, dto.ErrorCodeOrganizationMismatch) // her AI lead is in Acme
	})
	t.Run("a disabled host", func(*testing.T) {
		tok := w.agentToken(w.erin)
		sessionCheck(tok, f).RequireStatus(http.StatusOK)
		disabled := true
		w.env.NewAdmin("root").Patch("/api/v1/admin/users/"+w.erin.User.ID, dto.UpdateUserRequest{Disabled: &disabled}).RequireStatus(http.StatusOK)
		sessionCheck(tok, f).RequireStatus(http.StatusUnauthorized)
	})
}
