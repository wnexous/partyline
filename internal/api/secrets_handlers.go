package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
)

// secretRoutes serve the owner's named secrets for container AIs (CA-03): a session or a user
// token only, names and dates on GET, and a write-only value. An agent token is refused by the
// middleware, any other credential kind by userOnly, and an anonymous caller is 401.
func (s *server) secretRoutes(mux *http.ServeMux) {
	if s.Secrets == nil {
		return
	}
	s.handle(mux, "GET /api/v1/secrets", userOnly(s.listSecrets))
	s.handle(mux, "PUT /api/v1/secrets/{name}", userOnly(s.putSecret))
	s.handle(mux, "DELETE /api/v1/secrets/{name}", userOnly(s.deleteSecret))
}

// listSecrets returns the names and dates of the caller's secrets, never a value (CA-03, CA-08).
func (s *server) listSecrets(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	list, err := s.Secrets.ListNames(r.Context(), p.UserID)
	if err != nil {
		return err
	}
	out := dto.List[dto.Secret]{Items: make([]dto.Secret, len(list))}
	for i, sec := range list {
		out.Items[i] = dto.Secret{Name: sec.Name, CreatedAt: sec.CreatedAt, UpdatedAt: sec.UpdatedAt}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// putSecret stores or replaces the value under the name in the path and answers 204; the value
// stays in the sealed row and is never echoed (CA-03, CA-08).
func (s *server) putSecret(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.PutSecretRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if err := s.Secrets.Put(r.Context(), p.UserID, r.PathValue("name"), req.Value); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// deleteSecret removes the caller's secret by name: 204, or 404 when the name is unknown or
// belongs to another owner (CA-07).
func (s *server) deleteSecret(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	if err := s.Secrets.Delete(r.Context(), p.UserID, r.PathValue("name")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
