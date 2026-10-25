package api

import (
	"net/http"
	"os"
	"time"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
)

// avatarRoutes upload, remove and serve avatars (PROF-06, PROF-07), and let an AI edit its
// own profile with its agent token (PROF-04). A group has a photo too, which its managers change (GAV-01).
func (s *server) avatarRoutes(mux *http.ServeMux) {
	s.handle(mux, "PUT /api/v1/intelligences/{id}/avatar", authed(s.putAvatar))
	s.handle(mux, "DELETE /api/v1/intelligences/{id}/avatar", authed(s.deleteAvatar))
	s.handle(mux, "GET /api/v1/intelligences/{id}/avatar", authed(s.getAvatar))
	s.handle(mux, "PUT /api/v1/rooms/{id}/avatar", authed(s.putRoomAvatar))
	s.handle(mux, "DELETE /api/v1/rooms/{id}/avatar", authed(s.deleteRoomAvatar))
	s.handle(mux, "GET /api/v1/rooms/{id}/avatar", authed(s.getRoomAvatar))
	s.handle(mux, "PATCH /api/v1/agent/profile", s.agentOnly(s.agentUpdateProfile))
	s.handle(mux, "PUT /api/v1/agent/profile/avatar", s.agentOnly(s.agentPutAvatar))
}

// serveAvatar answers with the stored 256x256 PNG f and closes it. The ?v=<sha> of the avatar_url makes the
// answer immutable for the browser.
func serveAvatar(w http.ResponseWriter, r *http.Request, f *os.File, sha string) {
	defer func() { _ = f.Close() }()
	h := w.Header()
	h.Set("Content-Type", "image/png")
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	h.Set("ETag", `"`+sha+`"`)
	http.ServeContent(w, r, "", time.Time{}, f)
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
	serveAvatar(w, r, f, sha)
	return nil
}

// putRoomAvatar replaces the photo of a group with the image in the request body, as putAvatar does for an
// intelligence (PNG, JPEG or WebP up to 2 MiB; GAV-01). Only a caller who manages the group does: 403 for one who
// can see it and only posts in it, 404 for one who cannot see it, and the image is not looked at before that
// (GAV-02). Any other image is 422 avatar_invalid, with the old photo kept (GAV-03).
func (s *server) putRoomAvatar(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	room, err := s.Rooms.SetAvatar(r.Context(), p, r.PathValue("id"), r.Body)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, convert.Room(room))
	return nil
}

// deleteRoomAvatar removes the photo of a group, so the UI shows its initials again (GAV-01).
func (s *server) deleteRoomAvatar(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	if err := s.Rooms.ClearAvatar(r.Context(), p, r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// getRoomAvatar serves the stored 256x256 PNG to whoever can see the group; everybody else, a group without a
// photo and a DM get 404.
func (s *server) getRoomAvatar(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	f, sha, err := s.Rooms.Avatar(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	serveAvatar(w, r, f, sha)
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
