package testkit

import (
	"context"
	"database/sql"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

func nullable(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// PersonalOrg returns the personal organization of the client's account: the first organization the
// account created, with itself as admin (AD-021). It does not change when the account chooses another
// default organization.
func (c *Client) PersonalOrg() db.Organization {
	c.T.Helper()
	require.NotEmpty(c.T, c.User.ID, "PersonalOrg needs a logged-in client")
	org, err := c.env.App.Store.R().GetPersonalOrg(context.Background(), c.User.ID)
	require.NoError(c.T, err)
	return org
}

// AddPosition adds a position named name to the organization's chart under parent ("" for a root) and
// returns its id. It writes the position directly, with no authorization, to set a chart up.
func (e *Env) AddPosition(org, name, parent string) string {
	e.T.Helper()
	ctx := context.Background()
	id := store.NewID()
	require.NoError(e.T, e.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.CreatePosition(ctx, db.CreatePositionParams{
			ID: id, OrgID: org, Name: name, ParentID: nullable(parent), CreatedAt: time.Now().UnixMilli(),
		})
	}))
	e.App.Bus.Publish(events.OrgChanged{OrgID: org})
	return id
}

// AddMember makes the identity (a user or an AI) a member of the organization with the permission and
// position ("" for none), directly and with no authorization. It fails the test for an identity that
// already is a member.
func (e *Env) AddMember(org, iid string, perm orgs.Permission, position string) {
	e.T.Helper()
	e.addMember(orgs.AddMemberInput{OrgID: org, IID: iid, Permission: perm, PositionID: position})
}

// AddGuest makes the identity a guest of the organization, invited by inviter (an identity id), with an
// optional position.
func (e *Env) AddGuest(org, iid, inviter, position string) {
	e.T.Helper()
	e.addMember(orgs.AddMemberInput{OrgID: org, IID: iid, Permission: orgs.PermissionGuest, PositionID: position, InviterIID: inviter})
}

func (e *Env) addMember(in orgs.AddMemberInput) {
	e.T.Helper()
	ctx := context.Background()
	in.Now = time.Now()
	require.NoError(e.T, e.App.Store.WithTx(ctx, func(q *db.Queries) error { return orgs.AddMember(ctx, q, in) }))
	e.App.Bus.Publish(events.OrgChanged{OrgID: in.OrgID})
}

// SetCreator records the identity as the creator of the AI, so the creator rule of POS-03 applies to it.
func (e *Env) SetCreator(agent, iid string) {
	e.T.Helper()
	ctx := context.Background()
	require.NoError(e.T, e.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.SetAgentCreator(ctx, db.SetAgentCreatorParams{CreatorIid: nullable(iid), ID: agent})
	}))
}

// NewAIIn creates owner's AI of the folder FolderOf(name) with the organization as its home, as its first
// session would while the organization was the owner's default, and returns its id. The owner must be a
// member of the organization, and their own default is as it was afterwards. Like NewAI it is
// idempotent per (owner, name): an AI that exists keeps the home it has.
func (e *Env) NewAIIn(owner *Client, org, name string) string {
	e.T.Helper()
	ctx := context.Background()
	u, err := e.App.Store.R().GetUser(ctx, owner.User.ID)
	require.NoError(e.T, err)
	require.NoError(e.T, e.App.Orgs.SetDefault(ctx, owner.User.ID, org), "the owner belongs to the organization")
	id := e.NewAI(owner, name)
	require.NoError(e.T, e.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.SetUserDefaultOrg(ctx, db.SetUserDefaultOrgParams{DefaultOrgID: u.DefaultOrgID, ID: owner.User.ID})
	}))
	return id
}

// OrgRoles is one organization with an identity in every role of the authorization matrices (design,
// "Authorization matrix per route group"), all logged in:
//
//	Admin ── holds the root position "Admin" ── admin of the organization
//	 └ Manager ── member holding "Manager", above Target's position
//	    └ Target, Peer ── members holding "Worker" (the same position: peers)
//
//	Member ── member with no position and no relation to Target
//	Inviter ── member with no position; Guest ── a guest it invited (so it manages the guest)
//	Outsider ── not a member; InstanceAdmin ── the instance's admin account, not a member
//
// Admin manages Target and Manager through the root position; Manager manages Target; Peer does not;
// Inviter manages Guest; nobody manages Admin. TargetAI is an AI of the organization (home, member, no
// position) that Manager created and hosts.
type OrgRoles struct {
	Org db.Organization

	Admin, Manager, Target, Peer, Member, Inviter, Guest, Outsider, InstanceAdmin *Client

	// TargetAI is the id of an AI whose home is Org: a member with no position, created by Manager.
	TargetAI string
	// The positions of the chart, by id.
	AdminPosition, ManagerPosition, WorkerPosition string
}

// RoleClient is a client with the name of its role.
type RoleClient struct {
	Name   string
	Client *Client
}

// All lists the clients by role name - admin, manager, target, peer, member, inviter, guest, outsider
// and instance_admin - for tests that go through every role.
func (r *OrgRoles) All() []RoleClient {
	return []RoleClient{
		{"admin", r.Admin}, {"manager", r.Manager}, {"target", r.Target}, {"peer", r.Peer}, {"member", r.Member},
		{"inviter", r.Inviter}, {"guest", r.Guest}, {"outsider", r.Outsider}, {"instance_admin", r.InstanceAdmin},
	}
}

// OrgRoles creates the organization name (lower case, up to 20 characters: it also prefixes the usernames
// of its people, "<name>-admin", "<name>-manager", ...) with the roles described on OrgRoles. A second call with
// another name builds a second organization in the same environment. The instance admin is a new admin
// account ("<name>-root"), so the call works whatever accounts the environment already has.
func (e *Env) OrgRoles(name string) *OrgRoles {
	e.T.Helper()
	ctx := context.Background()
	user := func(role string) *Client { return e.NewUser(name + "-" + role) }
	r := &OrgRoles{
		Admin: user("admin"), Manager: user("manager"), Target: user("target"), Peer: user("peer"),
		Member: user("member"), Inviter: user("inviter"), Guest: user("guest"), Outsider: user("outsider"),
		InstanceAdmin: e.NewAdmin(name + "-root"),
	}
	created, err := e.App.Orgs.Create(ctx, r.Admin.User.ID, name)
	require.NoError(e.T, err)
	r.Org = created.Organization
	r.AdminPosition = created.PositionID
	r.ManagerPosition = e.AddPosition(r.Org.ID, "Manager", r.AdminPosition)
	r.WorkerPosition = e.AddPosition(r.Org.ID, "Worker", r.ManagerPosition)
	e.AddMember(r.Org.ID, r.Manager.User.ID, orgs.PermissionMember, r.ManagerPosition)
	e.AddMember(r.Org.ID, r.Target.User.ID, orgs.PermissionMember, r.WorkerPosition)
	e.AddMember(r.Org.ID, r.Peer.User.ID, orgs.PermissionMember, r.WorkerPosition)
	e.AddMember(r.Org.ID, r.Member.User.ID, orgs.PermissionMember, "")
	e.AddMember(r.Org.ID, r.Inviter.User.ID, orgs.PermissionMember, "")
	e.AddGuest(r.Org.ID, r.Guest.User.ID, r.Inviter.User.ID, "")
	r.TargetAI = e.NewAIIn(r.Manager, r.Org.ID, name+"-ai")
	return r
}
