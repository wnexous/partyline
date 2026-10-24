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

// MemberRow is a membership as the members list shows it: the identity with its profile summary,
// its permission and position, the inviter of a guest (OMEM-01, OMEM-03) and what the viewer may do to it
// (CHART-03).
type MemberRow struct {
	IID, Permission                    string
	PositionID, PositionName           string
	Kind, DisplayName, Handle          string
	AvatarSha256                       string
	InviterIID                         string
	InviterKind, InviterDisplayName    string
	InviterHandle, InviterAvatarSha256 string
	JoinedAt                           int64
	// CanDelete says the viewer may delete the member in this organization (POS-05); DeletedForGood what that
	// does: an AI whose home is the organization is deleted for good, anyone else is only removed (POS-06).
	// CanMove says the viewer may move the member to another position (POS-04).
	CanDelete, DeletedForGood, CanMove bool
	// Under is set by ChartView on a member with no position that hangs under another member.
	Under string
}

func memberRow(r db.ListOrgMembersRow) MemberRow {
	return MemberRow{
		IID: r.Iid, Permission: r.Permission, PositionID: r.PositionID, PositionName: r.PositionName,
		Kind: r.Kind, DisplayName: r.DisplayName, Handle: r.Handle, AvatarSha256: r.AvatarSha256,
		InviterIID: r.InviterIid, InviterKind: r.InviterKind, InviterDisplayName: r.InviterDisplayName,
		InviterHandle: r.InviterHandle, InviterAvatarSha256: r.InviterAvatarSha256, JoinedAt: r.JoinedAt,
	}
}

// orgSnapshot is one consistent read of an organization for a viewer: its chart, its members with their
// profiles and the AIs whose home it is.
type orgSnapshot struct {
	chart *Chart
	rows  []db.ListOrgMembersRow
	homes map[string]bool
}

// snapshot reads the organization in one read transaction, so the rows and the chart agree. The viewer must
// be a member (ErrNotFound otherwise). With only set it reads that one member, ErrNotFound when it is none.
func (s *Service) snapshot(ctx context.Context, viewerIID, orgID, only string) (orgSnapshot, error) {
	snap := orgSnapshot{homes: map[string]bool{}}
	err := s.store.ReadTx(ctx, func(q *db.Queries) error {
		var err error
		if snap.chart, err = Load(ctx, q, orgID); err != nil {
			return err
		}
		if _, ok := snap.chart.Member(viewerIID); !ok {
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
			home, err := q.GetAgentOrg(ctx, only) // empty for a user
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			snap.homes[only] = home == orgID
			return nil
		}
		if snap.rows, err = q.ListOrgMembers(ctx, orgID); err != nil {
			return err
		}
		homes, err := q.ListOrgHomeAgentIDs(ctx, nullable(orgID))
		for _, id := range homes {
			snap.homes[id] = true
		}
		return err
	})
	return snap, err
}

// memberRows renders the snapshot's members for the viewer, in the order they were read.
func (snap orgSnapshot) memberRows(viewerIID string) []MemberRow {
	actions := newViewerActions(snap.chart, viewerIID)
	out := make([]MemberRow, len(snap.rows))
	for i, r := range snap.rows {
		out[i] = memberRow(r)
		actions.apply(&out[i], snap.homes)
	}
	return out
}

// ListMembers lists the organization's members with their profile summary, permission, position and,
// for a guest, its inviter, and what the viewer may do to each; every member, guests included, sees the
// list (CHART-02).
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
	// PositionID is a position of the organization, "" for none.
	PositionID string
	// InviterIID is a guest's inviter; it is ignored for any other permission.
	InviterIID string
	// Now is the join time.
	Now time.Time
}

