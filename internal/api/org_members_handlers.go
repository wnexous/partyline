package api

import (
	"encoding/json"
	"net/http"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/validation"
)

// orgMemberRoutes list an organization's members and change a member's permission and position.
func (s *server) orgMemberRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/v1/orgs/{org}/members", authed(s.listOrgMembers))
	s.handle(mux, "GET /api/v1/orgs/{org}/members/{iid}/move-preview", authed(s.previewOrgMemberMove))
	s.handle(mux, "PATCH /api/v1/orgs/{org}/members/{iid}", authed(s.updateOrgMember))
}

// listOrgMembers lists the members with their permission, position and, for a guest, its inviter.
// Every member sees it, guests included; whoever is not a member does not find the organization
// (OMEM-03, CHART-02).
func (s *server) listOrgMembers(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	org, err := s.resolveOrg(r)
	if err != nil {
		return err
	}
	rows, err := s.Orgs.ListMembers(r.Context(), p.IID(), org.ID)
	if err != nil {
		return err
	}
	out := dto.List[dto.OrgMember]{Items: make([]dto.OrgMember, len(rows))}
	for i, row := range rows {
		out.Items[i] = convert.OrgMember(row)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// updateOrgMember changes a member's permission (admins only, OMEM-04; 409 last_admin when it would leave
// no admin, OMEM-05) and/or position (an admin gives any, a manager moves a subordinate to a position
// strictly below its own, POS-04). Both changes of one request are made together or not at all. It
// answers with the member as the list shows it.
func (s *server) updateOrgMember(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	org, err := s.resolveOrg(r)
	if err != nil {
		return err
	}
	var req dto.UpdateOrgMemberRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	update := orgs.MemberUpdate{}
	if req.Permission != nil {
		perm := orgs.Permission(*req.Permission)
		update.Permission = &perm
	}
	if len(req.PositionID) > 0 {
		var id *string // null clears the position, like ""
		if err := json.Unmarshal(req.PositionID, &id); err != nil {
			return &validation.Error{Fields: map[string]string{"position_id": "must be a position id or null"}}
		}
		position := ""
		if id != nil {
			position = *id
		}
		update.PositionID = &position
	}
	target := r.PathValue("iid")
	if _, err := s.Orgs.UpdateMember(r.Context(), p.IID(), org.ID, target, update); err != nil {
		return err
	}
	row, err := s.Orgs.MemberView(r.Context(), p.IID(), org.ID, target)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, convert.OrgMember(row))
	return nil
}

// previewOrgMemberMove shows who would stop managing a member if it moved to position_id ("" for
// none), before the move happens (POS-04). It applies the same authorization and validation as the
// PATCH route and changes nothing.
func (s *server) previewOrgMemberMove(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	org, err := s.resolveOrg(r)
	if err != nil {
		return err
	}
	ids, err := s.Orgs.MovePreview(r.Context(), p.IID(), org.ID, r.PathValue("iid"), r.URL.Query().Get("position_id"))
	if err != nil {
		return err
	}
	members, err := s.Orgs.ListMembers(r.Context(), p.IID(), org.ID)
	if err != nil {
		return err
	}
	refs := make(map[string]dto.IntelligenceRef, len(members))
	for _, m := range members {
		refs[m.IID] = convert.OrgMember(m).Intelligence
	}
	out := dto.MovePreview{LoseManagement: make([]dto.IntelligenceRef, 0, len(ids))}
	for _, id := range ids {
		if ref, ok := refs[id]; ok {
			out.LoseManagement = append(out.LoseManagement, ref)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// identityDeleteRoutes are the routes that delete an identity in an organization and end an AI's session.
func (s *server) identityDeleteRoutes(mux *http.ServeMux) {
	s.handle(mux, "DELETE /api/v1/orgs/{org}/members/{iid}", authed(s.deleteOrgMember))
	s.handle(mux, "DELETE /api/v1/agents/{id}/session", authed(s.endAgentSession))
}

// deleteOrgMember deletes the identity in the organization (POS-05, POS-06). Whoever is not a member of the
// organization is not found, and neither is a target that is not one (the services answer that). Deleting
// yourself is leaving (OMEM-05: the only admin cannot; an AI cannot leave its home organization). Anyone else is
// deleted by the right the chart gives the caller - an admin deletes any identity, whoever manages a guest
// deletes it - and then by ownership: an AI whose home is the organization is deleted for good, anything else (a
// person, or an AI of another organization) is only removed from the organization (OMEM-06).
func (s *server) deleteOrgMember(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	ctx := r.Context()
	org, err := s.Orgs.Resolve(ctx, r.PathValue("org"))
	if err != nil {
		return err
	}
	actor, target := p.IID(), r.PathValue("iid")
	if err := s.Orgs.Authz().RequireMember(ctx, org.ID, actor); err != nil {
		return err
	}
	if actor == target {
		err = s.Orgs.Leave(ctx, actor, org.ID)
	} else {
		var home string
		if home, err = s.Agents.HomeOrganization(ctx, target); err != nil {
			return err
		}
		if home == org.ID {
			err = s.Agents.Delete(ctx, p, target)
		} else {
			err = s.Orgs.Remove(ctx, actor, org.ID, target)
		}
	}
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// endAgentSession ends the live session of an AI the caller manages (POS-04): the socket closes with 4002 and the
// reason "ended by @handle". An AI that is not live has nothing to end, which is not an error.
func (s *server) endAgentSession(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	if err := s.Agents.EndSession(r.Context(), p, r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
