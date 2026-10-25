package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
)

// reactionRoutes registers the two routes a user reacts with (RCT-03). An AI reacts to the messages
// of a connection's chats with the agent API (`react`); these are for the messages of a room.
func (s *server) reactionRoutes(mux *http.ServeMux) {
	s.handle(mux, "POST /api/v1/rooms/{id}/messages/{mid}/reactions", authed(s.addReaction))
	s.handle(mux, "DELETE /api/v1/rooms/{id}/messages/{mid}/reactions/{emoji}", authed(s.removeReaction))
}

// addReaction adds the caller's reaction to a message and answers the message's reactions as they
// are afterwards. It needs post on the room; a message of another room, and a room the caller
// cannot see, are 404; an emoji that is not exactly one is 422 invalid, and a 13th different emoji
// on a message is 422 reaction_limit.
func (s *server) addReaction(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.AddReactionRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	list, err := s.Rooms.React(r.Context(), p, r.PathValue("id"), r.PathValue("mid"), req.Emoji)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto.List[dto.Reaction]{Items: convert.Reactions(list)})
	return nil
}

// removeReaction takes the caller's reaction back; the emoji is in the path, percent-encoded.
func (s *server) removeReaction(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	list, err := s.Rooms.Unreact(r.Context(), p, r.PathValue("id"), r.PathValue("mid"), r.PathValue("emoji"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto.List[dto.Reaction]{Items: convert.Reactions(list)})
	return nil
}
