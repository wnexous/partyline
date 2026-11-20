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
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store/db"
)

// placement is where a new AI goes and on whose behalf: decided from what the session asked for (--org,
// --parent) before anything is written.
type placement struct {
	// orgID is the AI's home organization.
	orgID string
	// creatorIID is the identity that created it: the token holder, or an AI they host (--parent).
	creatorIID string
	// instructorIID is the AI named by --parent, which becomes an instructor of the new AI (SR2-09); empty
	// without one.
	instructorIID string
}

// starter is the identity a start acts as: the token holder, or an AI they host and named as the parent.
type starter struct {
	iid string
	ai  bool
}

// place decides where a new AI goes, in the transaction that creates it (ORG-04, ORG-05, POS-08, SR2-09):
//
//  1. the creator is the AI named by Parent when the token holder may act for it (it is hosted on their
//     machines), else the token holder; an unknown or foreign parent is ignored without a word;
//  2. the home organization is Org when given (422 organization_required if there is none such), else the
//     creator's default organization;
//  3. the creator must be a member of it (403, ORG-05);
//  4. the AI joins it as a member (SR2-02), and a parent AI becomes its instructor (SR2-09).
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
	if _, ok, err := orgMember(ctx, q, home, by.iid); err != nil {
		return placement{}, err
	} else if !ok {
		return placement{}, notAMember(by)
	}
	p := placement{orgID: home, creatorIID: by.iid}
	if by.ai {
		p.instructorIID = by.iid
	}
	return p, nil
}

// orgMember reports whether iid is a member of the organization.
func orgMember(ctx context.Context, q *db.Queries, orgID, iid string) (db.OrgMember, bool, error) {
	m, err := q.GetOrgMember(ctx, db.GetOrgMemberParams{OrgID: orgID, Iid: iid})
	if errors.Is(err, sql.ErrNoRows) {
		return db.OrgMember{}, false, nil
	}
	return m, err == nil, err
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

// notAMember is the refusal of a start in an organization its creator does not belong to (ORG-05).
func notAMember(by starter) *httpx.Error {
	if by.ai {
		return httpx.NewError(http.StatusForbidden, dto.ErrorCodeForbidden, "the parent AI is not a member of that organization")
	}
	return httpx.NewError(http.StatusForbidden, dto.ErrorCodeForbidden, "you are not a member of that organization")
}

// checkStart applies the start rules to an AI that already exists, against the state q sees (ORG-04): --org
// may only name its home organization. Any other - a different one of the host's, one the host is not in, one
// that does not exist - is 409 organization_mismatch, and the answer names the home organization, because an
// AI does not move. --parent is ignored: an existing AI's creator and instructors do not change at a start
// (SR2-09). An AI without a home organization (a v0.3 identity no upgrade placed) has no rules.
func checkStart(ctx context.Context, q *db.Queries, a db.Agent, id FolderID) error {
	if !a.OrgID.Valid || id.Org == "" {
		return nil
	}
	home := a.OrgID.String
	o, err := orgs.ResolveRef(ctx, q, id.Org)
	switch {
	case errors.Is(err, orgs.ErrNotFound):
		return mismatch(ctx, q, home)
	case err != nil:
		return err
	case o.ID != home:
		return mismatch(ctx, q, home)
	}
	return nil
}

// CheckStart says whether a session that starts in this folder with this placement (--org, --parent) would be
// refused, and why, and changes nothing: the launcher asks before it starts the harness, so that the person
// reads the refusal in the terminal where they typed the command instead of finding a harness that cannot
// connect (ORG-05). It decides with the rules the start itself uses, on a read snapshot:
//
//   - the AI of the folder (or of the nearest folder above it, SI-04), if there is one, and the unlinked identity
//     a session would adopt (MIG-02) are judged by the rules of a later start (checkStart: --org may only name
//     the home organization, --parent is ignored);
//   - a folder that would get a new AI is judged by the rules of its creation (place).
//
// A start that names neither has no rules to check. A folder or machine that cannot be an identity is
// the caller's to refuse (ValidateFolder), and so are the reasons a start can fail that are not its placement,
// such as the agent limit: the session reports those when it really starts.
func (r *Registry) CheckStart(ctx context.Context, ownerID string, id FolderID) error {
	if id.Org == "" && id.Parent == "" {
		return nil
	}
	id.Machine = cleanText(id.Machine, MaxMachineLength)
	q := r.store.R()
	a, err := folderAgent(ctx, q, ownerID, id)
	switch {
	case err == nil:
	case errors.Is(err, sql.ErrNoRows):
		cand, _, found, err := adoptionCandidate(ctx, q, ownerID, id)
		switch {
		case err != nil:
			return err
		case !found:
			_, err = place(ctx, q, ownerID, id)
			return err
		}
		a = cand
	default:
		return err
	}
	return checkStart(ctx, q, a, id)
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

// restart applies the start rules to an AI that was found before any transaction began, on a read snapshot:
// a later start changes nothing, so it only refuses.
func (r *Registry) restart(ctx context.Context, a db.Agent, id FolderID) error {
	return checkStart(ctx, r.store.R(), a, id)
}
