package agents

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store/db"
)

// placement is where a new AI goes and on whose behalf: decided from what the session asked for
// (--org, --position, --parent) before anything is written.
type placement struct {
	// orgID is the AI's home organization.
	orgID string
	// creatorIID is the identity that created it: the token holder, or an AI they host (--parent).
	creatorIID string
	// permission is what the AI joins as: a member, or a guest of its creator (ORG-04).
	permission orgs.Permission
	// inviterIID is the guest's inviter, its creator; empty for a member.
	inviterIID string
	// positionID is the position it holds, empty for none.
	positionID string
}

// starter is the identity a start acts as: the token holder, or an AI they host and named as the parent.
type starter struct {
	iid string
	ai  bool
}

// place decides where a new AI goes, in the transaction that creates it (ORG-04, ORG-05, POS-07, POS-08):
//
//  1. the creator is the AI named by Parent when the token holder may act for it (it is hosted on their
//     machines), else the token holder; an unknown or foreign parent is ignored without a word;
//  2. the home organization is Org when given (422 organization_required if there is none such), else the
//     creator's default organization;
//  3. the creator must be a member of it (403, ORG-05);
//  4. Position, when given, must be a position of that organization (422 position_unknown) that is
//     strictly below the creator's own, unless the creator is an admin there (403, POS-07);
//  5. the AI joins as a guest its creator invited when the creator is an AI or itself only a guest, else as
//     a member (ORG-04), so whoever started an AI can manage it and a guest never places an AI only an admin
//     could delete.
//
// A refusal is an *httpx.Error, which the WebSocket handshake and the REST routes render alike.
func place(ctx context.Context, q *db.Queries, host string, id FolderID) (placement, error) {
	by, err := resolveStarter(ctx, q, host, id.Parent)
	if err != nil {
		return placement{}, err
	}
	home, err := homeOrganization(ctx, q, by, id.Org)
	if err != nil {
		return placement{}, err
	}
	chart, err := orgs.Load(ctx, q, home)
	if err != nil {
		return placement{}, err
	}
	standing, ok := chart.Member(by.iid)
	if !ok {
		return placement{}, notAMember(by)
	}
	p := placement{orgID: home, creatorIID: by.iid, permission: orgs.PermissionMember}
	if by.ai || standing.Permission == orgs.PermissionGuest {
		p.permission, p.inviterIID = orgs.PermissionGuest, by.iid
	}
	if id.Position != "" {
		pos, err := positionNamed(ctx, q, home, id.Position)
		if err != nil {
			return placement{}, err
		}
		if standing.Permission != orgs.PermissionAdmin && !chart.Above(standing.PositionID, pos.ID) {
			return placement{}, notBelow(by)
		}
		p.positionID = pos.ID
	}
	return p, nil
}

// resolveStarter finds who starts the AI (POS-08): the AI named by parent (an @handle or an id) when the
// token holder hosts it, else the token holder. The holder may act for themselves and for the AIs on their
// machines, the principle that lets a tools-only process act as its folder's AI; any other parent - unknown,
// a person, a user, an AI hosted by someone else - is ignored.
func resolveStarter(ctx context.Context, q *db.Queries, host, parent string) (starter, error) {
	self := starter{iid: host}
	ref := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(parent), "@"))
	if ref == "" {
		return self, nil
	}
	a, err := q.GetAgent(ctx, ref)
	if errors.Is(err, sql.ErrNoRows) {
		var i db.Intelligence
		if i, err = q.GetIntelligenceByHandle(ctx, ref); err == nil {
			a, err = q.GetAgent(ctx, i.ID)
		}
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return self, nil
	case err != nil:
		return starter{}, err
	case a.OwnerID != host:
		return self, nil
	}
	return starter{iid: a.ID, ai: true}, nil
}

// homeOrganization is the organization the AI joins (ORG-04): the one the session named, else the starter's
// default. A start that names none and has none to default to is refused (ErrNoOrganization).
func homeOrganization(ctx context.Context, q *db.Queries, by starter, ref string) (string, error) {
	if ref != "" {
		o, err := orgs.ResolveRef(ctx, q, ref)
		if errors.Is(err, orgs.ErrNotFound) {
			return "", folderError(dto.ErrorCodeOrganizationRequired, "org", "no organization has that slug or id: partyline orgs lists yours")
		}
		return o.ID, err
	}
	home, err := orgs.DefaultFor(ctx, q, by.iid)
	if err != nil {
		return "", err
	}
	if home == "" {
		return "", ErrNoOrganization
	}
	return home, nil
}

// positionNamed finds the position of the organization with that name, ignoring case (the rule that makes
// names unique), or refuses with 422 position_unknown.
func positionNamed(ctx context.Context, q *db.Queries, orgID, name string) (db.Position, error) {
	pos, err := q.GetPositionByName(ctx, db.GetPositionByNameParams{OrgID: orgID, Name: name})
	if errors.Is(err, sql.ErrNoRows) {
		return db.Position{}, folderError(dto.ErrorCodePositionUnknown, "position", "the organization has no position with that name: partyline orgs <slug> lists them")
	}
	return pos, err
}

// notAMember is the refusal of a start in an organization its creator does not belong to (ORG-05).
func notAMember(by starter) *httpx.Error {
	if by.ai {
		return httpx.NewError(http.StatusForbidden, dto.ErrorCodeForbidden, "the parent AI is not a member of that organization")
	}
	return httpx.NewError(http.StatusForbidden, dto.ErrorCodeForbidden, "you are not a member of that organization")
}

// notBelow is the refusal of a start in a position the creator may not give (POS-07).
func notBelow(by starter) *httpx.Error {
	if by.ai {
		return httpx.NewError(http.StatusForbidden, dto.ErrorCodeForbidden, "that position is not below the parent AI's own")
	}
	return httpx.NewError(http.StatusForbidden, dto.ErrorCodeForbidden, "that position is not below your own")
}
