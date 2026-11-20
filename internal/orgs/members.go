package orgs

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// Errors of the membership rules.
var (
	// ErrLastAdmin means the change would leave the organization without an admin (409 last_admin,
	// OMEM-05).
	ErrLastAdmin = errors.New("an organization needs at least one admin")
	// ErrHomeOrganization means an AI cannot leave or be removed from its home organization: it is
	// deleted there (409 home_organization, POS-06).
	ErrHomeOrganization = errors.New("an AI cannot leave its home organization: delete it instead")
	// ErrAlreadyMember means the identity already is a member of the organization (409
	// already_member).
	ErrAlreadyMember = errors.New("already a member of the organization")
)

// MemberRow is a membership as the members list shows it: the identity with its profile summary and its
// permission (OMEM-03), and what the viewer may do to it.
type MemberRow struct {
	IID, Permission           string
	Kind, DisplayName, Handle string
	AvatarSha256              string
	JoinedAt                  int64
	// CanDelete says the viewer may delete the member in this organization (SR2-12): an admin may take anyone
	// else out, and the host of an AI whose home is the organization may delete it. DeletedForGood says what
	// that does: an AI whose home is the organization is deleted for good, anyone else is only removed (POS-06).
	CanDelete, DeletedForGood bool
}

func memberRow(r db.ListOrgMembersRow) MemberRow {
	return MemberRow{
		IID: r.Iid, Permission: r.Permission, Kind: r.Kind, DisplayName: r.DisplayName, Handle: r.Handle,
		AvatarSha256: r.AvatarSha256, JoinedAt: r.JoinedAt,
	}
}

// orgSnapshot is one consistent read of an organization for a viewer: its roster, its members with their
// profiles and the AIs whose home it is, with their hosts.
type orgSnapshot struct {
	roster *Roster
	rows   []db.ListOrgMembersRow
	hosts  map[string]string // AI whose home is the organization → its host
}

// snapshot reads the organization in one read transaction, so the rows and the roster agree. The viewer must
// be a member (ErrNotFound otherwise). With only set it reads that one member, ErrNotFound when it is none.
func (s *Service) snapshot(ctx context.Context, viewerIID, orgID, only string) (orgSnapshot, error) {
	snap := orgSnapshot{hosts: map[string]string{}}
	err := s.store.ReadTx(ctx, func(q *db.Queries) error {
		var err error
		if snap.roster, err = Load(ctx, q, orgID); err != nil {
			return err
		}
		if _, ok := snap.roster.Member(viewerIID); !ok {
			return ErrNotFound
		}
		if only != "" {
			r, err := q.GetOrgMemberView(ctx, db.GetOrgMemberViewParams{OrgID: orgID, Iid: only})
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			snap.rows = []db.ListOrgMembersRow{db.ListOrgMembersRow(r)}
			home, err := homeOf(ctx, q, only) // empty for a user
			if err != nil {
				return err
			}
			if home == orgID {
				if snap.hosts[only], err = q.GetAgentHost(ctx, only); err != nil {
					return err
				}
			}
			return nil
		}
		if snap.rows, err = q.ListOrgMembers(ctx, orgID); err != nil {
			return err
		}
		homes, err := q.ListOrgHomeAgents(ctx, nullable(orgID))
		for _, h := range homes {
			snap.hosts[h.ID] = h.OwnerID
		}
		return err
	})
	return snap, err
}

// memberRows renders the snapshot's members for the viewer, in the order they were read.
func (snap orgSnapshot) memberRows(viewerIID string) []MemberRow {
	out := make([]MemberRow, len(snap.rows))
	for i, r := range snap.rows {
		out[i] = memberRow(r)
		host, home := snap.hosts[r.Iid]
		out[i].DeletedForGood = home
		out[i].CanDelete = snap.roster.CanRemove(viewerIID, r.Iid) || (home && host == viewerIID)
	}
	return out
}

// ListMembers lists the organization's members with their profile summary and permission, and what the
// viewer may do to each; every member sees the list.
func (s *Service) ListMembers(ctx context.Context, actorIID, orgID string) ([]MemberRow, error) {
	snap, err := s.snapshot(ctx, actorIID, orgID, "")
	if err != nil {
		return nil, err
	}
	return snap.memberRows(actorIID), nil
}

// MemberView returns one member as the members list shows it; an outsider, or a target that is not a
// member, is ErrNotFound.
func (s *Service) MemberView(ctx context.Context, actorIID, orgID, targetIID string) (MemberRow, error) {
	snap, err := s.snapshot(ctx, actorIID, orgID, targetIID)
	if err != nil {
		return MemberRow{}, err
	}
	return snap.memberRows(actorIID)[0], nil
}

// AddMemberInput describes a membership being created.
type AddMemberInput struct {
	OrgID, IID string
	Permission Permission
	// Now is the join time.
	Now time.Time
}

// AddMember makes an identity a member of an organization in the caller's transaction: invitations
// accepted (OMEM-02) and AIs started (ORG-04) use it. It refuses an identity that already is a member
// (ErrAlreadyMember) and a permission that is not admin or member (a validation error). It writes no
// audit entry and announces nothing: the caller does, after its commit.
func AddMember(ctx context.Context, q *db.Queries, in AddMemberInput) error {
	if !in.Permission.Valid() {
		return &validation.Error{Fields: map[string]string{"permission": "must be admin or member"}}
	}
	if _, ok, err := memberOf(ctx, q, in.OrgID, in.IID); err != nil || ok {
		if err == nil {
			err = ErrAlreadyMember
		}
		return err
	}
	return q.CreateOrgMember(ctx, db.CreateOrgMemberParams{
		OrgID: in.OrgID, Iid: in.IID, Permission: string(in.Permission), JoinedAt: in.Now.UnixMilli(),
	})
}

