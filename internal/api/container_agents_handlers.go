package api

import (
	"net/http"
	"strconv"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/containeragents"
)

// containerAgentRoutes serve the container AIs (CA-04, CA-06, CA-07): creating one on a runner of
// the caller, and reading and operating the container of an AI the caller manages. A user
// credential only: an AI's own token cannot create or operate containers.
func (s *server) containerAgentRoutes(mux *http.ServeMux) {
	if s.Containers == nil {
		return
	}
	s.handle(mux, "POST /api/v1/container-agents", userOnly(s.createContainerAgent))
	s.handle(mux, "GET /api/v1/agents/{id}/container", userOnly(s.getContainerAgent))
	s.handle(mux, "POST /api/v1/agents/{id}/container", userOnly(s.operateContainerAgent))
	s.handle(mux, "GET /api/v1/agents/{id}/container/logs", userOnly(s.containerAgentLogs))
	s.handle(mux, "DELETE /api/v1/agents/{id}/container", userOnly(s.deleteContainerAgent))
}

// containerDTO renders a container as the API shows it: the AI is a reference, the environment a
// list of variables and secret names, and never a value (CA-08).
func containerDTO(c containeragents.Container) dto.ContainerAgent {
	out := dto.ContainerAgent{
		Agent: dto.IntelligenceRef{
			ID: c.AgentID, Kind: dto.IntelligenceKindAI, Role: dto.IntelligenceRoleAI,
			DisplayName: c.DisplayName, Handle: c.Handle,
		},
		RunnerID: c.RunnerID, Harness: c.Harness, Image: c.Image, RepoURL: c.RepoURL, RepoRef: c.RepoRef,
		Volume: c.Volume, ContainerName: c.ContainerName, CPUs: c.CPUs, MemoryMB: c.MemoryMB,
		Desired: c.Desired, State: c.State, LastError: c.LastError,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
	out.Env = make([]dto.ContainerEnvVar, len(c.Env))
	for i, e := range c.Env {
		out.Env[i] = dto.ContainerEnvVar{Var: e.Var, SecretName: e.SecretName}
	}
	return out
}

// createContainerAgent creates the AI of a container on one of the caller's runners and starts it
// (CA-04): 201 with the AI and its container state; 409 runner_offline; 404 for another owner's
// runner; 422 for an invalid request.
func (s *server) createContainerAgent(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.CreateContainerAgentRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	env := make([]containeragents.EnvVar, len(req.Env))
	for i, e := range req.Env {
		env[i] = containeragents.EnvVar{Var: e.Var, SecretName: e.SecretName}
	}
	c, err := s.Containers.Create(r.Context(), p, containeragents.CreateInput{
		RunnerID: req.RunnerID, Harness: req.Harness, DisplayName: req.DisplayName, Handle: req.Handle,
		RepoURL: req.RepoURL, RepoRef: req.RepoRef, Env: env, CPUs: req.CPUs, MemoryMB: req.MemoryMB,
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, containerDTO(c))
	return nil
}

// getContainerAgent returns the container of an AI the caller manages, as the runner last reported
// it (CA-06, CA-07).
func (s *server) getContainerAgent(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	c, err := s.Containers.State(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, containerDTO(c))
	return nil
}

// operateContainerAgent starts, stops or restarts the container of an AI the caller manages (CA-06).
func (s *server) operateContainerAgent(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.ContainerActionRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	id := r.PathValue("id")
	var (
		c   containeragents.Container
		err error
	)
	switch req.Action {
	case containeragents.RequestStart:
		c, err = s.Containers.Start(r.Context(), p, id)
	case containeragents.RequestStop:
		c, err = s.Containers.Stop(r.Context(), p, id)
	case containeragents.RequestRestart:
		c, err = s.Containers.Restart(r.Context(), p, id)
	default:
		return httpx.Invalid(map[string]string{"action": "must be start, stop or restart"})
	}
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, containerDTO(c))
	return nil
}

// containerAgentLogs returns the last log lines of the container of an AI the caller manages
// (CA-06): tail defaults to 200 and is clamped to 500 by the service.
func (s *server) containerAgentLogs(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	tail := 0
	if raw := r.URL.Query().Get("tail"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return httpx.Invalid(map[string]string{"tail": "must be a number of lines"})
		}
		tail = n
	}
	logs, err := s.Containers.Logs(r.Context(), p, r.PathValue("id"), tail)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto.ContainerLogs{Logs: logs})
	return nil
}

// deleteContainerAgent removes the container of an AI the caller manages, and with volume=true its
// workspace volume too, revokes the AI's token and deletes the container row (CA-06); it answers
// 204. A runner that is offline keeps the row, so the container is never left behind silently.
func (s *server) deleteContainerAgent(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	volume := false
	if raw := r.URL.Query().Get("volume"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return httpx.Invalid(map[string]string{"volume": "must be true or false"})
		}
		volume = v
	}
	if err := s.Containers.Delete(r.Context(), p, r.PathValue("id"), volume); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
