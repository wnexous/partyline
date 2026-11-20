package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
)

// instructorRoutes list, add and remove who instructs an AI besides its host (SR2-10).
func (s *server) instructorRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/v1/agents/{id}/instructors", authed(s.listInstructors))
	s.handle(mux, "POST /api/v1/agents/{id}/instructors", authed(s.addInstructor))
	s.handle(mux, "DELETE /api/v1/agents/{id}/instructors/{iid}", authed(s.removeInstructor))
}

// seenAI is nil when the caller can see the AI, and access.ErrNotFound when it cannot: a caller learns nothing
// of an AI it does not see.
func (s *server) seenAI(r *http.Request, p authn.Principal, agentID string) error {
	ok, err := s.Intelligences.CanSee(r.Context(), p, agentID)
	if err != nil {
		return err
	}
	if !ok {
		return access.ErrNotFound
	}
	return nil
}

// listInstructors answers the host and the instructors of an AI (SR2-10): its host and the AI itself read them,
// anyone else who sees the AI gets 403, and whoever does not see it 404.
func (s *server) listInstructors(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	id := r.PathValue("id")
	if err := s.seenAI(r, p, id); err != nil {
		return err
	}
	host, list, err := s.Orgs.Instructors(r.Context(), p.IID(), id)
	if err != nil {
		return err
	}
	out := dto.AgentInstructors{
		Host:        convert.IntelligenceRef(host, dto.IntelligenceRoleUser, ""),
		Instructors: make([]dto.AgentInstructor, len(list)),
	}
	for i, in := range list {
		out.Instructors[i] = convert.AgentInstructor(in)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// addInstructor makes the identity named by @handle an instructor of the AI (SR2-10): 201 with the instructor.
// Only the host does; the AI itself and anyone else who sees the AI get 403. An unknown handle, a person or an
// AI the host cannot see is 404 contact_not_found; the AI itself or its host is 422 on handle.
func (s *server) addInstructor(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	id := r.PathValue("id")
	if err := s.seenAI(r, p, id); err != nil {
		return err
	}
	var req dto.AddInstructorRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	in, err := s.Orgs.AddInstructor(r.Context(), p.IID(), id, req.Handle)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, convert.AgentInstructor(in))
	return nil
}

// removeInstructor takes an instructor off the AI (SR2-10): 204, also when it was not one. Only the host does.
func (s *server) removeInstructor(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	id := r.PathValue("id")
	if err := s.seenAI(r, p, id); err != nil {
		return err
	}
	if err := s.Orgs.RemoveInstructor(r.Context(), p.IID(), id, r.PathValue("iid")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
