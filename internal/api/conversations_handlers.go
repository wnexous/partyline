package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/conversations"
)

func (s *server) conversationRoutes(mux *http.ServeMux) {
	s.handle(mux, "POST /api/v1/conversations/dm", authed(s.openDM))
	s.handle(mux, "POST /api/v1/groups", authed(s.createGroup))
}

// openDM opens the caller's direct message with an intelligence they can see: 201 with the new
// room, 200 with the existing one (DM-01).
func (s *server) openDM(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.OpenDMRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	room, created, err := s.Conversations.OpenDM(r.Context(), p, req.With)
	if err != nil {
		return err
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, convert.Room(room))
	return nil
}

// createGroup creates a group owned by the caller with the chosen users and AIs as members, or
// refuses it with the members that cannot be added and creates nothing (LAY-04).
func (s *server) createGroup(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.CreateGroupRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	room, err := s.Conversations.CreateGroup(r.Context(), p, conversations.GroupInput{Name: req.Name, Description: req.Description, Members: req.Members})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, convert.Room(room))
	return nil
}
