package api_test

import (
	"net/http"
	"testing"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// SR2-05: the routes of positions and of the org chart were removed with them. They answer 404 not_found to every
// role - the admin who could edit the chart, a member, an outsider and an admin of the instance alike - with an
// organization and a member that exist: there is nothing to authorize or to reveal.
func TestSR205OrgChartAndPositionRoutesAreGone(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	setup := func(*testing.T) map[string]string {
		return map[string]string{"org": roles.Org.ID, "member": roles.Peer.User.ID}
	}
	org := func(res map[string]string, suffix string) string { return "/api/v1/orgs/" + res["org"] + suffix }
	gone := everyRole(http.StatusNotFound)
	runRoleRoutes(t, roles, setup, []roleRoute{
		{"the org chart", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Get(org(res, "/chart"))
		}, gone},
		{"list positions", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Get(org(res, "/positions"))
		}, gone},
		{"create a position", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Post(org(res, "/positions"), map[string]any{"name": "Lead"})
		}, gone},
		{"change a position", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Patch(org(res, "/positions/any"), map[string]any{"name": "Lead"})
		}, gone},
		{"delete a position", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Delete(org(res, "/positions/any"))
		}, gone},
		{"preview a move", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Get(org(res, "/members/"+res["member"]+"/move-preview?position_id=any"))
		}, gone},
	})
	env.Anon().Get("/api/v1/orgs/"+roles.Org.ID+"/chart").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}
