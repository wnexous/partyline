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
	s.handle(mux, "PATCH /api/v1/agents/{id}", authed(s.updateAgent))
	s.handle(mux, "DELETE /api/v1/agents/{id}", authed(s.deleteAgent))
	s.handle(mux, "POST /api/v1/agents/{id}/merge", authed(s.mergeAgent))
	s.handle(mux, "GET /api/v1/agents/{id}/sharing", authed(s.getAgentSharing))
	s.handle(mux, "PUT /api/v1/agents/{id}/sharing", authed(s.putAgentSharing))
	s.handle(mux, "GET /api/v1/projects", authed(s.listProjects))
	s.handle(mux, "PATCH /api/v1/projects/{id}", authed(s.updateProject))
	s.handle(mux, "GET /api/v1/projects/{id}/sharing", authed(s.getProjectSharing))
	s.handle(mux, "PUT /api/v1/projects/{id}/sharing", authed(s.putProjectSharing))
}

func toAgent(a agents.Agent) dto.Agent {
	out := dto.Agent{
		ID: a.ID, Name: agents.GoesBy(a.Handle, a.Name), DisplayName: agents.NameOf(a.ProfileName, a.Agent), Description: a.Description,
		Harness: a.Harness, Machine: a.Machine, Kind: dto.AgentKindFixed, Archived: false,
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

func toProject(p agents.Project) dto.Project {
	return dto.Project{
		ID: p.ID, Slug: p.Slug, Name: p.Name,
		Owner:   dto.User{ID: p.OwnerID, Username: p.OwnerUsername, DisplayName: p.OwnerDisplayName},
		MyLevel: p.Level, CreatedAt: convert.Time(p.CreatedAt),
	}
}

// listProjects lists the projects the user owns or may use.
func (s *server) listProjects(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	list, err := s.Agents.Projects(r.Context(), p)
	if err != nil {
		return err
	}
	out := dto.List[dto.Project]{Items: make([]dto.Project, len(list))}
	for i, pr := range list {
		out.Items[i] = toProject(pr)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// updateProject renames the project (PROJ-05); only the owner may.
func (s *server) updateProject(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.UpdateProjectRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	pr, err := s.Agents.RenameProject(r.Context(), p, r.PathValue("id"), req.Name)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toProject(pr))
	return nil
}

func (s *server) getProjectSharing(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	v, err := s.Agents.ProjectSharing(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toSharing(v))
	return nil
}

// putProjectSharing replaces the project's use grants with a visibility preset.
func (s *server) putProjectSharing(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.Sharing
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	v, err := s.Agents.SetProjectSharing(r.Context(), p, r.PathValue("id"), visibilityFrom(req))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toSharing(v))
	return nil
}
