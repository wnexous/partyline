package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
)

func (s *server) conversationRoutes(mux *http.ServeMux) {
	s.handle(mux, "POST /api/v1/conversations/dm", authed(s.openDM))
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
