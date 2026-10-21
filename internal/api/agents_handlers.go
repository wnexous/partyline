package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
)

// agentRoutes are the management endpoints of agents for users (sessions or user tokens).
func (s *server) agentRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/v1/agents", authed(s.listAgents))
	s.handle(mux, "PATCH /api/v1/agents/{id}", authed(s.updateAgent))
	s.handle(mux, "DELETE /api/v1/agents/{id}", authed(s.deleteAgent))
	s.handle(mux, "GET /api/v1/agents/{id}/sharing", authed(s.getAgentSharing))
	s.handle(mux, "PUT /api/v1/agents/{id}/sharing", authed(s.putAgentSharing))
}

func toAgent(a agents.Agent) dto.Agent {
	out := dto.Agent{
		ID: a.ID, Name: a.Name, DisplayName: agents.DisplayName(a.Agent), Description: a.Description,
		Harness: a.Harness, Machine: a.Machine, Kind: dto.AgentKind(a.Kind), Archived: a.ArchivedAt.Valid,
		Owner:   dto.User{ID: a.OwnerID, Username: a.OwnerUsername, DisplayName: a.OwnerDisplayName},
		MyLevel: a.Level, Online: a.Online, LastSeenAt: convert.OptTime(a.LastSeenAt), CreatedAt: convert.Time(a.CreatedAt),
	}
	if a.ProjectID.Valid {
		out.Project = &dto.ProjectRef{ID: a.ProjectID.String, Slug: a.ProjectSlug, Name: a.ProjectName}
	}
	return out
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

func (s *server) updateAgent(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.UpdateAgentRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	id := r.PathValue("id")
	var a agents.Agent
	var err error
	if req.Description != nil {
		a, err = s.Agents.UpdateDescription(r.Context(), p, id, *req.Description)
	} else {
		a, err = s.Agents.Get(r.Context(), p, id)
		if err == nil && a.Level != dto.LevelManage {
			err = access.ErrForbidden
		}
	}
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
