package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// The authorization matrix (design, "Authorization matrix per route group"; spec success criterion: every
// route is tested with every role) is over the roles of testkit.OrgRoles, because an organization is the only
// way to share anything (AD-016): the admin and the manager above the target, the target and its peer, a plain
// member, an inviter and its guest, an outsider and the admin of the instance who is no member. A feature adds
// its routes by building a []roleRoute and a setup that prepares a fresh resource per case, and states the
// status each role gets with a want map (groupWant, dmWant, agentWant, connectionWant or everyRole).

// roleRoute is one route of an organization-role matrix: call performs the request as c against res, and
// want maps every role name of testkit.OrgRoles.All to the expected status.
type roleRoute struct {
	name string
	call func(t *testing.T, c *testkit.Client, res map[string]string) *testkit.Response
	want map[string]int
}

// everyRole is the expected status of a route that answers the same to every role.
func everyRole(status int) map[string]int {
	return map[string]int{
		"admin": status, "manager": status, "target": status, "peer": status, "member": status,
		"inviter": status, "guest": status, "outsider": status, "instance_admin": status,
	}
}

// runRoleRoutes calls every route as every role, each against a fresh resource from setup, and checks the
// status; 403 and 404 must carry the forbidden and not_found error codes.
func runRoleRoutes(t *testing.T, roles *testkit.OrgRoles, setup func(t *testing.T) map[string]string, routes []roleRoute) {
	t.Helper()
	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			for _, role := range roles.All() {
				t.Run(role.Name, func(t *testing.T) {
					want, ok := rt.want[role.Name]
					require.True(t, ok, "no expectation for %s", role.Name)
					res := setup(t)
					c := *role.Client
					c.T = t
					resp := rt.call(t, &c, res)
					switch want {
					case http.StatusForbidden:
						resp.RequireError(want, dto.ErrorCodeForbidden)
					case http.StatusNotFound:
						resp.RequireError(want, dto.ErrorCodeNotFound)
					default:
						resp.RequireStatus(want)
					}
				})
			}
		})
	}
}
