package api_test

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// orgStatus is the expected status of one organization route for one role.
type orgStatus struct {
	name string
	// get is GET /orgs/{org}; patch, avatar and deleteOrg the other routes of the Organizations
	// matrix (design, "Authorization matrix per route group").
	get, patch, avatar, deleteOrg int
}

func TestOrganizationRoutesMatrix(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")

	cases := []orgStatus{
		{"admin", http.StatusOK, http.StatusOK, http.StatusOK, http.StatusConflict},
		{"manager", http.StatusOK, http.StatusForbidden, http.StatusForbidden, http.StatusForbidden},
		{"target", http.StatusOK, http.StatusForbidden, http.StatusForbidden, http.StatusForbidden},
		{"peer", http.StatusOK, http.StatusForbidden, http.StatusForbidden, http.StatusForbidden},
		{"member", http.StatusOK, http.StatusForbidden, http.StatusForbidden, http.StatusForbidden},
		{"inviter", http.StatusOK, http.StatusForbidden, http.StatusForbidden, http.StatusForbidden},
		{"guest", http.StatusOK, http.StatusForbidden, http.StatusForbidden, http.StatusForbidden},
		{"outsider", http.StatusNotFound, http.StatusNotFound, http.StatusNotFound, http.StatusNotFound},
		{"instance_admin", http.StatusNotFound, http.StatusNotFound, http.StatusNotFound, http.StatusNotFound},
	}
	img := pngBytes(t, bands(640, 320))
	for _, tc := range cases {
		var c *testkit.Client
		for _, rc := range r.All() {
			if rc.Name == tc.name {
				c = rc.Client
			}
		}
		require.NotNil(t, c, "role %s", tc.name)
		t.Run(tc.name, func(t *testing.T) {
			c.Get("/api/v1/orgs/" + r.Org.ID).RequireStatus(tc.get)
			resp := c.Patch("/api/v1/orgs/"+r.Org.ID, dto.UpdateOrganizationRequest{Name: ptr("acme-" + tc.name)})
			switch tc.patch {
			case http.StatusForbidden:
				resp.RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
			case http.StatusNotFound:
				resp.RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
			default:
				resp.RequireStatus(tc.patch)
			}
			resp = c.DoRaw(http.MethodPut, "/api/v1/orgs/"+r.Org.ID+"/avatar", "image/png", bytes.NewReader(img))
			switch tc.avatar {
			case http.StatusForbidden:
				resp.RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
			case http.StatusNotFound:
				resp.RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
			default:
				var org dto.Organization
				resp.RequireStatus(tc.avatar).Decode(&org)
				require.NotEmpty(t, org.AvatarURL)
			}
			resp = c.Delete("/api/v1/orgs/" + r.Org.ID)
			switch tc.deleteOrg {
			case http.StatusForbidden:
				resp.RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
			case http.StatusNotFound:
				resp.RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
			default:
				// The organization still owns the TargetAI: ORG-06 refuses even an admin.
				body := resp.RequireError(tc.deleteOrg, dto.ErrorCodeOrganizationNotEmpty)
				require.Equal(t, "1", body.Details["agents"])
			}
		})
	}
}

// OMEM-03: GET /orgs lists the caller's own memberships only, guests included.
func TestListOrganizationsShowsOnlyMyMemberships(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	permission := map[string]dto.Permission{
		"admin": dto.PermissionAdmin, "manager": dto.PermissionMember, "target": dto.PermissionMember,
		"peer": dto.PermissionMember, "member": dto.PermissionMember, "inviter": dto.PermissionMember,
		"guest": dto.PermissionGuest,
	}

	for _, role := range r.All() {
		list := testkit.Decode[dto.List[dto.Organization]](role.Client.Get("/api/v1/orgs").RequireStatus(http.StatusOK))
		found := false
		for _, o := range list.Items {
			if o.ID == r.Org.ID {
				found = true
				require.Equal(t, permission[role.Name], o.Permission, "role %s", role.Name)
			}
		}
		require.Equal(t, role.Name != "outsider" && role.Name != "instance_admin", found, "role %s", role.Name)
	}
}

