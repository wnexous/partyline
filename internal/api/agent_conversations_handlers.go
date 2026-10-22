package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/conversations"
	"github.com/wnexous/partyline/internal/rooms"
)

func (s *server) agentConversationRoutes(mux *http.ServeMux) {
	s.handle(mux, "POST /api/v1/agent/dms", s.agentOnly(s.agentDM))
	s.handle(mux, "POST /api/v1/agent/groups", s.agentOnly(s.agentGroup))
}

// agentDM writes to an intelligence the AI's owner can see, opening the DM first when the pair has
// none (DM-04). The target is the "@handle", handle or id in to; one the owner cannot see, or that
// does not exist, is 404 contact_not_found. Posting counts against the AI's 30 messages a minute
// and a DM it has to create against its 10 new conversations an hour (429 rate_limited).
func (s *server) agentDM(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.AgentDMRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	in := rooms.PostInput{Text: req.Text, Recipients: req.ChatIDs}
	// Refuse what is invalid before the allowance of a post is spent or anything is created.
	if err := conversations.ValidateDM(req.To, in); err != nil {
		return err
	}
	if !s.Agents.AllowPost(p.AgentID) {
		return errAgentRateLimited
	}
	m, err := s.Conversations.SendDM(r.Context(), p, req.To, in)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, convert.Message(m))
	return nil
}

// agentGroup creates a group owned by the AI's owner with the AI and the members it names as
// members, by the add-member rules of the owner, all or nothing (AGRP-01, AGRP-02): a refused
// member makes it 422 members_refused listing them as they were named, and nothing is created.
func (s *server) agentGroup(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.CreateGroupRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	room, err := s.Conversations.CreateGroupNamed(r.Context(), p, conversations.GroupInput{Name: req.Name, Description: req.Description, Members: req.Members})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, dto.CreatedGroup{Room: room.ID, Name: room.Name})
	return nil
}