// AddMember makes an identity a member of an organization in the caller's transaction: invitations
// accepted (OMEM-02) and AIs started (ORG-04) use it. It refuses an identity that already is a member
// (ErrAlreadyMember), a permission that is not admin, member or guest and a position that is not the
// organization's (validation errors). It writes no audit entry and announces nothing: the caller does,
// after its commit.
func AddMember(ctx context.Context, q *db.Queries, in AddMemberInput) error {
	if !in.Permission.Valid() {
		return &validation.Error{Fields: map[string]string{"permission": "must be admin, member or guest"}}
	}
	if _, ok, err := memberOf(ctx, q, in.OrgID, in.IID); err != nil || ok {
		if err == nil {
			err = ErrAlreadyMember
		}
		return err
	}
	if in.PositionID != "" {
		if err := requirePositionIn(ctx, q, in.OrgID, in.PositionID, "position_id"); err != nil {
			return err
		}
	}
	inviter := in.InviterIID
	if in.Permission != PermissionGuest {
		inviter = ""
	}
	return q.CreateOrgMember(ctx, db.CreateOrgMemberParams{
		OrgID: in.OrgID, Iid: in.IID, Permission: string(in.Permission), PositionID: nullable(in.PositionID),
		InviterIid: nullable(inviter), JoinedAt: in.Now.UnixMilli(),
	})
}

func countAdmins(c *Chart) int {
	n := 0
	for _, m := range c.Members {
		if m.Permission == PermissionAdmin {
			n++
		}
	}
	return n
}

// MemberUpdate is a change to a member's permission and/or position; a nil field is left alone.
type MemberUpdate struct {
	Permission *Permission
	// PositionID moves the member to that position of the organization; a pointer to "" clears it.
	PositionID *string
}

// SetPermission gives a member of the organization another permission; only an admin does (OMEM-04).
// A change that would leave no admin is ErrLastAdmin (OMEM-05), decided in the transaction that
// changes it. A guest that becomes a member or admin is nobody's guest any more. The same permission
// again is nothing. It returns the membership as it is afterwards.
func (s *Service) SetPermission(ctx context.Context, actorIID, orgID, targetIID string, perm Permission) (Member, error) {
	return s.UpdateMember(ctx, actorIID, orgID, targetIID, MemberUpdate{Permission: &perm})
}

// SetPosition moves a member to another position of the organization (POS-04). An admin gives anyone
// any position of the organization, or none (positionID ""); a position that is not the organization's
// is a validation error on position_id. Anyone else must manage the member and move it to a position
// strictly below its own: to its own level, above it, to none, or by someone who does not manage the
// member, it is ErrForbidden. The same position again is nothing. It returns the membership
// afterwards.
func (s *Service) SetPosition(ctx context.Context, actorIID, orgID, targetIID, positionID string) (Member, error) {
	return s.UpdateMember(ctx, actorIID, orgID, targetIID, MemberUpdate{PositionID: &positionID})
}

// UpdateMember changes a member's permission and/or position in one transaction, so either both
// changes happen or neither does. The rules are those of SetPermission and SetPosition, and the actor's
// rights are the ones it had when it asked: an admin that steps down and moves in the same request may
// still make the move. A request that changes nothing is nothing; asking for nothing needs only that
// the actor is a member. It returns the membership afterwards.
func (s *Service) UpdateMember(ctx context.Context, actorIID, orgID, targetIID string, u MemberUpdate) (Member, error) {
	if u.Permission != nil {
		if err := s.z.RequireAdmin(ctx, orgID, actorIID); err != nil {
			return Member{}, err
		}
		if !u.Permission.Valid() {
			return Member{}, &validation.Error{Fields: map[string]string{"permission": "must be admin, member or guest"}}
		}
	} else if err := s.z.RequireMember(ctx, orgID, actorIID); err != nil {
		return Member{}, err
	}
	var out Member
	var changed bool
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		out, changed = Member{}, false
		chart, err := Load(ctx, q, orgID)
		if err != nil {
			return err
		}
		actor, ok := chart.Member(actorIID)
		if !ok {
			return ErrNotFound
		}
		if u.Permission != nil && actor.Permission != PermissionAdmin {
			return ErrForbidden
		}
		cur, ok := chart.Member(targetIID)
		if !ok {
			return ErrNotFound
		}
		if u.PositionID != nil {
			if err := checkPosition(chart, actor, targetIID, *u.PositionID); err != nil {
				return err
			}
		}
		out = cur
		if u.Permission != nil {
			moved, err := s.writePermission(ctx, q, chart, actorIID, orgID, &out, *u.Permission)
			if err != nil {
				return err
			}
			changed = changed || moved
		}
		if u.PositionID != nil {
			moved, err := s.writePosition(ctx, q, actorIID, orgID, &out, *u.PositionID)
			if err != nil {
				return err
			}
			changed = changed || moved
		}
		return nil
	})
	if err != nil {
		return Member{}, err
	}
	if changed {
		s.changed(orgID)
	}
	return out, nil
}

