package orgs

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// Invitation limits.
const (
	// InvitationTTL is how long an invitation waits for an answer (OMEM-07).
	InvitationTTL = 14 * 24 * time.Hour
	// InvitationsPerHour is the most invitations an organization sends per hour, counted from the audit
	// log: guest invitations and re-invitations included, AIs started with --parent not (they are not
	// invitations).
	InvitationsPerHour = 30
)

// Errors of the invitations.
var (
	// ErrContactNotFound means the @handle is not that of a user or an AI (404 contact_not_found).
	ErrContactNotFound = errors.New("contact not found")
	// ErrRateLimited means the organization already sent InvitationsPerHour invitations in the last
	// hour (429 rate_limited).
	ErrRateLimited = errors.New("too many invitations, try again later")
	// ErrGone means the invitation has expired (410 gone, OMEM-07).
	ErrGone = errors.New("the invitation has expired")
)

// Invitation is a pending invitation with the names a client shows.
type Invitation struct {
	ID                                           string
	OrgID, OrgName, OrgSlug                      string
	InviteeIID, InviteeHandle, InviteeName       string
	InviteeKind                                  string // "user" or "ai"
	Permission                                   Permission
	PositionID, PositionName                     string
	InvitedByIID, InvitedByHandle, InvitedByName string
	CreatedAt, ExpiresAt                         time.Time
}

// inviteesHandle cleans an @handle: surrounding spaces and one leading "@" go, the case is ignored by
// the column.
func inviteesHandle(h string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(h), "@"))
}

// Invite invites a user or an AI, named by @handle, to the organization (OMEM-01). An admin may give any
// permission and a position; every other member or guest may only invite as a guest, with no position,
// else ErrForbidden (OMEM-04). The inviter is recorded and becomes the guest's inviter when the
// invitation is accepted. The handle of a person (an external account), a disabled user or nobody is
// ErrContactNotFound; an identity that already is a member is ErrAlreadyMember. Inviting again updates
// the pending invitation and starts its 14 days over; whoever may invite only guests cannot replace a
// pending invitation for another permission. The organization sends at most InvitationsPerHour
// invitations per hour (ErrRateLimited): the count and the invitation are one transaction, so a burst
// cannot pass the limit.
func (s *Service) Invite(ctx context.Context, actorIID, orgID, handle string, perm Permission, positionID string) (Invitation, error) {
	if err := s.z.RequireMember(ctx, orgID, actorIID); err != nil {
		return Invitation{}, err
	}
	var invitationID string
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		actor, ok, err := memberOf(ctx, q, orgID, actorIID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if actor.Permission != PermissionAdmin {
			if perm != PermissionGuest || positionID != "" {
				return ErrForbidden
			}
		} else {
			if !perm.Valid() {
				return &validation.Error{Fields: map[string]string{"permission": "must be admin, member or guest"}}
			}
			if positionID != "" {
				if err := requirePositionIn(ctx, q, orgID, positionID, "position_id"); err != nil {
					return err
				}
			}
		}
		now := s.now()
		sent, err := q.CountAuditEntriesSince(ctx, db.CountAuditEntriesSinceParams{
			Action: audit.ActionOrgInvite, TargetKind: audit.TargetOrganization, TargetID: orgID, Since: now.Add(-time.Hour).UnixMilli(),
		})
		if err != nil {
			return err
		}
		if sent >= InvitationsPerHour {
			return ErrRateLimited
		}
		invitee, err := s.invitee(ctx, q, handle)
		if err != nil {
			return err
		}
		if _, ok, err := memberOf(ctx, q, orgID, invitee); err != nil || ok {
			if err == nil {
				err = ErrAlreadyMember
			}
			return err
		}
		pending, err := q.GetOrgInvitationOf(ctx, db.GetOrgInvitationOfParams{OrgID: orgID, InviteeIid: invitee})
		switch {
		case err == nil:
			// Someone who may only invite guests does not replace an invitation for more.
			if actor.Permission != PermissionAdmin && Permission(pending.Permission) != PermissionGuest && pending.ExpiresAt > now.UnixMilli() {
				return ErrForbidden
			}
			invitationID = pending.ID
		case errors.Is(err, sql.ErrNoRows):
			invitationID = store.NewID()
		default:
			return err
		}
		if err := q.UpsertOrgInvitation(ctx, db.UpsertOrgInvitationParams{
			ID: invitationID, OrgID: orgID, InviteeIid: invitee, Permission: string(perm), PositionID: nullable(positionID),
			InvitedBy: actorIID, CreatedAt: now.UnixMilli(), ExpiresAt: now.Add(InvitationTTL).UnixMilli(),
		}); err != nil {
			return err
		}
		return s.record(ctx, q, actorIID, audit.ActionOrgInvite, orgID, map[string]any{
			"invitation": invitationID, "invitee": invitee, "permission": string(perm), "position_id": positionID,
		})
	})
	if err != nil {
		return Invitation{}, err
	}
	inv, err := s.invitationView(ctx, invitationID)
	if err != nil {
		return Invitation{}, err
	}
	if s.bus != nil {
		s.bus.Publish(events.InvitationReceived{OrgID: orgID, InvitationID: invitationID, InviteeIID: inv.InviteeIID})
	}
	s.changed(orgID)
	return inv, nil
}

