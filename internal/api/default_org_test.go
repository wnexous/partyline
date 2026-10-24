package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

const defaultOrgPath = "/api/v1/me/default-org"

func getMe(t *testing.T, c *testkit.Client) dto.Me {
	t.Helper()
	return testkit.Decode[dto.Me](c.Get("/api/v1/me").RequireStatus(http.StatusOK))
}

// joinOrg makes the client a member of the organization through the API, as an invited member or guest.
func joinOrg(t *testing.T, admin, c *testkit.Client, org string, perm dto.Permission) {
	t.Helper()
	inv := invited(t, admin, org, c.User.Username, perm, "")
	answerInvitation(c, inv.ID, "accept").RequireStatus(http.StatusOK)
}

// ORG-04 (T17): a user chooses the organization their new AIs join. Before choosing it is their personal
// one; `me` shows the choice; and the next AI started without --org lands there, whether the session is a
// live WebSocket or a tools-only call (L-034). AIs that exist stay where they are.
func TestUserChoosesTheOrganizationNewAIsJoin(t *testing.T) {
	for _, path := range startPaths {
		t.Run(path.name, func(t *testing.T) {
			env := testkit.New(t)
			alice, bob := env.NewUser("alice"), env.NewUser("bob")
			acme := testkit.Decode[dto.Organization](alice.Post("/api/v1/orgs", dto.CreateOrganizationRequest{Name: "acme"}).RequireStatus(http.StatusCreated))
			joinOrg(t, alice, bob, acme.ID, dto.PermissionMember)
			personal := bob.PersonalOrg().ID
			tok := bob.Token(dto.TokenKindAgent, "laptop")

			require.Equal(t, personal, getMe(t, bob).DefaultOrgID, "before choosing: the personal organization")
			first, refused := path.start(t, tok, placementFolder("first"))
			require.Nil(t, refused)
			requireHome(t, env, first, personal, bob.User.ID)

			me := testkit.Decode[dto.Me](bob.Put(defaultOrgPath, dto.SetDefaultOrgRequest{OrgID: acme.ID}).RequireStatus(http.StatusOK))
			require.Equal(t, acme.ID, me.DefaultOrgID, "the answer is the account with the new choice")
			require.Equal(t, "bob", me.Username)
			require.Equal(t, acme.ID, getMe(t, bob).DefaultOrgID)
			require.Equal(t, acme.ID, defaultOrgOf(t, env, bob), "stored")

			second, refused := path.start(t, tok, placementFolder("second"))
			require.Nil(t, refused)
			requireHome(t, env, second, acme.ID, bob.User.ID)
			requireHome(t, env, first, personal, bob.User.ID)

			// Back to the personal organization: the next AI follows.
			bob.Put(defaultOrgPath, dto.SetDefaultOrgRequest{OrgID: personal}).RequireStatus(http.StatusOK)
			third, refused := path.start(t, tok, placementFolder("third"))
			require.Nil(t, refused)
			requireHome(t, env, third, personal, bob.User.ID)
			// Choosing the same one again is fine.
			bob.Put(defaultOrgPath, dto.SetDefaultOrgRequest{OrgID: personal}).RequireStatus(http.StatusOK)
		})
	}
}

