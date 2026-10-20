package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
)

// identityRoutes are the verified identity endpoints of the current user (IDN-01, IDN-04).
func (s *server) identityRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/v1/identities", authed(s.listIdentities))
	s.handle(mux, "POST /api/v1/identities/pairing", authed(s.createPairing))
	s.handle(mux, "DELETE /api/v1/identities/{id}", authed(s.deleteIdentity))
}

func (s *server) listIdentities(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	list, err := s.Identities.List(r.Context(), p.UserID)
	if err != nil {
		return err
	}
	out := dto.List[dto.Identity]{Items: make([]dto.Identity, len(list))}
	for i, ident := range list {
		out.Items[i] = convert.Identity(ident)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (s *server) createPairing(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	code, expires, err := s.Identities.CreatePairing(r.Context(), p.UserID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, dto.PairingCode{Code: code, ExpiresAt: expires.UTC()})
	return nil
}

func (s *server) deleteIdentity(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	if err := s.Identities.Delete(r.Context(), p.UserID, r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
