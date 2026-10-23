package users

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store/db"
)

// exec runs a statement on a second connection to the database.
func (f *fixture) exec(t *testing.T, query string) {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+f.path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	_, err = raw.Exec(query)
	require.NoError(t, err, query)
}

// requirePersonalOrg checks the personal organization of an account (AD-021): named name, at the address
// slug, with the account as its admin holding the only position, a root named Admin, and the account's
// default. It returns the organization.
func (f *fixture) requirePersonalOrg(t *testing.T, u db.User, name, slug string) db.Organization {
	t.Helper()
	ctx := context.Background()
	fresh, err := f.store.R().GetUser(ctx, u.ID)
	require.NoError(t, err)
	require.True(t, fresh.DefaultOrgID.Valid, "%s has a default organization", u.Username)
	org, err := f.store.R().GetOrganization(ctx, fresh.DefaultOrgID.String)
	require.NoError(t, err)
	require.Equal(t, name, org.Name)
	require.Equal(t, slug, org.Slug)
	require.Equal(t, u.ID, org.CreatedBy)
	chart, err := orgs.Load(ctx, f.store.R(), org.ID)
	require.NoError(t, err)
	require.Len(t, chart.Positions, 1)
	require.Len(t, chart.Members, 1, "the account is alone in its personal organization")
	m := chart.Members[u.ID]
	require.Equal(t, orgs.PermissionAdmin, m.Permission)
	require.Equal(t, "Admin", chart.Positions[m.PositionID].Name, "holding the root position Admin")
	require.Empty(t, chart.Positions[m.PositionID].ParentID)
	return org
}

// ORG-04, AD-021: every way an account comes to exist - the first-run setup, an accepted invite and an
// admin creating it - gives it a personal organization in the same transaction: named after it, with
// the account as admin holding the root position Admin, and its default organization.
func TestEveryNewAccountGetsAPersonalOrganization(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	admin, err := f.svc.Setup(ctx, f.svc.SetupToken(), AccountInput{Username: "admin", Password: adminPassword, DisplayName: "Ada Admin"})
	require.NoError(t, err)
	f.requirePersonalOrg(t, admin, "Ada Admin", "admin")

	_, secret := f.invite(t, dto.RoleMember)
	bob, err := f.svc.AcceptInvite(ctx, secret, AccountInput{Username: "bob", Password: memberPassword})
	require.NoError(t, err)
	f.requirePersonalOrg(t, bob, "bob", "bob")

	carol, err := f.svc.CreateUser(ctx, AccountInput{Username: "carol", Password: memberPassword, DisplayName: "Carol"}, dto.RoleMember)
	require.NoError(t, err)
	f.requirePersonalOrg(t, carol, "Carol", "carol")

	// A username that gives the same address as an existing organization gets the next free one.
	bob2, err := f.svc.CreateUser(ctx, AccountInput{Username: "bob_", Password: memberPassword}, dto.RoleMember)
	require.NoError(t, err)
	f.requirePersonalOrg(t, bob2, "bob_", "bob-2")

	// The audit log says who got an organization and that it is personal.
	entries, _, err := f.svc.audit.List(ctx, "")
	require.NoError(t, err)
	created := 0
	for _, e := range entries {
		if e.Action != "org.create" {
			continue
		}
		created++
		var detail map[string]any
		require.NoError(t, json.Unmarshal([]byte(e.DetailJson), &detail))
		require.Equal(t, true, detail["personal"])
		require.Equal(t, "organization", e.TargetKind)
		u, err := f.store.R().GetUserByUsername(ctx, detail["slug"].(string))
		if err == nil {
			require.Equal(t, u.ID, e.ActorID, "the account is the actor")
		}
	}
	require.Equal(t, 4, created, "one per account")
}

// The organization is part of the account: if creating the organization fails, no account is left.
func TestAnAccountAndItsPersonalOrganizationAreOneTransaction(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.setupAdmin(t)
	f.exec(t, `CREATE TRIGGER forced_failure BEFORE INSERT ON audit_log WHEN NEW.action = 'org.create'
		BEGIN SELECT RAISE(ABORT, 'forced failure'); END`)

	_, err := f.svc.CreateUser(ctx, AccountInput{Username: "dave", Password: memberPassword}, dto.RoleMember)
	require.Error(t, err)

	_, err = f.store.R().GetUserByUsername(ctx, "dave")
	require.ErrorIs(t, err, sql.ErrNoRows, "no account")
	_, err = f.store.R().GetOrganizationBySlug(ctx, "dave")
	require.ErrorIs(t, err, sql.ErrNoRows, "and no organization")
	n, err := f.store.R().CountIntelligenceHandle(ctx, "dave")
	require.NoError(t, err)
	require.Zero(t, n, "and no intelligence")
	require.EqualValues(t, 1, f.userCount(t))
}
