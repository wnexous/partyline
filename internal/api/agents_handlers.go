package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/intelligences"
)

// agentRoutes are the management endpoints of agents for users (sessions or user tokens).
func (s *server) agentRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/v1/agents", authed(s.listAgents))
	s.handle(mux, "GET /api/v1/agents/{id}", authed(s.getAgent))
	s.handle(mux, "PATCH /api/v1/agents/{id}", authed(s.updateAgent))
	s.handle(mux, "DELETE /api/v1/agents/{id}", authed(s.deleteAgent))
	s.handle(mux, "POST /api/v1/agents/{id}/merge", authed(s.mergeAgent))
	s.handle(mux, "GET /api/v1/agents/{id}/sessions/{label}/notifications", authed(s.sessionNotifications))
	s.handle(mux, "PUT /api/v1/agents/{id}/sessions/{label}/notifications/{room}", authed(s.setSessionNotification))
}

func toAgent(a agents.Agent) dto.Agent {
	out := dto.Agent{
		ID: a.ID,
		Intelligence: dto.IntelligenceRef{
			ID: a.ID, Kind: dto.IntelligenceKindAI, Role: dto.IntelligenceRoleAI,
			DisplayName: a.ProfileName, Handle: a.Handle, AvatarURL: convert.AvatarURL(a.ID, a.ProfileAvatar),
		},
		Description: a.Description, Harness: a.Harness, Machine: a.Machine,
		Folder: a.Folder, FolderLabel: intelligences.FolderLabel(a.Home, a.Folder),
		Owner:   dto.User{ID: a.OwnerID, Username: a.OwnerUsername, DisplayName: a.OwnerDisplayName},
		MyLevel: a.Level, Online: a.Online, LastSeenAt: convert.OptTime(a.LastSeenAt), CreatedAt: convert.Time(a.CreatedAt),
	}
	if a.OrgID.Valid {
		out.Organization = &dto.ContactOrg{ID: a.OrgID.String, Name: a.OrgName, Slug: a.OrgSlug}
	}
	return out
}

// agentWithSessions is toAgent with the AI's live sessions, the principal first; none while it is
// offline.
func (s *server) agentWithSessions(a agents.Agent) dto.Agent {
	out := toAgent(a)
	if lives := s.Agents.Sessions(a.ID); len(lives) > 0 {
		out.Sessions = liveSessions(lives)
		out.PrincipalOfflineSince = s.principalOfflineSince(a.ID)
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
		out.Items[i] = s.agentWithSessions(a)
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
	httpx.WriteJSON(w, http.StatusOK, s.agentWithSessions(a))
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
	httpx.WriteJSON(w, http.StatusOK, s.agentWithSessions(a))
	return nil
}

// deleteAgent deletes the AI for good in its home organization, with its memberships and cursors;
// whoever the organization lets delete it may (POS-05): an admin of it, or whoever manages a guest AI.
func (s *server) deleteAgent(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	if err := s.Agents.Delete(r.Context(), p, r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// mergeAgent merges the AI in the path into another AI of the caller: its groups, DMs, history,
// cursors and connectors move to it and it is deleted. The answer is the AI that remains
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
	httpx.WriteJSON(w, http.StatusOK, s.agentWithSessions(a))
	return nil
}
