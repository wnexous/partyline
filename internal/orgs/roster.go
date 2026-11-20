// Package orgs is the organization model (AD-016, AD-017): organizations, their members and invitations,
// the explicit links that say who instructs an AI (its host and its instructors, SR2-06) and the Authz
// facade every other service asks. The rules in this file are pure: they read a snapshot of one
// organization and do no I/O.
package orgs

// Permission is what a membership allows in the organization: an admin looks after it (settings,
// invitations, connections, deleting its identities), a member takes part (SR2-01).
type Permission string

// Permissions.
const (
	PermissionAdmin  Permission = "admin"
	PermissionMember Permission = "member"
)

// Valid reports whether p is one of the two permissions.
func (p Permission) Valid() bool {
	return p == PermissionAdmin || p == PermissionMember
}

// Member is one membership of an organization: an identity (a user or an AI) with its permission.
type Member struct {
	IID        string
	Permission Permission
}

// Roster is the snapshot of one organization's members the rules read. It is never changed after it is
// built, so it is safe to share within one operation.
type Roster struct {
	OrgID   string
	Members map[string]Member
}

// Member returns the membership of iid.
func (r *Roster) Member(iid string) (Member, bool) {
	m, ok := r.Members[iid]
	return m, ok
}

// IsAdmin reports whether iid is a member with the admin permission.
func (r *Roster) IsAdmin(iid string) bool {
	m, ok := r.Members[iid]
	return ok && m.Permission == PermissionAdmin
}

// CanRemove reports whether a may take b out of this organization (SR2-12): a is an admin and b another
// member. Leaving is the member's own (Service.Leave), and what removing an AI whose home is the
// organization does - deleting it - is the caller's to decide.
func (r *Roster) CanRemove(a, b string) bool {
	if a == b || !r.IsAdmin(a) {
		return false
	}
	_, ok := r.Members[b]
	return ok
}

// admins counts the members with the admin permission.
func (r *Roster) admins() int {
	n := 0
	for _, m := range r.Members {
		if m.Permission == PermissionAdmin {
			n++
		}
	}
	return n
}