// checkPosition decides whether actor may put the target in the position (POS-04): an admin may use
// any position of the organization or none, anyone else only a position strictly below its own for a
// member it manages.
func checkPosition(chart *Chart, actor Member, targetIID, positionID string) error {
	if actor.Permission == PermissionAdmin {
		if _, ok := chart.Positions[positionID]; positionID != "" && !ok {
			return &validation.Error{Fields: map[string]string{"position_id": "must be a position of this organization"}}
		}
		return nil
	}
	if positionID == "" || !chart.Manages(actor.IID, targetIID) || !chart.Above(actor.PositionID, positionID) {
		return ErrForbidden
	}
	return nil
}

// writePermission gives *m the permission and records it; m is the member as it is now, and is updated
// to how it is afterwards. It reports whether anything changed.
func (s *Service) writePermission(ctx context.Context, q *db.Queries, chart *Chart, actorIID, orgID string, m *Member, perm Permission) (bool, error) {
	if m.Permission == perm {
		return false, nil
	}
	if m.Permission == PermissionAdmin && countAdmins(chart) <= 1 {
		return false, ErrLastAdmin
	}
	from := m.Permission
	m.Permission = perm
	if perm != PermissionGuest {
		m.InviterIID = ""
	}
	if err := q.UpdateOrgMemberPermission(ctx, db.UpdateOrgMemberPermissionParams{
		Permission: string(perm), InviterIid: nullable(m.InviterIID), OrgID: orgID, Iid: m.IID,
	}); err != nil {
		return false, err
	}
	return true, s.record(ctx, q, actorIID, audit.ActionOrgMemberPermission, orgID,
		map[string]any{"iid": m.IID, "from": string(from), "to": string(perm)})
}

// writePosition moves *m to the position ("" for none) and records it; m is updated to how it is
// afterwards. It reports whether anything changed.
func (s *Service) writePosition(ctx context.Context, q *db.Queries, actorIID, orgID string, m *Member, positionID string) (bool, error) {
	if m.PositionID == positionID {
		return false, nil
	}
	from := m.PositionID
	m.PositionID = positionID
	if err := q.UpdateOrgMemberPosition(ctx, db.UpdateOrgMemberPositionParams{PositionID: nullable(positionID), OrgID: orgID, Iid: m.IID}); err != nil {
		return false, err
	}
	return true, s.record(ctx, q, actorIID, audit.ActionOrgMemberPosition, orgID,
		map[string]any{"iid": m.IID, "from": from, "to": positionID})
}

// Remove takes target out of the organization on behalf of actor (POS-05, OMEM-06): actor must be
// allowed to delete target - an admin, or whoever manages a guest - else ErrForbidden; an outsider
// actor or a target that is not a member is ErrNotFound. Removing yourself is Leave. What Remove does is
// only the removal: the person or the AI of another organization is out of the organization's groups
// and of its position, keeps its account and its other memberships, and the organization's AIs stay.
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
		chart, err := Load(ctx, q, orgID)
		if err != nil {
			return err
		}
		if _, ok := chart.Member(actorIID); !ok {
			return ErrNotFound
		}
		target, ok := chart.Member(targetIID)
		if !ok {
			return ErrNotFound
		}
		if actorIID != targetIID && !chart.CanDelete(actorIID, targetIID) {
			return ErrForbidden
		}
		home, err := q.GetAgentOrg(ctx, targetIID) // empty for a user
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if home == orgID {
			return ErrHomeOrganization
		}
		if target.Permission == PermissionAdmin && countAdmins(chart) <= 1 {
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
