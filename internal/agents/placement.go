package agents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/events"
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

// positionMove is a later start asking an existing AI to hold another position.
type positionMove struct {
	orgID, agentID string
	from, to       string
}

// checkStart applies the start rules to an AI that already exists, against the state q sees (ORG-04, POS-07):
//
//   - --org may only name its home organization: any other - a different one of the host's, one the host is not
//     in, one that does not exist - is 409 organization_mismatch, and the answer names the home organization,
//     because an AI does not move;
//   - --parent is ignored: the creator is fixed when the AI is created, and the token holder acts for themselves;
//   - --position, when it differs from the position the AI holds, asks the AI to move there. The token holder
//     may when they are an admin of the home organization, or manage the AI and hold a position strictly above
//     the new one (the rule of orgs.Service.SetPosition); otherwise 403. A position that does not exist is
//     422 position_unknown. The position it already holds is nothing, whoever asks; no --position never clears.
//
// It writes nothing: the move it returns, if any, is for applyMove. An AI without a home organization (a
// v0.3 identity no upgrade placed) has no rules.
func checkStart(ctx context.Context, q *db.Queries, host string, a db.Agent, id FolderID) (*positionMove, error) {
	if !a.OrgID.Valid || (id.Org == "" && id.Position == "") {
		return nil, nil
	}
	home := a.OrgID.String
	if id.Org != "" {
		o, err := orgs.ResolveRef(ctx, q, id.Org)
		switch {
		case errors.Is(err, orgs.ErrNotFound):
			return nil, mismatch(ctx, q, home)
		case err != nil:
			return nil, err
		case o.ID != home:
			return nil, mismatch(ctx, q, home)
		}
	}
	if id.Position == "" {
		return nil, nil
	}
	pos, err := positionNamed(ctx, q, home, id.Position)
	if err != nil {
		return nil, err
	}
	chart, err := orgs.Load(ctx, q, home)
	if err != nil {
		return nil, err
	}
	cur, ok := chart.Member(a.ID)
	if !ok || cur.PositionID == pos.ID {
		return nil, nil
	}
	by, ok := chart.Member(host)
	switch {
	case !ok:
		return nil, httpx.NewError(http.StatusForbidden, dto.ErrorCodeForbidden, "you are not a member of the organization this AI belongs to")
	case by.Permission == orgs.PermissionAdmin:
	case !chart.Manages(host, a.ID):
		return nil, httpx.NewError(http.StatusForbidden, dto.ErrorCodeForbidden, "you do not manage this AI, so you cannot change its position")
	case !chart.Above(by.PositionID, pos.ID):
		return nil, notBelow(starter{iid: host})
	}
	return &positionMove{orgID: home, agentID: a.ID, from: cur.PositionID, to: pos.ID}, nil
}

// mismatch is the refusal of a start that names an organization other than the AI's home.
func mismatch(ctx context.Context, q *db.Queries, home string) error {
	o, err := q.GetOrganization(ctx, home)
	if err != nil {
		return err
	}
	e := httpx.NewError(http.StatusConflict, dto.ErrorCodeOrganizationMismatch,
		fmt.Sprintf("this AI belongs to %s (%s): an AI does not move to another organization", o.Name, o.Slug))
	e.Details = map[string]string{dto.MismatchDetailOrganization: o.Slug, dto.MismatchDetailName: o.Name}
	return e
}

// applyMove writes a move checkStart allowed, in the caller's transaction, and audits it. The actor is the
// token holder, who started the session.
func (r *Registry) applyMove(ctx context.Context, q *db.Queries, host string, m positionMove) error {
	if err := q.UpdateOrgMemberPosition(ctx, db.UpdateOrgMemberPositionParams{
		PositionID: sql.NullString{String: m.to, Valid: m.to != ""}, OrgID: m.orgID, Iid: m.agentID,
	}); err != nil {
		return err
	}
	if r.audit == nil {
		return nil
	}
	return r.audit.Record(ctx, q, audit.Entry{
		ActorID: host, Action: audit.ActionOrgMemberPosition, TargetKind: audit.TargetOrganization, TargetID: m.orgID,
		Detail: map[string]any{"iid": m.agentID, "from": m.from, "to": m.to, "by": "start"},
	})
}

// startExisting applies the start rules to an existing AI inside the caller's transaction, and reports the
// organization whose chart changed ("" when none did).
func (r *Registry) startExisting(ctx context.Context, q *db.Queries, host string, a db.Agent, id FolderID) (string, error) {
	move, err := checkStart(ctx, q, host, a, id)
	if err != nil || move == nil {
		return "", err
	}
	return move.orgID, r.applyMove(ctx, q, host, *move)
}

// restart applies the start rules to an AI that was found before any transaction began. Most starts carry
// neither --org nor a position that differs, so the rules are decided on a read snapshot; only a move opens a
// write transaction, which decides it again under the write lock, so the check and the change cannot
// interleave with another change of the chart.
func (r *Registry) restart(ctx context.Context, host string, a db.Agent, id FolderID) error {
	move, err := checkStart(ctx, r.store.R(), host, a, id)
	if err != nil || move == nil {
		return err
	}
	changed := ""
	err = r.store.WithTx(ctx, func(q *db.Queries) (err error) {
		changed, err = r.startExisting(ctx, q, host, a, id)
		return err
	})
	if err == nil && changed != "" && r.bus != nil {
		r.bus.Publish(events.OrgChanged{OrgID: changed})
	}
	return err
}
