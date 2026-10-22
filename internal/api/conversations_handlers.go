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
	s.handle(mux, "GET /api/v1/conversations", authed(s.listConversations))
	s.handle(mux, "PUT /api/v1/rooms/{id}/read", authed(s.markRead))
}

// listConversations is the sidebar: the caller's groups and DMs, newest activity first, each with
// its last message and unread count (DM-06, LAY-01).
func (s *server) listConversations(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	list, err := s.Conversations.List(r.Context(), p)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto.List[dto.Conversation]{Items: list})
	return nil
}

// markRead moves the caller's read position in a room they can view forward (DM-07).
func (s *server) markRead(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.MarkReadRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	state, err := s.Conversations.MarkRead(r.Context(), p, r.PathValue("id"), req.Seq)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, state)
	return nil
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
