package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/connectors"
)

// chatRoutes list connector chats, refresh them and route a chat to an agent (CON-09, UI-13).
func (s *server) chatRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/v1/connectors/{id}/chats", authed(s.listConnectorChats))
	s.handle(mux, "POST /api/v1/connectors/{id}/chats/refresh", authed(s.refreshChats))
	s.handle(mux, "GET /api/v1/chats", authed(s.listUsableChats))
	s.handle(mux, "POST /api/v1/chats/{id}/route", authed(s.routeChat))
}

func toChats(list []connectors.ChatView) dto.List[dto.Chat] {
	out := dto.List[dto.Chat]{Items: make([]dto.Chat, len(list))}
	for i, c := range list {
		out.Items[i] = convert.Chat(c.Chat, c.Connector)
	}
	return out
}

func (s *server) listConnectorChats(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	list, err := s.Connectors.Chats(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toChats(list))
	return nil
}

func (s *server) refreshChats(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	if err := s.Connectors.RefreshChats(r.Context(), p, r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *server) listUsableChats(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	list, err := s.Connectors.UsableChats(r.Context(), p)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toChats(list))
	return nil
}

func (s *server) routeChat(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.RouteChatRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	room, err := s.Connectors.RouteChat(r.Context(), p, r.PathValue("id"), req.AgentID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, convert.Room(room))
	return nil
}
