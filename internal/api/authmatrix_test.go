package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// The authorization matrix (spec success criterion: every endpoint is tested with owner,
// grantee, stranger and admin-without-grant). Features add routes by building a []matrixRoute
// and a setup that prepares a fresh resource per case.

// matrixRole is an identity in the authorization matrix.
type matrixRole string

const (
	roleOwner    matrixRole = "owner"    // owns the resource
	roleViewer   matrixRole = "viewer"   // grantee with view (rooms) or use (agents, connectors)
	rolePoster   matrixRole = "poster"   // grantee with post (rooms)
	roleStranger matrixRole = "stranger" // member without any grant
	roleAdmin    matrixRole = "admin"    // instance admin without any grant (ACL-08)
)

var matrixRoles = []matrixRole{roleOwner, roleViewer, rolePoster, roleStranger, roleAdmin}

// matrix is one app with a logged-in client per role.
type matrix struct {
	env     *testkit.Env
	clients map[matrixRole]*testkit.Client
}

func newMatrix(t *testing.T) *matrix {
	t.Helper()
	env := testkit.New(t)
	return &matrix{env: env, clients: map[matrixRole]*testkit.Client{
		roleAdmin:    env.SetupAdmin(),
		roleOwner:    env.NewUser("owner"),
		roleViewer:   env.NewUser("viewer"),
		rolePoster:   env.NewUser("poster"),
		roleStranger: env.NewUser("stranger"),
	}}
}

// as returns role's client with its assertions bound to t (use it inside subtests).
func (m *matrix) as(t *testing.T, role matrixRole) *testkit.Client {
	c := *m.clients[role]
	c.T = t
	return &c
}

// matrixResource names the ids a setup created (e.g. "room", "member", "attachment").
type matrixResource map[string]string

// matrixRoute is one endpoint of the matrix: call performs the request as c against res,
// want maps every role to the expected status code.
type matrixRoute struct {
	name string
	call func(t *testing.T, m *matrix, c *testkit.Client, res matrixResource) *testkit.Response
	want map[matrixRole]int
}

// statuses builds a want map in matrixRoles order: owner, viewer, poster, stranger, admin.
func statuses(owner, viewer, poster, stranger, admin int) map[matrixRole]int {
	return map[matrixRole]int{roleOwner: owner, roleViewer: viewer, rolePoster: poster, roleStranger: stranger, roleAdmin: admin}
}

// run calls every route as every role, each against a fresh resource from setup, and checks
// the status; 403 and 404 must carry the forbidden and not_found error codes.
func (m *matrix) run(t *testing.T, setup func(t *testing.T, m *matrix) matrixResource, routes []matrixRoute) {
	t.Helper()
	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			for _, role := range matrixRoles {
				t.Run(string(role), func(t *testing.T) {
					want, ok := rt.want[role]
					require.True(t, ok, "no expectation for %s", role)
					res := setup(t, m)
					resp := rt.call(t, m, m.as(t, role), res)
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
