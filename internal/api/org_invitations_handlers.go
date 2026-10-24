package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/orgs"
)

// orgInvitationRoutes send, list, revoke and answer invitations to organizations (OMEM-01, OMEM-02).
func (s *server) orgInvitationRoutes(mux *http.ServeMux) {
	s.handle(mux, "POST /api/v1/orgs/{org}/invitations", authed(s.createInvitation))
	s.handle(mux, "GET /api/v1/orgs/{org}/invitations", authed(s.listOrgInvitations))
	s.handle(mux, "DELETE /api/v1/orgs/{org}/invitations/{id}", authed(s.revokeInvitation))
	s.handle(mux, "GET /api/v1/invitations", authed(s.listMyInvitations))
	s.handle(mux, "POST /api/v1/invitations/{id}/accept", authed(s.acceptInvitation))
	s.handle(mux, "POST /api/v1/invitations/{id}/decline", authed(s.declineInvitation))
}

// createInvitation invites a user or an AI, by @handle, to the organization: 201 with the pending invitation,
// which sending again updates in place. An admin gives any permission and position; every other member or
// guest invites guests with no position only (403 otherwise, OMEM-04). An identity that already is a member
// is 409 already_member, an unknown handle 404 contact_not_found, and more than 30 invitations an hour from
// the organization 429 rate_limited.
func (s *server) createInvitation(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	org, err := s.resolveOrg(r)
	if err != nil {
		return err
	}
	var req dto.CreateInvitationRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	inv, err := s.Orgs.Invite(r.Context(), p.IID(), org.ID, req.Handle, orgs.Permission(req.Permission), req.PositionID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, convert.Invitation(inv))
	return nil
}

// listOrgInvitations lists the organization's pending invitations, newest first; only an admin sees them.
func (s *server) listOrgInvitations(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	org, err := s.resolveOrg(r)
	if err != nil {
		return err
	}
	list, err := s.Orgs.ListInvitations(r.Context(), p.IID(), org.ID)
	if err != nil {
		return err
	}
	writeInvitations(w, list)
	return nil
}

// revokeInvitation withdraws a pending invitation of the organization; only an admin does.
func (s *server) revokeInvitation(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	org, err := s.resolveOrg(r)
	if err != nil {
		return err
	}
	if err := s.Orgs.Revoke(r.Context(), p.IID(), org.ID, r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// listMyInvitations lists the pending invitations the caller answers: its own, and those of the AIs it manages
// in their home organization, newest first.
func (s *server) listMyInvitations(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	list, err := s.Orgs.MyInvitations(r.Context(), p.IID())
	if err != nil {
		return err
	}
	writeInvitations(w, list)
	return nil
}

// acceptInvitation accepts an invitation the caller answers: the invitee becomes a member with the permission
// and position it carries (OMEM-02), and the answer is that membership. Anyone else is 404, an expired
// invitation 410 gone (OMEM-07), and an invitee that already is a member 409 already_member.
func (s *server) acceptInvitation(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	m, err := s.Orgs.Accept(r.Context(), p.IID(), r.PathValue("id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, convert.Organization(m))
	return nil
}

// declineInvitation declines an invitation the caller answers; it is deleted.
func (s *server) declineInvitation(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	if err := s.Orgs.Decline(r.Context(), p.IID(), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func writeInvitations(w http.ResponseWriter, list []orgs.Invitation) {
	out := dto.List[dto.Invitation]{Items: make([]dto.Invitation, len(list))}
	for i, inv := range list {
		out.Items[i] = convert.Invitation(inv)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
