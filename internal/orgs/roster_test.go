package orgs

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func roster(members ...Member) *Roster {
	r := &Roster{OrgID: "o", Members: map[string]Member{}}
	for _, m := range members {
		r.Members[m.IID] = m
	}
	return r
}

// SR2-01: a membership is admin or member; guest is no permission any more.
func TestSR201PermissionsAreAdminAndMember(t *testing.T) {
	require.True(t, PermissionAdmin.Valid())
	require.True(t, PermissionMember.Valid())
	require.False(t, Permission("guest").Valid())
	require.False(t, Permission("").Valid())
}

func TestIsAdmin(t *testing.T) {
	r := roster(Member{IID: "a", Permission: PermissionAdmin}, Member{IID: "m", Permission: PermissionMember})
	require.True(t, r.IsAdmin("a"))
	require.False(t, r.IsAdmin("m"))
	require.False(t, r.IsAdmin("x"))
}

// SR2-12: a person is removed from an organization by an admin; nobody removes itself (that is leaving) and
// nobody removes an outsider.
func TestSR212CanRemoveIsForAdmins(t *testing.T) {
	r := roster(
		Member{IID: "admin", Permission: PermissionAdmin}, Member{IID: "admin2", Permission: PermissionAdmin},
		Member{IID: "m1", Permission: PermissionMember}, Member{IID: "m2", Permission: PermissionMember},
	)
	require.True(t, r.CanRemove("admin", "m1"))
	require.True(t, r.CanRemove("admin", "admin2"))
	require.False(t, r.CanRemove("m1", "m2"), "a member removes nobody")
	require.False(t, r.CanRemove("admin", "admin"), "leaving is not removing")
	require.False(t, r.CanRemove("admin", "x"), "not a member")
	require.False(t, r.CanRemove("x", "m1"), "an outsider")
	require.Equal(t, 2, r.admins())
}
