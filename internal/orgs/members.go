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

// SetPermission gives a member of the organization another permission; only an admin does (OMEM-04).
// A change that would leave no admin is ErrLastAdmin (OMEM-05), decided in the transaction that
// changes it. A guest that becomes a member or admin is nobody's guest any more. The same permission
// again is nothing. It returns the membership as it is afterwards.
func (s *Service) SetPermission(ctx context.Context, actorIID, orgID, targetIID string, perm Permission) (Member, error) {
	if err := s.z.RequireAdmin(ctx, orgID, actorIID); err != nil {
		return Member{}, err
	}
	if !perm.Valid() {
		return Member{}, &validation.Error{Fields: map[string]string{"permission": "must be admin, member or guest"}}
	}
	var out Member
	var changed bool
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		if err := requireAdmin(ctx, q, orgID, actorIID); err != nil {
			return err
		}
		chart, err := Load(ctx, q, orgID)
		if err != nil {
			return err
		}
		cur, ok := chart.Member(targetIID)
		if !ok {
			return ErrNotFound
		}
		out = cur
		if cur.Permission == perm {
			return nil
		}
		if cur.Permission == PermissionAdmin && countAdmins(chart) <= 1 {
			return ErrLastAdmin
		}
		out.Permission = perm
		if perm != PermissionGuest {
			out.InviterIID = ""
		}
		changed = true
		if err := q.UpdateOrgMemberPermission(ctx, db.UpdateOrgMemberPermissionParams{
			Permission: string(perm), InviterIid: nullable(out.InviterIID), OrgID: orgID, Iid: targetIID,
		}); err != nil {
			return err
		}
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

// SetPosition moves a member to another position of the organization (POS-04). An admin gives anyone
// any position of the organization, or none (positionID ""); a position that is not the organization's
// is a validation error on position_id. Anyone else must manage the member and move it to a position
// strictly below its own: to its own level, above it, to none, or by someone who does not manage the
// member, it is ErrForbidden. The same position again is nothing. It returns the membership
// afterwards.
func (s *Service) SetPosition(ctx context.Context, actorIID, orgID, targetIID, positionID string) (Member, error) {
	if err := s.z.RequireMember(ctx, orgID, actorIID); err != nil {
		return Member{}, err
	}
	var out Member
	var changed bool
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		chart, err := Load(ctx, q, orgID)
		if err != nil {
			return err
		}
		actor, ok := chart.Member(actorIID)
		if !ok {
			return ErrNotFound
		}
		cur, ok := chart.Member(targetIID)
		if !ok {
			return ErrNotFound
		}
		if actor.Permission == PermissionAdmin {
			if _, ok := chart.Positions[positionID]; positionID != "" && !ok {
				return &validation.Error{Fields: map[string]string{"position_id": "must be a position of this organization"}}
			}
		} else if positionID == "" || !chart.Manages(actorIID, targetIID) || !chart.Above(actor.PositionID, positionID) {
			return ErrForbidden
		}
		out = cur
		if cur.PositionID == positionID {
			return nil
		}
		out.PositionID = positionID
		changed = true
		if err := q.UpdateOrgMemberPosition(ctx, db.UpdateOrgMemberPositionParams{PositionID: nullable(positionID), OrgID: orgID, Iid: targetIID}); err != nil {
			return err
		}
		return s.record(ctx, q, actorIID, audit.ActionOrgMemberPosition, orgID,
			map[string]any{"iid": targetIID, "from": cur.PositionID, "to": positionID})
	})
	if err != nil {
		return Member{}, err
	}
	if changed {
		s.changed(orgID)
	}
	return out, nil
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
