package api

import (
	"context"
	"net/http"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/devices"
)

// deviceRoutes approve new machines from the web app (DEV-01..06): the CLI creates and polls
// a request anonymously; a logged-in user types its code to read, approve or deny it. The code
// is always in the body, never in a URL.
func (s *server) deviceRoutes(mux *http.ServeMux) {
	if s.Devices == nil {
		return
	}
	s.handle(mux, "POST /api/v1/devices", s.createDevice)
	s.handle(mux, "POST /api/v1/devices/poll", s.pollDevice)
	s.handle(mux, "POST /api/v1/devices/lookup", authed(s.lookupDevice))
	s.handle(mux, "POST /api/v1/devices/approve", authed(s.approveDevice))
	s.handle(mux, "POST /api/v1/devices/deny", authed(s.denyDevice))
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
		Code: c.Code, PollSecret: c.PollSecret, ApproveURL: s.publicURL() + "/devices", ExpiresAt: c.ExpiresAt.UTC(),
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
	out := dto.DevicePoll{Status: p.Status, Token: p.Token, ApprovedBy: p.ApprovedBy}
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

// withCode reads the typed code from the body, runs fn (Lookup, Approve or Deny) and answers
// with the request.
func withCode(w http.ResponseWriter, r *http.Request, p authn.Principal, fn func(context.Context, authn.Principal, string) (devices.Request, error)) error {
	var req dto.DeviceCode
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	d, err := fn(r.Context(), p, req.Code)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, deviceDTO(req.Code, d))
	return nil
}

func (s *server) lookupDevice(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	return withCode(w, r, p, s.Devices.Lookup)
}

func (s *server) approveDevice(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	return withCode(w, r, p, s.Devices.Approve)
}

func (s *server) denyDevice(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	return withCode(w, r, p, s.Devices.Deny)
}
