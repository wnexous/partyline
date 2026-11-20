package api

import (
	"errors"
	"net/http"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/orgs"
)

// orgMemberRoutes list an organization's members and change a member's permission.
func (s *server) orgMemberRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/v1/orgs/{org}/members", authed(s.listOrgMembers))
	s.handle(mux, "PATCH /api/v1/orgs/{org}/members/{iid}", authed(s.updateOrgMember))
}

// listOrgMembers lists the members with their permission and what the caller may do to each. Every member
// sees it; whoever is not a member does not find the organization (OMEM-03).
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
// no admin, OMEM-05). It answers with the member as the list shows it.
func (s *server) updateOrgMember(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	org, err := s.resolveOrg(r)
	if err != nil {
		return err
	}
	var req dto.UpdateOrgMemberRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	target := r.PathValue("iid")
	if req.Permission != nil {
		if _, err := s.Orgs.SetPermission(r.Context(), p.IID(), org.ID, target, orgs.Permission(*req.Permission)); err != nil {
			return err
		}
	}
	row, err := s.Orgs.MemberView(r.Context(), p.IID(), org.ID, target)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, convert.OrgMember(row))
	return nil
}

// identityDeleteRoutes are the routes that delete an identity in an organization and end an AI's session.
func (s *server) identityDeleteRoutes(mux *http.ServeMux) {
	s.handle(mux, "DELETE /api/v1/orgs/{org}/members/{iid}", authed(s.deleteOrgMember))
	s.handle(mux, "DELETE /api/v1/agents/{id}/session", authed(s.endAgentSession))
	s.handle(mux, "POST /api/v1/agents/{id}/principal", authed(s.makePrincipal))
}

// deleteOrgMember deletes the identity in the organization (SR2-12, POS-06). Whoever is not a member of the
// organization is not found, and neither is a target that is not one (the services answer that). Deleting
// yourself is leaving (OMEM-05: the only admin cannot; an AI cannot leave its home organization). An AI whose
// home is the organization is deleted for good by its host or an admin; anything else (a person, or an AI of
// another organization) is only removed from the organization, by an admin (OMEM-06).
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

// endAgentSession ends the live session of an AI the caller manages (SR2-13): the socket closes with 4002 and the
// reason "ended by @handle". An AI that is not live has nothing to end, which is not an error.
func (s *server) endAgentSession(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	if err := s.Agents.EndSession(r.Context(), p, r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// makePrincipal makes one of the live sessions of an AI the caller manages its principal session (SESS-59): the
// harness session id is pinned and the sessions concerned connect again to take their new roles. A session that
// is not live, or has no harness session id to pin, is a 409.
func (s *server) makePrincipal(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.MakePrincipalRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	switch err := s.Agents.MakePrincipal(r.Context(), p, r.PathValue("id"), req.Label); {
	case errors.Is(err, agents.ErrSessionNotLive):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeConflict, "that session is not live")
	case errors.Is(err, agents.ErrSessionUnidentified):
		return httpx.NewError(http.StatusConflict, dto.ErrorCodeConflict, "that session has no harness session id, so it cannot be pinned as the principal")
	case err != nil:
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
