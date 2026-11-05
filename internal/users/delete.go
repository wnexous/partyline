package users

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store/db"
)

// ErrLastInstanceAdmin means the user is the last active instance admin: an admin must remain to inherit
// the organizations they would leave without one (409 last_instance_admin, OUPG-02).
var ErrLastInstanceAdmin = errors.New("the last active admin of the instance cannot delete their account")

// DeleteAccount deletes the user's own account (POS-06). A person owns their account, so nobody else deletes
// it (deleting a person in an organization only removes them from it), and an organization owns its AIs,
// groups and connections, so none of them goes with the account (AD-019). In one transaction, in this order:
//
//  1. a wrong password is a validation error on the field password (told before anything else);
//  2. an instance with no other active admin is ErrLastInstanceAdmin: the heir is the oldest active admin
//     other than the user;
//  3. the heir becomes an admin of each organization the user alone administered (OUPG-02), keeping the
//     position it held, if any; each is audited as org.admin_inherited;
//  4. the rooms and connections the user created are owned by the heir, so that the foreign keys that
//     delete them with their owner cannot (AD-019);
//  5. the AIs hosted on the user's computers stay in their organizations, with their positions, as
//     unlinked identities owned by the heir: no folder, and their id as v0.3 name;
//  6. the user's group memberships go, and the user itself, with what cascades from it: sessions, tokens,
//     linked chat accounts, machines, pairing codes, the intelligence, its organization memberships, its
//     invitations and its DMs. The deletion is audited as user.delete.
//
// After the commit the live sessions of the hosted AIs end (they are deleted AIs to the channel, 4001), the
// user's credentials are revoked at once so their sockets close, and every organization and room that
// changed is announced: those the user belonged to, those their hosted AIs belong to (their creator link is
// gone) and the groups and DMs the user was in. A failure anywhere changes nothing. The failed attempts at the password are not
// rate limited, like those of a password change.
func (s *Service) DeleteAccount(ctx context.Context, userID, password string) error {
	u, err := s.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	ok, err := s.verifyPassword(u.PasswordHash, password)
	if err != nil {
		return err
	}
	if !ok {
		return &ValidationError{Fields: map[string]string{"password": "is incorrect"}}
	}
	var hosted, ended, touched, rooms []string
	err = s.store.WithTx(ctx, func(q *db.Queries) error {
		hosted, ended, touched, rooms = nil, nil, nil, nil
		heir, err := q.GetHeirAdmin(ctx, userID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLastInstanceAdmin
		}
		if err != nil {
			return err
		}
		memberships, err := q.ListOrgsOfMember(ctx, userID)
		if err != nil {
			return err
		}
		for _, m := range memberships {
			touched = append(touched, m.Organization.ID)
		}
		hostedOrgs, err := q.ListHostedAgentOrgIDs(ctx, userID)
		if err != nil {
			return err
		}
		for _, id := range hostedOrgs {
			if !slices.Contains(touched, id) {
				touched = append(touched, id)
			}
		}
		alone, err := q.ListLastAdminOrgIDs(ctx, userID)
		if err != nil {
			return err
		}
		for _, orgID := range alone {
			if err := s.inheritAdmin(ctx, q, orgID, heir.ID, userID); err != nil {
				return err
			}
		}
		if err := q.RepointRoomOwners(ctx, db.RepointRoomOwnersParams{Heir: heir.ID, UserID: userID}); err != nil {
			return err
		}
		if err := q.RepointConnectorOwners(ctx, db.RepointConnectorOwnersParams{Heir: heir.ID, UserID: userID}); err != nil {
			return err
		}
		if hosted, err = q.ListHostedAgentIDs(ctx, userID); err != nil {
			return err
		}
		if err := q.RehomeHostedAgents(ctx, db.RehomeHostedAgentsParams{Heir: heir.ID, UserID: userID}); err != nil {
			return err
		}
		if rooms, err = q.DeleteUserRoomMemberships(ctx, userID); err != nil {
			return err
		}
		if ended, err = q.DeleteSessionsByUser(ctx, userID); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, q, audit.Entry{
			ActorID: userID, Action: audit.ActionUserDelete, TargetKind: audit.TargetUser, TargetID: userID,
			Detail: map[string]any{"username": u.Username, "hosted_ais": len(hosted), "heir": heir.ID},
		}); err != nil {
			return err
		}
		return q.DeleteUser(ctx, userID)
	})
	if err != nil {
		return err
	}
	s.announceDeleted(ctx, userID, hosted, ended, touched, rooms)
	return nil
}

// inheritAdmin makes the heir an admin of an organization whose last admin is leaving: a member they were not
// becomes one with no position, one they already were keeps their position and stops being anyone's guest.
func (s *Service) inheritAdmin(ctx context.Context, q *db.Queries, orgID, heirID, leavingID string) error {
	m, err := q.GetOrgMember(ctx, db.GetOrgMemberParams{OrgID: orgID, Iid: heirID})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		err = orgs.AddMember(ctx, q, orgs.AddMemberInput{OrgID: orgID, IID: heirID, Permission: orgs.PermissionAdmin, Now: s.now()})
	case err != nil:
	case m.Permission != string(orgs.PermissionAdmin):
		err = q.UpdateOrgMemberPermission(ctx, db.UpdateOrgMemberPermissionParams{
			Permission: string(orgs.PermissionAdmin), InviterIid: sql.NullString{}, OrgID: orgID, Iid: heirID,
		})
	}
	if err != nil {
		return err
	}
	return s.audit.Record(ctx, q, audit.Entry{
		ActorID: leavingID, Action: audit.ActionOrgAdminInherited, TargetKind: audit.TargetOrganization, TargetID: orgID,
		Detail: map[string]any{"heir": heirID, "user": leavingID},
	})
}

// announceDeleted tells the rest of the app, after the commit, that an account is gone: the hosted AIs'
// sessions end first (4001, as for a deleted AI), then the user's own credentials are revoked, then the
// organizations and rooms that changed are announced. Events are hints clients can recover from by
// refetching, so a lookup that fails here is not an error.
func (s *Service) announceDeleted(ctx context.Context, userID string, hosted, ended, orgIDs, roomIDs []string) {
	if s.kick != nil {
		for _, id := range hosted {
			s.kick(ctx, id)
		}
	}
	s.revoked(events.SessionsRevoked{UserID: userID, SessionIDs: ended, UserDisabled: true})
	if s.bus == nil {
		return
	}
	for _, id := range orgIDs {
		s.bus.Publish(events.OrgChanged{OrgID: id})
	}
	for _, id := range roomIDs {
		if _, err := s.store.R().GetRoom(ctx, id); errors.Is(err, sql.ErrNoRows) {
			s.bus.Publish(events.RoomChanged{RoomID: id, Deleted: true}) // a DM of the user
			continue
		}
		s.bus.Publish(events.MembersChanged{RoomID: id})
	}
}