// ORG-04 (T17): the choice must be an organization the user belongs to - as a member or a guest - and every
// other value is 403, whether the organization exists or not, so a refusal tells nothing about the
// organizations the user is not in. A pending invitation is not a membership. A refusal changes nothing.
func TestTheDefaultOrganizationMustBeOneYouBelongTo(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	acme := testkit.Decode[dto.Organization](alice.Post("/api/v1/orgs", dto.CreateOrganizationRequest{Name: "acme"}).RequireStatus(http.StatusCreated))
	personal := bob.PersonalOrg().ID
	invited(t, alice, acme.ID, "bob", dto.PermissionMember, "") // waiting, not accepted

	for name, org := range map[string]string{
		"an organization he is not in": acme.ID, "alice's personal organization": alice.PersonalOrg().ID,
		"one that does not exist": "no-such-organization", "an address, not an id": "acme", "none": "",
	} {
		bob.Put(defaultOrgPath, dto.SetDefaultOrgRequest{OrgID: org}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		require.Equal(t, personal, getMe(t, bob).DefaultOrgID, "%s: the choice is unchanged", name)
	}
	first, refused := startPaths[1].start(t, bob.Token(dto.TokenKindAgent, "laptop"), placementFolder("atlas"))
	require.Nil(t, refused)
	requireHome(t, env, first, personal, bob.User.ID)

	// A guest belongs to the organization too (the invitation is sent again as one for a guest).
	joinOrg(t, alice, bob, acme.ID, dto.PermissionGuest)
	me := testkit.Decode[dto.Me](bob.Put(defaultOrgPath, dto.SetDefaultOrgRequest{OrgID: acme.ID}).RequireStatus(http.StatusOK))
	require.Equal(t, acme.ID, me.DefaultOrgID)
}

// ORG-04 (T17): the default follows membership. A user who leaves the organization they chose has no choice
// left, and `me` shows where their new AIs go now: the organization they have administered the longest.
func TestTheDefaultOrganizationFollowsMembership(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	acme := testkit.Decode[dto.Organization](alice.Post("/api/v1/orgs", dto.CreateOrganizationRequest{Name: "acme"}).RequireStatus(http.StatusCreated))
	joinOrg(t, alice, bob, acme.ID, dto.PermissionMember)
	personal := bob.PersonalOrg().ID
	bob.Put(defaultOrgPath, dto.SetDefaultOrgRequest{OrgID: acme.ID}).RequireStatus(http.StatusOK)

	bob.Delete(memberPath(acme.ID, bob.User.ID)).RequireStatus(http.StatusNoContent) // leaves acme
	u, err := env.App.Store.R().GetUser(context.Background(), bob.User.ID)
	require.NoError(t, err)
	require.False(t, u.DefaultOrgID.Valid, "no choice is left")
	require.Equal(t, personal, getMe(t, bob).DefaultOrgID, "a new AI would join the organization bob administers")
	first, refused := startPaths[1].start(t, bob.Token(dto.TokenKindAgent, "laptop"), placementFolder("atlas"))
	require.Nil(t, refused)
	requireHome(t, env, first, personal, bob.User.ID)
}

// `me` carries default_org_id in every answer that describes the account, and the route is for people.
func TestMeCarriesTheDefaultOrganization(t *testing.T) {
	env := testkit.New(t)
	bob := env.NewUser("bob")
	personal := bob.PersonalOrg().ID

	require.Equal(t, personal, getMe(t, bob).DefaultOrgID)
	patched := testkit.Decode[dto.Me](bob.Patch("/api/v1/me", dto.UpdateMeRequest{DisplayName: ptr("Bob B")}).RequireStatus(http.StatusOK))
	require.Equal(t, personal, patched.DefaultOrgID)
	login := testkit.Decode[dto.Me](env.Anon().Post("/api/v1/auth/login", dto.LoginRequest{Username: "bob", Password: testkit.Password}).RequireStatus(http.StatusOK))
	require.Equal(t, personal, login.DefaultOrgID)
	first := testkit.New(t).SetupAdmin()
	require.Equal(t, first.PersonalOrg().ID, getMe(t, first).DefaultOrgID, "the first account too")

	// An agent token never reaches it, and nobody anonymous does; the token's user keeps their choice.
	bob.Token(dto.TokenKindAgent, "t").Put(defaultOrgPath, dto.SetDefaultOrgRequest{OrgID: personal}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	env.Anon().Put(defaultOrgPath, dto.SetDefaultOrgRequest{OrgID: personal}).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	bob.DoRaw(http.MethodPut, defaultOrgPath, "application/json", strings.NewReader("{nope")).RequireError(http.StatusBadRequest, dto.ErrorCodeInvalid)
}