// invitee resolves an @handle to a user or an AI (never a person or a disabled account).
func (s *Service) invitee(ctx context.Context, q *db.Queries, handle string) (string, error) {
	handle = inviteesHandle(handle)
	if handle == "" {
		return "", ErrContactNotFound
	}
	i, err := q.GetIntelligenceByHandle(ctx, handle)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrContactNotFound
	}
	if err != nil {
		return "", err
	}
	d, err := q.GetIntelligenceWithRole(ctx, i.ID)
	if err != nil {
		return "", err
	}
	switch d.Role {
	case "ai":
		return i.ID, nil
	case "user":
		active, err := activeUser(ctx, q, i.ID)
		if err != nil {
			return "", err
		}
		if active {
			return i.ID, nil
		}
	}
	return "", ErrContactNotFound
}

// invitationView reads one invitation with its names.
func (s *Service) invitationView(ctx context.Context, id string) (Invitation, error) {
	q := s.store.R()
	row, err := q.GetOrgInvitation(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Invitation{}, ErrNotFound
	}
	if err != nil {
		return Invitation{}, err
	}
	list, err := s.invitationViews(ctx, q, []db.OrgInvitation{row})
	if err != nil {
		return Invitation{}, err
	}
	return list[0], nil
}

// invitationViews adds the names of the organizations, positions, invitees and inviters to rows.
func (s *Service) invitationViews(ctx context.Context, q *db.Queries, rows []db.OrgInvitation) ([]Invitation, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	orgs := map[string]db.Organization{}
	positionNames := map[string]string{}
	people := map[string]db.Intelligence{}
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.InviteeIid, r.InvitedBy)
		if _, ok := orgs[r.OrgID]; ok {
			continue
		}
		o, err := q.GetOrganization(ctx, r.OrgID)
		if err != nil {
			return nil, err
		}
		orgs[r.OrgID] = o
		positions, err := q.ListOrgPositions(ctx, r.OrgID)
		if err != nil {
			return nil, err
		}
		for _, p := range positions {
			positionNames[p.ID] = p.Name
		}
	}
	found, err := q.ListIntelligencesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, i := range found {
		people[i.ID] = i
	}
	out := make([]Invitation, len(rows))
	for i, r := range rows {
		invitee, inviter := people[r.InviteeIid], people[r.InvitedBy]
		kind := "user"
		if invitee.Kind == "ai" {
			kind = "ai"
		}
		out[i] = Invitation{
			ID: r.ID, OrgID: r.OrgID, OrgName: orgs[r.OrgID].Name, OrgSlug: orgs[r.OrgID].Slug,
			InviteeIID: r.InviteeIid, InviteeHandle: invitee.Handle, InviteeName: invitee.DisplayName, InviteeKind: kind,
			Permission: Permission(r.Permission), PositionID: r.PositionID.String, PositionName: positionNames[r.PositionID.String],
			InvitedByIID: r.InvitedBy, InvitedByHandle: inviter.Handle, InvitedByName: inviter.DisplayName,
			CreatedAt: time.UnixMilli(r.CreatedAt).UTC(), ExpiresAt: time.UnixMilli(r.ExpiresAt).UTC(),
		}
	}
	return out, nil
}

// ListInvitations lists the organization's pending invitations, newest first; only an admin sees them.
func (s *Service) ListInvitations(ctx context.Context, actorIID, orgID string) ([]Invitation, error) {
	if err := s.z.RequireAdmin(ctx, orgID, actorIID); err != nil {
		return nil, err
	}
	q := s.store.R()
	rows, err := q.ListOrgInvitations(ctx, db.ListOrgInvitationsParams{OrgID: orgID, Now: s.now().UnixMilli()})
	if err != nil {
		return nil, err
	}
	return s.invitationViews(ctx, q, rows)
}

