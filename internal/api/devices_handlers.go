package api

import (
	"net/http"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/devices"
)

// deviceRoutes approve new machines from the web app (DEV-01..04): the CLI creates and polls
// a request anonymously; a logged-in user reads, approves or denies it.
func (s *server) deviceRoutes(mux *http.ServeMux) {
	if s.Devices == nil {
		return
	}
	s.handle(mux, "POST /api/v1/devices", s.createDevice)
	s.handle(mux, "POST /api/v1/devices/poll", s.pollDevice)
	s.handle(mux, "GET /api/v1/devices/{code}", authed(s.getDevice))
	s.handle(mux, "POST /api/v1/devices/{code}/approve", authed(s.approveDevice))
	s.handle(mux, "POST /api/v1/devices/{code}/deny", authed(s.denyDevice))
}

// publicURL is the instance URL without a trailing slash.
func (s *server) publicURL() string {
	u := *s.Config.PublicURL
	u.RawQuery, u.Fragment = "", ""
	out := u.String()
	for len(out) > 0 && out[len(out)-1] == '/' {
		out = out[:len(out)-1]
	}
	return out
}

func (s *server) createDevice(w http.ResponseWriter, r *http.Request) error {
	var req dto.CreateDeviceRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	c, err := s.Devices.Create(r.Context(), req.Machine, httpx.ClientIP(r.Context()))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, dto.DeviceRequestCreated{
		Code: c.Code, PollSecret: c.PollSecret, ApproveURL: s.publicURL() + "/devices/" + c.Code, ExpiresAt: c.ExpiresAt.UTC(),
	})
	return nil
}

func (s *server) pollDevice(w http.ResponseWriter, r *http.Request) error {
	var req dto.DevicePollRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	p, err := s.Devices.Poll(r.Context(), req.PollSecret)
	if err != nil {
		return err
	}
	out := dto.DevicePoll{Status: p.Status, Token: p.Token}
	if p.Token != "" {
		out.URL = s.publicURL()
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func deviceDTO(code string, d devices.Request) dto.DeviceRequest {
	return dto.DeviceRequest{
		Code: devices.NormalizeCode(code), Machine: d.Machine, IP: d.CreatedIp, Status: d.Status,
		CreatedAt: time.UnixMilli(d.CreatedAt).UTC(), ExpiresAt: time.UnixMilli(d.ExpiresAt).UTC(),
	}
}

func (s *server) getDevice(w http.ResponseWriter, r *http.Request, _ authn.Principal) error {
	d, err := s.Devices.Get(r.Context(), r.PathValue("code"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, deviceDTO(r.PathValue("code"), d))
	return nil
}

func (s *server) approveDevice(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	d, err := s.Devices.Approve(r.Context(), p, r.PathValue("code"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, deviceDTO(r.PathValue("code"), d))
	return nil
}

func (s *server) denyDevice(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	d, err := s.Devices.Deny(r.Context(), p, r.PathValue("code"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, deviceDTO(r.PathValue("code"), d))
	return nil
}
