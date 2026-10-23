package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/connectors"
)

// chatRoutes list the chats of a connection and refresh them (CON-09). Routing a chat to an agent
// and the list of every usable chat are retired (CID-22): a chat reaches the AI that operates its
// connection, and no room takes it as a member.
func (s *server) chatRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/v1/connectors/{id}/chats", authed(s.listConnectorChats))
	s.handle(mux, "POST /api/v1/connectors/{id}/chats/refresh", authed(s.refreshChats))
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