// MyInvitations lists the pending invitations the identity answers: its own, and those of the AIs it manages
// in their home organization (OMEM-01), newest first.
func (s *Service) MyInvitations(ctx context.Context, actorIID string) ([]Invitation, error) {
	if actorIID == "" {
		return nil, nil
	}
	q := s.store.R()
	now := s.now().UnixMilli()
	own, err := q.ListInvitationsForInvitee(ctx, db.ListInvitationsForInviteeParams{InviteeIid: actorIID, Now: now})
	if err != nil {
		return nil, err
	}
	around, err := q.ListInvitationsForAIsAround(ctx, db.ListInvitationsForAIsAroundParams{MemberIid: actorIID, Now: now})
	if err != nil {
		return nil, err
	}
	session := s.z.Session()
	seen := map[string]bool{}
	var rows []db.OrgInvitation
	for _, r := range own {
		seen[r.ID] = true
		rows = append(rows, r)
	}
	for _, r := range around {
		if seen[r.ID] {
			continue
		}
		home, err := session.homeOf(ctx, r.InviteeIid)
		if err != nil {
			return nil, err
		}
		if ok, err := session.Manages(ctx, actorIID, r.InviteeIid, home); err != nil {
			return nil, err
		} else if ok && home != "" {
			rows = append(rows, r)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].CreatedAt != rows[j].CreatedAt {
			return rows[i].CreatedAt > rows[j].CreatedAt
		}
		return rows[i].ID < rows[j].ID
	})
	return s.invitationViews(ctx, q, rows)
}

// mayAnswer reports whether actor answers an invitation of invitee: the user itself, or for an AI whoever
// manages it in its home organization.
func mayAnswer(ctx context.Context, q *db.Queries, actor, invitee string) (bool, error) {
	if actor == "" {
		return false, nil
	}
	d, err := q.GetIntelligenceWithRole(ctx, invitee)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	switch d.Role {
	case "user":
		return actor == invitee, nil
	case "ai":
		home, err := q.GetAgentOrg(ctx, invitee)
		if err != nil || home == "" {
			return false, err
		}
		chart, err := Load(ctx, q, home)
		if err != nil {
			return false, err
		}
		return chart.Manages(actor, invitee), nil
	}
	return false, nil
}

// answer finds the invitation for actor to answer inside the caller's transaction: ErrNotFound when it
// does not exist or actor does not answer it, ErrGone when it has expired.
func (s *Service) answer(ctx context.Context, q *db.Queries, actorIID, invitationID string) (db.OrgInvitation, error) {
	inv, err := q.GetOrgInvitation(ctx, invitationID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.OrgInvitation{}, ErrNotFound
	}
	if err != nil {
		return db.OrgInvitation{}, err
	}
	ok, err := mayAnswer(ctx, q, actorIID, inv.InviteeIid)
	if err != nil {
		return db.OrgInvitation{}, err
	}
	if !ok {
		return db.OrgInvitation{}, ErrNotFound
	}
	if s.now().UnixMilli() >= inv.ExpiresAt {
		return db.OrgInvitation{}, ErrGone
	}
	return inv, nil
}

