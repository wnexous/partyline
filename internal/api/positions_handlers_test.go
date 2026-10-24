package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// The Positions matrix (design, "Authorization matrix per route group"): an admin creates, edits
// and deletes; a member, manager or guest is refused; an outsider does not find the organization.
func TestPositionRoutesMatrix(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")

	want := map[string]int{
		"admin": http.StatusCreated, "manager": http.StatusForbidden, "target": http.StatusForbidden,
		"peer": http.StatusForbidden, "member": http.StatusForbidden, "inviter": http.StatusForbidden,
		"guest": http.StatusForbidden, "outsider": http.StatusNotFound, "instance_admin": http.StatusNotFound,
	}
	for _, role := range r.All() {
		t.Run(role.Name, func(t *testing.T) {
			resp := role.Client.Post("/api/v1/orgs/"+r.Org.ID+"/positions", dto.CreatePositionRequest{Name: "Place-" + role.Name, ParentID: r.AdminPosition})
			positionID := r.WorkerPosition
			switch want[role.Name] {
			case http.StatusForbidden:
				resp.RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
			case http.StatusNotFound:
				resp.RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
			default:
				created := testkit.Decode[dto.Position](resp.RequireStatus(http.StatusCreated))
				require.Equal(t, "Place-"+role.Name, created.Name)
				require.Equal(t, r.AdminPosition, created.ParentID)
				positionID = created.ID
			}
			resp = role.Client.Patch("/api/v1/orgs/"+r.Org.ID+"/positions/"+positionID, dto.UpdatePositionRequest{Name: ptr("Renamed-" + role.Name)})
			switch want[role.Name] {
			case http.StatusForbidden:
				resp.RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
			case http.StatusNotFound:
				resp.RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
			default:
				renamed := testkit.Decode[dto.Position](resp.RequireStatus(http.StatusOK))
				require.Equal(t, "Renamed-"+role.Name, renamed.Name)
			}
			resp = role.Client.Delete("/api/v1/orgs/" + r.Org.ID + "/positions/" + positionID)
			switch want[role.Name] {
			case http.StatusForbidden:
				resp.RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
			case http.StatusNotFound:
				resp.RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
			default:
				resp.RequireStatus(http.StatusNoContent)
			}
		})
	}
}

// POS-01: a name outside 1-60 characters or a parent of another organization is 422, and a
// duplicate name is 409 conflict (R2: names are unique per organization, ignoring case).
func TestPositionValidation(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")

	body := r.Admin.Post("/api/v1/orgs/"+r.Org.ID+"/positions", dto.CreatePositionRequest{Name: strings.Repeat("p", 61)}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, body.Fields, "name")

	other := testkit.Decode[dto.Organization](r.Admin.Post("/api/v1/orgs", dto.CreateOrganizationRequest{Name: "globex"}).RequireStatus(http.StatusCreated))
	foreign := testkit.Decode[dto.Position](r.Admin.Post("/api/v1/orgs/"+other.ID+"/positions", dto.CreatePositionRequest{Name: "Foreign"}).RequireStatus(http.StatusCreated))
	body = r.Admin.Post("/api/v1/orgs/"+r.Org.ID+"/positions", dto.CreatePositionRequest{Name: "Local", ParentID: foreign.ID}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, body.Fields, "parent_id")

	r.Admin.Post("/api/v1/orgs/"+r.Org.ID+"/positions", dto.CreatePositionRequest{Name: "worker"}).
		RequireError(http.StatusConflict, dto.ErrorCodeConflict)
}

// POS-02: a move that would close a loop is 409 position_cycle, and the chart does not change.
func TestPositionCycleIsConflict(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	create := func(name, parent string) dto.Position {
		return testkit.Decode[dto.Position](r.Admin.Post("/api/v1/orgs/"+r.Org.ID+"/positions",
			dto.CreatePositionRequest{Name: name, ParentID: parent}).RequireStatus(http.StatusCreated))
	}
	a := create("A", r.AdminPosition)
	b := create("B", a.ID)
	c := create("C", b.ID)

	r.Admin.Patch("/api/v1/orgs/"+r.Org.ID+"/positions/"+a.ID, dto.UpdatePositionRequest{ParentID: &c.ID}).
		RequireError(http.StatusConflict, dto.ErrorCodePositionCycle)
	// A's parent is still the root: moving it back to it changes nothing and does not fail.
	moved := testkit.Decode[dto.Position](r.Admin.Patch("/api/v1/orgs/"+r.Org.ID+"/positions/"+a.ID,
		dto.UpdatePositionRequest{ParentID: &r.AdminPosition}).RequireStatus(http.StatusOK))
	require.Equal(t, r.AdminPosition, moved.ParentID)
}

// POS-09: deleting a position moves its holders and its child positions up to its parent, and an
// unknown position is 404.
func TestDeletePositionMovesItsChildren(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	parent := newPosition(t, r.Admin, r.Org.ID, "Parent", r.AdminPosition)
	child := newPosition(t, r.Admin, r.Org.ID, "Child", parent)
	r.Admin.Patch(memberPath(r.Org.ID, r.Member.User.ID), toPosition(parent)).RequireStatus(http.StatusOK)
	require.Equal(t, parent, listMembers(t, r.Admin, r.Org.ID)[r.Member.User.ID].PositionID, "the holder is in Parent")
	parentOf := func() map[string]string {
		out := map[string]string{}
		for _, p := range getChart(t, r.Admin, r.Org.ID).Positions {
			out[p.ID] = p.ParentID
		}
		return out
	}
	require.Equal(t, parent, parentOf()[child], "Child hangs under Parent")

	r.Admin.Delete("/api/v1/orgs/" + r.Org.ID + "/positions/" + parent).RequireStatus(http.StatusNoContent)

	// Parent hung under the root, so both its holder and Child hang there now.
	require.Equal(t, r.AdminPosition, listMembers(t, r.Admin, r.Org.ID)[r.Member.User.ID].PositionID, "the holder moved up to the parent")
	positions := parentOf()
	require.NotContains(t, positions, parent, "Parent is gone")
	require.Equal(t, r.AdminPosition, positions[child], "Child moved up to the parent")

	r.Admin.Delete("/api/v1/orgs/"+r.Org.ID+"/positions/does-not-exist").
		RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}
