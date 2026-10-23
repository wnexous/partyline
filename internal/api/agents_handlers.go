package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
)

// agentRoutes are the management endpoints of agents for users (sessions or user tokens).
func (s *server) agentRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/v1/agents", authed(s.listAgents))
	s.handle(mux, "GET /api/v1/agents/{id}", authed(s.getAgent))
	s.handle(mux, "PATCH /api/v1/agents/{id}", authed(s.updateAgent))
	s.handle(mux, "DELETE /api/v1/agents/{id}", authed(s.deleteAgent))
	s.handle(mux, "POST /api/v1/agents/{id}/merge", authed(s.mergeAgent))
	s.handle(mux, "GET /api/v1/agents/{id}/sharing", authed(s.getAgentSharing))
	s.handle(mux, "PUT /api/v1/agents/{id}/sharing", authed(s.putAgentSharing))
}

func toAgent(a agents.Agent) dto.Agent {
	return dto.Agent{
		ID: a.ID,
		Intelligence: dto.IntelligenceRef{
			ID: a.ID, Kind: dto.IntelligenceKindAI, Role: dto.IntelligenceRoleAI,
			DisplayName: a.ProfileName, Handle: a.Handle, AvatarURL: convert.AvatarURL(a.ID, a.ProfileAvatar),
		},
		Description: a.Description, Harness: a.Harness, Machine: a.Machine,
		Owner:   dto.User{ID: a.OwnerID, Username: a.OwnerUsername, DisplayName: a.OwnerDisplayName},
		MyLevel: a.Level, Online: a.Online, LastSeenAt: convert.OptTime(a.LastSeenAt), CreatedAt: convert.Time(a.CreatedAt),
	}
}

func (s *server) listAgents(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	list, err := s.Agents.ListVisible(r.Context(), p)
	if err != nil {
		return err
	}
	out := dto.List[dto.Agent]{Items: make([]dto.Agent, len(list))}
	for i, a := range list {
		out.Items[i] = toAgent(a)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// getAgent renders one AI the caller hosts or shares an organization with; others get 404.
func (s *server) getAgent(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	a, err := s.Agents.Get(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toAgent(a))
	return nil
}

func (s *server) updateAgent(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.UpdateAgentRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	a, err := s.Agents.Update(r.Context(), p, r.PathValue("id"), agents.AgentUpdate{
		Description: req.Description, DisplayName: req.DisplayName, Fixed: req.Fixed,
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toAgent(a))
	return nil
}

// deleteAgent removes the agent, its memberships, grants and cursors; only the owner may.
func (s *server) deleteAgent(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	if err := s.Agents.Delete(r.Context(), p, r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// mergeAgent merges the AI in the path into another AI of the caller: its groups, DMs, history,
// cursors, grants and connectors move to it and it is deleted. The answer is the AI that remains
// (MRG-01).
func (s *server) mergeAgent(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.MergeAgentRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if err := s.Conversations.Merge(r.Context(), p, r.PathValue("id"), req.Into); err != nil {
		return err
	}
	a, err := s.Agents.Get(r.Context(), p, req.Into)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toAgent(a))
	return nil
}

func (s *server) getAgentSharing(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	v, err := s.Agents.Sharing(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toSharing(v))
	return nil
}

// putAgentSharing replaces the agent's use grants with a visibility preset (ACL-07).
func (s *server) putAgentSharing(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.Sharing
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	v, err := s.Agents.SetSharing(r.Context(), p, r.PathValue("id"), visibilityFrom(req))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toSharing(v))
	return nil
}