// ORG-01: a name of 1-100 characters creates the organization; a blank one is 422 on name.
func TestCreateOrganizationValidatesTheName(t *testing.T) {
	env := testkit.New(t)
	bob := env.NewUser("bob")

	created := testkit.Decode[dto.Organization](bob.Post("/api/v1/orgs", dto.CreateOrganizationRequest{Name: "acme"}).RequireStatus(http.StatusCreated))
	require.Equal(t, "acme", created.Name)
	require.Equal(t, "acme", created.Slug)
	require.Equal(t, dto.PermissionAdmin, created.Permission)
	require.NotEmpty(t, created.PositionID, "the creator holds the root position")

	resp := bob.Post("/api/v1/orgs", dto.CreateOrganizationRequest{Name: "  "})
	body := resp.RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, body.Fields, "name")
}

// A member who is not an admin cannot rename the organization (OMEM-04); an unknown slug is 404.
func TestUpdateOrganizationNeedsAdmin(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")

	r.Member.Patch("/api/v1/orgs/"+r.Org.ID, dto.UpdateOrganizationRequest{Name: ptr("nope")}).
		RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	r.Admin.Patch("/api/v1/orgs/"+r.Org.ID, dto.UpdateOrganizationRequest{Name: ptr("Acme Inc")}).
		RequireStatus(http.StatusOK)
	r.Admin.Patch("/api/v1/orgs/no-such-org", dto.UpdateOrganizationRequest{Name: ptr("x")}).
		RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	// A slug another organization has is 409 conflict.
	other := testkit.Decode[dto.Organization](r.Admin.Post("/api/v1/orgs", dto.CreateOrganizationRequest{Name: "globex"}).RequireStatus(http.StatusCreated))
	r.Admin.Patch("/api/v1/orgs/"+r.Org.ID, dto.UpdateOrganizationRequest{Slug: &other.Slug}).
		RequireError(http.StatusConflict, dto.ErrorCodeConflict)
}

// ORG-06: an organization that owns nothing deletes; one that still owns an AI is refused with
// counts (L-017).
func TestDeleteOrganizationRefusesWhileNotEmpty(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")

	empty := testkit.Decode[dto.Organization](r.Admin.Post("/api/v1/orgs", dto.CreateOrganizationRequest{Name: "empty"}).RequireStatus(http.StatusCreated))
	r.Admin.Delete("/api/v1/orgs/" + empty.ID).RequireStatus(http.StatusNoContent)
	r.Admin.Get("/api/v1/orgs/"+empty.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	body := r.Admin.Delete("/api/v1/orgs/"+r.Org.ID).RequireError(http.StatusConflict, dto.ErrorCodeOrganizationNotEmpty)
	require.Equal(t, "1", body.Details["agents"])
	require.Equal(t, "0", body.Details["connectors"])
	require.Equal(t, "0", body.Details["groups"])
}

// An agent token cannot create an organization: agent tokens only reach /api/v1/agent/ (TOK-03).
func TestAgentTokenCannotCreateOrganization(t *testing.T) {
	env := testkit.New(t)
	bob := env.NewUser("bob")
	tok := bob.Token(dto.TokenKindAgent, "t")
	tok.Post("/api/v1/orgs", dto.CreateOrganizationRequest{Name: "acme"}).
		RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
}

// ORG-01: a member reads the avatar, only an admin changes it, and clearing it is idempotent.
func TestOrganizationAvatarRoundTrip(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")

	img := pngBytes(t, bands(640, 320))
	var org dto.Organization
	r.Admin.DoRaw(http.MethodPut, "/api/v1/orgs/"+r.Org.ID+"/avatar", "image/png", bytes.NewReader(img)).
		RequireStatus(http.StatusOK).Decode(&org)
	require.NotEmpty(t, org.AvatarURL)
	requireGreenSquare(t, r.Member.Get(org.AvatarURL).RequireStatus(http.StatusOK).Body)
	r.Outsider.Get(org.AvatarURL).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	r.Admin.Delete("/api/v1/orgs/" + r.Org.ID + "/avatar").RequireStatus(http.StatusNoContent)
	r.Admin.Delete("/api/v1/orgs/" + r.Org.ID + "/avatar").RequireStatus(http.StatusNoContent)
	var cleared dto.Organization
	r.Admin.Get("/api/v1/orgs/" + r.Org.ID).RequireStatus(http.StatusOK).Decode(&cleared)
	require.Empty(t, cleared.AvatarURL)
}
