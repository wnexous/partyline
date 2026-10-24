package api

import (
	"net/http"
	"time"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store/db"
)

// orgRoutes serve organizations: their memberships, their address, their avatar and, later, their
// chart, members, positions and invitations (ORG-01, ORG-06).
func (s *server) orgRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/v1/orgs", authed(s.listOrgs))
	s.handle(mux, "POST /api/v1/orgs", authed(s.createOrg))
	s.handle(mux, "GET /api/v1/orgs/{org}", authed(s.getOrg))
	s.handle(mux, "PATCH /api/v1/orgs/{org}", authed(s.updateOrg))
	s.handle(mux, "DELETE /api/v1/orgs/{org}", authed(s.deleteOrg))
	s.handle(mux, "PUT /api/v1/orgs/{org}/avatar", authed(s.putOrgAvatar))
	s.handle(mux, "DELETE /api/v1/orgs/{org}/avatar", authed(s.deleteOrgAvatar))
	s.handle(mux, "GET /api/v1/orgs/{org}/avatar", authed(s.getOrgAvatar))
}

// resolveOrg finds the organization the {org} path segment names: its id or its slug (ORG-01).
// It says nothing about who may see the organization; the service checks that.
func (s *server) resolveOrg(r *http.Request) (db.Organization, error) {
	return s.Orgs.Resolve(r.Context(), r.PathValue("org"))
}

// listOrgs lists the organizations the caller belongs to, with its own permission and position in
// each (OMEM-03).
func (s *server) listOrgs(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	list, err := s.Orgs.ListMine(r.Context(), p.IID())
	if err != nil {
		return err
	}
	out := dto.List[dto.Organization]{Items: make([]dto.Organization, len(list))}
	for i, m := range list {
		out.Items[i] = convert.Organization(m)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// createOrg creates an organization with the caller as its admin, holding the root position
// (ORG-01). Agent tokens cannot reach this path.
func (s *server) createOrg(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.CreateOrganizationRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	m, err := s.Orgs.Create(r.Context(), p.IID(), req.Name)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, convert.Organization(m))
	return nil
}

// getOrg returns the organization as one of its members sees it (CHART-02).
func (s *server) getOrg(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	org, err := s.resolveOrg(r)
	if err != nil {
		return err
	}
	m, err := s.Orgs.Get(r.Context(), p.IID(), org.ID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, convert.Organization(m))
	return nil
}

// updateOrg renames the organization or changes its address; only an admin does (OMEM-04).
func (s *server) updateOrg(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	org, err := s.resolveOrg(r)
	if err != nil {
		return err
	}
	var req dto.UpdateOrganizationRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	m, err := s.Orgs.Update(r.Context(), p.IID(), org.ID, orgs.UpdateInput{Name: req.Name, Slug: req.Slug})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, convert.Organization(m))
	return nil
}

// deleteOrg deletes an empty organization; one that still owns AIs, connections or groups is
// refused with 409 organization_not_empty and their counts (ORG-06).
func (s *server) deleteOrg(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	org, err := s.resolveOrg(r)
	if err != nil {
		return err
	}
	if err := s.Orgs.Delete(r.Context(), p.IID(), org.ID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// putOrgAvatar replaces the organization's avatar with the image in the body; only an admin does
// (OMEM-04). Any other image is 422 avatar_invalid and the old avatar stays.
func (s *server) putOrgAvatar(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	org, err := s.resolveOrg(r)
	if err != nil {
		return err
	}
	m, err := s.Orgs.SetAvatar(r.Context(), p.IID(), org.ID, r.Body)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, convert.Organization(m))
	return nil
}

// deleteOrgAvatar removes the avatar; the web app shows initials again.
func (s *server) deleteOrgAvatar(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	org, err := s.resolveOrg(r)
	if err != nil {
		return err
	}
	if err := s.Orgs.ClearAvatar(r.Context(), p.IID(), org.ID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// getOrgAvatar serves the stored 256x256 PNG to the organization's members; everybody else, and an
// organization without an avatar, gets 404.
func (s *server) getOrgAvatar(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	org, err := s.resolveOrg(r)
	if err != nil {
		return err
	}
	f, sha, err := s.Orgs.Avatar(r.Context(), p.IID(), org.ID)
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
