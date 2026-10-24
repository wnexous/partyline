package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/authn"
)

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
