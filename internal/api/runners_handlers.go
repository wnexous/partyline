package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/runners"
)

// runnerRoutes are the runner endpoints (CA-01, CA-02, CA-07): the runner token's two routes,
// and the owner's list, rename and delete. The token kind restriction of the middleware keeps a
// runner token on /api/v1/runner/ and a user credential off it.
func (s *server) runnerRoutes(mux *http.ServeMux) {
	if s.Runners == nil {
		return
	}
	s.handle(mux, "POST /api/v1/runner/register", authed(s.registerRunner))
	if s.RunnerWS != nil {
		mux.Handle("GET /api/v1/runner/ws", s.RunnerWS)
	}
	s.handle(mux, "GET /api/v1/runners", authed(s.listRunners))
	s.handle(mux, "PATCH /api/v1/runners/{id}", authed(s.renameRunner))
	s.handle(mux, "DELETE /api/v1/runners/{id}", authed(s.deleteRunner))
}

// runnerDTO renders a runner as the owner's list shows it.
func runnerDTO(r runners.Runner) dto.Runner {
	out := dto.Runner{
		ID: r.ID, Name: r.Name, Machine: r.Machine, Online: r.Online,
		DockerVersion: r.DockerVersion, DockerError: r.DockerError,
		ContainerCount: r.ContainerCount, CreatedAt: r.CreatedAt,
	}
	if r.LastSeenAt != nil {
		t := r.LastSeenAt.UTC()
		out.LastSeenAt = &t
	}
	return out
}

// registerRunner records the runner of a runner token, idempotently per token (CA-01). 201 the
// first time, 200 when the token already registered one.
func (s *server) registerRunner(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.RegisterRunnerRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	runner, created, err := s.Runners.Register(r.Context(), p, runners.RegisterInput{Name: req.Name, Machine: req.Machine})
	if err != nil {
		return err
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, runnerDTO(runner))
	return nil
}

// listRunners returns the caller's runners with their online state and container count (CA-01).
func (s *server) listRunners(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	list, err := s.Runners.List(r.Context(), p.UserID)
	if err != nil {
		return err
	}
	out := dto.List[dto.Runner]{Items: make([]dto.Runner, 0, len(list))}
	for _, runner := range list {
		out.Items = append(out.Items, runnerDTO(runner))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// renameRunner changes the name the owner's list shows (CA-01).
func (s *server) renameRunner(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.RenameRunnerRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	runner, err := s.Runners.Rename(r.Context(), p.UserID, r.PathValue("id"), req.Name)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, runnerDTO(runner))
	return nil
}

// deleteRunner removes the runner and revokes its token (CA-01); 409 runner_in_use while one of
// its containers exists.
func (s *server) deleteRunner(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	if err := s.Runners.Delete(r.Context(), p.UserID, r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
