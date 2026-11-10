package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/schedules"
)

// scheduleRoutes are the routes of the AIs' scheduled heartbeats (HB-01..HB-12): whoever manages an AI on
// /agents/{id}/schedules, the AI itself on /agent/schedules. The schedules service decides who may do what.
func (s *server) scheduleRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/v1/agents/{id}/schedules", authed(s.listSchedules))
	s.handle(mux, "POST /api/v1/agents/{id}/schedules", authed(s.createSchedule))
	s.handle(mux, "GET /api/v1/agents/{id}/schedules/{sid}", authed(s.getSchedule))
	s.handle(mux, "PATCH /api/v1/agents/{id}/schedules/{sid}", authed(s.updateSchedule))
	s.handle(mux, "DELETE /api/v1/agents/{id}/schedules/{sid}", authed(s.deleteSchedule))
	s.handle(mux, "POST /api/v1/agents/{id}/schedules/{sid}/run", authed(s.runSchedule))
	s.handle(mux, "GET /api/v1/agents/{id}/schedules/{sid}/runs", authed(s.scheduleRuns))

	s.handle(mux, "GET /api/v1/agent/schedules", s.agentOnly(s.listSchedules))
	s.handle(mux, "POST /api/v1/agent/schedules", s.agentOnly(s.createSchedule))
	s.handle(mux, "PATCH /api/v1/agent/schedules/{sid}", s.agentOnly(s.updateSchedule))
	s.handle(mux, "DELETE /api/v1/agent/schedules/{sid}", s.agentOnly(s.deleteSchedule))
}

// scheduleAgent is the AI a schedule route is about: the one in the path for a user, the calling AI itself on
// the agent routes.
func scheduleAgent(r *http.Request, p authn.Principal) string {
	if p.Kind == authn.KindAgent {
		return p.AgentID
	}
	return r.PathValue("id")
}

// listSchedules lists the AI's schedules: in full to the AI and whoever manages it, only their number to the
// others who see it (HB-12).
func (s *server) listSchedules(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	out, err := s.Schedules.List(r.Context(), p, scheduleAgent(r, p))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (s *server) getSchedule(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	out, err := s.Schedules.Get(r.Context(), p, scheduleAgent(r, p), r.PathValue("sid"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// createSchedule adds a schedule (HB-01): too frequent or too many is 422, an AI over its writes an hour 429.
func (s *server) createSchedule(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.ScheduleRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	out, err := s.Schedules.Create(r.Context(), p, scheduleAgent(r, p), schedules.InputFrom(req))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
	return nil
}

// updateSchedule changes the fields given (HB-01); an AI may change only its own schedules (HB-02, 403).
func (s *server) updateSchedule(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.ScheduleRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	out, err := s.Schedules.Update(r.Context(), p, scheduleAgent(r, p), r.PathValue("sid"), schedules.InputFrom(req))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (s *server) deleteSchedule(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	if err := s.Schedules.Delete(r.Context(), p, scheduleAgent(r, p), r.PathValue("sid")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// runSchedule fires a run of the schedule now, for whoever manages the AI.
func (s *server) runSchedule(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	out, err := s.Schedules.RunNow(r.Context(), p, r.PathValue("id"), r.PathValue("sid"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusAccepted, out)
	return nil
}

// scheduleRuns lists the newest runs of a schedule (HB-11).
func (s *server) scheduleRuns(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	runs, err := s.Schedules.Runs(r.Context(), p, r.PathValue("id"), r.PathValue("sid"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto.List[dto.ScheduleRun]{Items: runs})
	return nil
}