// SetPermission gives a member of the organization another permission; only an admin does (OMEM-04).
// A change that would leave no admin is ErrLastAdmin (OMEM-05), decided in the transaction that
// changes it. The same permission again is nothing. It returns the membership as it is afterwards.
func (s *Service) SetPermission(ctx context.Context, actorIID, orgID, targetIID string, perm Permission) (Member, error) {
	if err := s.z.RequireAdmin(ctx, orgID, actorIID); err != nil {
		return Member{}, err
	}
	if !perm.Valid() {
		return Member{}, &validation.Error{Fields: map[string]string{"permission": "must be admin or member"}}
	}
	var out Member
	var changed bool
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		out, changed = Member{}, false
		roster, err := Load(ctx, q, orgID)
		if err != nil {
			return err
		}
		actor, ok := roster.Member(actorIID)
		if !ok {
			return ErrNotFound
		}
		if actor.Permission != PermissionAdmin {
			return ErrForbidden
		}
		cur, ok := roster.Member(targetIID)
		if !ok {
			return ErrNotFound
		}
		out = cur
		if cur.Permission == perm {
			return nil
		}
		if cur.Permission == PermissionAdmin && roster.admins() <= 1 {
			return ErrLastAdmin
		}
		if err := q.UpdateOrgMemberPermission(ctx, db.UpdateOrgMemberPermissionParams{
			Permission: string(perm), OrgID: orgID, Iid: targetIID,
		}); err != nil {
			return err
		}
		out.Permission, changed = perm, true
		return s.record(ctx, q, actorIID, audit.ActionOrgMemberPermission, orgID,
			map[string]any{"iid": targetIID, "from": string(cur.Permission), "to": string(perm)})
	})
	if err != nil {
		return Member{}, err
	}
	if changed {
		s.changed(orgID)
	}
	return out, nil
}

// Remove takes target out of the organization on behalf of actor (SR2-12, OMEM-06): actor must be an admin,
// else ErrForbidden; an outsider actor or a target that is not a member is ErrNotFound. Removing yourself is
// Leave. What Remove does is only the removal: the person or the AI of another organization is out of the
// organization's groups, keeps its account and its other memberships, and the organization's AIs stay.
// An AI whose home is the organization is not removed but deleted (ErrHomeOrganization): the caller
// does that.
func (s *Service) Remove(ctx context.Context, actorIID, orgID, targetIID string) error {
	return s.remove(ctx, actorIID, orgID, targetIID)
}

// Leave takes iid out of the organization at its own request. The last admin cannot
// (ErrLastAdmin) and an AI cannot leave its home organization (ErrHomeOrganization).
func (s *Service) Leave(ctx context.Context, iid, orgID string) error {
	return s.remove(ctx, iid, orgID, iid)
}

func (s *Service) remove(ctx context.Context, actorIID, orgID, targetIID string) error {
	var groups, orphaned []string
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		groups, orphaned = nil, nil
		roster, err := Load(ctx, q, orgID)
		if err != nil {
			return err
		}
		if _, ok := roster.Member(actorIID); !ok {
			return ErrNotFound
		}
		target, ok := roster.Member(targetIID)
		if !ok {
			return ErrNotFound
		}
		if actorIID != targetIID && !roster.CanRemove(actorIID, targetIID) {
			return ErrForbidden
		}
		home, err := homeOf(ctx, q, targetIID) // empty for a user
		if err != nil {
			return err
		}
		if home == orgID {
			return ErrHomeOrganization
		}
		if target.Permission == PermissionAdmin && roster.admins() <= 1 {
			return ErrLastAdmin
		}
		if groups, err = q.DeleteOrgGroupMemberships(ctx, db.DeleteOrgGroupMembershipsParams{Iid: targetIID, OrgID: nullable(orgID)}); err != nil {
			return err
		}
		if err := q.DeleteOrgMember(ctx, db.DeleteOrgMemberParams{OrgID: orgID, Iid: targetIID}); err != nil {
			return err
		}
		// OMEM-08: an AI that no longer belongs to the organization no longer operates its
		// connections, and the platform messages it held for them are not its to read.
		if orphaned, err = q.ClearOrgOperator(ctx, db.ClearOrgOperatorParams{OrgID: nullable(orgID), Iid: nullable(targetIID)}); err != nil {
			return err
		}
		for _, connectorID := range orphaned {
			if err := q.DeletePlatformItemsOfAgentOnConnector(ctx, db.DeletePlatformItemsOfAgentOnConnectorParams{AgentID: targetIID, ConnectorID: connectorID}); err != nil {
				return err
			}
		}
		// A default organization the user no longer belongs to is no default.
		if err := q.ClearUserDefaultOrg(ctx, db.ClearUserDefaultOrgParams{ID: targetIID, DefaultOrgID: nullable(orgID)}); err != nil {
			return err
		}
		action := audit.ActionOrgMemberRemove
		if actorIID == targetIID {
			action = audit.ActionOrgMemberLeave
		}
		detail := map[string]any{"iid": targetIID, "groups": len(groups)}
		if len(orphaned) > 0 {
			detail["operator_of"] = orphaned
		}
		return s.record(ctx, q, actorIID, action, orgID, detail)
	})
	if err != nil {
		return err
	}
	if s.bus != nil {
		for _, roomID := range groups {
			s.bus.Publish(events.MembersChanged{RoomID: roomID})
		}
		if len(orphaned) > 0 {
			s.bus.Publish(events.ConnectorsOrphaned{IDs: orphaned})
		}
	}
	s.changed(orgID)
	return nil
}
