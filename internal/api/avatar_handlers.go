package api

import (
	"net/http"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
)

// avatarRoutes upload, remove and serve avatars (PROF-06, PROF-07), and let an AI edit its
// own profile with its agent token (PROF-04).
func (s *server) avatarRoutes(mux *http.ServeMux) {
	s.handle(mux, "PUT /api/v1/intelligences/{id}/avatar", authed(s.putAvatar))
	s.handle(mux, "DELETE /api/v1/intelligences/{id}/avatar", authed(s.deleteAvatar))
	s.handle(mux, "GET /api/v1/intelligences/{id}/avatar", authed(s.getAvatar))
	s.handle(mux, "PATCH /api/v1/agent/profile", s.agentOnly(s.agentUpdateProfile))
	s.handle(mux, "PUT /api/v1/agent/profile/avatar", s.agentOnly(s.agentPutAvatar))
}

// putAvatar replaces the avatar with the image in the request body (PNG, JPEG or WebP up to
// 2 MiB): 422 avatar_invalid for anything else, with the old avatar kept; 403 for a caller who
// can see but not edit the intelligence, 404 for one who cannot see it.
func (s *server) putAvatar(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	prof, err := s.Intelligences.SetAvatar(r.Context(), p, r.PathValue("id"), r.Body)
	if err != nil {
		return err
	}
	return s.writeProfile(w, r, p, prof)
}

// deleteAvatar removes the avatar, so the UI shows initials again (PROF-08).
func (s *server) deleteAvatar(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	if err := s.Intelligences.ClearAvatar(r.Context(), p, r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// getAvatar serves the stored 256x256 PNG to users who can see the intelligence; everybody
// else, and an intelligence without an avatar, get 404. The ?v=<sha> of the profile's avatar_url
// makes the answer immutable for the browser.
func (s *server) getAvatar(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	f, sha, err := s.Intelligences.Avatar(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := w.Header()
	h.Set("Content-Type", "image/png")
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	h.Set("ETag", `"`+sha+`"`)
	http.ServeContent(w, r, "", time.Time{}, f)
	return nil
}

// agentUpdateProfile changes the profile of the calling agent's own AI (PROF-04); it never
// names another intelligence.
func (s *server) agentUpdateProfile(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.UpdateProfileRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	prof, err := s.Intelligences.Update(r.Context(), p, p.AgentID, profileUpdate(req))
	if err != nil {
		return err
	}
	return s.writeProfile(w, r, p, prof)
}

// agentPutAvatar replaces the calling agent's own avatar with the image in the body.
func (s *server) agentPutAvatar(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	prof, err := s.Intelligences.SetAvatar(r.Context(), p, p.AgentID, r.Body)
	if err != nil {
		return err
	}
	return s.writeProfile(w, r, p, prof)
}
