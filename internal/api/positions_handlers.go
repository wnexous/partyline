package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/orgs"
)

// positionRoutes edit an organization's org chart; every route is admin-only (OMEM-04).
func (s *server) positionRoutes(mux *http.ServeMux) {
	s.handle(mux, "POST /api/v1/orgs/{org}/positions", authed(s.createPosition))
	s.handle(mux, "PATCH /api/v1/orgs/{org}/positions/{id}", authed(s.updatePosition))
	s.handle(mux, "DELETE /api/v1/orgs/{org}/positions/{id}", authed(s.deletePosition))
}

// createPosition adds a position to the chart under an optional parent of the same organization
// (POS-01). A name outside 1-60 characters or a parent of another organization is 422, a duplicate
// name 409 conflict.
func (s *server) createPosition(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	org, err := s.resolveOrg(r)
	if err != nil {
		return err
	}
	var req dto.CreatePositionRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	pos, err := s.Orgs.CreatePosition(r.Context(), p.IID(), org.ID, req.Name, req.ParentID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, convert.Position(pos))
	return nil
}

// updatePosition renames a position or moves it under another; a move that would close a loop is
// 409 position_cycle (POS-02).
func (s *server) updatePosition(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	org, err := s.resolveOrg(r)
	if err != nil {
		return err
	}
	var req dto.UpdatePositionRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	pos, err := s.Orgs.UpdatePosition(r.Context(), p.IID(), org.ID, r.PathValue("id"), orgs.PositionUpdate{
		Name: req.Name, ParentID: req.ParentID,
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, convert.Position(pos))
	return nil
}

// deletePosition removes a position; its holders and children move to its parent (POS-09).
func (s *server) deletePosition(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	org, err := s.resolveOrg(r)
	if err != nil {
		return err
	}
	if err := s.Orgs.DeletePosition(r.Context(), p.IID(), org.ID, r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