// Accept accepts an invitation for the invitee: the identity becomes a member of the organization with the
// invitation's permission and position, and for a guest the inviter is recorded (OMEM-02). The invitee is
// the user itself, or for an AI whoever manages it; anyone else gets ErrNotFound, an expired invitation
// ErrGone (OMEM-07). An invitee that already became a member another way gets ErrAlreadyMember and the
// stale invitation goes. It returns the invitee's membership.
func (s *Service) Accept(ctx context.Context, actorIID, invitationID string) (Membership, error) {
	var out Membership
	var stale, gone bool
	var orgID string
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		stale, gone = false, false
		inv, err := s.answer(ctx, q, actorIID, invitationID)
		if err != nil {
			return err
		}
		orgID = inv.OrgID
		if _, ok, err := memberOf(ctx, q, inv.OrgID, inv.InviteeIid); err != nil || ok {
			if err != nil {
				return err
			}
			stale = true
			return q.DeleteOrgInvitation(ctx, inv.ID)
		}
		// An invitation for more than a guest is only as good as its inviter's standing now: once the
		// inviter is no longer an admin of the organization it is gone. (A guest invitation whose inviter
		// left still stands; the guest then has no inviter.)
		by, ok, err := memberOf(ctx, q, inv.OrgID, inv.InvitedBy)
		if err != nil {
			return err
		}
		if Permission(inv.Permission) != PermissionGuest && (!ok || by.Permission != PermissionAdmin) {
			gone = true
			return q.DeleteOrgInvitation(ctx, inv.ID)
		}
		inviter := ""
		if Permission(inv.Permission) == PermissionGuest {
			// The inviter is the guest's responsibility link; an account that is gone leaves none.
			if _, err := q.GetIntelligence(ctx, inv.InvitedBy); err == nil {
				inviter = inv.InvitedBy
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if err := AddMember(ctx, q, AddMemberInput{
			OrgID: inv.OrgID, IID: inv.InviteeIid, Permission: Permission(inv.Permission), PositionID: inv.PositionID.String,
			InviterIID: inviter, Now: s.now(),
		}); err != nil {
			return err
		}
		if err := q.DeleteOrgInvitation(ctx, inv.ID); err != nil {
			return err
		}
		if err := s.record(ctx, q, actorIID, audit.ActionOrgInviteAccept, inv.OrgID,
			map[string]any{"invitation": inv.ID, "invitee": inv.InviteeIid}); err != nil {
			return err
		}
		if err := s.record(ctx, q, actorIID, audit.ActionOrgMemberJoin, inv.OrgID, map[string]any{
			"iid": inv.InviteeIid, "by": "invitation", "permission": inv.Permission, "position_id": inv.PositionID.String, "invited_by": inv.InvitedBy,
		}); err != nil {
			return err
		}
		out, err = getMembership(ctx, q, inv.OrgID, inv.InviteeIid)
		return err
	})
	if err != nil {
		return Membership{}, err
	}
	s.changed(orgID)
	if stale {
		return Membership{}, ErrAlreadyMember
	}
	if gone {
		return Membership{}, ErrGone
	}
	return out, nil
}

// Decline declines an invitation for the invitee, with the same rules as Accept: the invitation is deleted.
func (s *Service) Decline(ctx context.Context, actorIID, invitationID string) error {
	var orgID string
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		inv, err := s.answer(ctx, q, actorIID, invitationID)
		if err != nil {
			return err
		}
		orgID = inv.OrgID
		if err := q.DeleteOrgInvitation(ctx, inv.ID); err != nil {
			return err
		}
		return s.record(ctx, q, actorIID, audit.ActionOrgInviteDecline, inv.OrgID,
			map[string]any{"invitation": inv.ID, "invitee": inv.InviteeIid})
	})
	if err != nil {
		return err
	}
	s.changed(orgID)
	return nil
}

// Revoke withdraws a pending invitation of the organization; only an admin does (OMEM-04). An invitation of
// another organization is ErrNotFound.
func (s *Service) Revoke(ctx context.Context, actorIID, orgID, invitationID string) error {
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		if err := requireAdmin(ctx, q, orgID, actorIID); err != nil {
			return err
		}
		inv, err := q.GetOrgInvitation(ctx, invitationID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && inv.OrgID != orgID) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := q.DeleteOrgInvitation(ctx, inv.ID); err != nil {
			return err
		}
		return s.record(ctx, q, actorIID, audit.ActionOrgInviteRevoke, orgID,
			map[string]any{"invitation": inv.ID, "invitee": inv.InviteeIid})
	})
	if err != nil {
		return err
	}
	s.changed(orgID)
	return nil
}

// PurgeExpiredInvitations deletes the invitations whose 14 days are over, at the instant they are over,
// and audits each. It is the retention job's task (OMEM-07) and returns how many it removed.
func (s *Service) PurgeExpiredInvitations(ctx context.Context) (int64, error) {
	var rows []db.OrgInvitation
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		var err error
		if rows, err = q.DeleteExpiredOrgInvitations(ctx, s.now().UnixMilli()); err != nil {
			return err
		}
		for _, r := range rows {
			if err := s.record(ctx, q, "", audit.ActionOrgInviteExpire, r.OrgID,
				map[string]any{"invitation": r.ID, "invitee": r.InviteeIid, "permission": r.Permission}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	announced := map[string]bool{}
	for _, r := range rows {
		if !announced[r.OrgID] {
			announced[r.OrgID] = true
			s.changed(r.OrgID)
		}
	}
	return int64(len(rows)), nil
}
